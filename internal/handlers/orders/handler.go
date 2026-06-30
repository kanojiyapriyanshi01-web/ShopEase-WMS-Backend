package orders

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
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

// List - GET /api/v1/orders
func (h *Handler) List(c *gin.Context) {
	claims := middleware.GetClaims(c)

	status := c.Query("status")
	priority := c.Query("priority")
	limit := 50
	offset := 0

	query := `
		SELECT wo.id, wo.shopease_order_id, wo.warehouse_id, wo.status, wo.priority, wo.picker_id, wo.packer_id, wo.dispatcher_id, wo.picking_started_at, wo.picking_done_at, wo.packing_done_at, wo.shipped_at, wo.delivered_at, wo.notes, wo.created_at, wo.updated_at, w.name as warehouse_name
		FROM wms.warehouse_orders wo
		JOIN wms.warehouses w ON w.id = wo.warehouse_id
		WHERE 1=1`

	args := []interface{}{}
	argCount := 1

	// Warehouse managers see only their warehouse
	if claims.Role == "warehouse_manager" && claims.WarehouseID != nil {
		query += ` AND wo.warehouse_id = $` + string(rune('0'+argCount))
		args = append(args, *claims.WarehouseID)
		argCount++
	}

	if status != "" {
		query += ` AND wo.status = $` + string(rune('0'+argCount))
		args = append(args, status)
		argCount++
	}
	if priority != "" {
		query += ` AND wo.priority = $` + string(rune('0'+argCount))
		args = append(args, priority)
		argCount++
	}

	query += ` ORDER BY wo.priority DESC, wo.created_at ASC LIMIT $` + string(rune('0'+argCount))
	args = append(args, limit)
	argCount++
	query += ` OFFSET $` + string(rune('0'+argCount))
	args = append(args, offset)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch orders"})
		return
	}
	defer rows.Close()

	var orders []models.WarehouseOrder
	for rows.Next() {
		var o models.WarehouseOrder
		var scanErr error
		if scanErr = rows.Scan(
			&o.ID, &o.ShopeaseOrderID, &o.WarehouseID, &o.Status,
			&o.Priority, &o.PickerID, &o.PackerID, &o.DispatcherID,
			&o.PickingStartedAt, &o.PickingDoneAt, &o.PackingDoneAt,
			&o.ShippedAt, &o.DeliveredAt, &o.Notes, &o.CreatedAt, &o.UpdatedAt, &o.WarehouseName,
		); scanErr != nil {
			log.Printf("SCAN ERROR: %v", scanErr)
			continue
		}
		orders = append(orders, o)
	}

	c.JSON(http.StatusOK, gin.H{
		"orders": orders,
		"total":  len(orders),
	})
}

// Detail - GET /api/v1/orders/:id
func (h *Handler) Detail(c *gin.Context) {
	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid order ID"})
		return
	}

	var order models.WarehouseOrder
	err = h.db.QueryRow(`
		SELECT id, shopease_order_id, warehouse_id, status, priority,
			   picker_id, packer_id, dispatcher_id,
			   picking_started_at, picking_done_at, packing_done_at,
			   shipped_at, delivered_at, notes, created_at, updated_at
		FROM wms.warehouse_orders WHERE id = $1`, orderID).Scan(
		&order.ID, &order.ShopeaseOrderID, &order.WarehouseID, &order.Status,
		&order.Priority, &order.PickerID, &order.PackerID, &order.DispatcherID,
		&order.PickingStartedAt, &order.PickingDoneAt, &order.PackingDoneAt,
		&order.ShippedAt, &order.DeliveredAt, &order.Notes, &order.CreatedAt, &order.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Order not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	c.JSON(http.StatusOK, order)
}

// UpdateStatus - PUT /api/v1/orders/:id/status
func (h *Handler) UpdateStatus(c *gin.Context) {
	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid order ID"})
		return
	}

	var req struct {
		Status string `json:"status" binding:"required"`
		Notes  string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	validStatuses := map[string]bool{
		"received": true, "inventory_reserved": true, "picking_assigned": true,
		"picked": true, "packing_assigned": true, "packed": true,
		"ready_to_dispatch": true, "shipped": true, "delivered": true, "cancelled": true,
	}
	if !validStatuses[req.Status] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status"})
		return
	}

	now := time.Now()
	query := `UPDATE wms.warehouse_orders SET status = $1, updated_at = $2`
	args := []interface{}{req.Status, now}

	// Auto-set timestamps
	switch req.Status {
	case "picking_assigned":
		query += `, picking_started_at = $3 WHERE id = $4`
		args = append(args, now, orderID)
	case "picked":
		query += `, picking_done_at = $3 WHERE id = $4`
		args = append(args, now, orderID)
	case "packed":
		query += `, packing_done_at = $3 WHERE id = $4`
		args = append(args, now, orderID)
	case "shipped":
		query += `, shipped_at = $3 WHERE id = $4`
		args = append(args, now, orderID)
	case "delivered":
		query += `, delivered_at = $3 WHERE id = $4`
		args = append(args, now, orderID)
	default:
		query += ` WHERE id = $3`
		args = append(args, orderID)
	}

	_, err = h.db.Exec(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update status"})
		return
	}

	// Push sync event to ShopEase (via Redis pub/sub in production)
	go h.notifyShopEase(orderID, req.Status)

	c.JSON(http.StatusOK, gin.H{
		"message": "Status updated",
		"status":  req.Status,
	})
}

