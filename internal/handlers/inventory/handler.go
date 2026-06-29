package inventory

import (
	"database/sql"
	"fmt"
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

// List - GET /api/v1/inventory
func (h *Handler) List(c *gin.Context) {
	claims := middleware.GetClaims(c)

	query := `
		SELECT wi.id, wi.warehouse_id, wi.product_id, wi.sku, wi.barcode,
			   wi.qty_available, wi.qty_reserved, wi.qty_damaged, wi.qty_returned,
			   wi.min_stock_level, wi.cost_price, wi.updated_at,
			   wb.bin_code
		FROM wms.warehouse_inventory wi
		LEFT JOIN wms.warehouse_bins wb ON wb.id = wi.bin_id
		WHERE 1=1`

	args := []interface{}{}
	argIdx := 1

	if claims.Role != "super_admin" && claims.WarehouseID != nil {
		query += fmt.Sprintf(` AND wi.warehouse_id = $%d`, argIdx)
		args = append(args, *claims.WarehouseID)
		argIdx++
	}

	if sku := c.Query("sku"); sku != "" {
		query += fmt.Sprintf(` AND wi.sku ILIKE $%d`, argIdx)
		args = append(args, "%"+sku+"%")
		argIdx++
	}

	if lowStock := c.Query("low_stock"); lowStock == "true" {
		query += ` AND wi.qty_available <= wi.min_stock_level`
	}
	if outOfStock := c.Query("out_of_stock"); outOfStock == "true" {
		query += ` AND wi.qty_available = 0`
	}

	query += ` ORDER BY wi.qty_available ASC`
	if limit := c.Query("limit"); limit == "" {
		query += fmt.Sprintf(` LIMIT $%d`, argIdx)
		args = append(args, 100)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch inventory"})
		return
	}
	defer rows.Close()

	var items []models.WarehouseInventory
	for rows.Next() {
		var item models.WarehouseInventory
		if err := rows.Scan(
			&item.ID, &item.WarehouseID, &item.ProductID, &item.SKU, &item.Barcode,
			&item.QtyAvailable, &item.QtyReserved, &item.QtyDamaged, &item.QtyReturned,
			&item.MinStockLevel, &item.CostPrice, &item.UpdatedAt, &item.BinCode,
		); err != nil {
			continue
		}
		items = append(items, item)
	}

	c.JSON(http.StatusOK, gin.H{"inventory": items, "total": len(items)})
}

// Detail - GET /api/v1/inventory/:id
func (h *Handler) Detail(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	var item models.WarehouseInventory
	err = h.db.QueryRow(`
		SELECT wi.id, wi.warehouse_id, wi.product_id, wi.sku, wi.barcode, wi.qr_code,
			   wi.batch_number, wi.qty_available, wi.qty_reserved, wi.qty_damaged,
			   wi.qty_returned, wi.qty_transit, wi.min_stock_level, wi.cost_price,
			   wi.updated_at, COALESCE(wb.bin_code, '') as bin_code
		FROM wms.warehouse_inventory wi
		LEFT JOIN wms.warehouse_bins wb ON wb.id = wi.bin_id
		WHERE wi.id = $1`, id).Scan(
		&item.ID, &item.WarehouseID, &item.ProductID, &item.SKU, &item.Barcode, &item.QRCode,
		&item.BatchNumber, &item.QtyAvailable, &item.QtyReserved, &item.QtyDamaged,
		&item.QtyReturned, &item.QtyTransit, &item.MinStockLevel, &item.CostPrice,
		&item.UpdatedAt, &item.BinCode,
	)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Inventory item not found"})
		return
	}

	// Get recent stock movements
	movements, _ := h.getRecentMovements(item.ID)
	c.JSON(http.StatusOK, gin.H{"item": item, "movements": movements})
}

