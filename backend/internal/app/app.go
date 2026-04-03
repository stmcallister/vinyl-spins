package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"

	"discogs-listen-tracker/backend/internal/demobig"
)

type App struct {
	addr string
	db   *pgxpool.Pool
	mux  http.Handler
}

func New(ctx context.Context) (*App, error) {
	// Load .env if present (no-op if missing).
	_ = loadDotenvUpward()

	// Reference demo package so builds can be intentionally "bigger" for demos.
	_ = demobig.Touch()

	port := getenvDefault("PORT", "8080")
	addr := ":" + port

	var db *pgxpool.Pool
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return nil, fmt.Errorf("db pool: %w", err)
		}
		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			return nil, fmt.Errorf("db ping: %w", err)
		}
		db = pool
	}

	a := &App{addr: addr, db: db}

	// Start daily spins backup if BACKUP_DIR is configured.
	if backupDir := os.Getenv("BACKUP_DIR"); backupDir != "" && db != nil {
		StartDailyExport(ctx, db, backupDir)
	}

	// Per-IP rate limiter: 10 req/sec with burst of 40.
	// Override with RATE_LIMIT_RPS / RATE_LIMIT_BURST env vars if needed.
	rl := newRateLimiter(
		envFloat("RATE_LIMIT_RPS", 10),
		envFloat("RATE_LIMIT_BURST", 40),
	)

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(rl.Middleware())
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins(),
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Health & meta
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	// Auth stubs (real Discogs OAuth comes next)
	r.Route("/auth", func(r chi.Router) {
		r.Get("/discogs/start", a.handleDiscogsOAuthStart())
		r.Get("/discogs/callback", a.handleDiscogsOAuthCallback())
		r.Post("/logout", a.handleLogout())
	})

	// API stubs
	r.Route("/api", func(r chi.Router) {
		r.Get("/me", a.handleMe())
		r.Delete("/me", a.handleDeleteMe())

		// Admin endpoints (protected by ADMIN_DISCOGS_USERNAMES env var).
		r.Get("/admin/users", a.handleAdminUsers())
		r.Post("/admin/users/{userID}/status", a.handleAdminSetUserStatus())
		r.Post("/admin/users/{userID}/admin", a.handleAdminSetUserAdmin())

		r.Get("/records", a.handleRecords())
		r.Get("/records/pick", a.handlePickRecord())
		r.Get("/records/{recordID}", a.handleRecordDetail())
		r.Post("/records/sync", a.handleRecordsSync())
		r.Get("/tags", a.handleTags())
		r.Post("/tags", a.handleCreateTag())
		r.Put("/tags/{tagID}", a.handleUpdateTag())
		r.Delete("/tags/{tagID}", a.handleDeleteTag())
		r.Post("/records/{recordID}/tags", a.handleAddRecordTag())
		r.Delete("/records/{recordID}/tags/{tagID}", a.handleRemoveRecordTag())

		// Legacy aliases (older clients called these "labels").
		r.Get("/labels", a.handleLabels())
		r.Post("/labels", a.handleCreateTag())
		r.Post("/records/{recordID}/labels", a.handleAddRecordTag())
		r.Delete("/records/{recordID}/labels/{tagID}", a.handleRemoveRecordTag())
		r.Get("/spins", a.handleSpins())
		r.Post("/spins", a.handleCreateSpin())
		r.Delete("/spins/{spinID}", a.handleDeleteSpin())

		r.Get("/reports", a.handleReports())

		// Imports
		r.Post("/import/ogger-playlog", a.handleImportOggerPlaylog())
	})

	a.mux = r
	return a, nil
}

func (a *App) Addr() string         { return a.addr }
func (a *App) Router() http.Handler { return a.mux }
func (a *App) DB() *pgxpool.Pool    { return a.db }

func envFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var f float64
	if _, err := fmt.Sscanf(v, "%f", &f); err != nil || f <= 0 {
		return def
	}
	return f
}

func getenvDefault(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}

func allowedOrigins() []string {
	// Dev defaults (docker-compose uses 5174; historical default is 5173).
	origins := []string{"http://localhost:5173", "http://localhost:5174"}

	// If FRONTEND_URL is set, derive the origin and allow it too.
	// e.g. "http://localhost:5174/" -> "http://localhost:5174"
	if v := os.Getenv("FRONTEND_URL"); v != "" {
		v = strings.TrimSpace(v)
		v = strings.TrimRight(v, "/")
		if v != "" && !containsString(origins, v) {
			origins = append(origins, v)
		}
	}

	return origins
}

func containsString(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func notImplemented(msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"error":"%s"}`, msg)))
	}
}
