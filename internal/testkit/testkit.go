// Package testkit spins up an in-process Bunker for tests.
package testkit

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/server"

	_ "github.com/kucukkanat/bunker/internal/auth"
	_ "github.com/kucukkanat/bunker/internal/sites"
	_ "github.com/kucukkanat/bunker/internal/web"
)

// Kit is a running test server.
type Kit struct {
	T   *testing.T
	App *core.App
	Srv *httptest.Server
	// Cookies per user email (dev sessions).
	jars map[string][]*http.Cookie
}

// New starts a dev-mode Bunker with a temp data dir. Extra modules must
// be imported by the test package itself.
func New(t *testing.T) *Kit { return newKit(t, true) }

// NewProd starts Bunker without dev mode (real sign-in required).
func NewProd(t *testing.T) *Kit { return newKit(t, false) }

func newKit(t *testing.T, dev bool) *Kit {
	t.Helper()
	cfg := &core.Config{DataDir: t.TempDir(), Dev: dev, Listen: "127.0.0.1:0", TLS: "off"}
	a, err := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.Handler(a))
	cfg.URL = srv.URL
	t.Cleanup(func() { srv.Close(); a.DB.Close() })
	return &Kit{T: t, App: a, Srv: srv, jars: map[string][]*http.Cookie{}}
}

// As returns a client acting as the given dev user ("" = default admin).
// groups is a comma-separated list.
func (k *Kit) As(email, groups string) *Client {
	c := &Client{k: k, email: email}
	if email != "" {
		if _, ok := k.jars[email]; !ok {
			req, _ := http.NewRequest("GET", k.Srv.URL+"/auth/dev?as="+email+"&groups="+groups+"&next=/api/v1/me", nil)
			res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
			if err != nil {
				k.T.Fatal(err)
			}
			res.Body.Close()
			k.jars[email] = res.Cookies()
		}
	}
	return c
}

// Client makes API requests as one user.
type Client struct {
	k     *Kit
	email string
	Token string
	Page  bool // send X-Bunker-Page like the SDK bridge
}

// WithCookies returns a client using raw session cookies.
func (k *Kit) WithCookies(cs []*http.Cookie) *Client {
	key := "cookies:" + core.RandomToken(6)
	k.jars[key] = cs
	return &Client{k: k, email: key}
}

// Res is a decoded response.
type Res struct {
	Status int
	Body   []byte
	Header http.Header
}

// JSON decodes the body into a map.
func (r Res) JSON() map[string]any {
	var m map[string]any
	json.Unmarshal(r.Body, &m)
	return m
}

// Do sends a request. body may be nil, []byte, string or any JSON value.
func (c *Client) Do(method, path string, body any, hdr ...string) Res {
	var rd io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	case string:
		rd = strings.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
		ct = "application/json"
	}
	req, _ := http.NewRequest(method, c.k.Srv.URL+path, rd)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("X-Bunker", "1")
	if c.Page {
		req.Header.Set("X-Bunker-Page", "1")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	} else {
		for _, ck := range c.k.jars[c.email] {
			req.AddCookie(ck)
		}
	}
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		c.k.T.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return Res{Status: res.StatusCode, Body: b, Header: res.Header}
}

// Must fails the test unless status is 2xx.
func (c *Client) Must(method, path string, body any) map[string]any {
	c.k.T.Helper()
	r := c.Do(method, path, body)
	if r.Status >= 300 {
		c.k.T.Fatalf("%s %s: %d %s", method, path, r.Status, r.Body)
	}
	return r.JSON()
}
