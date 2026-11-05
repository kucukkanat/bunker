package sites

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

// Content tickets: site files are served from
//
//	/s/<slug>/~r/<ticket>/<path>
//
// inside a sandboxed iframe with an opaque origin. Sandboxed documents
// don't send our SameSite cookies on subresource requests, so access to
// content is granted by a short-lived signed ticket bound to the site,
// version, viewer and expiry. The HTML document itself additionally
// requires the viewer's session cookie.
type Ticket struct {
	Site int64
	Ver  int64 // 0 = draft
	User int64
	Exp  int64
}

const ticketWindow = 6 * 3600

// MakeTicket issues a ticket. Expiry is bucketed so URLs stay stable (and
// cacheable) for several hours.
func MakeTicket(site, ver, user int64) string {
	exp := (core.Now()/ticketWindow + 2) * ticketWindow
	body := fmt.Sprintf("%d-%d-%d-%d", site, ver, user, exp)
	return body + "-" + core.MAC("t:"+body)
}

// ParseTicket validates a ticket string.
func ParseTicket(s string) (Ticket, bool) { return parseTicket(s, "t:") }

func parseTicket(s, domain string) (Ticket, bool) {
	// site-ver-user-exp-mac; the MAC is base64url and may itself contain '-'.
	parts := strings.SplitN(s, "-", 5)
	if len(parts) != 5 {
		return Ticket{}, false
	}
	body, mac := strings.Join(parts[:4], "-"), parts[4]
	if !core.MACEqual(mac, core.MAC(domain+body)) {
		return Ticket{}, false
	}
	var t Ticket
	var err error
	nums := []*int64{&t.Site, &t.Ver, &t.User, &t.Exp}
	for k, p := range parts[:4] {
		if *nums[k], err = strconv.ParseInt(p, 10, 64); err != nil {
			return Ticket{}, false
		}
	}
	if t.Exp < core.Now() {
		return Ticket{}, false
	}
	return t, true
}

// Embed tickets grant the same read access as content tickets but for
// pages framed by third-party hosts (MCP Apps in Claude, VS Code, Microsoft
// 365 Copilot). They live in a separate MAC domain and URL prefix
// (/s/<slug>/~e/<ticket>/...), only cover published versions, and expire
// after about an hour, so a link copied out of a chat transcript stops
// working soon.
const embedWindow = 15 * 60

// MakeEmbedTicket issues an embed ticket valid for 60–75 minutes.
func MakeEmbedTicket(site, ver, user int64) string {
	exp := (core.Now()/embedWindow + 5) * embedWindow
	body := fmt.Sprintf("%d-%d-%d-%d", site, ver, user, exp)
	return body + "-" + core.MAC("e:"+body)
}

// EmbedExpiry returns when an embed ticket made now expires.
func EmbedExpiry() int64 { return (core.Now()/embedWindow + 5) * embedWindow }

// ParseEmbedTicket validates an embed ticket.
func ParseEmbedTicket(s string) (Ticket, bool) { return parseTicket(s, "e:") }

// SandboxFlags are the iframe sandbox tokens for user content. No
// allow-popups: external links go through bunker.open() so the parent can
// open them safely (noopener, noreferrer) instead of a page leaking data
// through window.open URLs.
const SandboxFlags = "allow-scripts allow-forms allow-modals allow-downloads"

// ContentCSP is the policy for all user content. connect-src 'self' lets
// pages fetch their own files (./data.json); cookies aren't sent from the
// opaque origin and other sites need their own tickets, so this can't
// reach the API or other sites' content.
func ContentCSP() string {
	return "sandbox " + SandboxFlags + "; default-src 'none'; script-src 'self' 'unsafe-inline' 'unsafe-eval' 'wasm-unsafe-eval' blob:; " +
		"style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' data: blob:; font-src 'self' data:; " +
		"worker-src 'self' blob:; connect-src 'self'; form-action 'none'; frame-ancestors " + appOrigin() + "; base-uri 'none'; " +
		"frame-src 'self' blob: data:; object-src 'none'"
}

// EmbedCSP is ContentCSP without frame-ancestors: MCP hosts render the
// wrapper in a sandboxed (opaque-origin) frame, which no frame-ancestors
// source can match. The ticket in the path is what gates access.
func EmbedCSP() string {
	return strings.Replace(ContentCSP(), "frame-ancestors "+appOrigin()+"; ", "", 1)
}

