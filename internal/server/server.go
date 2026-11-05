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
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

// New builds the App: opens storage, runs migrations and initializes modules.
func New(cfg *core.Config, log *slog.Logger) (*core.App, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	if err := writable(cfg.DataDir); err != nil {
		return nil, fmt.Errorf("can't write to the data folder %s (%v). Give it to the user Bunker runs as, e.g. `sudo chown -R %d %s`, or in Docker use a named volume (-v bunker-data:/data) instead of a root-owned folder", cfg.DataDir, err, os.Getuid(), cfg.DataDir)
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
	if cfg.URL == "" {
		cfg.URL = a.Setting("server.url")
	}
	for _, m := range core.Modules() {
		if m.Init == nil {
			continue
		}
		if err := m.Init(a); err != nil {
			return nil, fmt.Errorf("module %s: %w", m.Name, err)
		}
	}
	// Startup work (migrations, asset compression, template seeding) leaves
	// garbage behind; hand that memory back to the OS before serving.
	debug.FreeOSMemory()
	applyMemoryLimit(log)
	a.Handle(core.Op{Name: "system.health", Method: "GET", Path: "/healthz", Summary: "Health check", Access: core.AccessPublic, Hidden: true,
		Fn: func(r *http.Request) (any, error) {
			if err := a.DB.PingContext(r.Context()); err != nil {
				return nil, core.Unavailable("database unavailable")
			}
			return map[string]any{"ok": true, "version": core.Version}, nil
		}})
	a.Mux.HandleFunc("GET /metrics", metricsHandler(a))
	a.Public("/metrics")
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

func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return errors.New("permission denied")
		}
		return err
	}
	f.Close()
	return os.Remove(f.Name())
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

		// The optional user-content origin only serves ticketed content and
		// platform assets: no sessions, no API, no dashboard.
		if a.Cfg.ContentURL != "" && strings.HasSuffix(a.Cfg.ContentURL, "://"+r.Host) {
			p := r.URL.Path
			if !(strings.HasPrefix(p, "/_/") || (strings.HasPrefix(p, "/s/") && (strings.Contains(p, "/~r/") || strings.Contains(p, "/~f/")))) {
				http.NotFound(w, r)
				return
			}
			a.Mux.ServeHTTP(w, r)
			return
		}
		var u *core.User
		if a.Authn != nil {
			u = a.Authn(r)
		}
		if u != nil && a.Cfg.URL == "" && u.IsAdmin && !u.ViaToken {
			// Only a signed-in admin's browser may teach us our address:
			// anyone else could send any Host header.
			learnURL(a, r)
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
				want := allowedOrigin
				if want == "" {
					want = originOf(a.Cfg.URL) // learned after start
				}
				if o := r.Header.Get("Origin"); o != "" && want != "" && o != want && !a.Cfg.Dev {
					core.Err(w, core.Forbidden("cross-origin request blocked"))
					return
				}
			}
			r = r.WithContext(core.WithUser(r.Context(), u))
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/oauth/") || strings.HasPrefix(r.URL.Path, "/mcp") || strings.HasPrefix(r.URL.Path, "/auth/password") || strings.HasPrefix(r.URL.Path, "/auth/invite") {
			if !(a.Cfg.Dev && u != nil) && rateLimited(w, r, u, ip) {
				return
			}
		}
		sw := &statusWriter{ResponseWriter: w, status: 200}
		mInflight.Add(1)
		a.Mux.ServeHTTP(sw, r)
		mInflight.Add(-1)
		observe(sw.status, time.Since(start))
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

func originOf(s string) string {
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return ""
}

// clientIP returns the caller's address. Behind a proxy, only the last
// X-Forwarded-For entry (the one our proxy appended) is trustworthy: the
// rest is whatever the client sent.
var trustAlways = core.Env("TRUST_PROXY", "") != ""

func clientIP(r *http.Request, trust bool) string {
	// By default only believe forwarding headers when the connection itself
	// comes from a proxy-like address (loopback or private network). A server
	// exposed directly to the internet then can't be fooled by a forged
	// X-Forwarded-For. BUNKER_TRUST_PROXY=true trusts them from anywhere.
	if trust && !trustAlways {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		trust = ip != nil && (ip.IsLoopback() || ip.IsPrivate())
	}
	if trust {
		if vs := r.Header.Values("X-Forwarded-For"); len(vs) > 0 {
			parts := strings.Split(vs[len(vs)-1], ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
				return ip
			}
		}
		if v := strings.TrimSpace(r.Header.Get("X-Real-Ip")); net.ParseIP(v) != nil {
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
	// Requests hang off baseCtx so long-lived streams (live updates,
	// notifications) can be ended on shutdown; otherwise Shutdown waits for
	// them and open dashboards don't notice that Bunker went away.
	baseCtx, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	srv.BaseContext = func(net.Listener) context.Context { return baseCtx }
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
	// Give normal requests a moment to finish, then end the streams.
	t := time.AfterFunc(1500*time.Millisecond, cancelBase)
	defer t.Stop()
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

var learnMu sync.Mutex

// learnURL sets the public URL from the first request of a signed-in
// admin when BUNKER_URL wasn't given (e.g. a bare `docker run`). It's
// persisted and can be changed later in Admin → Settings or with BUNKER_URL.
func learnURL(a *core.App, r *http.Request) {
	host := r.Host
	if host == "" || strings.ContainsAny(host, "/\\ @") || r.URL.Path == "/healthz" {
		return
	}
	// Health checkers and load balancers often use internal IPs; only learn
	// real hostnames (until then, links are relative and still work).
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	if net.ParseIP(strings.Trim(h, "[]")) != nil {
		return
	}
	scheme := "http"
	if r.TLS != nil || (a.Cfg.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https") {
		scheme = "https"
	}
	learnMu.Lock()
	defer learnMu.Unlock()
	if a.Cfg.URL != "" {
		return
	}
	a.Cfg.URL = scheme + "://" + host
	a.SetSetting(context.Background(), "server.url", a.Cfg.URL)
	a.Log.Info("learned public URL from first request; set BUNKER_URL to override", "url", a.Cfg.URL)
}

// applyMemoryLimit sets a soft memory limit for the Go runtime from
// BUNKER_MEMORY_LIMIT (e.g. "400MiB", "1GB", or bytes), unless GOMEMLIMIT
// is set. Near the limit the GC runs more often instead of letting the
// heap grow, which suits small VMs. Off by default.
func applyMemoryLimit(log *slog.Logger) {
	v := core.Env("MEMORY_LIMIT", "")
	if v == "" || os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	n, ok := parseBytes(v)
	if !ok || n <= 0 {
		log.Warn("ignoring BUNKER_MEMORY_LIMIT: use a size like 400MiB or 1GB", "value", v)
		return
	}
	debug.SetMemoryLimit(n)
	log.Info("memory limit set", "bytes", n)
}

func parseBytes(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	units := []struct {
		suf string
		mul int64
	}{{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"KB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"K", 1 << 10}, {"M", 1 << 20}, {"G", 1 << 30}, {"B", 1}}
	mul := int64(1)
	for _, u := range units {
		if len(s) > len(u.suf) && strings.EqualFold(s[len(s)-len(u.suf):], u.suf) {
			s, mul = strings.TrimSpace(s[:len(s)-len(u.suf)]), u.mul
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 || n > (1<<62)/mul {
		return 0, false
	}
	return n * mul, true
}
