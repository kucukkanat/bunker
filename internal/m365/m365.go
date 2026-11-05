// Package m365 lets site pages read Microsoft 365 data (SharePoint lists,
// Excel ranges, OneDrive/SharePoint files, any allowlisted Graph GET) with
// the viewer's own delegated rights. Pages never see tokens: Bunker makes
// the Graph call server-side and returns only the result.
package m365

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/auth"
	"github.com/kucukkanat/bunker/internal/core"
)

func init() {
	core.Register(core.Module{Name: "m365", Order: 60, Init: initM365})
}

var app *core.App

// HTTPClient is used for Graph calls (overridable in tests).
var HTTPClient = &http.Client{Timeout: 25 * time.Second}

// scopeRule maps a Graph path prefix to the delegated scopes that grant it.
// The longest matching prefix wins, so /me/drive needs Files.* even though
// /me only needs User.Read.
type scopeRule struct {
	Prefix string   `json:"prefix"`
	Scopes []string `json:"scopes"`
}

var rules = []scopeRule{
	{"/me", []string{"User.Read"}},
	{"/me/photo", []string{"User.Read"}},
	{"/me/memberof", []string{"User.Read"}},
	{"/me/transitivememberof", []string{"User.Read"}},
	{"/me/manager", []string{"User.Read.All", "User.ReadBasic.All"}},
	{"/me/directreports", []string{"User.Read.All", "User.ReadBasic.All"}},
	{"/me/drive", []string{"Files.Read", "Files.Read.All", "Files.ReadWrite", "Files.ReadWrite.All"}},
	{"/me/drives", []string{"Files.Read", "Files.Read.All"}},
	{"/me/events", []string{"Calendars.Read", "Calendars.ReadWrite"}},
	{"/me/calendar", []string{"Calendars.Read", "Calendars.ReadWrite"}},
	{"/me/calendars", []string{"Calendars.Read", "Calendars.ReadWrite"}},
	{"/me/calendarview", []string{"Calendars.Read", "Calendars.ReadWrite"}},
	{"/me/messages", []string{"Mail.Read", "Mail.ReadBasic"}},
	{"/me/mailfolders", []string{"Mail.Read", "Mail.ReadBasic"}},
	{"/me/people", []string{"People.Read"}},
	{"/me/planner", []string{"Tasks.Read", "Tasks.ReadWrite"}},
	{"/me/todo", []string{"Tasks.Read", "Tasks.ReadWrite"}},
	{"/me/joinedteams", []string{"Team.ReadBasic.All"}},
	{"/me/insights", []string{"Sites.Read.All"}},
	{"/users", []string{"User.ReadBasic.All", "User.Read.All"}},
	{"/groups", []string{"Group.Read.All", "GroupMember.Read.All"}},
	{"/sites", []string{"Sites.Read.All", "Sites.ReadWrite.All"}},
	{"/drives", []string{"Files.Read.All", "Sites.Read.All", "Files.ReadWrite.All", "Sites.ReadWrite.All"}},
	{"/shares", []string{"Files.Read.All", "Sites.Read.All", "Files.ReadWrite.All", "Sites.ReadWrite.All"}},
	{"/planner", []string{"Tasks.Read", "Tasks.ReadWrite"}},
	{"/teams", []string{"Team.ReadBasic.All", "TeamSettings.Read.All"}},
}

// scopesFor returns the scopes that grant path, or nil if Bunker never
// allows it (e.g. /applications, /directoryRoles).
func scopesFor(p string) []string {
	lp := strings.ToLower(p)
	best := -1
	for i, r := range rules {
		if (lp == r.Prefix || strings.HasPrefix(lp, r.Prefix+"/") || strings.HasPrefix(lp, r.Prefix+"(") || strings.HasPrefix(lp, r.Prefix+":")) && (best < 0 || len(r.Prefix) > len(rules[best].Prefix)) {
			best = i
		}
	}
	if best < 0 {
		return nil
	}
	return rules[best].Scopes
}

