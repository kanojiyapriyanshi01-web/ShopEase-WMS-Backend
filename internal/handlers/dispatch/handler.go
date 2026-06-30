package dispatch

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

func (h *Handler) PendingShipments(c *gin.Context) {
	claims := middleware.GetClaims(c)

	query := `
		SELECT id, shopease_order_id, warehouse_id, status, created_at
		FROM wms.warehouse_orders WHERE status = 'packed'`
	args := []interface{}{}

	if claims.WarehouseID != nil {
		query += ` AND warehouse_id = $1`
		args = append(args, *claims.WarehouseID)
	}
	query += ` ORDER BY created_at ASC`

	rows, err := h.db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch"})
		return
	}
	defer rows.Close()

	var orders []models.WarehouseOrder
	for rows.Next() {
		var o models.WarehouseOrder
		rows.Scan(&o.ID, &o.ShopeaseOrderID, &o.WarehouseID, &o.Status, &o.CreatedAt)
		orders = append(orders, o)
	}
	c.JSON(http.StatusOK, gin.H{"orders": orders})
}

func (h *Handler) CreateShipment(c *gin.Context) {
	claims := middleware.GetClaims(c)

	var req struct {
		WarehouseOrderID string  `json:"warehouse_order_id" binding:"required"`
		Courier          string  `json:"courier" binding:"required"`
		WeightKg         float64 `json:"weight_kg" binding:"required"`
		BoxSize          string  `json:"box_size"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	orderID, err := uuid.Parse(req.WarehouseOrderID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid order id"})
		return
	}

	// Generate AWB - in production, call actual courier API (Delhivery/Shiprocket)
	awb := fmt.Sprintf("%s%d", strUpper(req.Courier[:3]), time.Now().UnixNano()%1000000000)
	trackingURL := fmt.Sprintf("https://track.%s.com/%s", req.Courier, awb)

	tx, _ := h.db.Begin()
	defer tx.Rollback()

	var shipmentID uuid.UUID
	err = tx.QueryRow(`
		INSERT INTO wms.warehouse_shipments
			(warehouse_order_id, dispatcher_id, courier, awb_number, tracking_url, weight_kg, box_size, shipped_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), 'shipped')
		RETURNING id`,
		orderID, claims.UserID, req.Courier, awb, trackingURL, req.WeightKg, req.BoxSize,
	).Scan(&shipmentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create shipment: " + err.Error()})
		return
	}

	tx.Exec(`
		UPDATE wms.warehouse_orders
		SET status = 'shipped', dispatcher_id = $1, shipped_at = NOW(), updated_at = NOW()
		WHERE id = $2`, claims.UserID, orderID)

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "transaction failed"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"shipment_id":  shipmentID,
		"awb_number":   awb,
		"tracking_url": trackingURL,
		"message":      "shipment created, order marked shipped",
	})
}

func (h *Handler) ShipmentDetail(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var s models.WarehouseShipment
	err = h.db.QueryRow(`
		SELECT id, warehouse_order_id, courier, awb_number, tracking_url, weight_kg, box_size, shipped_at, status, created_at
		FROM wms.warehouse_shipments WHERE id = $1`, id).Scan(
		&s.ID, &s.WarehouseOrderID, &s.Courier, &s.AWBNumber, &s.TrackingURL, &s.WeightKg, &s.BoxSize, &s.ShippedAt, &s.Status, &s.CreatedAt,
	)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "shipment not found"})
		return
	}
	c.JSON(http.StatusOK, s)
}

func (h *Handler) DailyManifest(c *gin.Context) {
	rows, err := h.db.Query(`
		SELECT s.awb_number, s.courier, s.box_size, s.weight_kg, wo.shopease_order_id
		FROM wms.warehouse_shipments s
		JOIN wms.warehouse_orders wo ON wo.id = s.warehouse_order_id
		WHERE DATE(s.shipped_at) = CURRENT_DATE
		ORDER BY s.shipped_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch manifest"})
		return
	}
	defer rows.Close()

	var manifest []map[string]interface{}
	for rows.Next() {
		var awb, courier, boxSize string
		var weight float64
		var orderID uuid.UUID
		rows.Scan(&awb, &courier, &boxSize, &weight, &orderID)
		manifest = append(manifest, map[string]interface{}{
			"awb": awb, "courier": courier, "box_size": boxSize, "weight_kg": weight, "order_id": orderID,
		})
	}
	c.JSON(http.StatusOK, gin.H{"manifest": manifest, "date": time.Now().Format("2006-01-02")})
}

// CourierWebhook - receives delivery status updates from courier partners
func (h *Handler) CourierWebhook(c *gin.Context) {
	var payload struct {
		AWBNumber string `json:"awb_number" binding:"required"`
		Status    string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var warehouseOrderID uuid.UUID
	err := h.db.QueryRow(`SELECT warehouse_order_id FROM wms.warehouse_shipments WHERE awb_number = $1`, payload.AWBNumber).
		Scan(&warehouseOrderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "shipment not found"})
		return
	}

	h.db.Exec(`UPDATE wms.warehouse_shipments SET status = $1, updated_at = NOW() WHERE awb_number = $2`,
		payload.Status, payload.AWBNumber)

	if payload.Status == "delivered" {
		h.db.Exec(`UPDATE wms.warehouse_orders SET status = 'delivered', delivered_at = NOW(), updated_at = NOW() WHERE id = $1`,
			warehouseOrderID)
	}

	c.JSON(http.StatusOK, gin.H{"received": true})
}

func strUpper(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 32
		}
	}
	return string(b)
}
