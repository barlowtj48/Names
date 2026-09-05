package handlers

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestContainsSpam(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"Justin Case", ""},
		{"Anita Bath", ""},
		{"Seven Eleven", ""},
		{"Agent 007", ""},
		{"visit https://example.com now", "links aren't allowed"},
		{"www.example.org", "links aren't allowed"},
		{"me@example.com", "email addresses aren't allowed"},
		{"example.com", "domain names aren't allowed"},
		{"follow @someone", "social handles aren't allowed"},
		{"call 555-123-4567", "phone numbers aren't allowed"},
		{"+1 (555) 123 4567", "phone numbers aren't allowed"},
		{"1 2 3 4 5 6", ""}, // only six digits — not a phone number
	}
	for _, tc := range cases {
		if got := containsSpam(tc.text); got != tc.want {
			t.Errorf("containsSpam(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestEscapeLike(t *testing.T) {
	cases := map[string]string{
		"plain":   "plain",
		"100%":    `100\%`,
		"a_b":     `a\_b`,
		`back\sl`: `back\\sl`,
	}
	for in, want := range cases {
		if got := escapeLike(in); got != want {
			t.Errorf("escapeLike(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWindowCutoff(t *testing.T) {
	for _, w := range []string{"all", "", "bogus"} {
		if _, ok := windowCutoff(w); ok {
			t.Errorf("windowCutoff(%q) should report no window", w)
		}
	}
	now := time.Now().UTC()
	cutoff, ok := windowCutoff("today")
	if !ok {
		t.Fatal("today should have a window")
	}
	if d := now.Sub(cutoff); d < 23*time.Hour || d > 25*time.Hour {
		t.Errorf("today cutoff %v from now, want ~24h", d)
	}
	month, _ := windowCutoff("month")
	year, _ := windowCutoff("year")
	if !month.Before(cutoff) {
		t.Error("month cutoff should be earlier than today cutoff")
	}
	if !year.Before(month) {
		t.Error("year cutoff should be earlier than month cutoff")
	}
}

func TestParseListQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mk := func(url string) listQuery {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", url, nil)
		return parseListQuery(c)
	}

	def := mk("/api/names")
	if def.Sort != "new" || def.Limit != defaultPageSize || def.Offset != 0 || def.View != "active" {
		t.Errorf("defaults wrong: %+v", def)
	}

	if got := mk("/api/names?sort=garbage").Sort; got != "new" {
		t.Errorf("unknown sort should fall back to new, got %q", got)
	}
	if got := mk("/api/names?sort=controversial").Sort; got != "controversial" {
		t.Errorf("sort=controversial not preserved, got %q", got)
	}
	if got := mk("/api/names?limit=99999").Limit; got != defaultPageSize {
		t.Errorf("oversized limit should reset to default, got %d", got)
	}
	if got := mk("/api/names?offset=-5").Offset; got != 0 {
		t.Errorf("negative offset should clamp to 0, got %d", got)
	}
	if got := mk("/api/names?view=offensive").View; got != "offensive" {
		t.Errorf("view=offensive not honoured, got %q", got)
	}
	if got := mk("/api/names?view=anything").View; got != "active" {
		t.Errorf("unknown view should be active, got %q", got)
	}
	if got := mk("/api/names?q=%20Justin%20").Q; got != "Justin" {
		t.Errorf("q should be trimmed, got %q", got)
	}
}