// Config is the admin configuration.
type Config struct {
	Enabled         bool     `json:"enabled"`
	Scopes          []string `json:"scopes"`
	RequireApproval bool     `json:"requireApproval"`
	ApprovedSites   []string `json:"approvedSites"` // Bunker site slugs allowed when RequireApproval
	AllowedSites    []string `json:"allowedSites"`  // SharePoint site URLs pages may read (empty = any)
}

func splitList(s string) []string {
	var out []string
	for _, x := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' || r == ';' }) {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func loadConfig() Config {
	c := Config{
		Scopes:          splitList(app.Setting("m365.scopes")),
		RequireApproval: app.Setting("m365.require_approval") == "true",
		ApprovedSites:   splitList(app.Setting("m365.approved_sites")),
		AllowedSites:    splitList(app.Setting("m365.allowed_sites")),
	}
	switch app.Setting("m365.enabled") {
	case "true":
		c.Enabled = true
	case "false":
	default:
		c.Enabled = len(c.Scopes) > 0 // BUNKER_M365_SCOPES alone turns it on
	}
	if c.Scopes == nil {
		c.Scopes = []string{}
	}
	if c.ApprovedSites == nil {
		c.ApprovedSites = []string{}
	}
	if c.AllowedSites == nil {
		c.AllowedSites = []string{}
	}
	return c
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func hasFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// ---- response cache (protects Graph throttling limits) ----

type cacheEntry struct {
	status int
	body   []byte
	exp    time.Time
}

var (
	cacheMu  sync.Mutex
	cache    = map[string]cacheEntry{}
	cacheTTL = 30 * time.Second
)

const cacheMax = 2000

func cacheGet(k string) (cacheEntry, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	e, ok := cache[k]
	if !ok || time.Now().After(e.exp) {
		delete(cache, k)
		return cacheEntry{}, false
	}
	return e, true
}

// cacheMaxBody/cacheMaxBytes bound memory: entries were only counted, and
// 2000 answers of up to 8 MB each could pin gigabytes.
const (
	cacheMaxBody  = 512 << 10
	cacheMaxBytes = 64 << 20
)

var cacheBytes int

func cachePut(k string, e cacheEntry) {
	if len(e.body) > cacheMaxBody {
		return
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if old, ok := cache[k]; ok {
		cacheBytes -= len(old.body)
	}
	if len(cache) >= cacheMax || cacheBytes+len(e.body) > cacheMaxBytes {
		cacheBytes = 0
		now := time.Now()
		for key, v := range cache {
			if now.After(v.exp) {
				delete(cache, key)
			} else {
				cacheBytes += len(v.body)
			}
		}
		if len(cache) >= cacheMax || cacheBytes+len(e.body) > cacheMaxBytes {
			cache, cacheBytes = map[string]cacheEntry{}, 0
		}
	}
	cacheBytes += len(e.body)
	cache[k] = e
}

// ---- errors ----

// consentErr asks the shell to send the viewer through incremental consent.
type consentErr struct {
	msg    string
	url    string
	scopes []string
}

func (e *consentErr) Error() string { return "consent_required: " + e.msg }

func writeErr(w http.ResponseWriter, err error) {
	var ce *consentErr
	if errors.As(err, &ce) {
		core.JSON(w, 403, map[string]any{"error": map[string]any{"code": "consent_required", "message": ce.msg, "consentUrl": ce.url, "scopes": ce.scopes}})
		return
	}
	core.Err(w, err)
}

// ---- core call ----

type caller struct {
	r    *http.Request
	u    *core.User
	site *core.Site
	cfg  Config
}

func newCaller(r *http.Request) (*caller, error) {
	s, _, err := app.SiteFor(r, core.RoleViewer)
	if err != nil {
		return nil, err
	}
	cfg := loadConfig()
	if !cfg.Enabled {
		return nil, core.NewErr(403, "m365_disabled", "Microsoft 365 isn't turned on for this Bunker. Ask your admin to enable it under Admin → Microsoft 365.")
	}
	if cfg.RequireApproval && !hasFold(cfg.ApprovedSites, s.Slug) {
		return nil, core.NewErr(403, "m365_not_approved", "This site hasn't been approved to use Microsoft 365 data yet. Ask your admin to approve “"+s.Slug+"”.")
	}
	return &caller{r: r, u: core.MustUser(r), site: s, cfg: cfg}, nil
}

// validPath rejects anything that isn't a plain Graph resource path.
func validPath(p string) (string, string, error) {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "https://graph.microsoft.com")
	p = strings.TrimPrefix(p, "/v1.0")
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\#\r\n\t") || strings.Contains(p, "/../") || strings.HasSuffix(p, "/..") {
		return "", "", core.BadRequest("that isn't a Microsoft Graph path (it should look like /me or /sites/…)")
	}
	pathPart, query, _ := strings.Cut(p, "?")
	if dec, err := url.PathUnescape(pathPart); err != nil || strings.Contains(dec, "..") || strings.Contains(dec, "\\") {
		return "", "", core.BadRequest("that Graph path isn't allowed")
	}
	return pathPart, query, nil
}

