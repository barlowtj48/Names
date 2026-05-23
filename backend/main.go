package main

import (
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/barlowtj48/names/backend/handlers"
	"github.com/barlowtj48/names/backend/middlewares"
	"github.com/barlowtj48/names/shared/database"
	"github.com/barlowtj48/names/shared/secrets"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

const templatesGlob = "backend/templates/*.html"
const staticDir = "backend/static"

func main() {
	cfg, err := secrets.Load()
	if err != nil {
		fmt.Println("Error loading secrets:", err)
		return
	}
	fmt.Printf("Secrets loaded successfully for environment: %s\n", cfg.Env)

	if err := database.ConnectDatabase(
		cfg.DatabaseHost, cfg.DatabaseUsername, cfg.DatabasePassword,
		cfg.DatabaseName, cfg.DatabasePort, "disable", "UTC", cfg.Env,
	); err != nil {
		fmt.Println("Error connecting to database:", err)
		return
	}
	if err := database.MigrateDatabase(); err != nil {
		fmt.Println("Error migrating database:", err)
		return
	}

	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())

	// Trust Cloudflare → Traefik chain in production.
	_ = r.SetTrustedProxies(nil)
	if cfg.IsProduction() {
		r.TrustedPlatform = "CF-Connecting-IP"
	}

	// Templates — production layout has them next to the binary.
	if cfg.IsProduction() {
		r.LoadHTMLGlob(filepath.Join("templates", "*.html"))
		r.Static("/static", "static")
		r.StaticFile("/favicon.ico", filepath.Join("static", "favicon.ico"))
	} else {
		r.SetFuncMap(template.FuncMap{})
		r.LoadHTMLGlob(templatesGlob)
		r.Static("/static", staticDir)
		r.StaticFile("/favicon.ico", filepath.Join(staticDir, "favicon.ico"))
	}

	// Cache-bust static assets on every process start so Cloudflare/browser
	// caches release after each deploy.
	handlers.StaticVersion = strconv.FormatInt(time.Now().Unix(), 10)

	// Health
	r.GET("/healthz", handlers.Health)

	// Pages
	r.GET("/", handlers.Index)
	r.GET("/admin", handlers.AdminPage)

	// Voter identity is needed for nearly every endpoint.
	r.Use(middlewares.VoterIdentity())

	submitLimiter := middlewares.NewLimiter(rate.Every(15*1e9), 5) // ~4/min, burst 5
	voteLimiter := middlewares.NewLimiter(rate.Every(1e9), 20)     // 1/sec, burst 20
	flagLimiter := middlewares.NewLimiter(rate.Every(20*1e9), 5)   // ~3/min, burst 5

	api := r.Group("/api")
	{
		api.GET("/names", handlers.ListNames)
		api.POST("/names",
			middlewares.RequireFingerprint(),
			submitLimiter.Middleware(),
			handlers.SubmitName,
		)
		api.POST("/names/:id/vote",
			middlewares.RequireFingerprint(),
			voteLimiter.Middleware(),
			handlers.Vote,
		)
		api.POST("/names/:id/flag",
			middlewares.RequireFingerprint(),
			flagLimiter.Middleware(),
			handlers.Flag,
		)

		api.POST("/admin/login", handlers.AdminLogin)
		api.POST("/admin/logout", handlers.AdminLogout)

		admin := api.Group("/admin", middlewares.AdminAuth())
		admin.GET("/names", handlers.AdminListNames)
		admin.DELETE("/names/:id", handlers.DeleteName)
		admin.GET("/names/queue", handlers.AdminQueue)
		admin.POST("/names/:id/decision", handlers.AdminDecision)
	}

	addr := ":" + cfg.BackendPort
	fmt.Println("Listening on", addr)
	if err := r.Run(addr); err != nil && err != http.ErrServerClosed {
		fmt.Println("server error:", err)
	}
}
