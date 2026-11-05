// Package tlsauto provides automatic HTTPS with Let's Encrypt (ACME
// HTTP-01 + TLS-ALPN-01). Set BUNKER_URL=https://bunker.corp.com on a
// machine with ports 80 and 443 reachable and certificates just work.
package tlsauto

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/server"
)

func init() {
	server.SetAutoTLS(serve)
}

func serve(a *core.App, srv *http.Server) error {
	u, err := url.Parse(a.Cfg.PublicURL())
	if err != nil || u.Hostname() == "" {
		return errors.New("BUNKER_TLS=auto needs BUNKER_URL=https://your.domain")
	}
	host := u.Hostname()
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(host),
		Cache:      autocert.DirCache(filepath.Join(a.Cfg.DataDir, "certs")),
		Email:      core.Env("ACME_EMAIL", ""),
	}
	if d := core.Env("ACME_DIRECTORY", ""); d != "" {
		m.Client = &acme.Client{DirectoryURL: d}
	}
	// Port 80: ACME challenges + redirect everything else to HTTPS.
	redirect := &http.Server{
		Addr:              core.Env("HTTP_LISTEN", ":80"),
		ReadHeaderTimeout: 10 * time.Second,
		Handler: m.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
		})),
	}
	go func() {
		if err := redirect.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.Log.Warn("port 80 unavailable; certificates will use TLS-ALPN only", "err", err)
		}
	}()
	if srv.Addr == "" || srv.Addr == ":8080" {
		srv.Addr = ":443"
	}
	cfg := m.TLSConfig()
	cfg.MinVersion = tls.VersionTLS12
	srv.TLSConfig = cfg
	a.Log.Info("automatic HTTPS enabled", "host", host, "addr", srv.Addr)
	return srv.ListenAndServeTLS("", "")
}
