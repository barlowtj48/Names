package middlewares

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type limiterEntry struct {
	limiter *rate.Limiter
	seen    time.Time
}

type Limiter struct {
	mu       sync.Mutex
	visitors map[string]*limiterEntry
	rps      rate.Limit
	burst    int
}

func NewLimiter(rps rate.Limit, burst int) *Limiter {
	l := &Limiter{
		visitors: make(map[string]*limiterEntry),
		rps:      rps,
		burst:    burst,
	}
	go l.cleanupLoop()
	return l
}

func (l *Limiter) cleanupLoop() {
	for {
		time.Sleep(10 * time.Minute)
		l.mu.Lock()
		cutoff := time.Now().Add(-30 * time.Minute)
		for k, e := range l.visitors {
			if e.seen.Before(cutoff) {
				delete(l.visitors, k)
			}
		}
		l.mu.Unlock()
	}
}

func (l *Limiter) get(key string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.visitors[key]
	if !ok {
		e = &limiterEntry{limiter: rate.NewLimiter(l.rps, l.burst)}
		l.visitors[key] = e
	}
	e.seen = time.Now()
	return e.limiter
}

// Middleware returns a gin middleware that rate-limits by voter hash (falling back to IP).
func (l *Limiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := VoterHash(c)
		if key == "" {
			key = c.ClientIP()
		}
		res := l.get(key).Reserve()
		if !res.OK() || res.Delay() > 0 {
			wait := res.Delay()
			res.Cancel()
			secs := int(wait.Seconds() + 0.999)
			if secs < 1 {
				secs = 1
			}
			c.Header("Retry-After", strconv.Itoa(secs))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": fmt.Sprintf("slow down — try again in %ds", secs),
			})
			return
		}
		c.Next()
	}
}