// appOrigin is who may frame user content: Bunker's own origin.
func appOrigin() string {
	if app != nil && app.Cfg.ContentURL != "" && app.Cfg.URL != "" {
		return app.Cfg.URL
	}
	return "'self'"
}

// ContentHeaders sets the isolation headers on every user-content response.
func ContentHeaders(h http.Header) {
	h.Set("Content-Security-Policy", ContentCSP())
	// Chromium/Edge 152+: blocks WebRTC, navigations, prefetch and redirects
	// to anywhere but our own origin (CSP can't stop WebRTC exfiltration).
	h.Set("Connection-Allowlist", "(response-origin)")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	// Opaque-origin frames are cross-origin; tickets gate access.
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), usb=(), serial=(), hid=(), payment=(), display-capture=(), clipboard-read=()")
	h.Add("Vary", "Sec-Fetch-Dest")
	h.Del("Cross-Origin-Opener-Policy")
}

func registerServing(a *core.App) {
	a.Mux.HandleFunc("GET /s/{site}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/s/"+r.PathValue("site")+"/", http.StatusMovedPermanently)
	})
	a.Mux.HandleFunc("GET /s/{site}/{path...}", serveSite)
	a.Mux.HandleFunc("HEAD /s/{site}/{path...}", serveSite)
}

var viewMu sync.Mutex
var viewSeen = map[string]int64{}

func auditView(ctx context.Context, s *SiteRecord, u *core.User) {
	key := itoa64(s.ID) + ":" + itoa64(u.ID)
	now := core.Now()
	viewMu.Lock()
	last := viewSeen[key]
	if now-last < 3600 {
		viewMu.Unlock()
		return
	}
	if len(viewSeen) > 200000 {
		viewSeen = map[string]int64{}
	}
	viewSeen[key] = now
	viewMu.Unlock()
	app.Audit(ctx, "site.view", s.ID, s.Slug, nil)
	app.Emit(ctx, "site.viewed", map[string]any{"siteId": s.ID, "userId": u.ID})
}

// RawHandler serves ticket-gated content under /s/<slug>/<prefix><ticket>/...
// Other modules (uploads, generated assets) register prefixes like "~f/".
type RawHandler func(w http.ResponseWriter, r *http.Request, s *SiteRecord, t Ticket, viewer *core.User, rest string)

var rawPrefixes = map[string]RawHandler{}

// RegisterRawPrefix installs a handler for /s/<slug>/<prefix>... (prefix like "~f/").
// The ticket is validated (signature, expiry, site, viewer access) before h runs.
func RegisterRawPrefix(prefix string, h RawHandler) { rawPrefixes[prefix] = h }

