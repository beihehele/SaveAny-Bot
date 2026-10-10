// Package admin serves the optional, single-administrator web console.
package admin

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/krau/SaveAny-Bot/api"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/adminauth"
)

//go:embed static
var assets embed.FS

// Server owns the console listener, bounded sessions and audit records.
type Server struct {
	httpServer *http.Server
	cfg        config.AdminConfig
	auth       *authentication
	factory    *api.TaskFactory
	handlers   *api.Handlers
	started    time.Time
	audit      auditLog
	secrets    []string
	protected  []string
}

// NewServer constructs a console without starting it or changing bot behavior.
func NewServer(ctx context.Context, cfg config.AdminConfig) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	encoded := cfg.PasswordHash
	if cfg.Password != "" {
		var err error
		encoded, err = adminauth.Hash([]byte(cfg.Password))
		if err != nil {
			return nil, err
		}
	}
	verifier, err := adminauth.Parse(encoded)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg: cfg, auth: newAuthentication(verifier, cfg.SessionTTL),
		factory: api.NewTaskFactory(ctx), started: time.Now(),
		secrets: configSecrets(config.C()), protected: protectedPaths(),
	}
	s.cfg.Password, s.cfg.PasswordHash = "", ""
	s.handlers = api.NewHandlers(s.factory)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /admin/{$}", s.index)
	mux.HandleFunc("GET /admin/assets/{file...}", s.asset)
	mux.HandleFunc("POST /admin/api/v1/login", s.login)
	s.route(mux, "GET /admin/api/v1/session", s.session)
	s.route(mux, "POST /admin/api/v1/logout", s.logout)
	s.route(mux, "GET /admin/api/v1/overview", s.overview)
	s.route(mux, "GET /admin/api/v1/tasks", s.tasks)
	s.route(mux, "POST /admin/api/v1/tasks", s.createTask)
	s.route(mux, "POST /admin/api/v1/tasks/{id}/cancel", s.cancelTask)
	s.route(mux, "GET /admin/api/v1/storages", s.handlers.ListStoragesHandler)
	s.route(mux, "GET /admin/api/v1/task-types", s.handlers.GetTaskTypesHandler)
	s.route(mux, "GET /admin/api/v1/files", s.files)
	s.route(mux, "GET /admin/api/v1/download", s.download)
	s.route(mux, "GET /admin/api/v1/preferences", s.preferences)
	s.route(mux, "GET /admin/api/v1/diagnostics", s.diagnostics)
	s.httpServer = &http.Server{
		Addr: net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port)), Handler: s.security(mux),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	return s, nil
}

// Handler exposes the exact production router for isolated HTTP tests.
func (s *Server) Handler() http.Handler { return s.httpServer.Handler }

// Start binds synchronously and shuts down with the application context.
func (s *Server) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen for admin console: %w", err)
	}
	logger := log.FromContext(ctx).WithPrefix("admin")
	logger.Info("Management console started", "address", listener.Addr().String())
	go func() {
		if err := s.httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Management server stopped", "error", err)
		}
	}()
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.auth.cleanup(time.Now())
				api.CleanupExpired()
			case <-ctx.Done():
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
					logger.Warn("Management shutdown deadline exceeded")
					if err := s.httpServer.Close(); err != nil {
						logger.Error("Close management listener", "error", err)
					}
				}
				s.auth.clear()
				return
			}
		}
	}()
	return nil
}

// Start initializes only the enabled management server; API tokens are unrelated.
func Start(ctx context.Context) error {
	cfg := config.C().Admin
	if !cfg.Enable {
		return nil
	}
	server, err := NewServer(ctx, cfg)
	if err != nil {
		return fmt.Errorf("refusing to start admin console: %w", err)
	}
	return server.Start(ctx)
}

func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		defer func() {
			if recover() != nil {
				log.FromContext(r.Context()).Error("Management request failed", "path", r.URL.Path)
				s.fail(w, http.StatusInternalServerError, "internal_error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	content, err := assets.ReadFile("static/index.html")
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "internal_error")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(content); err != nil {
		log.FromContext(r.Context()).Debug("Console page client disconnected")
	}
}

func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	contentType := ""
	switch file {
	case "app.js":
		contentType = "text/javascript; charset=utf-8"
	case "style.css":
		contentType = "text/css; charset=utf-8"
	case "locales/zh-Hans.json", "locales/en.json":
		contentType = "application/json; charset=utf-8"
	}
	if contentType == "" || strings.Contains(file, "..") {
		s.fail(w, http.StatusNotFound, "not_found")
		return
	}
	content, err := assets.ReadFile("static/" + file)
	if err != nil {
		s.fail(w, http.StatusNotFound, "not_found")
		return
	}
	w.Header().Set("Content-Type", contentType)
	if _, err := w.Write(content); err != nil {
		log.FromContext(r.Context()).Debug("Console asset client disconnected")
	}
}

func (s *Server) write(w http.ResponseWriter, status int, value any) {
	if err := api.WriteJSON(w, status, value); err != nil {
		// Never log the response body: it can include a session-bound CSRF token.
		log.Debug("Management response client disconnected")
	}
}

func (s *Server) fail(w http.ResponseWriter, status int, code string) {
	s.write(w, status, api.ErrorResponse{Error: code})
}
