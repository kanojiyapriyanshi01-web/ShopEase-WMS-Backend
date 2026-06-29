package models

import (
	"time"

	"github.com/google/uuid"
)

// ─── Warehouse ───────────────────────────────────────────────────────────────

type Warehouse struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	Name         string     `json:"name" db:"name"`
	Code         string     `json:"code" db:"code"`
	Address      string     `json:"address" db:"address"`
	City         string     `json:"city" db:"city"`
	State        string     `json:"state" db:"state"`
	Pincode      string     `json:"pincode" db:"pincode"`
	ManagerID    *uuid.UUID `json:"manager_id" db:"manager_id"`
	CapacitySqft int        `json:"capacity_sqft" db:"capacity_sqft"`
	Status       string     `json:"status" db:"status"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
}

// ─── Warehouse User ───────────────────────────────────────────────────────────

type WarehouseUser struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	WarehouseID *uuid.UUID `json:"warehouse_id" db:"warehouse_id"`
	Name        string     `json:"name" db:"name"`
	Email       string     `json:"email" db:"email"`
	Phone       string     `json:"phone" db:"phone"`
	Role        string     `json:"role" db:"role"`
	Shift       string     `json:"shift" db:"shift"`
	IsActive    bool       `json:"is_active" db:"is_active"`
	FCMToken    string     `json:"fcm_token,omitempty" db:"fcm_token"`
	LastLoginAt *time.Time `json:"last_login_at" db:"last_login_at"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
}

// ─── Inventory ───────────────────────────────────────────────────────────────

type WarehouseInventory struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	WarehouseID   uuid.UUID  `json:"warehouse_id" db:"warehouse_id"`
	ProductID     uuid.UUID  `json:"product_id" db:"product_id"`
	SellerID      *uuid.UUID `json:"seller_id" db:"seller_id"`
	SKU           string     `json:"sku" db:"sku"`
	Barcode       string     `json:"barcode" db:"barcode"`
	QRCode        string     `json:"qr_code" db:"qr_code"`
	BatchNumber   string     `json:"batch_number" db:"batch_number"`
	BinID         *uuid.UUID `json:"bin_id" db:"bin_id"`
	QtyAvailable  int        `json:"qty_available" db:"qty_available"`
	QtyReserved   int        `json:"qty_reserved" db:"qty_reserved"`
	QtyDamaged    int        `json:"qty_damaged" db:"qty_damaged"`
	QtyReturned   int        `json:"qty_returned" db:"qty_returned"`
	QtyTransit    int        `json:"qty_transit" db:"qty_transit"`
	MinStockLevel int        `json:"min_stock_level" db:"min_stock_level"`
	CostPrice     float64    `json:"cost_price" db:"cost_price"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`

	// Enriched fields (joins)
	BinCode     string `json:"bin_code,omitempty" db:"bin_code"`
	ProductName string `json:"product_name,omitempty" db:"product_name"`
}

// ─── Bin ─────────────────────────────────────────────────────────────────────

type WarehouseBin struct {
	ID            uuid.UUID `json:"id" db:"id"`
	WarehouseID   uuid.UUID `json:"warehouse_id" db:"warehouse_id"`
	Floor         string    `json:"floor" db:"floor"`
	Zone          string    `json:"zone" db:"zone"`
	Rack          string    `json:"rack" db:"rack"`
	Shelf         string    `json:"shelf" db:"shelf"`
	BinCode       string    `json:"bin_code" db:"bin_code"`
	Barcode       string    `json:"barcode" db:"barcode"`
	CapacityUnits int       `json:"capacity_units" db:"capacity_units"`
	CurrentUnits  int       `json:"current_units" db:"current_units"`
	IsActive      bool      `json:"is_active" db:"is_active"`
}

// ─── Warehouse Order ─────────────────────────────────────────────────────────

type WarehouseOrder struct {
	ID                uuid.UUID  `json:"id" db:"id"`
	ShopeaseOrderID   uuid.UUID  `json:"shopease_order_id" db:"shopease_order_id"`
	WarehouseID       uuid.UUID  `json:"warehouse_id" db:"warehouse_id"`
	Status            string     `json:"status" db:"status"`
	Priority          int        `json:"priority" db:"priority"`
	PickerID          *uuid.UUID `json:"picker_id" db:"picker_id"`
	PackerID          *uuid.UUID `json:"packer_id" db:"packer_id"`
	DispatcherID      *uuid.UUID `json:"dispatcher_id" db:"dispatcher_id"`
	PickingStartedAt  *time.Time `json:"picking_started_at" db:"picking_started_at"`
	PickingDoneAt     *time.Time `json:"picking_done_at" db:"picking_done_at"`
	PackingDoneAt     *time.Time `json:"packing_done_at" db:"packing_done_at"`
	ShippedAt         *time.Time `json:"shipped_at" db:"shipped_at"`
	DeliveredAt       *time.Time `json:"delivered_at" db:"delivered_at"`
	Notes             string     `json:"notes" db:"notes"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" db:"updated_at"`
}