// Assign - POST /api/v1/orders/:id/assign
func (h *Handler) Assign(c *gin.Context) {
	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid order ID"})
		return
	}

	var req struct {
		PickerID     *string `json:"picker_id"`
		PackerID     *string `json:"packer_id"`
		DispatcherID *string `json:"dispatcher_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.PickerID != nil {
		pickerID, _ := uuid.Parse(*req.PickerID)
		h.db.Exec(`UPDATE wms.warehouse_orders SET picker_id = $1, status = 'picking_assigned', updated_at = NOW() WHERE id = $2`,
			pickerID, orderID)

		// Create picking task
		h.db.Exec(`
			INSERT INTO wms.warehouse_pickings (warehouse_order_id, picker_id, status)
			VALUES ($1, $2, 'assigned')`, orderID, pickerID)

		go h.sendPickerNotification(pickerID, orderID)
	}

	if req.PackerID != nil {
		packerID, _ := uuid.Parse(*req.PackerID)
		h.db.Exec(`UPDATE wms.warehouse_orders SET packer_id = $1, status = 'packing_assigned', updated_at = NOW() WHERE id = $2`,
			packerID, orderID)
	}

	if req.DispatcherID != nil {
		dispatcherID, _ := uuid.Parse(*req.DispatcherID)
		h.db.Exec(`UPDATE wms.warehouse_orders SET dispatcher_id = $1, updated_at = NOW() WHERE id = $2`,
			dispatcherID, orderID)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Assigned successfully"})
}

// ReceiveWebhook - POST /api/v1/orders/webhook (called by ShopEase backend)
func (h *Handler) ReceiveWebhook(c *gin.Context) {
    secret := c.GetHeader("X-WMS-Secret")
    if secret != os.Getenv("WMS_SHOPEASE_SECRET") {
        c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid secret"})
        return
    }

    var payload struct {
        Event string          `json:"event"`
        Data  json.RawMessage `json:"data"`
    }

    if err := c.ShouldBindJSON(&payload); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    var inner struct {
        ID      string `json:"id"`
        OrderID string `json:"order_id"`
    }
    json.Unmarshal(payload.Data, &inner)

    idStr := inner.ID
    if idStr == "" {
        idStr = inner.OrderID
    }
    orderID, _ := uuid.Parse(idStr)

    switch payload.Event {
    case "order.created":
        go h.handleNewOrder(orderID, payload.Data)
    case "order.cancelled":
        go h.handleCancelledOrder(orderID)
    case "return.created":
        go h.handleNewReturn(orderID, payload.Data)
    }

	c.JSON(http.StatusOK, gin.H{"received": true})
}

// ListWarehouses - GET /api/v1/warehouses
func (h *Handler) ListWarehouses(c *gin.Context) {
	rows, err := h.db.Query(`SELECT id, name, code, address, city, status FROM wms.warehouses ORDER BY name`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch warehouses"})
		return
	}
	defer rows.Close()

	var warehouses []models.Warehouse
	for rows.Next() {
		var w models.Warehouse
		rows.Scan(&w.ID, &w.Name, &w.Code, &w.Address, &w.City, &w.Status)
		warehouses = append(warehouses, w)
	}
	c.JSON(http.StatusOK, gin.H{"warehouses": warehouses})
}

// CreateWarehouse - POST /api/v1/warehouses
func (h *Handler) CreateWarehouse(c *gin.Context) {
	var w models.Warehouse
	if err := c.ShouldBindJSON(&w); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := h.db.QueryRow(`
		INSERT INTO wms.warehouses (name, code, address, city, state, pincode, capacity_sqft)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		w.Name, w.Code, w.Address, w.City, w.State, w.Pincode, w.CapacitySqft,
	).Scan(&w.ID)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create warehouse"})
		return
	}

	c.JSON(http.StatusCreated, w)
}

// Internal helpers

func (h *Handler) handleNewOrder(orderID uuid.UUID, data json.RawMessage) {
	// Parse order data and determine best warehouse to assign
	// Select warehouse with highest available stock for these products
	// Insert into wms.warehouse_orders
	// Reserve inventory
	h.db.Exec(`
		INSERT INTO wms.warehouse_orders (shopease_order_id, warehouse_id, status)
		SELECT $1, id, 'received'
		FROM wms.warehouses WHERE status = 'active' LIMIT 1
		ON CONFLICT (shopease_order_id) DO NOTHING`, orderID)
}

func (h *Handler) handleCancelledOrder(orderID uuid.UUID) {
	// Release reserved inventory
	h.db.Exec(`
		UPDATE wms.warehouse_orders
		SET status = 'cancelled', updated_at = NOW()
		WHERE shopease_order_id = $1`, orderID)
}

func (h *Handler) handleNewReturn(orderID uuid.UUID, data json.RawMessage) {
	// Create return record and alert QC team
}

func (h *Handler) notifyShopEase(orderID uuid.UUID, status string) {
	// POST to ShopEase backend webhook endpoint
	// shopease-backend.onrender.com/api/warehouse/order-status
}

func (h *Handler) sendPickerNotification(pickerID, orderID uuid.UUID) {
	// Send FCM push notification to picker's device
}


