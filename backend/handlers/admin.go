package handlers

import (
	"net/http"

	"github.com/barlowtj48/names/backend/middlewares"
	"github.com/barlowtj48/names/shared/secrets"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func AdminLogin(c *gin.Context) {
	var body struct {
		Username string `form:"username" json:"username"`
		Password string `form:"password" json:"password"`
	}
	if err := c.ShouldBind(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	cfg := secrets.Get()
	if body.Username != cfg.AdminUsername {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(cfg.AdminPasswordBcrypt), []byte(body.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	token, err := middlewares.IssueAdminJWT(body.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token error"})
		return
	}

	// Set httpOnly cookie for browser admin page, also return token in JSON.
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("admin_token", token, 12*3600, "/", "", cfg.IsProduction(), true)
	c.JSON(http.StatusOK, gin.H{"token": token})
}

func AdminLogout(c *gin.Context) {
	c.SetCookie("admin_token", "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