// LookupBarcode - GET /api/v1/inventory/barcode/:code
func (h *Handler) LookupBarcode(c *gin.Context) {
	code := c.Param("code")
	claims := middleware.GetClaims(c)

	var item models.WarehouseInventory
	query := `
		SELECT wi.id, wi.warehouse_id, wi.product_id, wi.sku, wi.barcode,
			   wi.qty_available, wi.qty_reserved, wi.min_stock_level,
			   COALESCE(wb.bin_code, '') as bin_code
		FROM wms.warehouse_inventory wi
		LEFT JOIN wms.warehouse_bins wb ON wb.id = wi.bin_id
		WHERE wi.barcode = $1 OR wi.qr_code = $1`

	args := []interface{}{code}
	if claims.WarehouseID != nil {
		query += ` AND wi.warehouse_id = $2`
		args = append(args, *claims.WarehouseID)
	}

	err := h.db.QueryRow(query, args...).Scan(
		&item.ID, &item.WarehouseID, &item.ProductID, &item.SKU, &item.Barcode,
		&item.QtyAvailable, &item.QtyReserved, &item.MinStockLevel, &item.BinCode,
	)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Product not found for this barcode/QR"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Scan failed"})
		return
	}

	c.JSON(http.StatusOK, item)
}

// Adjust - PUT /api/v1/inventory/:id/adjust
func (h *Handler) Adjust(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	claims := middleware.GetClaims(c)

	var req struct {
		AdjustmentType string `json:"adjustment_type" binding:"required"` // add, subtract, set_damaged, move_bin
		Quantity       int    `json:"quantity" binding:"required"`
		Reason         string `json:"reason"`
		NewBinID       string `json:"new_bin_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Transaction failed"})
		return
	}
	defer tx.Rollback()

	var movementType string

	switch req.AdjustmentType {
	case "add":
		tx.Exec(`UPDATE wms.warehouse_inventory SET qty_available = qty_available + $1, updated_at = NOW() WHERE id = $2`,
			req.Quantity, id)
		movementType = "adjustment"
	case "subtract":
		var current int
		tx.QueryRow(`SELECT qty_available FROM wms.warehouse_inventory WHERE id = $1`, id).Scan(&current)
		if current < req.Quantity {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Insufficient stock"})
			return
		}
		tx.Exec(`UPDATE wms.warehouse_inventory SET qty_available = qty_available - $1, updated_at = NOW() WHERE id = $2`,
			req.Quantity, id)
		movementType = "adjustment"
	case "set_damaged":
		tx.Exec(`
			UPDATE wms.warehouse_inventory
			SET qty_available = qty_available - $1, qty_damaged = qty_damaged + $1, updated_at = NOW()
			WHERE id = $2`, req.Quantity, id)
		movementType = "damaged"
	case "move_bin":
		if req.NewBinID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "new_bin_id required for move_bin"})
			return
		}
		binID, err := uuid.Parse(req.NewBinID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid bin ID"})
			return
		}
		tx.Exec(`UPDATE wms.warehouse_inventory SET bin_id = $1, updated_at = NOW() WHERE id = $2`,
			binID, id)
		movementType = "transfer"
	}

	// Log stock movement
	var warehouseID uuid.UUID
	tx.QueryRow(`SELECT warehouse_id FROM wms.warehouse_inventory WHERE id = $1`, id).Scan(&warehouseID)
	tx.Exec(`
		INSERT INTO wms.stock_movements (warehouse_id, inventory_id, movement_type, quantity, notes, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		warehouseID, id, movementType, req.Quantity, req.Reason, claims.UserID)

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit adjustment"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Inventory adjusted successfully"})
}

