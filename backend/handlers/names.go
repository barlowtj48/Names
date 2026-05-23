package handlers

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/TwiN/go-away"
	"github.com/barlowtj48/names/backend/middlewares"
	"github.com/barlowtj48/names/shared/database"
	"github.com/barlowtj48/names/shared/models"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxNameLength = 80
const minNameLength = 1

// FlagsToHide is the number of distinct voter flags required to move a name
// out of the public list and into the admin review queue.
const FlagsToHide = 3

// Spam patterns: URLs, emails, phone numbers, and the @-handles people use
// as link substitutes. Run case-insensitively against the trimmed text.
var (
	spamURLRe    = regexp.MustCompile(`(?i)\b(?:https?://|www\.)\S+`)
	spamDomainRe = regexp.MustCompile(`(?i)\b[a-z0-9-]+\.(?:com|net|org|io|co|app|dev|gg|xyz|info|biz|us|uk|ca|me|tv|shop|store|site|online|link|click|live|stream)\b`)
	spamEmailRe  = regexp.MustCompile(`(?i)[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}`)
	spamHandleRe = regexp.MustCompile(`(?:^|\s)@[A-Za-z0-9_]{2,}`)
	// Phone: 7+ digits total, allowing common separators ( ) - . space, optional +.
	spamPhoneRe = regexp.MustCompile(`\+?\d[\d\s().-]{6,}\d`)
)

// containsSpam returns a reason if the text looks like a URL, email,
// phone number, or social handle.
func containsSpam(text string) string {
	switch {
	case spamURLRe.MatchString(text):
		return "links aren't allowed"
	case spamEmailRe.MatchString(text):
		return "email addresses aren't allowed"
	case spamDomainRe.MatchString(text):
		return "domain names aren't allowed"
	case spamHandleRe.MatchString(text):
		return "social handles aren't allowed"
	case spamPhoneRe.MatchString(stripNonPhone(text)) && countDigits(text) >= 7:
		return "phone numbers aren't allowed"
	}
	return ""
}