// ─── Picking ─────────────────────────────────────────────────────────────────

type PickingItem struct {
	InventoryID uuid.UUID  `json:"inventory_id"`
	SKU         string     `json:"sku"`
	ProductName string     `json:"product_name"`
	BinCode     string     `json:"bin_code"`
	QtyRequired int        `json:"qty_required"`
	QtyPicked   int        `json:"qty_picked"`
	ScannedAt   *time.Time `json:"scanned_at"`
}

type WarehousePicking struct {
	ID               uuid.UUID     `json:"id" db:"id"`
	WarehouseOrderID uuid.UUID     `json:"warehouse_order_id" db:"warehouse_order_id"`
	PickerID         uuid.UUID     `json:"picker_id" db:"picker_id"`
	PickingList      []PickingItem `json:"picking_list"`
	Status           string        `json:"status" db:"status"`
	StartedAt        *time.Time    `json:"started_at" db:"started_at"`
	CompletedAt      *time.Time    `json:"completed_at" db:"completed_at"`
	Notes            string        `json:"notes" db:"notes"`
	CreatedAt        time.Time     `json:"created_at" db:"created_at"`
}

// ─── Packing ─────────────────────────────────────────────────────────────────

type PackingDimensions struct {
	Length float64 `json:"l"`
	Width  float64 `json:"w"`
	Height float64 `json:"h"`
}

type WarehousePacking struct {
	ID               uuid.UUID          `json:"id" db:"id"`
	WarehouseOrderID uuid.UUID          `json:"warehouse_order_id" db:"warehouse_order_id"`
	PackerID         uuid.UUID          `json:"packer_id" db:"packer_id"`
	BoxSize          string             `json:"box_size" db:"box_size"`
	WeightKg         float64            `json:"weight_kg" db:"weight_kg"`
	Dimensions       *PackingDimensions `json:"dimensions_cm"`
	InvoiceURL       string             `json:"invoice_url" db:"invoice_url"`
	LabelURL         string             `json:"label_url" db:"label_url"`
	Status           string             `json:"status" db:"status"`
	CompletedAt      *time.Time         `json:"completed_at" db:"completed_at"`
}

// ─── Shipment ─────────────────────────────────────────────────────────────────

type WarehouseShipment struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	WarehouseOrderID uuid.UUID  `json:"warehouse_order_id" db:"warehouse_order_id"`
	DispatcherID     *uuid.UUID `json:"dispatcher_id" db:"dispatcher_id"`
	Courier          string     `json:"courier" db:"courier"`
	AWBNumber        string     `json:"awb_number" db:"awb_number"`
	TrackingURL      string     `json:"tracking_url" db:"tracking_url"`
	WeightKg         float64    `json:"weight_kg" db:"weight_kg"`
	BoxSize          string     `json:"box_size" db:"box_size"`
	ShippedAt        *time.Time `json:"shipped_at" db:"shipped_at"`
	ExpectedDelivery *time.Time `json:"expected_delivery" db:"expected_delivery"`
	Status           string     `json:"status" db:"status"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
}

// ─── Return ───────────────────────────────────────────────────────────────────

type WarehouseReturn struct {
	ID                uuid.UUID  `json:"id" db:"id"`
	ShopeaseReturnID  uuid.UUID  `json:"shopease_return_id" db:"shopease_return_id"`
	ShopeaseOrderID   uuid.UUID  `json:"shopease_order_id" db:"shopease_order_id"`
	WarehouseID       uuid.UUID  `json:"warehouse_id" db:"warehouse_id"`
	ProductID         uuid.UUID  `json:"product_id" db:"product_id"`
	Qty               int        `json:"qty" db:"qty"`
	CustomerReason    string     `json:"customer_reason" db:"customer_reason"`
	Status            string     `json:"status" db:"status"`
	Condition         string     `json:"condition" db:"condition"`
	QCInspectorID     *uuid.UUID `json:"qc_inspector_id" db:"qc_inspector_id"`
	QCNotes           string     `json:"qc_notes" db:"qc_notes"`
	Resolution        string     `json:"resolution" db:"resolution"` // refund, replacement, rejected
	ReceivedAt        *time.Time `json:"received_at" db:"received_at"`
	QCDoneAt          *time.Time `json:"qc_done_at" db:"qc_done_at"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
}

// ─── Transfer ─────────────────────────────────────────────────────────────────

