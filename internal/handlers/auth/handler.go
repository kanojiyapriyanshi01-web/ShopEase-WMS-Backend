package auth

import (
	"database/sql"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"shopease-wms/internal/models"
)

type Handler struct {
	db *sql.DB
}

func NewHandler(db *sql.DB) *Handler {
	return &Handler{db: db}
}

func (h *Handler) Login(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var user models.WarehouseUser
	var passwordHash string
	err := h.db.QueryRow(`
		SELECT id, warehouse_id, name, email, role, password_hash, is_active
		FROM wms.warehouse_users WHERE email = $1`, req.Email).Scan(
		&user.ID, &user.WarehouseID, &user.Name, &user.Email, &user.Role, &passwordHash, &user.IsActive,
	)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	if !user.IsActive {
		c.JSON(http.StatusForbidden, gin.H{"error": "account deactivated"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	token, err := generateToken(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}

	h.db.Exec(`UPDATE wms.warehouse_users SET last_login_at = NOW() WHERE id = $1`, user.ID)

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user":  user,
	})
}

func (h *Handler) RefreshToken(c *gin.Context) {
	var req struct {
		UserID string `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	var user models.WarehouseUser
	err = h.db.QueryRow(`
		SELECT id, warehouse_id, name, email, role, is_active
		FROM wms.warehouse_users WHERE id = $1`, userID).Scan(
		&user.ID, &user.WarehouseID, &user.Name, &user.Email, &user.Role, &user.IsActive,
	)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	token, _ := generateToken(user)
	c.JSON(http.StatusOK, gin.H{"token": token})
}

func (h *Handler) ListStaff(c *gin.Context) {
	rows, err := h.db.Query(`
		SELECT id, warehouse_id, name, email, phone, role, shift, is_active, created_at
		FROM wms.warehouse_users ORDER BY created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch staff"})
		return
	}
	defer rows.Close()

	var staff []models.WarehouseUser
	for rows.Next() {
		var u models.WarehouseUser
		rows.Scan(&u.ID, &u.WarehouseID, &u.Name, &u.Email, &u.Phone, &u.Role, &u.Shift, &u.IsActive, &u.CreatedAt)
		staff = append(staff, u)
	}
	c.JSON(http.StatusOK, gin.H{"staff": staff})
}

func (h *Handler) CreateStaff(c *gin.Context) {
	var req struct {
		WarehouseID string `json:"warehouse_id"`
		Name        string `json:"name" binding:"required"`
		Email       string `json:"email" binding:"required"`
		Phone       string `json:"phone"`
		Role        string `json:"role" binding:"required"`
		Password    string `json:"password" binding:"required"`
		Shift       string `json:"shift"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "password hashing failed"})
		return
	}

	var warehouseID *uuid.UUID
	if req.WarehouseID != "" {
		wid, err := uuid.Parse(req.WarehouseID)
		if err == nil {
			warehouseID = &wid
		}
	}

	var newID uuid.UUID
	err = h.db.QueryRow(`
		INSERT INTO wms.warehouse_users (warehouse_id, name, email, phone, role, password_hash, shift)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		warehouseID, req.Name, req.Email, req.Phone, req.Role, string(hash), req.Shift,
	).Scan(&newID)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create staff: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": newID, "message": "staff created"})
}

func generateToken(user models.WarehouseUser) (string, error) {
	secret := os.Getenv("WMS_JWT_SECRET")
	if secret == "" {
		secret = "wms-dev-secret-change-in-production"
	}

	claims := jwt.MapClaims{
		"user_id": user.ID.String(),
		"role":    user.Role,
		"email":   user.Email,
		"exp":     time.Now().Add(24 * time.Hour).Unix(),
	}
	if user.WarehouseID != nil {
		claims["warehouse_id"] = user.WarehouseID.String()
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}
