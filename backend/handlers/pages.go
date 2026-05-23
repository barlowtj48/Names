package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// StaticVersion is appended to static asset URLs as ?v=... to bust
// upstream caches (Cloudflare, browsers) on each deploy. Set from main.
var StaticVersion = "dev"

func Index(c *gin.Context) {
	c.HTML(http.StatusOK, "index.html", gin.H{"staticVersion": StaticVersion})
}

func AdminPage(c *gin.Context) {
	c.HTML(http.StatusOK, "admin.html", gin.H{"staticVersion": StaticVersion})
}