func serveSite(w http.ResponseWriter, r *http.Request) {
	rest := r.PathValue("path")
	if strings.HasPrefix(rest, "~r/") {
		serveRaw(w, r, strings.TrimPrefix(rest, "~r/"), false)
		return
	}
	if strings.HasPrefix(rest, "~e/") {
		serveRaw(w, r, strings.TrimPrefix(rest, "~e/"), true)
		return
	}
	for pre, h := range rawPrefixes {
		if strings.HasPrefix(rest, pre) {
			tail := strings.TrimPrefix(rest, pre)
			i := strings.Index(tail, "/")
			if i < 0 {
				rawError(w, http.StatusNotFound, "Not found")
				return
			}
			t, ok := ParseTicket(tail[:i])
			if !ok {
				rawError(w, http.StatusForbidden, "This link has expired. Reload the page.")
				return
			}
			s, err := GetBySlug(r.Context(), r.PathValue("site"))
			if err != nil || s.ID != t.Site || s.ArchivedAt != nil {
				rawError(w, http.StatusNotFound, "Not found")
				return
			}
			vu := loadTicketUser(r.Context(), t.User)
			if vu == nil || RoleOf(r.Context(), vu, s) == core.RoleNone {
				rawError(w, http.StatusForbidden, "Access revoked")
				return
			}
			ContentHeaders(w.Header())
			h(w, r, s, t, vu, tail[i+1:])
			return
		}
	}
	u := core.UserFrom(r.Context())
	if u == nil {
		http.Redirect(w, r, "/auth/login?next="+urlQueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	s, err := GetBySlug(r.Context(), r.PathValue("site"))
	role := core.RoleNone
	if err == nil {
		role = RoleOf(r.Context(), u, s)
	}
	if err != nil || role == core.RoleNone {
		renderNotFound(w, r, u, r.PathValue("site"))
		return
	}
	if s.ArchivedAt != nil {
		renderArchived(w, u, s, role)
		return
	}
	draft := r.URL.Query().Get("draft") == "1" && role >= core.RoleEditor
	ver := s.Version
	if draft {
		ver = 0
		if _, err := LoadManifest(r.Context(), s.ID, 0); err != nil {
			draft = false
			ver = s.Version
		}
	}
	if ver == 0 && !draft {
		renderEmpty(w, r, s, role)
		return
	}
	if v := r.URL.Query().Get("v"); v != "" && role >= core.RoleEditor {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			if _, err := LoadManifest(r.Context(), s.ID, n); err == nil {
				ver = n
			}
		}
	}
	auditView(r.Context(), s, u)
	markVisit(r.Context(), u.ID, s.ID)
	t := MakeTicket(s.ID, ver, u.ID)
	base := "/s/" + s.Slug + "/~r/" + t + "/"
	src := app.Cfg.ContentURL + base + rest
	q := r.URL.Query()
	q.Del("draft")
	q.Del("v")
	if enc := q.Encode(); enc != "" {
		src += "?" + enc
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src 'self' "+app.Cfg.ContentURL+"; frame-ancestors 'self'; base-uri 'none'; form-action 'self'")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	shellTmpl.Execute(w, map[string]any{
		"Title": s.Title, "Slug": s.Slug, "Src": src, "Base": base, "Role": role.String(),
		"Draft": draft, "Version": ver, "Live": s.Version, "Icon": s.Icon,
		"Badge": !s.Settings.HideBadge || role >= core.RoleEditor, "User": u.Name, "SiteID": s.ID, "Sandbox": SandboxFlags,
	})
}

var shellTmpl = template.Must(template.New("shell").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
<link rel="icon" href="/_/icon.svg">
<link rel="stylesheet" href="/_/shell.css">
</head><body>
<iframe id="bk-frame" title="{{.Title}}" src="{{.Src}}"
 sandbox="{{.Sandbox}}"
 allow="clipboard-write; fullscreen" data-base="{{.Base}}"></iframe>
<div id="bk-shell" data-site="{{.Slug}}" data-site-id="{{.SiteID}}" data-role="{{.Role}}" data-version="{{.Version}}" data-live="{{.Live}}" data-draft="{{.Draft}}" data-badge="{{.Badge}}" data-title="{{.Title}}" data-icon="{{.Icon}}" data-user="{{.User}}"></div>
<script src="/_/shell.js"></script>
</body></html>`))

func renderNotFound(w http.ResponseWriter, r *http.Request, u *core.User, slug string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self'; form-action 'self'")
	w.WriteHeader(http.StatusNotFound)
	// Same page whether or not the site exists, so addresses can't be probed.
	tok := core.MAC(fmt.Sprintf("access:%d:%s", u.ID, slug))
	fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><title>You need access</title><link rel=stylesheet href="/_/kit.css">
<main class="bk-center"><form class="bk-card" method="post" action="/access-request"><img src="/_/icon.svg" width=40 height=40 alt=""><h1>You need access</h1>
<p>This site is private, or the address is wrong. Ask the owner and they can let you in with one click.</p>
<input type="hidden" name="site" value="%s"><input type="hidden" name="t" value="%s">
<p><textarea name="message" rows="2" maxlength="500" placeholder="Add a note (optional)" style="width:100%%;box-sizing:border-box;font:inherit;padding:8px;border-radius:8px;border:1px solid var(--bk-line);background:transparent;color:inherit"></textarea></p>
<p><button class="bk-btn" type="submit" style="border:0;cursor:pointer;font:inherit;font-weight:600">Ask for access</button></p>
<p style="font-size:14px;opacity:.75">Signed in as %s · <a href="/">Go to Bunker</a></p></form></main>`, htmlEscape(slug), htmlEscape(tok), htmlEscape(u.Email))
}

