// Package connections lets site pages call external HTTPS APIs without
// ever seeing the secrets. An admin defines a connection (base URL,
// secret headers, allowed methods and paths, which sites may use it);
// pages call POST /api/v1/sites/{site}/connections/{name}/fetch and Bunker
// makes the request server-side with SSRF protection, size and rate limits,
// and an audit entry for every call.
package connections

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/kucukkanat/bunker/internal/core"
)

func init() {
	core.Register(core.Module{Name: "connections", Order: 60, Migrations: []string{`
CREATE TABLE connections (
  id INTEGER PRIMARY KEY,
  name TEXT UNIQUE NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  base_url TEXT NOT NULL,
  headers_enc TEXT NOT NULL DEFAULT '',
  allowed_methods TEXT NOT NULL DEFAULT 'GET',
  allowed_prefixes TEXT NOT NULL DEFAULT '/',
  sites TEXT NOT NULL DEFAULT '',
  rate_per_min INTEGER NOT NULL DEFAULT 60,
  timeout_ms INTEGER NOT NULL DEFAULT 15000,
  allow_private INTEGER NOT NULL DEFAULT 0,
  created_by INTEGER,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);`}, Init: initConn})
}

var app *core.App

// MaxResponse is the largest upstream body returned to a page.
const MaxResponse = 5 << 20

// TLSConfig is used for upstream calls (tests add their CA).
var TLSConfig *tls.Config

// Connection is an admin-defined external API.
type Connection struct {
	ID              int64             `json:"id"`
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	BaseURL         string            `json:"baseUrl"`
	Headers         map[string]string `json:"headers,omitempty"` // values masked in responses
	AllowedMethods  []string          `json:"allowedMethods"`
	AllowedPrefixes []string          `json:"allowedPathPrefixes"`
	Sites           []string          `json:"sites"`
	RatePerMin      int               `json:"rateLimitPerMinute"`
	TimeoutMs       int               `json:"timeoutMs"`
	AllowPrivate    bool              `json:"allowPrivate"`
	CreatedAt       int64             `json:"createdAt"`
	UpdatedAt       int64             `json:"updatedAt"`
}

const mask = "••••••"

func (c Connection) masked() Connection {
	m := map[string]string{}
	for k := range c.Headers {
		m[k] = mask
	}
	c.Headers = m
	return c
}