type StockTransferItem struct {
	InventoryID uuid.UUID `json:"inventory_id"`
	SKU         string    `json:"sku"`
	Qty         int       `json:"qty"`
}

type WarehouseTransfer struct {
	ID              uuid.UUID           `json:"id" db:"id"`
	FromWarehouseID uuid.UUID           `json:"from_warehouse_id" db:"from_warehouse_id"`
	ToWarehouseID   uuid.UUID           `json:"to_warehouse_id" db:"to_warehouse_id"`
	Items           []StockTransferItem `json:"items"`
	Status          string              `json:"status" db:"status"`
	RequestedBy     *uuid.UUID          `json:"requested_by" db:"requested_by"`
	ApprovedBy      *uuid.UUID          `json:"approved_by" db:"approved_by"`
	DispatchedAt    *time.Time          `json:"dispatched_at" db:"dispatched_at"`
	ReceivedAt      *time.Time          `json:"received_at" db:"received_at"`
	Notes           string              `json:"notes" db:"notes"`
	CreatedAt       time.Time           `json:"created_at" db:"created_at"`
}

// ─── Audit ───────────────────────────────────────────────────────────────────

type AuditItem struct {
	InventoryID uuid.UUID `json:"inventory_id"`
	SKU         string    `json:"sku"`
	ExpectedQty int       `json:"expected_qty"`
	ActualQty   int       `json:"actual_qty"`
	Difference  int       `json:"difference"`
	Reason      string    `json:"reason"`
}

type WarehouseAudit struct {
	ID                  uuid.UUID   `json:"id" db:"id"`
	WarehouseID         uuid.UUID   `json:"warehouse_id" db:"warehouse_id"`
	AuditType           string      `json:"audit_type" db:"audit_type"`
	ConductedBy         *uuid.UUID  `json:"conducted_by" db:"conducted_by"`
	AuditItems          []AuditItem `json:"audit_items"`
	TotalDiscrepancies  int         `json:"total_discrepancies" db:"total_discrepancies"`
	Status              string      `json:"status" db:"status"`
	StartedAt           time.Time   `json:"started_at" db:"started_at"`
	CompletedAt         *time.Time  `json:"completed_at" db:"completed_at"`
	Notes               string      `json:"notes" db:"notes"`
}

// ─── GRN ─────────────────────────────────────────────────────────────────────

type GRNItem struct {
	ProductID    uuid.UUID `json:"product_id"`
	SKU          string    `json:"sku"`
	RecvQty      int       `json:"recv_qty"`
	AcceptedQty  int       `json:"accepted_qty"`
	RejectedQty  int       `json:"rejected_qty"`
	Reason       string    `json:"reason"`
}

type WarehouseGRN struct {
	ID            uuid.UUID `json:"id" db:"id"`
	GRNNumber     string    `json:"grn_number" db:"grn_number"`
	WarehouseID   uuid.UUID `json:"warehouse_id" db:"warehouse_id"`
	SellerID      uuid.UUID `json:"seller_id" db:"seller_id"`
	InvoiceNumber string    `json:"invoice_number" db:"invoice_number"`
	BatchNumber   string    `json:"batch_number" db:"batch_number"`
	ReceivedBy    *uuid.UUID `json:"received_by" db:"received_by"`
	Items         []GRNItem `json:"items"`
	Notes         string    `json:"notes" db:"notes"`
	Status        string    `json:"status" db:"status"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}

// ─── Analytics Dashboard ─────────────────────────────────────────────────────

type DashboardStats struct {
	OrdersToday       int     `json:"orders_today"`
	PendingOrders     int     `json:"pending_orders"`
	InPicking         int     `json:"in_picking"`
	InPacking         int     `json:"in_packing"`
	ReadyToShip       int     `json:"ready_to_ship"`
	ShippedToday      int     `json:"shipped_today"`
	DeliveredToday    int     `json:"delivered_today"`
	Returns           int     `json:"returns"`
	Replacements      int     `json:"replacements"`
	LowStockSKUs      int     `json:"low_stock_skus"`
	OutOfStockSKUs    int     `json:"out_of_stock_skus"`
	DamagedStock      int     `json:"damaged_stock"`
	DispatchRate      float64 `json:"dispatch_rate"`
	ReturnRate        float64 `json:"return_rate"`
	InventoryValue    float64 `json:"inventory_value"`
	WarehouseCapacity float64 `json:"warehouse_capacity_pct"`
}

// ─── JWT Claims ──────────────────────────────────────────────────────────────

type WMSClaims struct {
	UserID      uuid.UUID `json:"user_id"`
	WarehouseID *uuid.UUID `json:"warehouse_id"`
	Role        string    `json:"role"`
	Email       string    `json:"email"`
}