// get performs a cached, allowlisted, audited Graph GET for the viewer.
// sitesChecked is true when a helper already enforced AllowedSites.
func (c *caller) get(ctx context.Context, rawPath string, sitesChecked bool) (int, []byte, error) {
	p, query, err := validPath(rawPath)
	if err != nil {
		return 0, nil, err
	}
	need := scopesFor(p)
	if need == nil {
		return 0, nil, core.Forbidden("pages can't read " + p + " from Microsoft 365")
	}
	var allowed []string
	for _, s := range need {
		// User.Read is part of every Entra sign-in, so /me always works.
		if s == "User.Read" || hasFold(c.cfg.Scopes, s) {
			allowed = append(allowed, canonicalScope(c.cfg.Scopes, s))
		}
	}
	if len(allowed) == 0 {
		return 0, nil, core.NewErr(403, "scope_not_allowed", fmt.Sprintf("Your admin hasn't allowed pages to read %s. It needs one of: %s.", p, strings.Join(need, ", ")))
	}
	if !sitesChecked && len(c.cfg.AllowedSites) > 0 {
		if err := c.checkSitePath(p); err != nil {
			return 0, nil, err
		}
	}
	full := p
	if query != "" {
		full += "?" + query
	}
	key := strconv.FormatInt(c.u.ID, 10) + "|" + full
	if e, ok := cacheGet(key); ok {
		return e.status, e.body, nil
	}
	tok, err := c.token(ctx, allowed)
	if err != nil {
		return 0, nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", auth.GraphBase+full, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if strings.Contains(strings.ToLower(query), "$search") || strings.Contains(strings.ToLower(query), "$count") || strings.Contains(strings.ToLower(p), "microsoft.graph.") {
		req.Header.Set("ConsistencyLevel", "eventual")
	}
	start := time.Now()
	res, err := HTTPClient.Do(req)
	if err != nil {
		app.Audit(ctx, "m365.call", c.site.ID, p, map[string]any{"status": 0, "error": "unreachable"})
		return 0, nil, core.Unavailable("Microsoft 365 didn't answer. Try again in a moment.")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20+1))
	if err != nil {
		return 0, nil, core.Unavailable("Microsoft 365 connection dropped")
	}
	if len(body) > 8<<20 {
		return 0, nil, core.TooLarge("that Microsoft 365 result is too big (over 8 MB). Ask for fewer items with $top or $select.")
	}
	app.Audit(ctx, "m365.call", c.site.ID, p, map[string]any{"status": res.StatusCode, "ms": time.Since(start).Milliseconds()})
	if res.StatusCode >= 300 {
		return res.StatusCode, nil, graphError(res.StatusCode, body, res.Header.Get("Retry-After"))
	}
	cachePut(key, cacheEntry{status: res.StatusCode, body: body, exp: time.Now().Add(cacheTTL)})
	return res.StatusCode, body, nil
}