func list(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

const cols = `id,name,description,base_url,headers_enc,allowed_methods,allowed_prefixes,sites,rate_per_min,timeout_ms,allow_private,created_at,updated_at`

func scan(row interface{ Scan(...any) error }) (*Connection, error) {
	var c Connection
	var henc, methods, prefixes, sites string
	var priv int
	if err := row.Scan(&c.ID, &c.Name, &c.Description, &c.BaseURL, &henc, &methods, &prefixes, &sites, &c.RatePerMin, &c.TimeoutMs, &priv, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.AllowedMethods, c.AllowedPrefixes, c.Sites, c.AllowPrivate = list(methods), list(prefixes), list(sites), priv == 1
	c.Headers = map[string]string{}
	if henc != "" {
		if plain, err := core.Decrypt(henc); err == nil {
			json.Unmarshal([]byte(plain), &c.Headers)
		}
	}
	return &c, nil
}

func get(ctx context.Context, name string) (*Connection, error) {
	c, err := scan(app.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM connections WHERE name=?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.NotFound("there's no connection called " + name)
	}
	return c, err
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,40}$`)
var headerNameRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

var validMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true}

// input is the admin create/update body.
type input struct {
	Name            *string            `json:"name"`
	Description     *string            `json:"description"`
	BaseURL         *string            `json:"baseUrl"`
	Headers         map[string]*string `json:"headers"` // null value removes; "••••••" or "" keeps
	AllowedMethods  *[]string          `json:"allowedMethods"`
	AllowedPrefixes *[]string          `json:"allowedPathPrefixes"`
	Sites           *[]string          `json:"sites"`
	RatePerMin      *int               `json:"rateLimitPerMinute"`
	TimeoutMs       *int               `json:"timeoutMs"`
	AllowPrivate    *bool              `json:"allowPrivate"`
}

func (in input) apply(c *Connection) error {
	if in.Name != nil {
		c.Name = strings.ToLower(strings.TrimSpace(*in.Name))
	}
	if !nameRe.MatchString(c.Name) {
		return core.BadRequest("name: use lowercase letters, numbers, - or _ (e.g. weather)")
	}
	if in.Description != nil {
		c.Description = strings.TrimSpace(*in.Description)
	}
	if in.BaseURL != nil {
		c.BaseURL = strings.TrimRight(strings.TrimSpace(*in.BaseURL), "/")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return core.BadRequest("baseUrl must be an https:// address without a query, e.g. https://api.example.com/v1")
	}
	if c.Headers == nil {
		c.Headers = map[string]string{}
	}
	for k, v := range in.Headers {
		if !headerNameRe.MatchString(k) {
			return core.BadRequest("header name " + k + " isn't valid")
		}
		k = http.CanonicalHeaderKey(k)
		switch {
		case v == nil:
			delete(c.Headers, k)
		case *v == "" || *v == mask:
			// keep the stored secret
		case strings.ContainsAny(*v, "\r\n"):
			return core.BadRequest("header values can't contain line breaks")
		default:
			c.Headers[k] = *v
		}
	}
	if in.AllowedMethods != nil {
		c.AllowedMethods = nil
		for _, m := range *in.AllowedMethods {
			m = strings.ToUpper(strings.TrimSpace(m))
			if !validMethods[m] {
				return core.BadRequest("method " + m + " isn't supported")
			}
			c.AllowedMethods = append(c.AllowedMethods, m)
		}
	}
	if len(c.AllowedMethods) == 0 {
		c.AllowedMethods = []string{"GET"}
	}
	if in.AllowedPrefixes != nil {
		c.AllowedPrefixes = nil
		for _, p := range *in.AllowedPrefixes {
			p = strings.TrimSpace(p)
			if !strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.ContainsAny(p, "?#,") {
				return core.BadRequest("path prefixes start with / (e.g. /forecast)")
			}
			c.AllowedPrefixes = append(c.AllowedPrefixes, p)
		}
	}
	if len(c.AllowedPrefixes) == 0 {
		c.AllowedPrefixes = []string{"/"}
	}
	if in.Sites != nil {
		c.Sites = nil
		for _, s := range *in.Sites {
			if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
				if strings.Contains(s, ",") {
					return core.BadRequest("site addresses can't contain commas")
				}
				c.Sites = append(c.Sites, s)
			}
		}
	}
	if c.Sites == nil {
		c.Sites = []string{}
	}
	if in.RatePerMin != nil {
		c.RatePerMin = *in.RatePerMin
	}
	if c.RatePerMin <= 0 {
		c.RatePerMin = 60
	}
	c.RatePerMin = min(c.RatePerMin, 6000)
	if in.TimeoutMs != nil {
		c.TimeoutMs = *in.TimeoutMs
	}
	if c.TimeoutMs <= 0 {
		c.TimeoutMs = 15000
	}
	c.TimeoutMs = min(max(c.TimeoutMs, 500), 60000)
	if in.AllowPrivate != nil {
		c.AllowPrivate = *in.AllowPrivate
	}
	return nil
}

func save(ctx context.Context, c *Connection, uid int64) error {
	hj, _ := json.Marshal(c.Headers)
	henc := ""
	if len(c.Headers) > 0 {
		var err error
		if henc, err = core.Encrypt(string(hj)); err != nil {
			return err
		}
	}
	now := core.Now()
	args := []any{c.Name, c.Description, c.BaseURL, henc, strings.Join(c.AllowedMethods, ","), strings.Join(c.AllowedPrefixes, ","), strings.Join(c.Sites, ","), c.RatePerMin, c.TimeoutMs, b2i(c.AllowPrivate)}
	if c.ID == 0 {
		res, err := app.DB.ExecContext(ctx, `INSERT INTO connections(name,description,base_url,headers_enc,allowed_methods,allowed_prefixes,sites,rate_per_min,timeout_ms,allow_private,created_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			append(args, uid, now, now)...)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return core.Conflict("a connection called " + c.Name + " already exists")
			}
			return err
		}
		c.ID, _ = res.LastInsertId()
		c.CreatedAt, c.UpdatedAt = now, now
		return nil
	}
	_, err := app.DB.ExecContext(ctx, `UPDATE connections SET name=?,description=?,base_url=?,headers_enc=?,allowed_methods=?,allowed_prefixes=?,sites=?,rate_per_min=?,timeout_ms=?,allow_private=?,updated_at=? WHERE id=?`,
		append(args, now, c.ID)...)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return core.Conflict("a connection called " + c.Name + " already exists")
	}
	c.UpdatedAt = now
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (c *Connection) siteAllowed(slug string) bool {
	for _, s := range c.Sites {
		if s == "*" || strings.EqualFold(s, slug) {
			return true
		}
	}
	return false
}

