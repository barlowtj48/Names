package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

func TestLimiterBurstThenRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	l := NewLimiter(rate.Every(1e9), 2) // 1/sec, burst 2

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(VoterHashKey, "voter-a"); c.Next() })
	r.POST("/x", l.Middleware(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	do := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/x", nil))
		return w
	}

	for i := 0; i < 2; i++ {
		if w := do(); w.Code != http.StatusNoContent {
			t.Fatalf("request %d: got %d, want 204", i+1, w.Code)
		}
	}
	w := do()
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("third request: got %d, want 429", w.Code)
	}
	if ra := w.Header().Get("Retry-After"); ra == "" || ra == "0" {
		t.Errorf("expected a positive Retry-After header, got %q", ra)
	}
}

func TestLimiterIsPerVoter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	l := NewLimiter(rate.Every(1e9), 1)

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(VoterHashKey, c.GetHeader("X-Test-Voter")); c.Next() })
	r.POST("/x", l.Middleware(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	do := func(voter string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/x", nil)
		req.Header.Set("X-Test-Voter", voter)
		r.ServeHTTP(w, req)
		return w.Code
	}
	if c := do("a"); c != http.StatusNoContent {
		t.Fatalf("voter a first: %d", c)
	}
	if c := do("a"); c != http.StatusTooManyRequests {
		t.Fatalf("voter a second should be limited: %d", c)
	}
	if c := do("b"); c != http.StatusNoContent {
		t.Fatalf("voter b should have its own bucket: %d", c)
	}
}
