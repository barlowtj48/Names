package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/barlowtj48/names/backend/middlewares"
	"github.com/barlowtj48/names/shared/database"
	"github.com/barlowtj48/names/shared/models"
	"github.com/barlowtj48/names/shared/secrets"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
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

// AdminQueueRow is a name awaiting admin review with derived flag stats.
type AdminQueueRow struct {
	ID          uint      `json:"id"`
	Text        string    `json:"text"`
	FlagCount   int64     `json:"flag_count"`
	LastFlagged time.Time `json:"last_flagged"`
	CreatedAt   time.Time `json:"created_at"`
}

func AdminQueue(c *gin.Context) {
	var rows []AdminQueueRow
	err := database.DB.Raw(`
SELECT n.id, n.text, n.created_at,
       COUNT(f.id) AS flag_count,
       MAX(f.created_at) AS last_flagged
FROM names n
JOIN name_flags f ON f.name_id = n.id
WHERE n.status = ?
GROUP BY n.id
ORDER BY MAX(f.created_at) DESC`, models.NameStatusPendingReview).Scan(&rows).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"queue": rows})
}

// AdminDecision applies a moderator decision to a name in the queue.
// dismiss → back to active and the existing flag rows are deleted so prior
// flaggers can re-flag if it offends again. confirm → hidden behind the
// "show offensive" toggle, can never be re-flagged. remove → hard-removed.
func AdminDecision(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	var body struct {
		Action string `form:"action" json:"action"`
	}
	if err := c.ShouldBind(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	var newStatus models.NameStatus
	clearFlags := false
	switch body.Action {
	case "dismiss":
		newStatus = models.NameStatusActive
		clearFlags = true
	case "confirm":
		newStatus = models.NameStatusOffensive
	case "remove":
		newStatus = models.NameStatusRemoved
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "action must be dismiss, confirm, or remove"})
		return
	}

	err = database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Name{}).Where("id = ?", id).
			Update("status", newStatus).Error; err != nil {
			return err
		}
		if clearFlags {
			if err := tx.Where("name_id = ?", id).Delete(&models.NameFlag{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "status": newStatus})
}