func (c *Connection) methodAllowed(m string) bool {
	for _, x := range c.AllowedMethods {
		if x == m {
			return true
		}
	}
	return false
}

// ---- rate limiting (token bucket per site+connection) ----

type bucket struct {
	tokens float64
	last   time.Time
}

var (
	rlMu    sync.Mutex
	buckets = map[string]*bucket{}
)

func allow(key string, perMin int) bool {
	rlMu.Lock()
	defer rlMu.Unlock()
	now := time.Now()
	b := buckets[key]
	if b == nil {
		if len(buckets) > 50000 {
			buckets = map[string]*bucket{}
		}
		b = &bucket{tokens: float64(perMin), last: now}
		buckets[key] = b
	}
	b.tokens = min(float64(perMin), b.tokens+now.Sub(b.last).Minutes()*float64(perMin))
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// ---- SSRF-safe HTTP client ----

var blockedNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
		"224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32",
		"::/128", "::1/128", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001:db8::/32", "fc00::/7", "fe80::/10", "ff00::/8", "fec0::/10",
	} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// blockedIP reports whether ip is private, loopback, link-local (incl.
// cloud metadata 169.254.169.254), multicast or otherwise not public.
func blockedIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

type ssrfError struct{ ip string }

func (e *ssrfError) Error() string { return "blocked private address " + e.ip }

func client(c *Connection) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if !c.AllowPrivate {
		// Checked after DNS resolution, on the exact IP being dialed, so DNS
		// rebinding can't sneak past it.
		d.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || blockedIP(ip) {
				return &ssrfError{ip: host}
			}
			return nil
		}
	}
	tr := &http.Transport{
		Proxy:                 nil, // never route through env proxies: the checks must see the real target
		DialContext:           d.DialContext,
		TLSClientConfig:       TLSConfig,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: time.Duration(c.TimeoutMs) * time.Millisecond,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	base, _ := url.Parse(c.BaseURL)
	return &http.Client{
		Transport: tr,
		Timeout:   time.Duration(c.TimeoutMs) * time.Millisecond,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Host, base.Host) {
				return &redirectError{to: req.URL.Host}
			}
			// Secrets were set on the first request; Go copies them only to
			// the same host, which is the only host allowed here.
			return nil
		},
	}
}

type redirectError struct{ to string }

func (e *redirectError) Error() string { return "redirect to another host (" + e.to + ") blocked" }

var (
	clientMu sync.Mutex
	clients  = map[string]*http.Client{}
)

func clientFor(c *Connection) *http.Client {
	key := fmt.Sprintf("%d|%d|%v|%s|%d", c.ID, c.UpdatedAt, c.AllowPrivate, c.BaseURL, c.TimeoutMs)
	clientMu.Lock()
	defer clientMu.Unlock()
	if cl, ok := clients[key]; ok {
		return cl
	}
	if len(clients) > 200 {
		for _, cl := range clients {
			cl.CloseIdleConnections()
		}
		clients = map[string]*http.Client{}
	}
	cl := client(c)
	clients[key] = cl
	return cl
}

// ---- page call ----

// Headers a page may set (never auth or cookies).
var pageHeaders = map[string]bool{"Accept": true, "Accept-Language": true, "Content-Type": true, "If-None-Match": true, "If-Modified-Since": true, "If-Match": true, "Prefer": true, "Cache-Control": true}

// Response headers passed back to the page.
var safeResponseHeaders = []string{"Content-Type", "Content-Length", "Content-Language", "Etag", "Last-Modified", "Cache-Control", "Expires", "Date", "Retry-After", "Link", "X-Ratelimit-Limit", "X-Ratelimit-Remaining", "X-Ratelimit-Reset", "X-Total-Count", "X-Request-Id"}

type fetchIn struct {
	Method     string          `json:"method"`
	Path       string          `json:"path"`
	Query      map[string]any  `json:"query"`
	Headers    map[string]any  `json:"headers"`
	Body       json.RawMessage `json:"body"`
	BodyBase64 string          `json:"bodyBase64"`
}