func renderEmpty(w http.ResponseWriter, r *http.Request, s *SiteRecord, role core.Role) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self'")
	if role >= core.RoleEditor {
		simplePage(w, s.Title, "This site hasn't been published yet.", "/sites/"+s.Slug, "Open the editor")
		return
	}
	simplePage(w, s.Title, "This site hasn't been published yet. Check back soon.", "/", "Go to Bunker")
}

func simplePage(w io.Writer, title, msg, href, cta string) {
	fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><title>%s</title><link rel=stylesheet href="/_/kit.css">
<main class="bk-center"><div class="bk-card"><img src="/_/icon.svg" width=40 height=40 alt=""><h1>%s</h1><p>%s</p><p><a class="bk-btn" href="%s">%s</a></p></div></main>`,
		htmlEscape(title), htmlEscape(title), htmlEscape(msg), htmlEscape(href), htmlEscape(cta))
}

// rawCache caches transformed HTML/CSS (SDK injection + root path rewrite).
type rawEntry struct {
	b, br, gz []byte
	exp       time.Time
}

var (
	rawMu    sync.Mutex
	rawCache = map[string]rawEntry{}
)

func serveRaw(w http.ResponseWriter, r *http.Request, rest string, embed bool) {
	i := strings.Index(rest, "/")
	tstr, p := rest, ""
	if i >= 0 {
		tstr, p = rest[:i], rest[i+1:]
	}
	t, ok := ParseTicket(tstr)
	if embed {
		t, ok = ParseEmbedTicket(tstr)
		ok = ok && t.Ver > 0
	}
	if !ok {
		rawError(w, http.StatusForbidden, "This link has expired. Reload the page.")
		return
	}
	ctx := r.Context()
	s, err := GetBySlug(ctx, r.PathValue("site"))
	if err != nil || s.ID != t.Site {
		rawError(w, http.StatusNotFound, "Not found")
		return
	}
	if s.ArchivedAt != nil {
		rawError(w, http.StatusForbidden, "This site was archived. Reload the page.")
		return
	}
	// Re-check the viewer's access (cached) so revoking a share takes effect.
	vu := core.UserFrom(ctx)
	if vu == nil || vu.ID != t.User {
		vu = loadTicketUser(ctx, t.User)
	}
	if vu == nil {
		rawError(w, http.StatusForbidden, "Access revoked")
		return
	}
	role := RoleOf(ctx, vu, s)
	if role == core.RoleNone || (t.Ver == 0 && role < core.RoleEditor) {
		rawError(w, http.StatusForbidden, "Access revoked")
		return
	}
	m, err := LoadManifest(ctx, s.ID, t.Ver)
	if err != nil {
		rawError(w, http.StatusNotFound, "Version not found")
		return
	}
	clean := strings.TrimSuffix(p, "/")
	name, f, found := resolveFile(m, p, s.Settings)
	status := http.StatusOK
	if !found {
		status = http.StatusNotFound
		if s.Settings.NotFound != "" {
			if nf, ok := m[s.Settings.NotFound]; ok {
				name, f, found = s.Settings.NotFound, nf, true
			}
		} else if nf, ok := m["404.html"]; ok {
			name, f, found = "404.html", nf, true
		}
		if !found {
			rawError(w, http.StatusNotFound, "No page at /"+clean)
			return
		}
	}
	h := w.Header()
	ContentHeaders(h)
	if embed {
		h.Set("Content-Security-Policy", EmbedCSP())
	}
	ct := f.Type
	isHTML := strings.HasPrefix(ct, "text/html")
	isCSS := strings.HasPrefix(ct, "text/css")
	// Session cookie required for documents (not for subresources, which
	// sandboxed frames can't send cookies with).
	if isHTML && !IsContentHost(r) && r.Header.Get("Sec-Fetch-Dest") != "iframe" && r.Header.Get("Sec-Fetch-Dest") != "" && core.UserFrom(ctx) == nil {
		rawError(w, http.StatusForbidden, "Open this page through Bunker.")
		return
	}
	etag := `"` + f.Hash[:20] + `"`
	if t.Ver > 0 {
		h.Set("Cache-Control", "private, max-age=86400, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	h.Set("ETag", etag)
	h.Set("Content-Type", ct)
	if r.Header.Get("If-None-Match") == etag && status == 200 {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if isHTML || isCSS {
		prefix := "/s/" + s.Slug + "/~r/" + tstr + "/"
		if embed {
			prefix = "/s/" + s.Slug + "/~e/" + tstr + "/"
		}
		key := f.Hash + "|" + prefix
		rawMu.Lock()
		e, ok := rawCache[key]
		rawMu.Unlock()
		if !ok || time.Now().After(e.exp) {
			b, err := app.Blobs.Read(f.Hash)
			if err != nil {
				rawError(w, 500, "content missing")
				return
			}
			if isHTML {
				b = InjectSDK(rewriteRoot(b, prefix), name)
			} else {
				b = rewriteCSS(b, prefix)
			}
			e = rawEntry{b: b, exp: time.Now().Add(10 * time.Minute)}
			if len(b) > 1024 {
				e.br, e.gz = core.CompressBrotli(b), core.CompressGzip(b)
			}
			rawMu.Lock()
			if len(rawCache) > 2000 {
				rawCache = map[string]rawEntry{}
			}
			rawCache[key] = e
			rawMu.Unlock()
		}
		body := e.b
		ae := r.Header.Get("Accept-Encoding")
		h.Add("Vary", "Accept-Encoding")
		if e.br != nil && strings.Contains(ae, "br") {
			body = e.br
			h.Set("Content-Encoding", "br")
		} else if e.gz != nil && strings.Contains(ae, "gzip") {
			body = e.gz
			h.Set("Content-Encoding", "gzip")
		}
		h.Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			w.Write(body)
		}
		return
	}
	ae := r.Header.Get("Accept-Encoding")
	if compressible(ct) && f.Size > 1024 && r.Header.Get("Range") == "" && (strings.Contains(ae, "gzip") || strings.Contains(ae, "br")) {
		enc, variant := "gzip", ""
		if strings.Contains(ae, "br") {
			if variant = app.Blobs.Brotli(f.Hash); variant != "" {
				enc = "br"
			}
		}
		if variant == "" && strings.Contains(ae, "gzip") {
			variant = app.Blobs.Gzip(f.Hash)
		}
		if gz := variant; gz != "" {
			if fh, err := os.Open(gz); err == nil {
				defer fh.Close()
				h.Set("Content-Encoding", enc)
				h.Add("Vary", "Accept-Encoding")
				if st, err := fh.Stat(); err == nil {
					h.Set("Content-Length", strconv.FormatInt(st.Size(), 10))
				}
				w.WriteHeader(status)
				if r.Method != http.MethodHead {
					io.Copy(w, fh)
				}
				return
			}
		}
	}
	fh, err := app.Blobs.Open(f.Hash)
	if err != nil {
		rawError(w, 500, "content missing")
		return
	}
	defer fh.Close()
	if status != 200 {
		w.WriteHeader(status)
		io.Copy(w, fh)
		return
	}
	http.ServeContent(w, r, "", time.Time{}, fh) // handles Range for video/audio
}

// IsContentHost reports whether r arrived on the separate user-content origin.
func IsContentHost(r *http.Request) bool {
	if app.Cfg.ContentURL == "" {
		return false
	}
	return strings.HasSuffix(app.Cfg.ContentURL, "://"+r.Host)
}

func loadTicketUser(ctx context.Context, id int64) *core.User {
	var email, name, gj string
	var admin, disabled int
	if app.DB.QueryRowContext(ctx, `SELECT email,name,is_admin,groups_json,disabled FROM users WHERE id=?`, id).Scan(&email, &name, &admin, &gj, &disabled) != nil || disabled == 1 {
		return nil
	}
	u := &core.User{ID: id, Email: email, Name: name, IsAdmin: admin == 1}
	_ = jsonUnmarshal(gj, &u.Groups)
	return u
}

func resolveFile(m Manifest, p string, st Settings) (string, FileRef, bool) {
	if p == "" || strings.HasSuffix(p, "/") {
		entry := "index.html"
		if p == "" && st.Entry != "" {
			entry = st.Entry
		}
		if f, ok := m[p+entry]; ok {
			return p + entry, f, true
		}
		if p == "" {
			return "", FileRef{}, false
		}
		p = strings.TrimSuffix(p, "/")
	}
	if f, ok := m[p]; ok {
		return p, f, true
	}
	if f, ok := m[p+"/index.html"]; ok {
		return p + "/index.html", f, true
	}
	if f, ok := m[p+".html"]; ok {
		return p + ".html", f, true
	}
	if st.SPA && path.Ext(p) == "" {
		if f, ok := m["index.html"]; ok {
			return "index.html", f, true
		}
	}
	return "", FileRef{}, false
}

func compressible(ct string) bool {
	return strings.HasPrefix(ct, "text/") || strings.Contains(ct, "javascript") || strings.Contains(ct, "json") || strings.Contains(ct, "svg") || strings.Contains(ct, "wasm") || strings.Contains(ct, "xml")
}

func rawError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	ContentHeaders(w.Header())
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><body style="font:15px system-ui;display:grid;place-items:center;min-height:90vh;color:#5a635d">%s</body>`, htmlEscape(msg))
}

