package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
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
	r.Use(middlewares.SecurityHeaders())

	// Trust Cloudflare → Traefik chain in production.
	_ = r.SetTrustedProxies(nil)
	if cfg.IsProduction() {
		r.TrustedPlatform = "CF-Connecting-IP"
	}

	// Templates — production layout has them next to the binary.
	tmplGlob, static := templatesGlob, staticDir
	if cfg.IsProduction() {
		tmplGlob, static = filepath.Join("templates", "*.html"), "static"
	}
	r.SetFuncMap(template.FuncMap{})
	r.LoadHTMLGlob(tmplGlob)

	// Static assets are referenced with ?v=<StaticVersion>, so they can be
	// cached aggressively; a new deploy changes the URL and busts the cache.
	assets := r.Group("/", middlewares.CacheControl("public, max-age=31536000, immutable"))
	assets.Static("/static", static)
	assets.StaticFile("/favicon.ico", filepath.Join(static, "favicon.ico"))

	// Cache-bust static assets on every process start so Cloudflare/browser
	// caches release after each deploy.
	handlers.StaticVersion = strconv.FormatInt(time.Now().Unix(), 10)

	// Health
	r.GET("/healthz", handlers.Health)

	// Pages and API responses are per-viewer (voter cookie, admin state) and
	// change constantly — never let an intermediary cache them.
	r.Use(middlewares.CacheControl("no-cache, no-store"))

	// Pages
	r.GET("/", handlers.Index)
	r.GET("/admin", handlers.AdminPage)

	// Voter identity is needed for nearly every endpoint.
	r.Use(middlewares.VoterIdentity())

	// Live updates: clients open /ws and receive {"type":"names.changed"}
	// whenever something is submitted/voted/flagged/moderated.
	r.GET("/ws", handlers.WSHandler)

	submitLimiter := middlewares.NewLimiter(rate.Every(15*time.Second), 5) // ~4/min, burst 5
	voteLimiter := middlewares.NewLimiter(rate.Every(time.Second), 20)     // 1/sec, burst 20
	flagLimiter := middlewares.NewLimiter(rate.Every(20*time.Second), 5)   // ~3/min, burst 5

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

	srv := &http.Server{
		Addr:              ":" + cfg.BackendPort,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// No WriteTimeout: the websocket upgrade hijacks the connection and
		// clears deadlines, but a global write deadline would still cut off
		// slow-but-legitimate page loads behind Cloudflare.
		IdleTimeout: 120 * time.Second,
	}

	// Serve until SIGINT/SIGTERM (docker stop), then drain in-flight requests
	// so a rolling deploy doesn't drop votes mid-write.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		fmt.Println("Listening on", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Println("server error:", err)
			stop()
		}
	}()

	<-ctx.Done()
	fmt.Println("Shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	handlers.CloseAllClients()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		fmt.Println("shutdown error:", err)
	}
}