// cleanPath validates the page-supplied path and joins it to the base.
func cleanPath(c *Connection, p string) (string, error) {
	if p == "" {
		p = "/"
	}
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\#?\r\n\t ") {
		return "", core.BadRequest("path must start with / and can't contain ?, # or spaces (put query values in query)")
	}
	dec, err := url.PathUnescape(p)
	if err != nil {
		return "", core.BadRequest("path isn't valid")
	}
	for _, seg := range strings.Split(dec, "/") {
		if seg == ".." || seg == "." {
			return "", core.BadRequest("path can't contain . or .. segments")
		}
	}
	if strings.ContainsAny(dec, "\\") || strings.Contains(dec, "//") {
		return "", core.BadRequest("path isn't valid")
	}
	ok := false
	for _, pre := range c.AllowedPrefixes {
		if pre == "/" || dec == pre || strings.HasPrefix(dec, strings.TrimRight(pre, "/")+"/") || (strings.HasSuffix(pre, "*") && strings.HasPrefix(dec, strings.TrimSuffix(pre, "*"))) {
			ok = true
			break
		}
	}
	if !ok {
		return "", core.Forbidden("this connection only allows paths under " + strings.Join(c.AllowedPrefixes, ", "))
	}
	return p, nil
}

func textual(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") || strings.Contains(ct, "xml") || strings.Contains(ct, "javascript") || strings.Contains(ct, "x-www-form-urlencoded") || strings.Contains(ct, "csv") || strings.Contains(ct, "yaml")
}

func doFetch(r *http.Request) (any, error) {
	s, _, err := app.SiteFor(r, core.RoleViewer)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	c, err := get(ctx, r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	if !c.siteAllowed(s.Slug) {
		return nil, core.Forbidden("this site isn't allowed to use the " + c.Name + " connection. Ask your admin to add “" + s.Slug + "” to it.")
	}
	var in fetchIn
	if r.ContentLength != 0 {
		if err := core.Decode(r, &in); err != nil {
			return nil, err
		}
	}
	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if method == "" {
		method = "GET"
	}
	if !validMethods[method] || !c.methodAllowed(method) {
		return nil, core.Forbidden("the " + c.Name + " connection doesn't allow " + method + " (allowed: " + strings.Join(c.AllowedMethods, ", ") + ")")
	}
	p, err := cleanPath(c, in.Path)
	if err != nil {
		return nil, err
	}
	if !allow(strconv.FormatInt(s.ID, 10)+"|"+strconv.FormatInt(c.ID, 10), c.RatePerMin) {
		app.Audit(ctx, "connection.rate_limited", s.ID, c.Name, nil)
		return nil, core.Quota(fmt.Sprintf("too many calls to %s from this site (limit %d a minute). Wait a moment.", c.Name, c.RatePerMin))
	}
	target := c.BaseURL + p
	q := url.Values{}
	for k, v := range in.Query {
		switch x := v.(type) {
		case []any:
			for _, y := range x {
				q.Add(k, fmt.Sprint(y))
			}
		case nil:
		default:
			q.Add(k, fmt.Sprint(x))
		}
	}
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	var body io.Reader
	ctype := ""
	if in.BodyBase64 != "" {
		b, err := base64.StdEncoding.DecodeString(in.BodyBase64)
		if err != nil {
			return nil, core.BadRequest("bodyBase64 isn't valid base64")
		}
		body = strings.NewReader(string(b))
	} else if len(in.Body) > 0 && string(in.Body) != "null" {
		var str string
		if json.Unmarshal(in.Body, &str) == nil {
			body = strings.NewReader(str)
			ctype = "text/plain; charset=utf-8"
		} else {
			body = strings.NewReader(string(in.Body))
			ctype = "application/json"
		}
	}
	tctx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutMs)*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(tctx, method, target, body)
	if err != nil {
		return nil, core.BadRequest("couldn't build the request")
	}
	req.Header.Set("User-Agent", "Bunker/"+core.Version)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for k, v := range in.Headers {
		ck := http.CanonicalHeaderKey(k)
		if !pageHeaders[ck] {
			return nil, core.BadRequest("pages can't set the " + k + " header (allowed: " + strings.Join(sortedKeys(pageHeaders), ", ") + ")")
		}
		sv := fmt.Sprint(v)
		if strings.ContainsAny(sv, "\r\n") {
			return nil, core.BadRequest("header values can't contain line breaks")
		}
		req.Header.Set(ck, sv)
	}
	for k, v := range c.Headers { // secrets last: pages can't override them
		req.Header.Set(k, v)
	}
	start := time.Now()
	meta := map[string]any{"connection": c.Name, "method": method, "path": p}
	res, err := clientFor(c).Do(req)
	if err != nil {
		meta["error"] = err.Error()
		meta["ms"] = time.Since(start).Milliseconds()
		app.Audit(ctx, "connection.call", s.ID, c.Name, meta)
		var se *ssrfError
		var re *redirectError
		switch {
		case errors.As(err, &se):
			return nil, core.Forbidden(c.Name + " points to a private network address, which Bunker blocks. An admin can allow it on the connection if that's intended.")
		case errors.As(err, &re):
			return nil, core.NewErr(502, "upstream", c.Name+" tried to redirect to another site ("+re.to+"); Bunker blocked it.")
		case errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Timeout"):
			return nil, core.NewErr(504, "timeout", c.Name+" didn't answer in time.")
		}
		return nil, core.NewErr(502, "upstream", "couldn't reach "+c.Name+". Check that the service is up.")
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, MaxResponse+1))
	meta["status"] = res.StatusCode
	meta["bytes"] = len(data)
	meta["ms"] = time.Since(start).Milliseconds()
	if err != nil {
		meta["error"] = "read failed"
		app.Audit(ctx, "connection.call", s.ID, c.Name, meta)
		return nil, core.NewErr(502, "upstream", c.Name+" stopped answering halfway.")
	}
	if len(data) > MaxResponse {
		meta["error"] = "too large"
		app.Audit(ctx, "connection.call", s.ID, c.Name, meta)
		return nil, core.TooLarge(c.Name + " sent back more than 5 MB. Ask for less data (paging or filters).")
	}
	app.Audit(ctx, "connection.call", s.ID, c.Name, meta)
	hdr := map[string]string{}
	for _, k := range safeResponseHeaders {
		if v := res.Header.Get(k); v != "" {
			hdr[strings.ToLower(k)] = v
		}
	}
	out := map[string]any{"status": res.StatusCode, "ok": res.StatusCode < 400, "headers": hdr}
	ct := res.Header.Get("Content-Type")
	if (ct == "" || textual(ct)) && utf8.Valid(data) {
		out["body"] = string(data)
		out["encoding"] = "text"
		if strings.Contains(strings.ToLower(ct), "json") {
			var j any
			if json.Unmarshal(data, &j) == nil {
				out["json"] = j
			}
		}
	} else {
		out["body"] = base64.StdEncoding.EncodeToString(data)
		out["encoding"] = "base64"
	}
	return out, nil
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---- ops ----

