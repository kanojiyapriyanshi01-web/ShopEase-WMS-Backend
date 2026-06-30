package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"shopease-wms/internal/handlers/auth"
	"shopease-wms/internal/db"
	"shopease-wms/internal/handlers/analytics"
	"shopease-wms/internal/handlers/audits"
	"shopease-wms/internal/handlers/dispatch"
	"shopease-wms/internal/handlers/inventory"
	"shopease-wms/internal/handlers/orders"
	"shopease-wms/internal/handlers/packing"
	"shopease-wms/internal/handlers/picking"
	"shopease-wms/internal/handlers/returns"
	"shopease-wms/internal/handlers/transfers"
	"shopease-wms/internal/middleware"
	"shopease-wms/internal/sync"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	database, err := db.Connect(os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("DB connection failed: %v", err)
	}

	if err := db.RunMigrations(database); err != nil {
		log.Fatalf("Migrations failed: %v", err)
	}

	// Start Redis sync worker
	syncWorker := sync.NewWorker(os.Getenv("REDIS_URL"), database)
	go syncWorker.Start()

	r := gin.Default()

	r.Use(middleware.CORS())

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "service": "shopease-wms"})
	})

	// Auth routes (public)
	authHandler := auth.NewHandler(database)
	oh := orders.NewHandler(database)
	r.POST("/api/v1/auth/login", authHandler.Login)
	r.POST("/api/v1/orders/webhook", oh.ReceiveWebhook) // public, secret-verified
	r.POST("/api/v1/auth/refresh", authHandler.RefreshToken)

	// Protected routes
	api := r.Group("/api/v1", middleware.JWTAuth())
	{
		// Orders
		api.GET("/orders", middleware.RequireRole("super_admin", "warehouse_manager", "dispatcher"), oh.List)
		api.GET("/orders/:id", oh.Detail)
		api.PUT("/orders/:id/status", middleware.RequireRole("super_admin", "warehouse_manager"), oh.UpdateStatus)
		api.POST("/orders/:id/assign", middleware.RequireRole("super_admin", "warehouse_manager"), oh.Assign)
	
		// Inventory
		ih := inventory.NewHandler(database)
		api.GET("/inventory", ih.List)
		api.GET("/inventory/:id", ih.Detail)
		api.PUT("/inventory/:id/adjust", middleware.RequireRole("super_admin", "warehouse_manager", "inventory_staff"), ih.Adjust)
		api.GET("/inventory/barcode/:code", ih.LookupBarcode)
		api.POST("/grn", middleware.RequireRole("super_admin", "warehouse_manager", "inventory_staff"), ih.CreateGRN)
		api.GET("/grn", ih.ListGRN)
		api.GET("/bins", ih.ListBins)

		// Picking
		pkh := picking.NewHandler(database)
		api.GET("/picking/my-tasks", middleware.RequireRole("picker"), pkh.MyTasks)
		api.GET("/picking/:id", pkh.Detail)
		api.POST("/picking/:id/scan", middleware.RequireRole("picker"), pkh.ScanItem)
		api.PUT("/picking/:id/complete", middleware.RequireRole("picker"), pkh.Complete)

		// Packing
		pah := packing.NewHandler(database)
		api.GET("/packing/my-tasks", middleware.RequireRole("packer"), pah.MyTasks)
		api.POST("/packing/:id/complete", middleware.RequireRole("packer"), pah.Complete)
		api.GET("/packing/:id/label", pah.GetLabel)

		// Dispatch
		dh := dispatch.NewHandler(database)
		api.GET("/dispatch/pending", middleware.RequireRole("dispatcher", "warehouse_manager"), dh.PendingShipments)
		api.POST("/shipments", middleware.RequireRole("dispatcher"), dh.CreateShipment)
		api.GET("/shipments/:id", dh.ShipmentDetail)
		api.GET("/manifests", dh.DailyManifest)
		api.POST("/shipments/courier-webhook", dh.CourierWebhook)

		// Returns
		rh := returns.NewHandler(database)
		api.GET("/returns", rh.List)
		api.GET("/returns/:id", rh.Detail)
		api.POST("/returns/:id/qc", middleware.RequireRole("qc_inspector", "warehouse_manager"), rh.SubmitQC)
		api.POST("/replacements", middleware.RequireRole("warehouse_manager", "super_admin"), rh.CreateReplacement)

		// Transfers
		th := transfers.NewHandler(database)
		api.POST("/transfers", middleware.RequireRole("warehouse_manager", "super_admin"), th.Create)
		api.PUT("/transfers/:id/approve", middleware.RequireRole("super_admin"), th.Approve)
		api.PUT("/transfers/:id/receive", middleware.RequireRole("warehouse_manager"), th.Receive)

		// Audits
		ah := audits.NewHandler(database)
		api.POST("/audits", middleware.RequireRole("warehouse_manager", "super_admin"), ah.Create)
		api.GET("/audits", ah.List)
		api.POST("/audits/:id/submit", ah.Submit)

		// Analytics
		anh := analytics.NewHandler(database)
		api.GET("/analytics/dashboard", middleware.RequireRole("super_admin", "warehouse_manager"), anh.Dashboard)
		api.GET("/analytics/inventory-value", anh.InventoryValue)
		api.GET("/analytics/dispatch-rate", anh.DispatchRate)
		api.GET("/reports/inventory", anh.InventoryReport)
		api.GET("/reports/staff-performance", anh.StaffPerformance)

		// Warehouse & staff management
		api.GET("/warehouses", middleware.RequireRole("super_admin"), oh.ListWarehouses)
		api.POST("/warehouses", middleware.RequireRole("super_admin"), oh.CreateWarehouse)
		api.GET("/staff", middleware.RequireRole("super_admin", "warehouse_manager"), authHandler.ListStaff)
		api.POST("/staff", middleware.RequireRole("super_admin", "warehouse_manager"), authHandler.CreateStaff)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	log.Printf("ShopEase WMS backend running on :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
