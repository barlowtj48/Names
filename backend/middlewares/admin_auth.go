package middlewares

import (
	"net/http"
	"strings"
	"time"

	"github.com/barlowtj48/names/shared/secrets"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const AdminUsernameKey = "admin_username"

type AdminClaims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

func IssueAdminJWT(username string) (string, error) {
	cfg := secrets.Get()
	claims := AdminClaims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(12 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   username,
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(cfg.AdminJWTSecret))
}

func AdminAuth() gin.HandlerFunc {
	cfg := secrets.Get()
	return func(c *gin.Context) {
		raw := extractToken(c)
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		claims := &AdminClaims{}
		_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(cfg.AdminJWTSecret), nil
		})
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set(AdminUsernameKey, claims.Username)
		c.Next()
	}
}

func extractToken(c *gin.Context) string {
	if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if t, err := c.Cookie("admin_token"); err == nil {
		return t
	}
	return ""
}
