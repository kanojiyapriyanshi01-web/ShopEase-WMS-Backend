package middleware

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"shopease-wms/internal/models"
)

const ClaimsKey = "wms_claims"

// JWTAuth validates the WMS JWT token
func JWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header required"})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid authorization format"})
			c.Abort()
			return
		}

		tokenStr := parts[1]
		secret := os.Getenv("WMS_JWT_SECRET")
		if secret == "" {
			secret = "wms-dev-secret-change-in-production"
		}

		token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(secret), nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}

		mapClaims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token claims"})
			c.Abort()
			return
		}

		claims := &models.WMSClaims{}
		if uid, ok := mapClaims["user_id"].(string); ok {
			claims.UserID, _ = uuid.Parse(uid)
		}
		if wid, ok := mapClaims["warehouse_id"].(string); ok {
			parsed, err := uuid.Parse(wid)
			if err == nil {
				claims.WarehouseID = &parsed
			}
		}
		claims.Role = mapClaims["role"].(string)
		claims.Email = mapClaims["email"].(string)

		c.Set(ClaimsKey, claims)
		c.Next()
	}
}

// RequireRole restricts access to specific roles
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := GetClaims(c)
		if claims == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Not authenticated"})
			c.Abort()
			return
		}

		for _, role := range roles {
			if claims.Role == role {
				c.Next()
				return
			}
		}

		c.JSON(http.StatusForbidden, gin.H{
			"error":          "Access denied",
			"required_roles": roles,
			"your_role":      claims.Role,
		})
		c.Abort()
	}
}

// GetClaims extracts WMS claims from gin context
func GetClaims(c *gin.Context) *models.WMSClaims {
	if v, exists := c.Get(ClaimsKey); exists {
		if claims, ok := v.(*models.WMSClaims); ok {
			return claims
		}
	}
	return nil
}

// CORS allows cross-origin requests from WMS Flutter app and admin panel
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS,PATCH")
		c.Header("Access-Control-Allow-Headers", "Content-Type,Authorization")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
