package handlers

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	goaway "github.com/TwiN/go-away"
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

// defaultPageSize is how many names one list request returns. Clients page
// with ?offset= and the "Load more" button the list template renders when
// HasMore is set.
const defaultPageSize = 50
const maxPageSize = 500

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

// escapeLike escapes the ILIKE wildcards so a search for "100%" or "a_b"
// matches literally instead of acting as a pattern.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// NameRow is the projected row returned to clients/templates.
type NameRow struct {
	ID        uint      `json:"id"`
	Text      string    `json:"text"`
	Up        int       `json:"up"`
	Down      int       `json:"down"`
	Score     int       `json:"score"`
	MyVote    int       `json:"my_vote"` // -1, 0, +1
	MyFlag    bool      `json:"my_flag"`
	Mine      bool      `json:"mine"` // submitted by the current voter
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	// Rank is the 1-based position in the current sort, only populated for
	// list responses under sort=top so the template can show "#3".
	Rank int `json:"rank,omitempty"`
}

// listQuery is the parsed, validated form of the list filter parameters.
type listQuery struct {
	Sort   string
	Q      string
	View   string
	Window string
	Mine   string
	Limit  int
	Offset int
}

func parseListQuery(c *gin.Context) listQuery {
	lq := listQuery{
		Sort:   c.DefaultQuery("sort", "new"),
		Q:      strings.TrimSpace(c.Query("q")),
		View:   currentView(c),
		Window: c.DefaultQuery("window", "all"),
		Mine:   c.Query("mine"),
	}
	switch lq.Sort {
	case "top", "controversial", "new":
	default:
		lq.Sort = "new"
	}
	lq.Limit, _ = strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(defaultPageSize)))
	if lq.Limit <= 0 || lq.Limit > maxPageSize {
		lq.Limit = defaultPageSize
	}
	lq.Offset, _ = strconv.Atoi(c.DefaultQuery("offset", "0"))
	if lq.Offset < 0 {
		lq.Offset = 0
	}
	return lq
}

func ListNames(c *gin.Context) {
	lq := parseListQuery(c)
	rows, hasMore, err := queryNames(c, lq, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if wantsHTML(c) {
		c.HTML(http.StatusOK, "_name_list.html", gin.H{
			"Names":      rows,
			"View":       lq.View,
			"Sort":       lq.Sort,
			"Query":      lq.Q,
			"Offset":     lq.Offset,
			"NextOffset": lq.Offset + len(rows),
			"HasMore":    hasMore,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"names": rows, "has_more": hasMore})
}

func AdminListNames(c *gin.Context) {
	lq := parseListQuery(c)
	rows, hasMore, err := queryNames(c, lq, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"names": rows, "has_more": hasMore})
}

func SubmitName(c *gin.Context) {
	var body struct {
		Text string `form:"text" json:"text"`
	}
	if err := c.ShouldBind(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	// Collapse internal whitespace too, so "Justin  Case" and "Justin Case"
	// are the same submission.
	text := strings.Join(strings.Fields(body.Text), " ")
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
	var dupes int64
	if err := database.DB.Model(&models.Name{}).
		Where("lower(text) = lower(?)", text).
		Count(&dupes).Error; err == nil && dupes > 0 {
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

	BroadcastChange()
	if wantsHTML(c) {
		// names:refresh reloads the list; names:submitted lets the page show
		// a confirmation toast with the accepted text.
		c.Header("HX-Trigger", `{"names:refresh":null,"names:submitted":{"text":`+strconv.Quote(text)+`}}`)
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
		c.JSON(http.StatusGone, gin.H{"error": "this name has been removed"})
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
		// Atomic upsert on the (name_id, voter_hash) unique index so two quick
		// clicks can't race into a duplicate-key error.
		v := models.Vote{NameID: n.ID, VoterHash: voterHash, Value: int8(body.Value)}
		if err := database.DB.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "name_id"}, {Name: "voter_hash"}},
			DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
		}).Create(&v).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	BroadcastChange()
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

	BroadcastChange()
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
	res := database.DB.Model(&models.Name{}).Where("id = ?", id).
		Update("status", models.NameStatusRemoved)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": res.Error.Error()})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "name not found"})
		return
	}
	BroadcastChange()
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

// nameProjection is the shared SELECT that aggregates votes per name for the
// current voter. queryNames wraps it in a subquery so ORDER BY can refer to
// the aggregate aliases (Postgres only allows bare output-column names in
// ORDER BY, not expressions over them).
const nameProjection = `
SELECT n.id, n.text, n.status, n.created_at,
       COALESCE(SUM(CASE WHEN v.value =  1 THEN 1 ELSE 0 END), 0) AS up_count,
       COALESCE(SUM(CASE WHEN v.value = -1 THEN 1 ELSE 0 END), 0) AS down_count,
       COALESCE(SUM(v.value), 0) AS score,
       COALESCE(MAX(CASE WHEN v.voter_hash = ? THEN v.value END), 0) AS my_vote,
       EXISTS (SELECT 1 FROM name_flags f WHERE f.name_id = n.id AND f.voter_hash = ?) AS my_flag,
       (n.submitter_hash <> '' AND n.submitter_hash = ?) AS mine
FROM names n
`

