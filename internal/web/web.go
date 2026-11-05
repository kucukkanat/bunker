// Package web serves the embedded dashboard (single-page app) and the
// platform assets under /_/ (shell, SDK, UI kit).
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

//go:embed static app
var files embed.FS

func init() {
	core.Register(core.Module{Name: "web", Order: 900, Init: initWeb})
}

type asset struct {
	b, br, gz []byte
	etag      string
	ct        string
}

// serve writes an asset, picking a precompressed variant when accepted.
func (as *asset) serve(w http.ResponseWriter, r *http.Request, mod time.Time) {
	h := w.Header()
	h.Add("Vary", "Accept-Encoding")
	body := as.b
	ae := r.Header.Get("Accept-Encoding")
	if as.br != nil && strings.Contains(ae, "br") {
		body = as.br
		h.Set("Content-Encoding", "br")
	} else if as.gz != nil && strings.Contains(ae, "gzip") {
		body = as.gz
		h.Set("Content-Encoding", "gzip")
	}
	http.ServeContent(w, r, "", mod, bytes.NewReader(body))
}

var assets = map[string]*asset{}

func load(prefix string) {
	fs.WalkDir(files, prefix, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, _ := files.ReadFile(p)
		sum := sha256.Sum256(b)
		as := &asset{b: b, etag: `"` + hex.EncodeToString(sum[:8]) + `"`, ct: contentType(p)}
		if len(b) > 1024 && compressibleType(as.ct) {
			as.br, as.gz = core.CompressBrotli(b), core.CompressGzip(b)
		}
		assets[p] = as
		return nil
	})
}

func contentType(p string) string {
	switch path.Ext(p) {
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json":
		return "application/json"
	case ".png":
		return "image/png"
	case ".woff2":
		return "font/woff2"
	}
	return "application/octet-stream"
}

func compressibleType(ct string) bool {
	return strings.HasPrefix(ct, "text/") || strings.Contains(ct, "javascript") || strings.Contains(ct, "json") || strings.Contains(ct, "svg")
}

// AppCSP is the policy for Bunker's own pages.
const AppCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; frame-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'"

func initWeb(a *core.App) error {
	load("static")
	load("app")
	a.Public("/_/")
	start := time.Now()
	// Platform assets. Loaded by sandboxed (opaque-origin) pages too, so
	// they must be public and allow cross-origin embedding.
	a.Mux.HandleFunc("GET /_/{path...}", func(w http.ResponseWriter, r *http.Request) {
		as := assets["static/"+r.PathValue("path")]
		if as == nil {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", as.ct)
		h.Set("ETag", as.etag)
		h.Set("Cross-Origin-Resource-Policy", "cross-origin")
		h.Set("Access-Control-Allow-Origin", "*")
		if r.URL.Query().Get("v") != "" {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "public, max-age=300")
		}
		if r.Header.Get("If-None-Match") == as.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		as.serve(w, r, start)
	})
	// Dashboard SPA routes.
	appIndex := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/setup" && r.URL.Path != "/auth/signed-out" && core.UserFrom(r.Context()) == nil {
			if !configured(a) {
				http.Redirect(w, r, "/setup", http.StatusFound)
				return
			}
			http.Redirect(w, r, "/auth/login?next="+r.URL.RequestURI(), http.StatusFound)
			return
		}
		as := assets["app/index.html"]
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", AppCSP)
		h.Set("X-Frame-Options", "DENY")
		w.Write(as.b)
	}
	for _, p := range []string{"/{$}", "/new", "/sites/", "/admin", "/admin/", "/settings", "/settings/", "/device", "/templates", "/trash", "/docs", "/welcome"} {
		a.Mux.HandleFunc("GET "+p, appIndex)
	}
	a.Mux.HandleFunc("GET /setup", appIndex)
	a.Mux.HandleFunc("GET /auth/signed-out", appIndex)
	a.Mux.HandleFunc("GET /app/{path...}", func(w http.ResponseWriter, r *http.Request) {
		as := assets["app/"+r.PathValue("path")]
		if as == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", as.ct)
		w.Header().Set("ETag", as.etag)
		w.Header().Set("Cache-Control", "no-cache")
		if r.Header.Get("If-None-Match") == as.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		as.serve(w, r, start)
	})
	a.Public("/app/")
	a.Mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/_/icon.svg", http.StatusMovedPermanently)
	})
	a.Public("/favicon.ico")
	a.Mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("User-agent: *\nDisallow: /\n"))
	})
	a.Public("/robots.txt")
	return nil
}

// configured asks the setup op without importing auth (avoids a cycle).
func configured(a *core.App) bool {
	return a.Cfg.Dev || a.Setting("auth.client_id") != "" || core.Env("ENTRA_CLIENT_ID", "") != "" || a.Setting("auth.local_ready") == "1"
}

// Asset exposes an embedded file (used by other modules, e.g. templates).
func Asset(p string) []byte {
	if as := assets[p]; as != nil {
		return as.b
	}
	return nil
}

var _ = strings.TrimSpace
