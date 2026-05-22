package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func Index(c *gin.Context) {
	c.HTML(http.StatusOK, "index.html", gin.H{})
}

func AdminPage(c *gin.Context) {
	c.HTML(http.StatusOK, "admin.html", gin.H{})
}