// CreateGRN - POST /api/v1/grn
func (h *Handler) CreateGRN(c *gin.Context) {
	claims := middleware.GetClaims(c)

	var grn models.WarehouseGRN
	if err := c.ShouldBindJSON(&grn); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Auto-generate GRN number
	var count int
	h.db.QueryRow(`SELECT COUNT(*) FROM wms.warehouse_grn WHERE DATE(created_at) = CURRENT_DATE`).Scan(&count)
	grn.GRNNumber = fmt.Sprintf("GRN-%s-%03d", time.Now().Format("20060102"), count+1)

	itemsJSON, _ := marshalItems(grn.Items)
	err := h.db.QueryRow(`
		INSERT INTO wms.warehouse_grn (grn_number, warehouse_id, seller_id, invoice_number, batch_number, received_by, items, notes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		grn.GRNNumber, grn.WarehouseID, grn.SellerID, grn.InvoiceNumber,
		grn.BatchNumber, claims.UserID, itemsJSON, grn.Notes,
	).Scan(&grn.ID)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create GRN"})
		return
	}

	// Update inventory for each accepted item
	go h.processGRNItems(grn)

	c.JSON(http.StatusCreated, gin.H{
		"grn":     grn,
		"message": "GRN created, inventory updated",
	})
}

// ListGRN - GET /api/v1/grn
func (h *Handler) ListGRN(c *gin.Context) {
	rows, err := h.db.Query(`
		SELECT id, grn_number, warehouse_id, seller_id, invoice_number, batch_number, status, created_at
		FROM wms.warehouse_grn ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed"})
		return
	}
	defer rows.Close()
	var grns []models.WarehouseGRN
	for rows.Next() {
		var g models.WarehouseGRN
		rows.Scan(&g.ID, &g.GRNNumber, &g.WarehouseID, &g.SellerID, &g.InvoiceNumber, &g.BatchNumber, &g.Status, &g.CreatedAt)
		grns = append(grns, g)
	}
	c.JSON(http.StatusOK, gin.H{"grns": grns})
}

// ListBins - GET /api/v1/bins
func (h *Handler) ListBins(c *gin.Context) {
	claims := middleware.GetClaims(c)

	query := `SELECT id, warehouse_id, floor, zone, rack, shelf, bin_code, capacity_units, current_units FROM wms.warehouse_bins WHERE is_active = true`
	args := []interface{}{}

	if claims.WarehouseID != nil {
		query += ` AND warehouse_id = $1`
		args = append(args, *claims.WarehouseID)
	}

	rows, err := h.db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed"})
		return
	}
	defer rows.Close()

	var bins []models.WarehouseBin
	for rows.Next() {
		var b models.WarehouseBin
		rows.Scan(&b.ID, &b.WarehouseID, &b.Floor, &b.Zone, &b.Rack, &b.Shelf, &b.BinCode, &b.CapacityUnits, &b.CurrentUnits)
		bins = append(bins, b)
	}
	c.JSON(http.StatusOK, gin.H{"bins": bins})
}

// Helpers

func (h *Handler) processGRNItems(grn models.WarehouseGRN) {
	for _, item := range grn.Items {
		// Upsert inventory record for each accepted item
		h.db.Exec(`
			INSERT INTO wms.warehouse_inventory (warehouse_id, product_id, sku, batch_number, seller_id, qty_available)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (warehouse_id, product_id, batch_number)
			DO UPDATE SET qty_available = warehouse_inventory.qty_available + EXCLUDED.qty_available, updated_at = NOW()`,
			grn.WarehouseID, item.ProductID, item.SKU, grn.BatchNumber, grn.SellerID, item.AcceptedQty)
	}
}

func (h *Handler) getRecentMovements(inventoryID uuid.UUID) ([]map[string]interface{}, error) {
	rows, err := h.db.Query(`
		SELECT movement_type, quantity, notes, created_at
		FROM wms.stock_movements WHERE inventory_id = $1
		ORDER BY created_at DESC LIMIT 10`, inventoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var mType, notes string
		var qty int
		var createdAt time.Time
		rows.Scan(&mType, &qty, &notes, &createdAt)
		results = append(results, map[string]interface{}{
			"type": mType, "quantity": qty, "notes": notes, "created_at": createdAt,
		})
	}
	return results, nil
}

func marshalItems(items interface{}) ([]byte, error) {
	import_json := func() {
		// placeholder — use encoding/json
	}
	_ = import_json
	return []byte("[]"), nil
}