func canonicalScope(list []string, s string) string {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return x
		}
	}
	return s
}

// token returns a delegated token carrying one of the allowed scopes, or a
// consent error naming them.
func (c *caller) token(ctx context.Context, allowed []string) (string, error) {
	var lastErr error
	for _, s := range allowed {
		var tok string
		var err error
		if s == "User.Read" {
			tok, err = auth.AccessToken(ctx, c.u.ID)
		} else {
			tok, err = auth.AccessToken(ctx, c.u.ID, s)
		}
		if err == nil {
			return tok, nil
		}
		lastErr = err
		var he *core.HTTPError
		if !errors.As(err, &he) || he.Code != "consent_required" {
			return "", err
		}
	}
	msg := "Allow Bunker to read this Microsoft 365 data for you (one-time)."
	if lastErr != nil {
		var he *core.HTTPError
		if errors.As(lastErr, &he) && strings.HasPrefix(he.Message, "sign in again") {
			msg = "Sign in again to let Bunker read Microsoft 365 data for you."
		}
	}
	return "", &consentErr{msg: msg, url: auth.ConsentURL("/s/"+c.site.Slug+"/", allowed[0]), scopes: allowed[:1]}
}

func graphError(status int, body []byte, retry string) error {
	var ge struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	json.Unmarshal(body, &ge)
	m := ge.Error.Message
	switch status {
	case 401:
		return core.NewErr(403, "consent_required", "Sign in again to let Bunker read Microsoft 365 data for you.")
	case 403:
		return core.NewErr(403, "forbidden", "Microsoft 365 says you don't have access to this. "+m)
	case 404:
		return core.NotFound("Microsoft 365 couldn't find that. Check the address or name. " + m)
	case 429, 503:
		s := "Microsoft 365 is busy. Try again in a minute."
		if retry != "" {
			s = "Microsoft 365 is busy. Try again in " + retry + " seconds."
		}
		return core.NewErr(429, "throttled", s)
	case 400:
		return core.BadRequest("Microsoft 365 didn't understand the request: " + m)
	}
	return core.NewErr(502, "upstream", fmt.Sprintf("Microsoft 365 error %d: %s", status, m))
}

// ---- SharePoint helpers ----

// spSite parses a SharePoint URL into host and site path
// ("https://contoso.sharepoint.com/sites/Sales/Shared Documents/x" →
// "contoso.sharepoint.com", "/sites/Sales").
func spSite(raw string) (string, string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", "", core.BadRequest("give the SharePoint site as a full https:// address, e.g. https://contoso.sharepoint.com/sites/Sales")
	}
	host := strings.ToLower(u.Hostname())
	if !strings.HasSuffix(host, ".sharepoint.com") && !strings.HasSuffix(host, ".sharepoint.us") && !strings.HasSuffix(host, ".sharepoint.de") && !strings.HasSuffix(host, ".sharepoint.cn") {
		return "", "", core.BadRequest(host + " isn't a SharePoint address")
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	p := ""
	if len(segs) >= 2 && (strings.EqualFold(segs[0], "sites") || strings.EqualFold(segs[0], "teams") || strings.EqualFold(segs[0], "personal")) {
		p = "/" + segs[0] + "/" + segs[1]
	}
	return host, p, nil
}

// siteAllowed reports whether host+path is on the admin's AllowedSites list.
func (c *caller) siteAllowed(host, p string) bool {
	if len(c.cfg.AllowedSites) == 0 {
		return true
	}
	for _, a := range c.cfg.AllowedSites {
		ah, ap, err := spSite(a)
		if err != nil {
			ah = strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(a, "https://"), "http://"))
			ah, ap, _ = strings.Cut(ah, "/")
			if ap != "" {
				ap = "/" + ap
			}
		}
		if ah == host && (ap == "" || strings.EqualFold(strings.TrimRight(ap, "/"), p)) {
			return true
		}
	}
	return false
}

