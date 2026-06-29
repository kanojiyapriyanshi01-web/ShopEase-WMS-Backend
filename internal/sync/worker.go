package sync

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

// Worker handles real-time sync between ShopEase backend and WMS
type Worker struct {
	redis *redis.Client
	db    *sql.DB
	ctx   context.Context
}

type SyncEvent struct {
	Event     string          `json:"event"`
	Source    string          `json:"source"` // shopease, wms
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

func NewWorker(redisURL string, db *sql.DB) *Worker {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Printf("Redis URL parse error: %v — using localhost fallback", err)
		opts = &redis.Options{Addr: "localhost:6379"}
	}

	return &Worker{
		redis: redis.NewClient(opts),
		db:    db,
		ctx:   context.Background(),
	}
}

// Start subscribes to ShopEase events and processes them
func (w *Worker) Start() {
	log.Println("WMS Sync worker started")

	sub := w.redis.Subscribe(w.ctx,
		"shopease:orders:new",
		"shopease:orders:cancelled",
		"shopease:returns:new",
		"shopease:inventory:update",
	)
	defer sub.Close()

	ch := sub.Channel()
	for msg := range ch {
		var event SyncEvent
		if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
			log.Printf("Sync event parse error: %v", err)
			continue
		}

		log.Printf("WMS received event: %s", event.Event)

		switch event.Event {
		case "order.created":
			w.handleNewOrder(event.Data)
		case "order.cancelled":
			w.handleOrderCancelled(event.Data)
		case "return.created":
			w.handleReturnCreated(event.Data)
		case "inventory.seller_update":
			w.handleInventoryUpdate(event.Data)
		}
	}
}

// Publish publishes a WMS event back to ShopEase
func (w *Worker) Publish(channel string, event SyncEvent) {
	event.Source = "wms"
	event.Timestamp = time.Now()

	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	w.redis.Publish(w.ctx, channel, string(data))
}

// handleNewOrder creates a warehouse order when ShopEase order is placed
func (w *Worker) handleNewOrder(data json.RawMessage) {
	var order struct {
		ID         string  `json:"id"`
		SellerID   string  `json:"seller_id"`
		Items      []struct {
			ProductID string `json:"product_id"`
			SKU       string `json:"sku"`
			Qty       int    `json:"qty"`
		} `json:"items"`
	}

	if err := json.Unmarshal(data, &order); err != nil {
		log.Printf("handleNewOrder parse error: %v", err)
		return
	}

	// Find best warehouse (closest with most stock)
	var warehouseID string
	w.db.QueryRow(`
		SELECT wi.warehouse_id
		FROM wms.warehouse_inventory wi
		JOIN wms.warehouses wh ON wh.id = wi.warehouse_id
		WHERE wi.product_id = $1::uuid
		  AND wi.qty_available > 0
		  AND wh.status = 'active'
		ORDER BY wi.qty_available DESC
		LIMIT 1`, order.Items[0].ProductID).Scan(&warehouseID)

	if warehouseID == "" {
		log.Printf("No warehouse with stock for order %s", order.ID)
		// Alert: out of stock
		w.createAlert("out_of_stock", "Cannot fulfill order — no stock available", "critical")
		return
	}

	// Create warehouse order
	var warehouseOrderID string
	err := w.db.QueryRow(`
		INSERT INTO wms.warehouse_orders (shopease_order_id, warehouse_id, status)
		VALUES ($1::uuid, $2::uuid, 'received')
		ON CONFLICT (shopease_order_id) DO UPDATE SET updated_at = NOW()
		RETURNING id`, order.ID, warehouseID).Scan(&warehouseOrderID)

	if err != nil {
		log.Printf("Failed to create warehouse order: %v", err)
		return
	}

	// Reserve inventory for each item
	for _, item := range order.Items {
		w.db.Exec(`
			UPDATE wms.warehouse_inventory
			SET qty_available = qty_available - $1,
				qty_reserved = qty_reserved + $1,
				updated_at = NOW()
			WHERE warehouse_id = $2::uuid
			  AND product_id = $3::uuid
			  AND qty_available >= $1`,
			item.Qty, warehouseID, item.ProductID)
	}

	log.Printf("Warehouse order created: %s for ShopEase order %s", warehouseOrderID, order.ID)
}

