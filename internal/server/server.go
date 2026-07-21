package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/modules/health"
	"github.com/runix/runix/internal/platform/config"
	"github.com/runix/runix/internal/platform/version"
	"github.com/runix/runix/internal/webui"
)

// Server assembles the control-plane HTTP transport: middleware chain,
// module routes and lifecycle. Modules register their routes here; none of
// them know about each other.
type Server struct {
	cfg    config.Server
	log    *slog.Logger
	http   *http.Server
	health *health.Service
	api    *gin.RouterGroup
}

func New(cfg config.Server, log *slog.Logger) *Server {
	if cfg.Env == config.EnvDevelopment {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.New()
	engine.Use(RequestID(), AccessLog(log), Recovery(log), CORS(cfg.CORSOrigins))

	// Anything that is not an API route is the operator console, served
	// from the binary. API paths keep answering JSON so a mistyped
	// endpoint does not hand a client a page of HTML.
	ui := webui.Handler()
	engine.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		ui.ServeHTTP(c.Writer, c.Request)
	})
	if !webui.Built() {
		log.Warn("no web UI compiled into this binary; serving the API only")
	}

	healthSvc := health.NewService()
	health.RegisterRoutes(engine, health.NewHandler(healthSvc))

	api := engine.Group("/api/v1")
	api.GET("/version", func(c *gin.Context) {
		c.JSON(http.StatusOK, version.Get())
	})

	return &Server{
		cfg: cfg,
		log: log,
		http: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           engine,
			ReadHeaderTimeout: 10 * time.Second,
		},
		health: healthSvc,
		api:    api,
	}
}

// API exposes the /api/v1 group so the composition root can mount module
// routes.
func (s *Server) API() *gin.RouterGroup {
	return s.api
}

// Health exposes the readiness registry so infrastructure components
// (database, redis, agent hub) can add their checks during wiring.
func (s *Server) Health() *health.Service {
	return s.health
}

// Handler exposes the assembled HTTP handler for tests.
func (s *Server) Handler() http.Handler {
	return s.http.Handler
}

// Run serves HTTP until ctx is canceled, then shuts down gracefully within
// the configured timeout.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.http.ListenAndServe()
	}()
	s.log.Info("http server listening", "addr", s.cfg.HTTPAddr)

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	s.log.Info("shutting down http server", "timeout", s.cfg.ShutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()
	if err := s.http.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}
