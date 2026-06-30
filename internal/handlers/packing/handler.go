package packing

import (
	"database/sql"
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

func (h *Handler) MyTasks(c *gin.Context) {
	claims := middleware.GetClaims(c)

	rows, err := h.db.Query(`
		SELECT wo.id, wo.warehouse_order_id, wo.status
		FROM wms.warehouse_orders wo
		WHERE wo.packer_id = $1 AND wo.status IN ('packing_assigned', 'packed')
		ORDER BY wo.updated_at ASC`, claims.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch tasks"})
		return
	}
	defer rows.Close()

	var tasks []map[string]interface{}
	for rows.Next() {
		var id, orderID, status string
		rows.Scan(&id, &orderID, &status)
		tasks = append(tasks, map[string]interface{}{"id": id, "warehouse_order_id": orderID, "status": status})
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

func (h *Handler) Complete(c *gin.Context) {
	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	claims := middleware.GetClaims(c)

	var req struct {
		BoxSize  string  `json:"box_size" binding:"required"`
		WeightKg float64 `json:"weight_kg" binding:"required"`
		Length   float64 `json:"length_cm"`
		Width    float64 `json:"width_cm"`
		Height   float64 `json:"height_cm"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dims := models.PackingDimensions{Length: req.Length, Width: req.Width, Height: req.Height}

	tx, _ := h.db.Begin()
	defer tx.Rollback()

	var packingID uuid.UUID
	tx.QueryRow(`
		INSERT INTO wms.warehouse_packings (warehouse_order_id, packer_id, box_size, weight_kg, dimensions_cm, status, completed_at)
		VALUES ($1, $2, $3, $4, $5, 'completed', NOW())
		RETURNING id`,
		orderID, claims.UserID, req.BoxSize, req.WeightKg, dimsJSON(dims),
	).Scan(&packingID)

	tx.Exec(`UPDATE wms.warehouse_orders SET status = 'packed', packing_done_at = NOW(), updated_at = NOW() WHERE id = $1`,
		orderID)

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to complete packing"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"packing_id":    packingID,
		"message":       "packing completed",
		"order_status":  "packed",
	})
}

func (h *Handler) GetLabel(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var labelURL string
	err = h.db.QueryRow(`SELECT COALESCE(label_url, '') FROM wms.warehouse_packings WHERE warehouse_order_id = $1`, id).
		Scan(&labelURL)
	if err == sql.ErrNoRows || labelURL == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "label not generated yet"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"label_url": labelURL})
}

func dimsJSON(d models.PackingDimensions) string {
	return `{"l":` + ftoa(d.Length) + `,"w":` + ftoa(d.Width) + `,"h":` + ftoa(d.Height) + `}`
}

func ftoa(f float64) string {
	if f == float64(int(f)) {
		return itoa(int(f))
	}
	return itoa(int(f))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}
