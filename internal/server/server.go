// Package server assembles the App, applies global middleware and runs
// the HTTP server.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

// New builds the App: opens storage, runs migrations and initializes modules.
func New(cfg *core.Config, log *slog.Logger) (*core.App, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	if err := core.LoadSecretKey(cfg.DataDir); err != nil {
		return nil, fmt.Errorf("secret key: %w", err)
	}
	db, err := core.OpenDB(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	blobs, err := core.NewBlobs(filepath.Join(cfg.DataDir, "blobs"))
	if err != nil {
		return nil, fmt.Errorf("blobs: %w", err)
	}
	a := &core.App{Cfg: cfg, DB: db, Blobs: blobs, Bus: core.NewBus(), Log: log, Mux: http.NewServeMux()}
	if err := a.Migrate(context.Background()); err != nil {
		return nil, err
	}
	for _, m := range core.Modules() {
		if m.Init == nil {
			continue
		}
		if err := m.Init(a); err != nil {
			return nil, fmt.Errorf("module %s: %w", m.Name, err)
		}
	}
	a.Handle(core.Op{Name: "system.health", Method: "GET", Path: "/healthz", Summary: "Health check", Access: core.AccessPublic, Hidden: true,
		Fn: func(r *http.Request) (any, error) {
			if err := a.DB.PingContext(r.Context()); err != nil {
				return nil, core.Unavailable("database unavailable")
			}
			return map[string]any{"ok": true, "version": core.Version}, nil
		}})
	a.Handle(core.Op{Name: "system.openapi", Method: "GET", Path: "/api/v1/openapi.json", Summary: "OpenAPI document", Access: core.AccessPublic, Hidden: true,
		Fn: func(r *http.Request) (any, error) { return a.OpenAPI(), nil }})
	a.Handle(core.Op{Name: "system.ops", Method: "GET", Path: "/api/v1/ops", Summary: "List every API operation (used by the CLI and MCP)", Access: core.AccessPublic, Hidden: true,
		Fn: func(r *http.Request) (any, error) {
			var out []core.Op
			for _, o := range a.Ops() {
				if !o.Hidden {
					out = append(out, o)
				}
			}
			return map[string]any{"version": core.Version, "ops": out}, nil
		}})
	return a, nil
}

// Handler wraps the mux with global middleware.
func Handler(a *core.App) http.Handler {
	var allowedOrigin string
	if u, err := url.Parse(a.Cfg.URL); err == nil && u.Host != "" {
		allowedOrigin = u.Scheme + "://" + u.Host
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		if strings.HasPrefix(a.Cfg.URL, "https://") {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		ip := clientIP(r, a.Cfg.TrustProxy)
		ctx := core.WithIP(r.Context(), ip)
		r = r.WithContext(ctx)

		var u *core.User
		if a.Authn != nil {
			u = a.Authn(r)
		}
		if u != nil {
			// CSRF: cookie-authenticated unsafe requests must come from our
			// own origin with our custom header. Browsers can't send custom
			// headers cross-origin without a CORS preflight, which we never allow.
			if !u.ViaToken && isUnsafe(r.Method) && !a.IsPublic(r.URL.Path) {
				if r.Header.Get("X-Bunker") == "" {
					core.Err(w, core.Forbidden("missing X-Bunker header"))
					return
				}
				if o := r.Header.Get("Origin"); o != "" && allowedOrigin != "" && o != allowedOrigin && !a.Cfg.Dev {
					core.Err(w, core.Forbidden("cross-origin request blocked"))
					return
				}
			}
			r = r.WithContext(core.WithUser(r.Context(), u))
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/oauth/") || strings.HasPrefix(r.URL.Path, "/mcp") {
			if rateLimited(w, r, u, ip) {
				return
			}
		}
		sw := &statusWriter{ResponseWriter: w, status: 200}
		a.Mux.ServeHTTP(sw, r)
		if d := time.Since(start); d > 2*time.Second && !strings.Contains(r.URL.Path, "/events") && !strings.HasPrefix(r.URL.Path, "/mcp") {
			a.Log.Warn("slow request", "path", r.URL.Path, "ms", d.Milliseconds(), "status", sw.status)
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(c int) { s.status = c; s.ResponseWriter.WriteHeader(c) }
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func isUnsafe(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

func clientIP(r *http.Request, trust bool) string {
	if trust {
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			return strings.TrimSpace(strings.Split(v, ",")[0])
		}
		if v := r.Header.Get("X-Real-Ip"); v != "" {
			return v
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Run serves until SIGINT/SIGTERM.
func Run(a *core.App) error {
	srv := &http.Server{
		Addr:              a.Cfg.Listen,
		Handler:           Handler(a),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	// Speak HTTP/2 without TLS too (h2c) so reverse proxies that support it
	// can multiplex SSE streams and many small assets over one connection.
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetHTTP2(true)
	srv.Protocols.SetUnencryptedHTTP2(true)
	go func() {
		t := time.NewTicker(3 * time.Hour)
		defer t.Stop()
		for range t.C {
			a.DB.Exec(`PRAGMA optimize`)
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		a.Log.Info("bunker listening", "addr", a.Cfg.Listen, "url", a.Cfg.URL, "version", core.Version)
		var err error
		switch a.Cfg.TLS {
		case "file":
			err = srv.ListenAndServeTLS(a.Cfg.TLSCert, a.Cfg.TLSKey)
		case "auto":
			err = serveAutoTLS(a, srv)
		default:
			err = srv.ListenAndServe()
		}
		if !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
		close(errc)
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	a.Log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.Emit(sctx, "shutdown", nil)
	err := srv.Shutdown(sctx)
	a.DB.Exec(`PRAGMA optimize`)
	a.DB.Close()
	return err
}

// serveAutoTLS is replaced by the tls module when compiled in.
var serveAutoTLS = func(a *core.App, srv *http.Server) error {
	return errors.New("BUNKER_TLS=auto is not available in this build")
}

// SetAutoTLS lets the TLS module install its implementation.
func SetAutoTLS(f func(a *core.App, srv *http.Server) error) { serveAutoTLS = f }
