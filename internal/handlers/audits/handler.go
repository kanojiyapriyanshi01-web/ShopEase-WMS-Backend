package audits

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"shopease-wms/internal/middleware"
	"shopease-wms/internal/models"
)

type Handler struct {
	db *sql.DB
}

func NewHandler(db *sql.DB) *Handler {
	return &Handler{db: db}
}

func (h *Handler) Create(c *gin.Context) {
	claims := middleware.GetClaims(c)

	var req struct {
		WarehouseID string `json:"warehouse_id" binding:"required"`
		AuditType   string `json:"audit_type" binding:"required"` // daily, weekly, monthly, annual, spot
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Snapshot expected quantities from current inventory
	rows, err := h.db.Query(`
		SELECT id, sku, qty_available FROM wms.warehouse_inventory WHERE warehouse_id = $1`, req.WarehouseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to snapshot inventory"})
		return
	}
	defer rows.Close()

	var items []models.AuditItem
	for rows.Next() {
		var item models.AuditItem
		rows.Scan(&item.InventoryID, &item.SKU, &item.ExpectedQty)
		items = append(items, item)
	}

	itemsJSON, _ := json.Marshal(items)

	var auditID uuid.UUID
	err = h.db.QueryRow(`
		INSERT INTO wms.warehouse_audits (warehouse_id, audit_type, conducted_by, audit_items, status)
		VALUES ($1, $2, $3, $4, 'in_progress')
		RETURNING id`,
		req.WarehouseID, req.AuditType, claims.UserID, itemsJSON,
	).Scan(&auditID)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create audit: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"audit_id":    auditID,
		"items_count": len(items),
		"message":     "audit started, enter actual counts and submit",
	})
}

func (h *Handler) List(c *gin.Context) {
	claims := middleware.GetClaims(c)

	query := `
		SELECT id, warehouse_id, audit_type, total_discrepancies, status, started_at, completed_at
		FROM wms.warehouse_audits WHERE 1=1`
	args := []interface{}{}

	if claims.WarehouseID != nil {
		query += ` AND warehouse_id = $1`
		args = append(args, *claims.WarehouseID)
	}
	query += ` ORDER BY started_at DESC LIMIT 50`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch audits"})
		return
	}
	defer rows.Close()

	var audits []models.WarehouseAudit
	for rows.Next() {
		var a models.WarehouseAudit
		rows.Scan(&a.ID, &a.WarehouseID, &a.AuditType, &a.TotalDiscrepancies, &a.Status, &a.StartedAt, &a.CompletedAt)
		audits = append(audits, a)
	}
	c.JSON(http.StatusOK, gin.H{"audits": audits})
}

// Submit - staff enters actual counted quantities, system computes differences
func (h *Handler) Submit(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req struct {
		ActualCounts map[string]int `json:"actual_counts" binding:"required"` // inventory_id -> actual_qty
		Reasons      map[string]string `json:"reasons"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var itemsJSON []byte
	err = h.db.QueryRow(`SELECT audit_items FROM wms.warehouse_audits WHERE id = $1`, id).Scan(&itemsJSON)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "audit not found"})
		return
	}

	var items []models.AuditItem
	json.Unmarshal(itemsJSON, &items)

	totalDiscrepancies := 0
	for i := range items {
		idStr := items[i].InventoryID.String()
		if actual, ok := req.ActualCounts[idStr]; ok {
			items[i].ActualQty = actual
			items[i].Difference = actual - items[i].ExpectedQty
			if items[i].Difference != 0 {
				totalDiscrepancies++
				items[i].Reason = req.Reasons[idStr]
			}
		}
	}

	updatedJSON, _ := json.Marshal(items)
	status := "completed"
	if totalDiscrepancies > 0 {
		status = "flagged"
	}

	h.db.Exec(`
		UPDATE wms.warehouse_audits
		SET audit_items = $1, total_discrepancies = $2, status = $3, completed_at = NOW()
		WHERE id = $4`, updatedJSON, totalDiscrepancies, status, id)

	// Optionally auto-correct inventory to match actual counts
	for _, item := range items {
		if item.Difference != 0 {
			h.db.Exec(`UPDATE wms.warehouse_inventory SET qty_available = $1, updated_at = NOW() WHERE id = $2`,
				item.ActualQty, item.InventoryID)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":              "audit submitted",
		"total_discrepancies": totalDiscrepancies,
		"status":               status,
	})
}
