package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/barlowtj48/names/backend/middlewares"
	"github.com/barlowtj48/names/shared/database"
	"github.com/barlowtj48/names/shared/models"
	"github.com/gin-gonic/gin"
	"github.com/TwiN/go-away"
)

const maxNameLength = 80
const minNameLength = 1

// NameRow is the projected row returned to clients/templates.
type NameRow struct {
	ID         uint   `json:"id"`
	Text       string `json:"text"`
	Up         int    `json:"up"`
	Down       int    `json:"down"`
	Score      int    `json:"score"`
	MyVote     int    `json:"my_vote"` // -1, 0, +1
	Status     string `json:"status"`
}

func ListNames(c *gin.Context) {
	rows, err := queryNames(c, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if wantsHTML(c) {
		c.HTML(http.StatusOK, "_name_list.html", gin.H{"Names": rows})
		return
	}
	c.JSON(http.StatusOK, gin.H{"names": rows})
}

func AdminListNames(c *gin.Context) {
	rows, err := queryNames(c, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"names": rows})
}

func SubmitName(c *gin.Context) {
	var body struct {
		Text string `form:"text" json:"text"`
	}
	if err := c.ShouldBind(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	text := strings.TrimSpace(body.Text)
	if len(text) < minNameLength || len(text) > maxNameLength {
		respondError(c, http.StatusBadRequest, "name must be 1–80 characters")
		return
	}
	if goaway.IsProfane(text) {
		respondError(c, http.StatusBadRequest, "name rejected by profanity filter")
		return
	}

	voterHash := middlewares.VoterHash(c)
	name := models.Name{
		Text:          text,
		Status:        models.NameStatusActive,
		SubmitterHash: voterHash,
	}
	if err := database.DB.Create(&name).Error; err != nil {
		// Likely unique-violation on lower(text)
		respondError(c, http.StatusConflict, "name already exists")
		return
	}

	if wantsHTML(c) {
		c.Header("HX-Trigger", "names:refresh")
		c.String(http.StatusOK, "")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": name.ID})
}

func Vote(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}

	var body struct {
		Value int `form:"value" json:"value"`
	}
	if err := c.ShouldBind(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	if body.Value != -1 && body.Value != 0 && body.Value != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "value must be -1, 0, or 1"})
		return
	}

	// Ensure name exists and is active.
	var n models.Name
	if err := database.DB.First(&n, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "name not found"})
		return
	}
	if n.Status != models.NameStatusActive {
		c.JSON(http.StatusGone, gin.H{"error": "name removed"})
		return
	}

	voterHash := middlewares.VoterHash(c)

	if body.Value == 0 {
		// Remove vote
		if err := database.DB.
			Where("name_id = ? AND voter_hash = ?", n.ID, voterHash).
			Delete(&models.Vote{}).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	} else {
		// Upsert via find+update or create.
		var existing models.Vote
		err := database.DB.Where("name_id = ? AND voter_hash = ?", n.ID, voterHash).First(&existing).Error
		if err == nil {
			existing.Value = int8(body.Value)
			if err := database.DB.Save(&existing).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		} else {
			v := models.Vote{NameID: n.ID, VoterHash: voterHash, Value: int8(body.Value)}
			if err := database.DB.Create(&v).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
	}

	if wantsHTML(c) {
		row, err := queryOneName(c, n.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.HTML(http.StatusOK, "_name_row.html", gin.H{"N": row})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func DeleteName(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	if err := database.DB.Model(&models.Name{}).Where("id = ?", id).
		Update("status", models.NameStatusRemoved).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// --- helpers ---

func wantsHTML(c *gin.Context) bool {
	return c.GetHeader("HX-Request") != "" ||
		strings.Contains(c.GetHeader("Accept"), "text/html")
}

func respondError(c *gin.Context, status int, msg string) {
	if wantsHTML(c) {
		c.Header("HX-Reswap", "innerHTML")
		c.Header("HX-Retarget", "#submit-error")
		c.String(status, msg)
		return
	}
	c.JSON(status, gin.H{"error": msg})
}

func queryNames(c *gin.Context, includeRemoved bool) ([]NameRow, error) {
	sort := c.DefaultQuery("sort", "top")
	q := strings.TrimSpace(c.Query("q"))
	voterHash := middlewares.VoterHash(c)

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}

	orderBy := ""
	switch sort {
	case "new":
		orderBy = "n.created_at DESC"
	case "controversial":
		orderBy = "(LEAST(up_count, down_count)::float * LN(up_count + down_count + 1)) DESC, n.created_at DESC"
	default: // top
		orderBy = "score DESC, n.created_at DESC"
	}

	statusWhere := "n.status = 'active'"
	if includeRemoved {
		statusWhere = "TRUE"
	}
	args := []any{voterHash}
	searchWhere := ""
	if q != "" {
		searchWhere = " AND n.text ILIKE ?"
		args = append(args, "%"+q+"%")
	}
	args = append(args, limit, offset)

	sql := `
SELECT n.id, n.text, n.status,
       COALESCE(SUM(CASE WHEN v.value =  1 THEN 1 ELSE 0 END), 0) AS up_count,
       COALESCE(SUM(CASE WHEN v.value = -1 THEN 1 ELSE 0 END), 0) AS down_count,
       COALESCE(SUM(v.value), 0) AS score,
       COALESCE(MAX(CASE WHEN v.voter_hash = ? THEN v.value END), 0) AS my_vote
FROM names n
LEFT JOIN votes v ON v.name_id = n.id
WHERE ` + statusWhere + searchWhere + `
GROUP BY n.id
ORDER BY ` + orderBy + `
LIMIT ? OFFSET ?`

	type scanRow struct {
		ID        uint
		Text      string
		Status    string
		UpCount   int `gorm:"column:up_count"`
		DownCount int `gorm:"column:down_count"`
		Score     int
		MyVote    int `gorm:"column:my_vote"`
	}
	var rows []scanRow
	if err := database.DB.Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]NameRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, NameRow{
			ID: r.ID, Text: r.Text, Status: r.Status,
			Up: r.UpCount, Down: r.DownCount, Score: r.Score, MyVote: r.MyVote,
		})
	}
	return out, nil
}

func queryOneName(c *gin.Context, id uint) (NameRow, error) {
	voterHash := middlewares.VoterHash(c)
	sql := `
SELECT n.id, n.text, n.status,
       COALESCE(SUM(CASE WHEN v.value =  1 THEN 1 ELSE 0 END), 0) AS up_count,
       COALESCE(SUM(CASE WHEN v.value = -1 THEN 1 ELSE 0 END), 0) AS down_count,
       COALESCE(SUM(v.value), 0) AS score,
       COALESCE(MAX(CASE WHEN v.voter_hash = ? THEN v.value END), 0) AS my_vote
FROM names n
LEFT JOIN votes v ON v.name_id = n.id
WHERE n.id = ?
GROUP BY n.id`
	type scanRow struct {
		ID        uint
		Text      string
		Status    string
		UpCount   int `gorm:"column:up_count"`
		DownCount int `gorm:"column:down_count"`
		Score     int
		MyVote    int `gorm:"column:my_vote"`
	}
	var r scanRow
	if err := database.DB.Raw(sql, voterHash, id).Scan(&r).Error; err != nil {
		return NameRow{}, err
	}
	return NameRow{
		ID: r.ID, Text: r.Text, Status: r.Status,
		Up: r.UpCount, Down: r.DownCount, Score: r.Score, MyVote: r.MyVote,
	}, nil
}