func initConn(a *core.App) error {
	app = a
	nameParam := core.Param{Name: "name", In: "path", Required: true, Desc: "Connection name"}
	bodyParams := []core.Param{
		{Name: "name", In: "body", Desc: "Short name pages use, e.g. weather"},
		{Name: "description", In: "body"},
		{Name: "baseUrl", In: "body", Desc: "https:// base address"},
		{Name: "headers", In: "body", Type: "object", Desc: "Secret headers added to every call, e.g. {\"Authorization\":\"Bearer …\"}. Stored encrypted; send null to remove one."},
		{Name: "allowedMethods", In: "body", Type: "array", Desc: "Default [GET]"},
		{Name: "allowedPathPrefixes", In: "body", Type: "array", Desc: "Default [/]"},
		{Name: "sites", In: "body", Type: "array", Desc: "Site addresses allowed to use it, or * for all"},
		{Name: "rateLimitPerMinute", In: "body", Type: "integer", Desc: "Per site (default 60)"},
		{Name: "timeoutMs", In: "body", Type: "integer", Desc: "Default 15000"},
		{Name: "allowPrivate", In: "body", Type: "boolean", Desc: "Allow private/internal network addresses (off by default)"},
	}
	a.Handle(core.Op{Name: "admin.connections.list", Method: "GET", Path: "/api/v1/admin/connections", Access: core.AccessAdmin, Summary: "List external API connections",
		Fn: func(r *http.Request) (any, error) {
			rows, err := a.DB.QueryContext(r.Context(), `SELECT `+cols+` FROM connections ORDER BY name`)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			out := []Connection{}
			for rows.Next() {
				c, err := scan(rows)
				if err != nil {
					return nil, err
				}
				out = append(out, c.masked())
			}
			return map[string]any{"connections": out}, nil
		}})
	a.Handle(core.Op{Name: "admin.connections.create", Method: "POST", Path: "/api/v1/admin/connections", Access: core.AccessAdmin, Summary: "Add an external API connection", Params: bodyParams,
		Fn: func(r *http.Request) (any, error) {
			var in input
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			c := &Connection{}
			if err := in.apply(c); err != nil {
				return nil, err
			}
			if err := save(r.Context(), c, core.MustUser(r).ID); err != nil {
				return nil, err
			}
			a.Audit(r.Context(), "admin.config", 0, "connection:"+c.Name, map[string]any{"action": "create", "baseUrl": c.BaseURL, "sites": c.Sites, "headers": sortedHeaderNames(c), "allowPrivate": c.AllowPrivate})
			return core.Created{V: c.masked()}, nil
		}})
	a.Handle(core.Op{Name: "admin.connections.get", Method: "GET", Path: "/api/v1/admin/connections/{name}", Access: core.AccessAdmin, Summary: "Show a connection (secrets hidden)", Params: []core.Param{nameParam},
		Fn: func(r *http.Request) (any, error) {
			c, err := get(r.Context(), r.PathValue("name"))
			if err != nil {
				return nil, err
			}
			return c.masked(), nil
		}})
	a.Handle(core.Op{Name: "admin.connections.update", Method: "PUT", Path: "/api/v1/admin/connections/{name}", Access: core.AccessAdmin, Summary: "Change a connection", Params: append([]core.Param{nameParam}, bodyParams...),
		Fn: func(r *http.Request) (any, error) {
			c, err := get(r.Context(), r.PathValue("name"))
			if err != nil {
				return nil, err
			}
			var in input
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			if err := in.apply(c); err != nil {
				return nil, err
			}
			if err := save(r.Context(), c, 0); err != nil {
				return nil, err
			}
			a.Audit(r.Context(), "admin.config", 0, "connection:"+c.Name, map[string]any{"action": "update", "baseUrl": c.BaseURL, "sites": c.Sites, "headers": sortedHeaderNames(c), "allowPrivate": c.AllowPrivate})
			return c.masked(), nil
		}})
	a.Handle(core.Op{Name: "admin.connections.delete", Method: "DELETE", Path: "/api/v1/admin/connections/{name}", Access: core.AccessAdmin, Summary: "Delete a connection", Destructive: true, Params: []core.Param{nameParam},
		Fn: func(r *http.Request) (any, error) {
			res, err := a.DB.ExecContext(r.Context(), `DELETE FROM connections WHERE name=?`, r.PathValue("name"))
			if err != nil {
				return nil, err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, core.NotFound("there's no connection called " + r.PathValue("name"))
			}
			a.Audit(r.Context(), "admin.config", 0, "connection:"+r.PathValue("name"), map[string]any{"action": "delete"})
			return map[string]any{"ok": true}, nil
		}})
	a.Handle(core.Op{Name: "connections.list", Method: "GET", Path: "/api/v1/sites/{site}/connections", PageSafe: true, Summary: "Connections this site may call",
		Params: []core.Param{{Name: "site", In: "path", Required: true}},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := a.SiteFor(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			rows, err := a.DB.QueryContext(r.Context(), `SELECT `+cols+` FROM connections ORDER BY name`)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			out := []map[string]any{}
			for rows.Next() {
				c, err := scan(rows)
				if err != nil {
					return nil, err
				}
				if c.siteAllowed(s.Slug) {
					out = append(out, map[string]any{"name": c.Name, "description": c.Description, "methods": c.AllowedMethods, "paths": c.AllowedPrefixes})
				}
			}
			return map[string]any{"connections": out}, nil
		}})
	a.Handle(core.Op{Name: "connections.fetch", Method: "POST", Path: "/api/v1/sites/{site}/connections/{name}/fetch", PageSafe: true,
		Summary: "Call an external API through an admin-defined connection (secrets stay on the server)",
		Params: []core.Param{{Name: "site", In: "path", Required: true}, nameParam,
			{Name: "method", In: "body", Desc: "GET (default), POST, …"}, {Name: "path", In: "body", Desc: "Path under the connection's base URL, e.g. /forecast"},
			{Name: "query", In: "body", Type: "object"}, {Name: "headers", In: "body", Type: "object", Desc: "Only Accept, Content-Type and similar"},
			{Name: "body", In: "body", Type: "object", Desc: "JSON value or string"}, {Name: "bodyBase64", In: "body"}},
		Fn: doFetch})
	return nil
}

func sortedHeaderNames(c *Connection) []string {
	var out []string
	for k := range c.Headers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