type scanRow struct {
	ID        uint
	Text      string
	Status    string
	CreatedAt time.Time `gorm:"column:created_at"`
	UpCount   int       `gorm:"column:up_count"`
	DownCount int       `gorm:"column:down_count"`
	Score     int
	MyVote    int  `gorm:"column:my_vote"`
	MyFlag    bool `gorm:"column:my_flag"`
	Mine      bool
}

func (r scanRow) toNameRow() NameRow {
	return NameRow{
		ID: r.ID, Text: r.Text, Status: r.Status,
		Up: r.UpCount, Down: r.DownCount, Score: r.Score,
		MyVote: r.MyVote, MyFlag: r.MyFlag, Mine: r.Mine,
		CreatedAt: r.CreatedAt,
	}
}

// voteJoin returns the LEFT JOIN on votes, restricted to the timeframe window
// for sort=top/controversial so the score reflects activity within that
// period. For sort=new votes stay all-time (the window applies to name
// creation instead).
func voteJoin(sort, window string) (string, []any) {
	cutoff, hasWindow := windowCutoff(window)
	if hasWindow && sort != "new" {
		return "LEFT JOIN votes v ON v.name_id = n.id AND v.created_at >= ?", []any{cutoff}
	}
	return "LEFT JOIN votes v ON v.name_id = n.id", nil
}

// queryNames returns one page of names plus whether another page exists.
func queryNames(c *gin.Context, lq listQuery, adminAllStatuses bool) ([]NameRow, bool, error) {
	voterHash := middlewares.VoterHash(c)

	orderBy := ""
	switch lq.Sort {
	case "top":
		orderBy = "score DESC, created_at DESC"
	case "controversial":
		orderBy = "(LEAST(up_count, down_count)::float * LN(up_count + down_count + 1)) DESC, created_at DESC"
	default: // new
		orderBy = "created_at DESC"
	}

	statusWhere := "n.status = 'active'"
	switch {
	case adminAllStatuses:
		statusWhere = "TRUE"
	case lq.View == "offensive":
		statusWhere = "n.status = 'offensive'"
	}

	args := []any{voterHash, voterHash, voterHash} // my_vote, my_flag, mine
	joinClause, joinArgs := voteJoin(lq.Sort, lq.Window)
	args = append(args, joinArgs...)

	whereExtra := ""
	if lq.Q != "" {
		whereExtra += ` AND n.text ILIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(lq.Q)+"%")
	}
	if cutoff, hasWindow := windowCutoff(lq.Window); hasWindow && lq.Sort == "new" {
		whereExtra += " AND n.created_at >= ?"
		args = append(args, cutoff)
	}
	if lq.Mine == "unvoted" && voterHash != "" {
		whereExtra += " AND NOT EXISTS (SELECT 1 FROM votes uv WHERE uv.name_id = n.id AND uv.voter_hash = ?)"
		args = append(args, voterHash)
	}

	// Fetch one extra row to learn whether a further page exists.
	args = append(args, lq.Limit+1, lq.Offset)

	sql := `SELECT * FROM (` + nameProjection + joinClause + `
WHERE ` + statusWhere + whereExtra + `
GROUP BY n.id) s
ORDER BY ` + orderBy + `
LIMIT ? OFFSET ?`

	var rows []scanRow
	if err := database.DB.Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(rows) > lq.Limit
	if hasMore {
		rows = rows[:lq.Limit]
	}
	out := make([]NameRow, 0, len(rows))
	for i, r := range rows {
		nr := r.toNameRow()
		if lq.Sort == "top" {
			nr.Rank = lq.Offset + i + 1
		}
		out = append(out, nr)
	}
	return out, hasMore, nil
}

func queryOneName(c *gin.Context, id uint) (NameRow, error) {
	voterHash := middlewares.VoterHash(c)
	// Honour the current sort/window for vote counts so a row refreshed after
	// a vote click shows counts consistent with the filtered list.
	lq := parseListQuery(c)
	args := []any{voterHash, voterHash, voterHash}
	joinClause, joinArgs := voteJoin(lq.Sort, lq.Window)
	args = append(args, joinArgs...)
	args = append(args, id)

	sql := nameProjection + joinClause + `
WHERE n.id = ?
GROUP BY n.id`

	var r scanRow
	if err := database.DB.Raw(sql, args...).Scan(&r).Error; err != nil {
		return NameRow{}, err
	}
	return r.toNameRow(), nil
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