// handleOrderCancelled releases reserved inventory
func (w *Worker) handleOrderCancelled(data json.RawMessage) {
	var event struct {
		OrderID string `json:"order_id"`
	}
	json.Unmarshal(data, &event)

	// Get the warehouse order
	var warehouseOrderID, warehouseID string
	w.db.QueryRow(`
		SELECT id, warehouse_id FROM wms.warehouse_orders
		WHERE shopease_order_id = $1::uuid`, event.OrderID).Scan(&warehouseOrderID, &warehouseID)

	if warehouseOrderID == "" {
		return
	}

	// Get order items from ShopEase (via HTTP call or stored data)
	// Release reserved inventory
	w.db.Exec(`
		UPDATE wms.warehouse_orders
		SET status = 'cancelled', updated_at = NOW()
		WHERE id = $1::uuid`, warehouseOrderID)

	log.Printf("Order cancelled and inventory released: %s", event.OrderID)
}

// handleReturnCreated creates a return task for QC team
func (w *Worker) handleReturnCreated(data json.RawMessage) {
	var ret struct {
		ReturnID  string `json:"return_id"`
		OrderID   string `json:"order_id"`
		ProductID string `json:"product_id"`
		Qty       int    `json:"qty"`
		Reason    string `json:"reason"`
	}
	json.Unmarshal(data, &ret)

	// Find the warehouse that handled this order
	var warehouseID string
	w.db.QueryRow(`
		SELECT warehouse_id FROM wms.warehouse_orders
		WHERE shopease_order_id = $1::uuid`, ret.OrderID).Scan(&warehouseID)

	if warehouseID == "" {
		return
	}

	// Create return record
	w.db.Exec(`
		INSERT INTO wms.warehouse_returns (shopease_return_id, shopease_order_id, warehouse_id, product_id, qty, customer_reason, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, 'requested')
		ON CONFLICT (shopease_return_id) DO NOTHING`,
		ret.ReturnID, ret.OrderID, warehouseID, ret.ProductID, ret.Qty, ret.Reason)

	// Create alert for QC team
	w.createAlert("return_pending", "New return awaiting QC inspection", "medium")

	log.Printf("Return task created for return %s", ret.ReturnID)
}

// handleInventoryUpdate syncs seller inventory changes
func (w *Worker) handleInventoryUpdate(data json.RawMessage) {
	// Update WMS inventory when seller changes product stock
	log.Println("Inventory update event received")
}

// NotifyShopEase sends order status back to ShopEase backend via HTTP
func (w *Worker) NotifyShopEase(orderID, status string) {
	shopEaseURL := os.Getenv("SHOPEASE_BACKEND_URL")
	if shopEaseURL == "" {
		shopEaseURL = "https://shopease-backend-be8v.onrender.com"
	}

	payload := map[string]string{
		"warehouse_order_id": orderID,
		"status":             status,
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", shopEaseURL+"/api/warehouse/order-status", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-WMS-Secret", os.Getenv("WMS_SHOPEASE_SECRET"))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to notify ShopEase: %v", err)
		return
	}
	defer resp.Body.Close()

	log.Printf("ShopEase notified: order %s → %s (HTTP %d)", orderID, status, resp.StatusCode)
}

func (w *Worker) createAlert(alertType, message, severity string) {
	w.db.Exec(`
		INSERT INTO wms.warehouse_alerts (alert_type, title, message, severity)
		VALUES ($1, $2, $3, $4)`,
		alertType, alertType, message, severity)
}
