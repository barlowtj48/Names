package middlewares

import "github.com/gin-gonic/gin"

// SecurityHeaders sets the cheap, always-safe browser hardening headers.
// A CSP is deliberately omitted: htmx's hx-on attributes need unsafe-eval.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Next()
	}
}

// CacheControl sets a fixed Cache-Control value on every response that passes
// through it. Handlers may still override it afterwards.
func CacheControl(value string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Cache-Control", value)
		c.Next()
	}
}
