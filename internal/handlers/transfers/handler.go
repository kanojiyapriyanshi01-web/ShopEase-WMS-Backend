package transfers

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
		FromWarehouseID string                     `json:"from_warehouse_id" binding:"required"`
		ToWarehouseID   string                     `json:"to_warehouse_id" binding:"required"`
		Items           []models.StockTransferItem `json:"items" binding:"required,min=1"`
		Notes           string                     `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	itemsJSON, _ := json.Marshal(req.Items)

	var transferID uuid.UUID
	err := h.db.QueryRow(`
		INSERT INTO wms.warehouse_transfers (from_warehouse_id, to_warehouse_id, items, requested_by, notes, status)
		VALUES ($1, $2, $3, $4, $5, 'requested')
		RETURNING id`,
		req.FromWarehouseID, req.ToWarehouseID, itemsJSON, claims.UserID, req.Notes,
	).Scan(&transferID)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create transfer: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"transfer_id": transferID, "message": "transfer requested"})
}

func (h *Handler) Approve(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	claims := middleware.GetClaims(c)

	var itemsJSON []byte
	var fromWarehouseID uuid.UUID
	err = h.db.QueryRow(`SELECT items, from_warehouse_id FROM wms.warehouse_transfers WHERE id = $1`, id).
		Scan(&itemsJSON, &fromWarehouseID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "transfer not found"})
		return
	}

	var items []models.StockTransferItem
	json.Unmarshal(itemsJSON, &items)

	tx, _ := h.db.Begin()
	defer tx.Rollback()

	// Move stock from source warehouse: available -> transit
	for _, item := range items {
		tx.Exec(`
			UPDATE wms.warehouse_inventory
			SET qty_available = qty_available - $1, qty_transit = qty_transit + $1, updated_at = NOW()
			WHERE id = $2`, item.Qty, item.InventoryID)
	}

	tx.Exec(`
		UPDATE wms.warehouse_transfers
		SET status = 'approved', approved_by = $1, dispatched_at = NOW(), updated_at = NOW()
		WHERE id = $2`, claims.UserID, id)

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "approval failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "transfer approved, stock in transit"})
}

func (h *Handler) Receive(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var itemsJSON []byte
	var toWarehouseID uuid.UUID
	err = h.db.QueryRow(`SELECT items, to_warehouse_id FROM wms.warehouse_transfers WHERE id = $1`, id).
		Scan(&itemsJSON, &toWarehouseID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "transfer not found"})
		return
	}

	var items []models.StockTransferItem
	json.Unmarshal(itemsJSON, &items)

	tx, _ := h.db.Begin()
	defer tx.Rollback()

	for _, item := range items {
		// Reduce transit stock at source
		tx.Exec(`UPDATE wms.warehouse_inventory SET qty_transit = qty_transit - $1, updated_at = NOW() WHERE id = $2`,
			item.Qty, item.InventoryID)

		// Add to destination warehouse inventory (upsert by sku)
		tx.Exec(`
			INSERT INTO wms.warehouse_inventory (warehouse_id, product_id, sku, qty_available)
			SELECT $1, product_id, sku, $2 FROM wms.warehouse_inventory WHERE id = $3
			ON CONFLICT (warehouse_id, product_id, batch_number)
			DO UPDATE SET qty_available = wms.warehouse_inventory.qty_available + EXCLUDED.qty_available, updated_at = NOW()`,
			toWarehouseID, item.Qty, item.InventoryID)
	}

	tx.Exec(`UPDATE wms.warehouse_transfers SET status = 'completed', received_at = NOW(), updated_at = NOW() WHERE id = $1`, id)

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "receive failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "transfer received, stock updated"})
}
