package returns

import (
	"database/sql"
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

// List - GET /api/v1/returns
func (h *Handler) List(c *gin.Context) {
	claims := middleware.GetClaims(c)

	query := `
		SELECT id, shopease_return_id, shopease_order_id, warehouse_id,
			   product_id, qty, customer_reason, status, condition,
			   resolution, received_at, created_at
		FROM wms.warehouse_returns WHERE 1=1`

	args := []interface{}{}
	argIdx := 1

	if claims.WarehouseID != nil {
		query += ` AND warehouse_id = $1`
		args = append(args, *claims.WarehouseID)
		argIdx++
	}

	if status := c.Query("status"); status != "" {
		query += ` AND status = $` + itoa(argIdx)
		args = append(args, status)
		argIdx++
	}

	query += ` ORDER BY created_at DESC LIMIT 50`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed"})
		return
	}
	defer rows.Close()

	var returns []models.WarehouseReturn
	for rows.Next() {
		var r models.WarehouseReturn
		rows.Scan(
			&r.ID, &r.ShopeaseReturnID, &r.ShopeaseOrderID, &r.WarehouseID,
			&r.ProductID, &r.Qty, &r.CustomerReason, &r.Status, &r.Condition,
			&r.Resolution, &r.ReceivedAt, &r.CreatedAt,
		)
		returns = append(returns, r)
	}
	c.JSON(http.StatusOK, gin.H{"returns": returns, "total": len(returns)})
}

// Detail - GET /api/v1/returns/:id
func (h *Handler) Detail(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	var r models.WarehouseReturn
	err = h.db.QueryRow(`
		SELECT id, shopease_return_id, shopease_order_id, warehouse_id,
			   product_id, qty, customer_reason, status, condition,
			   qc_inspector_id, qc_notes, resolution,
			   received_at, qc_done_at, created_at
		FROM wms.warehouse_returns WHERE id = $1`, id).Scan(
		&r.ID, &r.ShopeaseReturnID, &r.ShopeaseOrderID, &r.WarehouseID,
		&r.ProductID, &r.Qty, &r.CustomerReason, &r.Status, &r.Condition,
		&r.QCInspectorID, &r.QCNotes, &r.Resolution,
		&r.ReceivedAt, &r.QCDoneAt, &r.CreatedAt,
	)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Return not found"})
		return
	}
	c.JSON(http.StatusOK, r)
}

// SubmitQC - POST /api/v1/returns/:id/qc
func (h *Handler) SubmitQC(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	claims := middleware.GetClaims(c)

	var req struct {
		Condition  string   `json:"condition" binding:"required"` // good_condition, damaged, used, wrong_product, missing_parts, fake_product
		QCNotes    string   `json:"qc_notes"`
		Resolution string   `json:"resolution" binding:"required"` // refund, replacement, rejected
		QCImages   []string `json:"qc_images"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now()

	tx, _ := h.db.Begin()
	defer tx.Rollback()

	// Update return record
	_, err = tx.Exec(`
		UPDATE wms.warehouse_returns
		SET condition = $1, qc_notes = $2, resolution = $3,
			qc_inspector_id = $4, qc_done_at = $5,
			status = 'approved', updated_at = NOW()
		WHERE id = $6`,
		req.Condition, req.QCNotes, req.Resolution,
		claims.UserID, now, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update QC"})
		return
	}

	// Get return details for inventory update
	var warehouseID, productID uuid.UUID
	var qty int
	tx.QueryRow(`SELECT warehouse_id, product_id, qty FROM wms.warehouse_returns WHERE id = $1`, id).
		Scan(&warehouseID, &productID, &qty)

	switch req.Resolution {
	case "refund":
		// Condition good — add back to available stock
		if req.Condition == "good_condition" {
			tx.Exec(`
				UPDATE wms.warehouse_inventory
				SET qty_available = qty_available + $1,
					qty_returned = qty_returned - $1,
					updated_at = NOW()
				WHERE warehouse_id = $2 AND product_id = $3`,
				qty, warehouseID, productID)
		} else {
			// Damaged — move to damaged stock
			tx.Exec(`
				UPDATE wms.warehouse_inventory
				SET qty_damaged = qty_damaged + $1,
					qty_returned = qty_returned - $1,
					updated_at = NOW()
				WHERE warehouse_id = $2 AND product_id = $3`,
				qty, warehouseID, productID)
		}
		// Trigger refund via ShopEase webhook
		go h.triggerRefund(id)

	case "replacement":
		// Reserve stock for replacement order
		tx.Exec(`
			INSERT INTO wms.warehouse_replacements (return_id, warehouse_id, product_id, qty, inventory_reserved)
			VALUES ($1, $2, $3, $4, false)`, id, warehouseID, productID, qty)
		go h.createReplacementOrder(id, warehouseID, productID, qty)

	case "rejected":
		// Mark stock as blocked/rejected
		tx.Exec(`
			UPDATE wms.warehouse_inventory
			SET qty_damaged = qty_damaged + $1,
				updated_at = NOW()
			WHERE warehouse_id = $2 AND product_id = $3`,
			qty, warehouseID, productID)
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "QC submitted",
		"resolution": req.Resolution,
	})
}

// CreateReplacement - POST /api/v1/replacements
func (h *Handler) CreateReplacement(c *gin.Context) {
	var req struct {
		ReturnID    string `json:"return_id" binding:"required"`
		WarehouseID string `json:"warehouse_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	returnID, _ := uuid.Parse(req.ReturnID)

	// Get return details
	var productID uuid.UUID
	var warehouseID uuid.UUID
	var qty int
	err := h.db.QueryRow(`
		SELECT product_id, warehouse_id, qty
		FROM wms.warehouse_returns WHERE id = $1`, returnID).
		Scan(&productID, &warehouseID, &qty)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Return not found"})
		return
	}

	// Check stock availability
	var available int
	h.db.QueryRow(`
		SELECT qty_available FROM wms.warehouse_inventory
		WHERE warehouse_id = $1 AND product_id = $2`, warehouseID, productID).Scan(&available)

	if available < qty {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":     "Insufficient stock for replacement",
			"available": available,
			"required":  qty,
		})
		return
	}

	// Reserve stock
	h.db.Exec(`
		UPDATE wms.warehouse_inventory
		SET qty_available = qty_available - $1,
			qty_reserved = qty_reserved + $1,
			updated_at = NOW()
		WHERE warehouse_id = $2 AND product_id = $3`,
		qty, warehouseID, productID)

	// Create replacement order in WMS
	var replacementID uuid.UUID
	h.db.QueryRow(`
		INSERT INTO wms.warehouse_replacements (return_id, warehouse_id, product_id, qty, inventory_reserved)
		VALUES ($1, $2, $3, $4, true)
		RETURNING id`, returnID, warehouseID, productID, qty).Scan(&replacementID)

	// Notify ShopEase
	go h.notifyReplacementReady(returnID)

	c.JSON(http.StatusCreated, gin.H{
		"replacement_id": replacementID,
		"message":        "Replacement order created, stock reserved",
	})
}

// Internal helpers

func (h *Handler) triggerRefund(returnID uuid.UUID) {
	// POST to ShopEase backend to approve refund
}

func (h *Handler) createReplacementOrder(returnID, warehouseID, productID uuid.UUID, qty int) {
	// Create a new warehouse order for the replacement shipment
}

func (h *Handler) notifyReplacementReady(returnID uuid.UUID) {
	// Notify ShopEase that replacement is ready to ship
}

func itoa(n int) string {
	return string(rune('0' + n))
}
