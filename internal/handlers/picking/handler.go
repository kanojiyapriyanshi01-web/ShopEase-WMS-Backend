package picking

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

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

func (h *Handler) MyTasks(c *gin.Context) {
	claims := middleware.GetClaims(c)

	rows, err := h.db.Query(`
		SELECT id, warehouse_order_id, picker_id, picking_list, status, started_at, created_at
		FROM wms.warehouse_pickings
		WHERE picker_id = $1 AND status != 'completed'
		ORDER BY created_at ASC`, claims.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch tasks"})
		return
	}
	defer rows.Close()

	var tasks []models.WarehousePicking
	for rows.Next() {
		var t models.WarehousePicking
		var listJSON []byte
		rows.Scan(&t.ID, &t.WarehouseOrderID, &t.PickerID, &listJSON, &t.Status, &t.StartedAt, &t.CreatedAt)
		json.Unmarshal(listJSON, &t.PickingList)
		tasks = append(tasks, t)
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

func (h *Handler) Detail(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var t models.WarehousePicking
	var listJSON []byte
	err = h.db.QueryRow(`
		SELECT id, warehouse_order_id, picker_id, picking_list, status, started_at, completed_at, created_at
		FROM wms.warehouse_pickings WHERE id = $1`, id).Scan(
		&t.ID, &t.WarehouseOrderID, &t.PickerID, &listJSON, &t.Status, &t.StartedAt, &t.CompletedAt, &t.CreatedAt,
	)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	json.Unmarshal(listJSON, &t.PickingList)
	c.JSON(http.StatusOK, t)
}

// ScanItem - verifies a barcode scan against the picking list
func (h *Handler) ScanItem(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req struct {
		Barcode string `json:"barcode" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Lookup the scanned barcode in inventory
	var sku string
	err = h.db.QueryRow(`SELECT sku FROM wms.warehouse_inventory WHERE barcode = $1`, req.Barcode).Scan(&sku)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"match": false, "message": "barcode not recognized"})
		return
	}

	// Load picking list, find matching item, increment qty_picked
	var listJSON []byte
	h.db.QueryRow(`SELECT picking_list FROM wms.warehouse_pickings WHERE id = $1`, id).Scan(&listJSON)

	var items []models.PickingItem
	json.Unmarshal(listJSON, &items)

	matched := false
	for i := range items {
		if items[i].SKU == sku && items[i].QtyPicked < items[i].QtyRequired {
			now := time.Now()
			items[i].QtyPicked++
			items[i].ScannedAt = &now
			matched = true
			break
		}
	}

	if !matched {
		c.JSON(http.StatusOK, gin.H{"match": false, "message": "item already picked or not in this order"})
		return
	}

	updatedJSON, _ := json.Marshal(items)
	h.db.Exec(`
		UPDATE wms.warehouse_pickings
		SET picking_list = $1, status = 'in_progress', started_at = COALESCE(started_at, NOW())
		WHERE id = $2`, updatedJSON, id)

	c.JSON(http.StatusOK, gin.H{"match": true, "sku": sku, "message": "item picked successfully"})
}

func (h *Handler) Complete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var listJSON []byte
	var warehouseOrderID uuid.UUID
	err = h.db.QueryRow(`SELECT picking_list, warehouse_order_id FROM wms.warehouse_pickings WHERE id = $1`, id).
		Scan(&listJSON, &warehouseOrderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	var items []models.PickingItem
	json.Unmarshal(listJSON, &items)

	for _, item := range items {
		if item.QtyPicked < item.QtyRequired {
			c.JSON(http.StatusBadRequest, gin.H{"error": "not all items picked yet", "sku": item.SKU})
			return
		}
	}

	tx, _ := h.db.Begin()
	defer tx.Rollback()

	tx.Exec(`UPDATE wms.warehouse_pickings SET status = 'completed', completed_at = NOW() WHERE id = $1`, id)
	tx.Exec(`UPDATE wms.warehouse_orders SET status = 'picked', picking_done_at = NOW(), updated_at = NOW() WHERE id = $1`,
		warehouseOrderID)

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to complete picking"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "picking completed", "order_status": "picked"})
}