var sdkTag = []byte(`<script src="/_/bunker.js"></script>`)
var headRe = regexp.MustCompile(`(?i)<head[^>]*>`)
var htmlRe = regexp.MustCompile(`(?i)<html[^>]*>`)

// InjectSDK adds the Bunker SDK script as the first thing in <head>.
func InjectSDK(b []byte, name string) []byte {
	if bytes.Contains(b, sdkTag) {
		return b
	}
	if loc := headRe.FindIndex(b); loc != nil {
		return concat(b[:loc[1]], sdkTag, b[loc[1]:])
	}
	if loc := htmlRe.FindIndex(b); loc != nil {
		return concat(b[:loc[1]], sdkTag, b[loc[1]:])
	}
	return concat(sdkTag, b)
}

func concat(parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// Root-relative URLs ("/assets/app.js") would escape the site's prefix;
// rewrite them in HTML attributes and CSS so build tools with base "/" work.
var attrRe = regexp.MustCompile(`(?i)(\s(?:src|href|action|poster|data|formaction)\s*=\s*["']?)/([^/"'\s>][^"'\s>]*)?`)
var srcsetRe = regexp.MustCompile(`(?i)(\ssrcset\s*=\s*["'])([^"']*)`)
var cssURLRe = regexp.MustCompile(`(?i)(url\(\s*["']?)/([^/)"'][^)"']*)`)
var importRe = regexp.MustCompile(`(?i)(@import\s+["'])/([^/"'][^"']*)`)

func rewriteRoot(b []byte, prefix string) []byte {
	if !bytes.Contains(b, []byte(`="/`)) && !bytes.Contains(b, []byte(`='/`)) && !bytes.Contains(b, []byte("url(")) && !bytes.Contains(b, []byte("srcset")) {
		return b
	}
	b = attrRe.ReplaceAllFunc(b, func(m []byte) []byte {
		sm := attrRe.FindSubmatch(m)
		if bytes.HasPrefix(sm[2], []byte("_/")) || bytes.HasPrefix(sm[2], []byte("s/")) {
			return m // Bunker platform assets or other sites
		}
		return concat(sm[1], []byte(prefix), sm[2])
	})
	b = srcsetRe.ReplaceAllFunc(b, func(m []byte) []byte {
		sm := srcsetRe.FindSubmatch(m)
		parts := bytes.Split(sm[2], []byte(","))
		for i, p := range parts {
			t := bytes.TrimLeft(p, " ")
			if bytes.HasPrefix(t, []byte("/")) && !bytes.HasPrefix(t, []byte("//")) {
				lead := p[:len(p)-len(t)]
				parts[i] = concat(lead, []byte(prefix), t[1:])
			}
		}
		return concat(sm[1], bytes.Join(parts, []byte(",")))
	})
	return rewriteCSS(b, prefix)
}

func rewriteCSS(b []byte, prefix string) []byte {
	b = cssURLRe.ReplaceAll(b, []byte("${1}"+prefix+"${2}"))
	return importRe.ReplaceAll(b, []byte("${1}"+prefix+"${2}"))
}

func urlQueryEscape(s string) string {
	return strings.NewReplacer("%", "%25", "&", "%26", "?", "%3F", "#", "%23", "+", "%2B", " ", "%20").Replace(s)
}
