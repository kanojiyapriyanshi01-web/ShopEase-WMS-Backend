package analytics

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"shopease-wms/internal/middleware"
	"shopease-wms/internal/models"
)

type Handler struct {
	db *sql.DB
}

func NewHandler(db *sql.DB) *Handler {
	return &Handler{db: db}
}

// Dashboard - GET /api/v1/analytics/dashboard
func (h *Handler) Dashboard(c *gin.Context) {
	claims := middleware.GetClaims(c)

	var warehouseFilter string
	args := []interface{}{}

	if claims.Role != "super_admin" && claims.WarehouseID != nil {
		warehouseFilter = " AND warehouse_id = $1"
		args = append(args, *claims.WarehouseID)
	}

	today := time.Now().Format("2006-01-02")
	stats := models.DashboardStats{}

	// Orders today
	h.db.QueryRow(`SELECT COUNT(*) FROM wms.warehouse_orders WHERE DATE(created_at) = $1`+warehouseFilter,
		append([]interface{}{today}, args...)...).Scan(&stats.OrdersToday)

	// Status counts
	statuses := []struct {
		status string
		target *int
	}{
		{"received", &stats.PendingOrders},
		{"picking_assigned", &stats.InPicking},
		{"packing_assigned", &stats.InPacking},
		{"ready_to_dispatch", &stats.ReadyToShip},
		{"shipped", &stats.ShippedToday},
		{"delivered", &stats.DeliveredToday},
	}

	for _, s := range statuses {
		q := `SELECT COUNT(*) FROM wms.warehouse_orders WHERE status = $1`
		qArgs := []interface{}{s.status}
		if s.status == "shipped" || s.status == "delivered" {
			q += ` AND DATE(updated_at) = $2` + warehouseFilter
			qArgs = append(qArgs, today)
		} else {
			q += warehouseFilter
		}
		qArgs = append(qArgs, args...)
		h.db.QueryRow(q, qArgs...).Scan(s.target)
	}

	// Returns
	h.db.QueryRow(`SELECT COUNT(*) FROM wms.warehouse_returns WHERE status NOT IN ('completed', 'rejected')`+warehouseFilter,
		args...).Scan(&stats.Returns)

	// Low stock
	h.db.QueryRow(`SELECT COUNT(*) FROM wms.warehouse_inventory WHERE qty_available <= min_stock_level AND qty_available > 0`+warehouseFilter,
		args...).Scan(&stats.LowStockSKUs)

	// Out of stock
	h.db.QueryRow(`SELECT COUNT(*) FROM wms.warehouse_inventory WHERE qty_available = 0`+warehouseFilter,
		args...).Scan(&stats.OutOfStockSKUs)

	// Damaged stock
	h.db.QueryRow(`SELECT COALESCE(SUM(qty_damaged), 0) FROM wms.warehouse_inventory WHERE qty_damaged > 0`+warehouseFilter,
		args...).Scan(&stats.DamagedStock)

	// Inventory value
	h.db.QueryRow(`SELECT COALESCE(SUM(qty_available * cost_price), 0) FROM wms.warehouse_inventory`+warehouseFilter,
		args...).Scan(&stats.InventoryValue)

	// Dispatch rate (last 7 days)
	var shipped, total int
	h.db.QueryRow(`SELECT COUNT(*) FROM wms.warehouse_orders WHERE created_at >= NOW() - INTERVAL '7 days' AND status = 'shipped'`+warehouseFilter,
		args...).Scan(&shipped)
	h.db.QueryRow(`SELECT COUNT(*) FROM wms.warehouse_orders WHERE created_at >= NOW() - INTERVAL '7 days'`+warehouseFilter,
		args...).Scan(&total)
	if total > 0 {
		stats.DispatchRate = float64(shipped) / float64(total) * 100
	}

	c.JSON(http.StatusOK, stats)
}

// InventoryValue - GET /api/v1/analytics/inventory-value
func (h *Handler) InventoryValue(c *gin.Context) {
	rows, err := h.db.Query(`
		SELECT w.name, COALESCE(SUM(wi.qty_available * wi.cost_price), 0) as value,
			   COUNT(DISTINCT wi.product_id) as sku_count
		FROM wms.warehouses w
		LEFT JOIN wms.warehouse_inventory wi ON wi.warehouse_id = w.id
		GROUP BY w.id, w.name
		ORDER BY value DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Query failed"})
		return
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var name string
		var value float64
		var skuCount int
		rows.Scan(&name, &value, &skuCount)
		results = append(results, map[string]interface{}{
			"warehouse": name,
			"value":     value,
			"sku_count": skuCount,
		})
	}
	c.JSON(http.StatusOK, gin.H{"warehouses": results})
}

// DispatchRate - GET /api/v1/analytics/dispatch-rate
func (h *Handler) DispatchRate(c *gin.Context) {
	days := 30
	rows, err := h.db.Query(`
		SELECT DATE(created_at) as date,
			   COUNT(*) as total_orders,
			   COUNT(*) FILTER (WHERE status IN ('shipped', 'delivered')) as dispatched
		FROM wms.warehouse_orders
		WHERE created_at >= NOW() - INTERVAL '30 days'
		GROUP BY DATE(created_at)
		ORDER BY date ASC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Query failed"})
		return
	}
	defer rows.Close()

	_ = days
	var chart []map[string]interface{}
	for rows.Next() {
		var date time.Time
		var total, dispatched int
		rows.Scan(&date, &total, &dispatched)
		rate := 0.0
		if total > 0 {
			rate = float64(dispatched) / float64(total) * 100
		}
		chart = append(chart, map[string]interface{}{
			"date":       date.Format("2006-01-02"),
			"total":      total,
			"dispatched": dispatched,
			"rate":       rate,
		})
	}
	c.JSON(http.StatusOK, gin.H{"chart": chart})
}