func (c *caller) checkSitePath(p string) error {
	lp := strings.ToLower(p)
	if !strings.HasPrefix(lp, "/sites") && !strings.HasPrefix(lp, "/drives") && !strings.HasPrefix(lp, "/shares") {
		return nil
	}
	// Accept /sites/{host}:/{path}[:/...] when that site is allowed.
	if strings.HasPrefix(lp, "/sites/") {
		rest := p[len("/sites/"):]
		if i := strings.Index(rest, ":/"); i > 0 {
			host := strings.ToLower(rest[:i])
			sp := rest[i+1:]
			if j := strings.Index(sp, ":"); j >= 0 {
				sp = sp[:j]
			}
			segs := strings.Split(strings.Trim(sp, "/"), "/")
			if len(segs) >= 2 && c.siteAllowed(host, "/"+segs[0]+"/"+segs[1]) {
				return nil
			}
		}
	}
	return core.Forbidden("your admin limited Microsoft 365 to certain SharePoint sites. Use the site's address (lists/items helper) for one of: " + strings.Join(c.cfg.AllowedSites, ", "))
}

func encodeSeg(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func (c *caller) getJSON(ctx context.Context, p string, out any) error {
	_, body, err := c.get(ctx, p, true)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

// resolveSiteID turns a SharePoint URL into a Graph site ID.
func (c *caller) resolveSiteID(ctx context.Context, raw string) (string, error) {
	host, p, err := spSite(raw)
	if err != nil {
		return "", err
	}
	if !c.siteAllowed(host, p) {
		return "", core.Forbidden("your admin hasn't allowed pages to read that SharePoint site")
	}
	gp := "/sites/" + host
	if p != "" {
		gp += ":" + encodeSeg(p)
	}
	var s struct {
		ID string `json:"id"`
	}
	if err := c.getJSON(ctx, gp+"?$select=id", &s); err != nil {
		return "", err
	}
	if s.ID == "" {
		return "", core.NotFound("SharePoint site not found")
	}
	return s.ID, nil
}

// shareID encodes a sharing URL for /shares/{id} (u! + base64url, no padding).
func shareID(link string) string {
	return "u!" + strings.TrimRight(base64.URLEncoding.EncodeToString([]byte(link)), "=")
}

// driveItem resolves a share/SharePoint URL or a OneDrive path to a Graph
// item path prefix like /drives/{d}/items/{i} or /me/drive/root:/a/b.xlsx:
func (c *caller) driveItem(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", core.BadRequest("which file? Give a sharing link or a OneDrive path like /Documents/Budget.xlsx")
	}
	if strings.HasPrefix(ref, "https://") {
		u, err := url.Parse(ref)
		if err != nil {
			return "", core.BadRequest("that link doesn't look right")
		}
		host := strings.ToLower(u.Hostname())
		if strings.Contains(host, "sharepoint.") {
			_, sp, _ := spSite(ref)
			if !c.siteAllowed(host, sp) {
				return "", core.Forbidden("your admin hasn't allowed pages to read files from that SharePoint site")
			}
		} else if host != "1drv.ms" && host != "onedrive.live.com" {
			return "", core.BadRequest(host + " isn't a OneDrive or SharePoint link")
		} else if len(c.cfg.AllowedSites) > 0 {
			return "", core.Forbidden("your admin limited Microsoft 365 to certain SharePoint sites")
		}
		var it struct {
			ID        string `json:"id"`
			ParentRef struct {
				DriveID string `json:"driveId"`
			} `json:"parentReference"`
		}
		if err := c.getJSON(ctx, "/shares/"+shareID(ref)+"/driveItem?$select=id,parentReference", &it); err != nil {
			return "", err
		}
		if it.ID == "" || it.ParentRef.DriveID == "" {
			return "", core.NotFound("couldn't open that link")
		}
		return "/drives/" + url.PathEscape(it.ParentRef.DriveID) + "/items/" + url.PathEscape(it.ID), nil
	}
	if len(c.cfg.AllowedSites) > 0 {
		return "", core.Forbidden("your admin limited Microsoft 365 to certain SharePoint sites; use a SharePoint link")
	}
	p := "/" + strings.Trim(ref, "/")
	return "/me/drive/root:" + encodeSeg(p) + ":", nil
}

// ---- ops ----

var siteParam = core.Param{Name: "site", In: "path", Required: true, Desc: "Bunker site address"}

func handle(fn func(c *caller, r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := newCaller(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		v, err := fn(c, r)
		if err != nil {
			writeErr(w, err)
			return
		}
		if raw, ok := v.(json.RawMessage); ok {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Write(raw)
			return
		}
		core.JSON(w, 200, v)
	}
}

func initM365(a *core.App) error {
	app = a
	a.Handle(core.Op{Name: "m365.graph", Method: "GET", Path: "/api/v1/sites/{site}/m365/graph", PageSafe: true,
		Summary: "Read Microsoft Graph as the viewer (GET only, admin-allowlisted paths)",
		Params:  []core.Param{siteParam, {Name: "path", In: "query", Required: true, Desc: "Graph path, e.g. /me or /sites/contoso.sharepoint.com:/sites/Sales"}},
		Handler: handle(func(c *caller, r *http.Request) (any, error) {
			_, body, err := c.get(r.Context(), r.URL.Query().Get("path"), false)
			if err != nil {
				return nil, err
			}
			return json.RawMessage(body), nil
		})})
	a.Handle(core.Op{Name: "m365.lists.items", Method: "GET", Path: "/api/v1/sites/{site}/m365/lists/items", PageSafe: true,
		Summary: "Read a SharePoint list's items as the viewer",
		Params: []core.Param{siteParam, {Name: "siteUrl", In: "query", Required: true, Desc: "SharePoint site URL (also accepted as ?site=)"},
			{Name: "list", In: "query", Required: true, Desc: "List name or ID"}, {Name: "top", In: "query", Type: "integer", Desc: "Max items (default 200, max 5000)"}},
		Handler: handle(listItems)})
	a.Handle(core.Op{Name: "m365.excel.range", Method: "GET", Path: "/api/v1/sites/{site}/m365/excel/range", PageSafe: true,
		Summary: "Read cells from an Excel workbook in OneDrive/SharePoint as the viewer",
		Params: []core.Param{siteParam, {Name: "file", In: "query", Required: true, Desc: "Sharing link or OneDrive path"},
			{Name: "range", In: "query", Desc: "e.g. Sheet1!A1:D20 (default: used range of the first sheet)"}},
		Handler: handle(excelRange)})
	a.Handle(core.Op{Name: "m365.files", Method: "GET", Path: "/api/v1/sites/{site}/m365/files", PageSafe: true,
		Summary: "List files in a OneDrive/SharePoint folder as the viewer",
		Params:  []core.Param{siteParam, {Name: "folder", In: "query", Desc: "Folder sharing link or OneDrive path (default: OneDrive root)"}},
		Handler: handle(listFiles)})

	a.Handle(core.Op{Name: "admin.m365.get", Method: "GET", Path: "/api/v1/admin/m365", Access: core.AccessAdmin, Summary: "Microsoft 365 settings",
		Fn: func(r *http.Request) (any, error) {
			cfg := loadConfig()
			known := map[string]bool{}
			for _, ru := range rules {
				for _, s := range ru.Scopes {
					known[s] = true
				}
			}
			var ks []string
			for s := range known {
				ks = append(ks, s)
			}
			sort.Strings(ks)
			return map[string]any{"config": cfg, "knownScopes": ks, "paths": rules, "entra": auth.Provider().Kind == "entra",
				"pinned": map[string]bool{"scopes": a.SettingFromEnv("m365.scopes"), "enabled": a.SettingFromEnv("m365.enabled")}}, nil
		}})
	a.Handle(core.Op{Name: "admin.m365.update", Method: "PUT", Path: "/api/v1/admin/m365", Access: core.AccessAdmin, Summary: "Change Microsoft 365 settings",
		Params: []core.Param{{Name: "enabled", In: "body", Type: "boolean"}, {Name: "scopes", In: "body", Type: "array", Desc: "Delegated Graph scopes pages may use, e.g. Sites.Read.All"},
			{Name: "requireApproval", In: "body", Type: "boolean"}, {Name: "approvedSites", In: "body", Type: "array", Desc: "Bunker site slugs approved for M365"},
			{Name: "allowedSites", In: "body", Type: "array", Desc: "SharePoint site URLs pages may read (empty = any)"}},
		Fn: func(r *http.Request) (any, error) {
			var in struct {
				Enabled         *bool     `json:"enabled"`
				Scopes          *[]string `json:"scopes"`
				RequireApproval *bool     `json:"requireApproval"`
				ApprovedSites   *[]string `json:"approvedSites"`
				AllowedSites    *[]string `json:"allowedSites"`
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			ctx := r.Context()
			old := loadConfig()
			if in.Scopes != nil {
				for _, s := range *in.Scopes {
					if s == "" || strings.ContainsAny(s, " /:?#") {
						return nil, core.BadRequest("scope names look like Sites.Read.All")
					}
				}
				a.SetSetting(ctx, "m365.scopes", strings.Join(*in.Scopes, ","))
			}
			if in.Enabled != nil {
				a.SetSetting(ctx, "m365.enabled", strconv.FormatBool(*in.Enabled))
			}
			if in.RequireApproval != nil {
				a.SetSetting(ctx, "m365.require_approval", strconv.FormatBool(*in.RequireApproval))
			}
			if in.ApprovedSites != nil {
				a.SetSetting(ctx, "m365.approved_sites", strings.Join(*in.ApprovedSites, ","))
			}
			if in.AllowedSites != nil {
				for _, s := range *in.AllowedSites {
					if _, _, err := spSite(s); err != nil {
						return nil, core.BadRequest(s + ": " + err.Error())
					}
				}
				a.SetSetting(ctx, "m365.allowed_sites", strings.Join(*in.AllowedSites, ","))
			}
			cfg := loadConfig()
			a.Audit(ctx, "admin.config", 0, "m365", map[string]any{"old": old, "new": cfg})
			cacheMu.Lock()
			cache, cacheBytes = map[string]cacheEntry{}, 0
			cacheMu.Unlock()
			return map[string]any{"config": cfg}, nil
		}})
	return nil
}

func listItems(c *caller, r *http.Request) (any, error) {
	q := r.URL.Query()
	list := strings.TrimSpace(q.Get("list"))
	if list == "" {
		return nil, core.BadRequest("which list? Pass list=<name>")
	}
	top := 200
	if n, err := strconv.Atoi(q.Get("top")); err == nil && n > 0 {
		top = min(n, 5000)
	}
	ctx := r.Context()
	id, err := c.resolveSiteID(ctx, firstNonEmpty(q.Get("siteUrl"), q.Get("site")))
	if err != nil {
		return nil, err
	}
	p := "/sites/" + url.PathEscape(id) + "/lists/" + url.PathEscape(list) + "/items?expand=fields&$top=" + strconv.Itoa(min(top, 999))
	var items []map[string]any
	for p != "" && len(items) < top {
		var page struct {
			Value []struct {
				ID     string         `json:"id"`
				Fields map[string]any `json:"fields"`
				WebURL string         `json:"webUrl"`
			} `json:"value"`
			Next string `json:"@odata.nextLink"`
		}
		if err := c.getJSON(ctx, p, &page); err != nil {
			return nil, err
		}
		for _, v := range page.Value {
			f := map[string]any{}
			for k, x := range v.Fields {
				if !strings.HasPrefix(k, "@odata") {
					f[k] = x
				}
			}
			f["id"] = v.ID
			items = append(items, f)
		}
		p = ""
		if page.Next != "" {
			if i := strings.Index(page.Next, "/v1.0/"); i >= 0 {
				p = page.Next[i+len("/v1.0"):]
			}
		}
	}
	if len(items) > top {
		items = items[:top]
	}
	if items == nil {
		items = []map[string]any{}
	}
	return map[string]any{"items": items, "count": len(items)}, nil
}

// splitRange splits "Sheet1!A1:D20" into sheet and address.
func splitRange(s string) (string, string) {
	sheet, addr, ok := strings.Cut(s, "!")
	if !ok {
		return "", s
	}
	return strings.Trim(sheet, "'"), addr
}

func excelRange(c *caller, r *http.Request) (any, error) {
	q := r.URL.Query()
	ctx := r.Context()
	item, err := c.driveItem(ctx, q.Get("file"))
	if err != nil {
		return nil, err
	}
	sheet, addr := splitRange(strings.TrimSpace(q.Get("range")))
	for _, ch := range addr {
		if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == ':' || ch == '$') {
			return nil, core.BadRequest("range should look like Sheet1!A1:D20")
		}
	}
	wb := item + "/workbook"
	if sheet == "" {
		var ws struct {
			Value []struct{ Name string } `json:"value"`
		}
		if err := c.getJSON(ctx, wb+"/worksheets?$select=name&$top=1", &ws); err != nil {
			return nil, err
		}
		if len(ws.Value) == 0 {
			return nil, core.NotFound("that workbook has no sheets")
		}
		sheet = ws.Value[0].Name
	}
	sq := "'" + strings.ReplaceAll(sheet, "'", "''") + "'"
	var p string
	if addr == "" {
		p = wb + "/worksheets(" + url.PathEscape(sq) + ")/usedRange(valuesOnly=true)?$select=address,values,text"
	} else {
		p = wb + "/worksheets(" + url.PathEscape(sq) + ")/range(address='" + url.PathEscape(addr) + "')?$select=address,values,text"
	}
	var out struct {
		Address string  `json:"address"`
		Values  [][]any `json:"values"`
		Text    [][]any `json:"text"`
	}
	if err := c.getJSON(ctx, p, &out); err != nil {
		return nil, err
	}
	return map[string]any{"address": out.Address, "sheet": sheet, "values": out.Values, "text": out.Text}, nil
}

func listFiles(c *caller, r *http.Request) (any, error) {
	ctx := r.Context()
	folder := strings.TrimSpace(r.URL.Query().Get("folder"))
	var p string
	if folder == "" || folder == "/" {
		if len(c.cfg.AllowedSites) > 0 {
			return nil, core.Forbidden("your admin limited Microsoft 365 to certain SharePoint sites; pass a folder link")
		}
		p = "/me/drive/root/children"
	} else {
		item, err := c.driveItem(ctx, folder)
		if err != nil {
			return nil, err
		}
		p = item + "/children"
	}
	p += "?$top=500&$select=id,name,size,webUrl,lastModifiedDateTime,folder,file,lastModifiedBy"
	var res struct {
		Value []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Size     int64  `json:"size"`
			WebURL   string `json:"webUrl"`
			Modified string `json:"lastModifiedDateTime"`
			Folder   *struct {
				ChildCount int `json:"childCount"`
			} `json:"folder"`
			File *struct {
				MimeType string `json:"mimeType"`
			} `json:"file"`
			By struct {
				User struct {
					DisplayName string `json:"displayName"`
				} `json:"user"`
			} `json:"lastModifiedBy"`
		} `json:"value"`
	}
	if err := c.getJSON(ctx, p, &res); err != nil {
		return nil, err
	}
	files := make([]map[string]any, 0, len(res.Value))
	for _, v := range res.Value {
		f := map[string]any{"id": v.ID, "name": v.Name, "size": v.Size, "webUrl": v.WebURL, "modified": v.Modified, "folder": v.Folder != nil, "modifiedBy": v.By.User.DisplayName}
		if v.File != nil {
			f["mimeType"] = v.File.MimeType
		}
		if v.Folder != nil {
			f["childCount"] = v.Folder.ChildCount
		}
		files = append(files, f)
	}
	return map[string]any{"files": files}, nil
}