func countDigits(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// stripNonPhone keeps only characters that could form a phone number
// so digit runs separated by words don't accidentally match.
func stripNonPhone(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9',
			r == '+', r == '-', r == ' ', r == '.', r == '(', r == ')':
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return b.String()
}

// NameRow is the projected row returned to clients/templates.
type NameRow struct {
	ID     uint   `json:"id"`
	Text   string `json:"text"`
	Up     int    `json:"up"`
	Down   int    `json:"down"`
	Score  int    `json:"score"`
	MyVote int    `json:"my_vote"` // -1, 0, +1
	MyFlag bool   `json:"my_flag"`
	Status string `json:"status"`
}

func ListNames(c *gin.Context) {
	rows, err := queryNames(c, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if wantsHTML(c) {
		c.HTML(http.StatusOK, "_name_list.html", gin.H{
			"Names": rows,
			"View":  currentView(c),
		})
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
	if reason := containsSpam(text); reason != "" {
		respondError(c, http.StatusBadRequest, reason)
		return
	}

	// Pre-check for an existing (case-insensitive) name so we return a friendly
	// message; the unique index on lower(text) is the authoritative guard.
	var existing models.Name
	if err := database.DB.
		Where("lower(text) = lower(?)", text).
		First(&existing).Error; err == nil {
		respondError(c, http.StatusConflict, "that name has already been submitted")
		return
	}

	voterHash := middlewares.VoterHash(c)
	name := models.Name{
		Text:          text,
		Status:        models.NameStatusActive,
		SubmitterHash: voterHash,
	}
	if err := database.DB.Create(&name).Error; err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			respondError(c, http.StatusConflict, "that name has already been submitted")
			return
		}
		respondError(c, http.StatusInternalServerError, "could not save name")
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

	// Ensure name exists and is votable.
	var n models.Name
	if err := database.DB.First(&n, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "name not found"})
		return
	}
	if n.Status != models.NameStatusActive && n.Status != models.NameStatusOffensive {
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
		c.HTML(http.StatusOK, "_name_row.html", gin.H{"N": row, "View": currentView(c)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Flag records an offensive flag from the current voter. If a name accumulates
// FlagsToHide distinct flags it is moved from "active" to "pending_review" so
// it disappears from the public list and shows up in the admin queue. Names
// already in any non-active state cannot be flagged.
func Flag(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	voterHash := middlewares.VoterHash(c)

	var n models.Name
	if err := database.DB.First(&n, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "name not found"})
		return
	}
	if n.Status != models.NameStatusActive {
		c.JSON(http.StatusConflict, gin.H{"error": "name cannot be flagged"})
		return
	}

	err = database.DB.Transaction(func(tx *gorm.DB) error {
		flag := models.NameFlag{NameID: uint(id), VoterHash: voterHash}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&flag).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&models.NameFlag{}).Where("name_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count >= FlagsToHide {
			if err := tx.Model(&models.Name{}).
				Where("id = ? AND status = ?", id, models.NameStatusActive).
				Update("status", models.NameStatusPendingReview).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if wantsHTML(c) {
		// If the name is still active, return the updated row (now MyFlag=true,
		// so the flag button disappears). If it has been moved to pending_review,
		// return an empty body — HTMX outerHTML swap will remove it.
		row, qerr := queryOneName(c, uint(id))
		if qerr == nil && row.Status == string(models.NameStatusActive) {
			c.HTML(http.StatusOK, "_name_row.html", gin.H{"N": row, "View": currentView(c)})
			return
		}
		c.String(http.StatusOK, "")
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

func queryNames(c *gin.Context, adminAllStatuses bool) ([]NameRow, error) {
	sort := c.DefaultQuery("sort", "top")
	q := strings.TrimSpace(c.Query("q"))
	view := currentView(c)
	window := c.DefaultQuery("window", "all")
	mine := c.Query("mine")
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
		sort = "top"
		orderBy = "score DESC, n.created_at DESC"
	}

	statusWhere := "n.status = 'active'"
	switch {
	case adminAllStatuses:
		statusWhere = "TRUE"
	case view == "offensive":
		statusWhere = "n.status = 'offensive'"
	}

	// Build the LEFT JOIN. For sort=top/controversial we restrict vote rows to
	// the chosen window so the score reflects activity within that period. For
	// sort=new we keep votes all-time (the window applies to name creation).
	cutoff, hasWindow := windowCutoff(window)
	joinClause := "LEFT JOIN votes v ON v.name_id = n.id"
	args := []any{voterHash, voterHash} // my_vote, my_flag
	if hasWindow && sort != "new" {
		joinClause = "LEFT JOIN votes v ON v.name_id = n.id AND v.created_at >= ?"
		args = append(args, cutoff)
	}

	whereExtra := ""
	if q != "" {
		whereExtra += " AND n.text ILIKE ?"
		args = append(args, "%"+q+"%")
	}
	if hasWindow && sort == "new" {
		whereExtra += " AND n.created_at >= ?"
		args = append(args, cutoff)
	}
	if mine == "unvoted" && voterHash != "" {
		whereExtra += " AND NOT EXISTS (SELECT 1 FROM votes uv WHERE uv.name_id = n.id AND uv.voter_hash = ?)"
		args = append(args, voterHash)
	}

	args = append(args, limit, offset)

	sql := `
SELECT n.id, n.text, n.status,
       COALESCE(SUM(CASE WHEN v.value =  1 THEN 1 ELSE 0 END), 0) AS up_count,
       COALESCE(SUM(CASE WHEN v.value = -1 THEN 1 ELSE 0 END), 0) AS down_count,
       COALESCE(SUM(v.value), 0) AS score,
       COALESCE(MAX(CASE WHEN v.voter_hash = ? THEN v.value END), 0) AS my_vote,
       EXISTS (SELECT 1 FROM name_flags f WHERE f.name_id = n.id AND f.voter_hash = ?) AS my_flag
FROM names n
` + joinClause + `
WHERE ` + statusWhere + whereExtra + `
GROUP BY n.id
ORDER BY ` + orderBy + `
LIMIT ? OFFSET ?`

	type scanRow struct {
		ID        uint
		Text      string
		Status    string
		UpCount   int  `gorm:"column:up_count"`
		DownCount int  `gorm:"column:down_count"`
		Score     int
		MyVote    int  `gorm:"column:my_vote"`
		MyFlag    bool `gorm:"column:my_flag"`
	}
	var rows []scanRow
	if err := database.DB.Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]NameRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, NameRow{
			ID: r.ID, Text: r.Text, Status: r.Status,
			Up: r.UpCount, Down: r.DownCount, Score: r.Score,
			MyVote: r.MyVote, MyFlag: r.MyFlag,
		})
	}
	return out, nil
}

func queryOneName(c *gin.Context, id uint) (NameRow, error) {
	voterHash := middlewares.VoterHash(c)
	// Honour the current window for vote counts so a row refreshed after a
	// vote click shows counts consistent with the filtered list. sort=new keeps
	// votes all-time, matching queryNames.
	window := c.DefaultQuery("window", "all")
	sort := c.DefaultQuery("sort", "top")
	cutoff, hasWindow := windowCutoff(window)

	joinClause := "LEFT JOIN votes v ON v.name_id = n.id"
	args := []any{voterHash, voterHash}
	if hasWindow && sort != "new" {
		joinClause = "LEFT JOIN votes v ON v.name_id = n.id AND v.created_at >= ?"
		args = append(args, cutoff)
	}
	args = append(args, id)

	sql := `
SELECT n.id, n.text, n.status,
       COALESCE(SUM(CASE WHEN v.value =  1 THEN 1 ELSE 0 END), 0) AS up_count,
       COALESCE(SUM(CASE WHEN v.value = -1 THEN 1 ELSE 0 END), 0) AS down_count,
       COALESCE(SUM(v.value), 0) AS score,
       COALESCE(MAX(CASE WHEN v.voter_hash = ? THEN v.value END), 0) AS my_vote,
       EXISTS (SELECT 1 FROM name_flags f WHERE f.name_id = n.id AND f.voter_hash = ?) AS my_flag
FROM names n
` + joinClause + `
WHERE n.id = ?
GROUP BY n.id`

	type scanRow struct {
		ID        uint
		Text      string
		Status    string
		UpCount   int  `gorm:"column:up_count"`
		DownCount int  `gorm:"column:down_count"`
		Score     int
		MyVote    int  `gorm:"column:my_vote"`
		MyFlag    bool `gorm:"column:my_flag"`
	}
	var r scanRow
	if err := database.DB.Raw(sql, args...).Scan(&r).Error; err != nil {
		return NameRow{}, err
	}
	return NameRow{
		ID: r.ID, Text: r.Text, Status: r.Status,
		Up: r.UpCount, Down: r.DownCount, Score: r.Score,
		MyVote: r.MyVote, MyFlag: r.MyFlag,
	}, nil
}

// currentView returns "offensive" only when the request explicitly opts in;
// any other value (or absence) is treated as the default "active" view.
func currentView(c *gin.Context) string {
	if c.Query("view") == "offensive" {
		return "offensive"
	}
	return "active"
}

// windowCutoff maps the timeframe filter to a SQL timestamp. The "all" value
// (or any unknown value) returns hasWindow=false so callers can skip the clause.
func windowCutoff(window string) (time.Time, bool) {
	now := time.Now().UTC()
	switch window {
	case "today":
		return now.Add(-24 * time.Hour), true
	case "month":
		return now.AddDate(0, -1, 0), true
	case "year":
		return now.AddDate(-1, 0, 0), true
	default:
		return time.Time{}, false
	}
}