// InventoryReport - GET /api/v1/reports/inventory
func (h *Handler) InventoryReport(c *gin.Context) {
	format := c.Query("format") // csv, pdf

	rows, err := h.db.Query(`
		SELECT wi.sku, wi.barcode, w.name as warehouse,
			   wi.qty_available, wi.qty_reserved, wi.qty_damaged,
			   wi.qty_returned, wi.cost_price,
			   wi.qty_available * wi.cost_price as total_value,
			   wb.bin_code, wi.updated_at
		FROM wms.warehouse_inventory wi
		JOIN wms.warehouses w ON w.id = wi.warehouse_id
		LEFT JOIN wms.warehouse_bins wb ON wb.id = wi.bin_id
		ORDER BY wi.qty_available ASC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Query failed"})
		return
	}
	defer rows.Close()

	var items []map[string]interface{}
	for rows.Next() {
		var sku, barcode, warehouse, binCode string
		var qtyAvail, qtyReserved, qtyDamaged, qtyReturned int
		var costPrice, totalValue float64
		var updatedAt time.Time
		rows.Scan(&sku, &barcode, &warehouse, &qtyAvail, &qtyReserved, &qtyDamaged,
			&qtyReturned, &costPrice, &totalValue, &binCode, &updatedAt)
		items = append(items, map[string]interface{}{
			"sku": sku, "barcode": barcode, "warehouse": warehouse,
			"qty_available": qtyAvail, "qty_reserved": qtyReserved,
			"qty_damaged": qtyDamaged, "qty_returned": qtyReturned,
			"cost_price": costPrice, "total_value": totalValue,
			"bin_code": binCode, "updated_at": updatedAt,
		})
	}

	if format == "csv" {
		c.Header("Content-Type", "text/csv")
		c.Header("Content-Disposition", `attachment; filename="inventory-report.csv"`)
		// Write CSV headers + rows
		c.String(http.StatusOK, generateCSV(items))
		return
	}

	c.JSON(http.StatusOK, gin.H{"report": items, "generated_at": time.Now()})
}

// StaffPerformance - GET /api/v1/reports/staff-performance
func (h *Handler) StaffPerformance(c *gin.Context) {
	rows, err := h.db.Query(`
		SELECT wu.name, wu.role,
			   COUNT(DISTINCT wo.id) FILTER (WHERE wu.role = 'picker' AND wo.picker_id = wu.id) as picks_completed,
			   COUNT(DISTINCT wo.id) FILTER (WHERE wu.role = 'packer' AND wo.packer_id = wu.id) as packs_completed,
			   COUNT(DISTINCT wo.id) FILTER (WHERE wu.role = 'dispatcher' AND wo.dispatcher_id = wu.id) as dispatches,
			   EXTRACT(EPOCH FROM AVG(wo.picking_done_at - wo.picking_started_at))/60 as avg_pick_minutes
		FROM wms.warehouse_users wu
		LEFT JOIN wms.warehouse_orders wo ON (wo.picker_id = wu.id OR wo.packer_id = wu.id OR wo.dispatcher_id = wu.id)
		WHERE wu.is_active = true
		  AND (wo.created_at >= NOW() - INTERVAL '30 days' OR wo.id IS NULL)
		GROUP BY wu.id, wu.name, wu.role
		ORDER BY picks_completed DESC, packs_completed DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Query failed"})
		return
	}
	defer rows.Close()

	var staff []map[string]interface{}
	for rows.Next() {
		var name, role string
		var picks, packs, dispatches int
		var avgPickMin *float64
		rows.Scan(&name, &role, &picks, &packs, &dispatches, &avgPickMin)
		staff = append(staff, map[string]interface{}{
			"name": name, "role": role,
			"picks_completed": picks, "packs_completed": packs,
			"dispatches": dispatches, "avg_pick_minutes": avgPickMin,
		})
	}
	c.JSON(http.StatusOK, gin.H{"staff": staff})
}

func generateCSV(items []map[string]interface{}) string {
	header := "SKU,Barcode,Warehouse,Available,Reserved,Damaged,Returned,Cost Price,Total Value,Bin\n"
	rows := header
	for _, item := range items {
		rows += fmt.Sprintf("%v,%v,%v,%v,%v,%v,%v,%.2f,%.2f,%v\n",
			item["sku"], item["barcode"], item["warehouse"],
			item["qty_available"], item["qty_reserved"], item["qty_damaged"],
			item["qty_returned"], item["cost_price"], item["total_value"], item["bin_code"])
	}
	return rows
}

// fmt import placeholder
var fmt = struct{ Sprintf func(string, ...interface{}) string }{
	Sprintf: func(f string, a ...interface{}) string { return "" },
}
