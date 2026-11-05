package mcp

// MCP Apps (SEP-1865, spec 2026-01-26): Bunker sites rendered inside MCP
// hosts such as Claude, VS Code Copilot and Microsoft 365 Copilot.
//
// How it fits together:
//   - The open_site tool declares _meta.ui.resourceUri (ui://bunker/_open)
//     in tools/list and returns, as structuredContent, a short-lived embed
//     link for the caller (GET /api/v1/sites/{site}/embed). Its result also
//     carries _meta.ui.resourceUri = ui://bunker/<site>.
//   - resources/read of ui://bunker/<site> (or ui://bunker/_open) returns a
//     small HTML wrapper (mimeType text/html;profile=mcp-app). The host
//     renders it in its own sandboxed iframe; the wrapper frames the site's
//     embed URL (/s/<slug>/~e/<ticket>/) in a second sandboxed iframe.
//   - Auth: the wrapper never holds a session or token. The embed ticket in
//     the URL is bound to (site, live version, viewer) and expires in about
//     an hour; it only grants read access to the published files. SDK calls
//     from the page (bunker.db, bunker.me, ...) go page → wrapper → host
//     → tools/call site_api → Bunker, which forwards them with
//     X-Bunker-Page: 1, so exactly the page-safe operations are reachable,
//     as the connected person, with the same checks as in the Bunker shell.
//   - CSP: the resource declares _meta.ui.csp with the Bunker origin in
//     connectDomains/resourceDomains and the content origin in frameDomains.

import (
	"context"
	"encoding/json"
	"html/template"
	"net/url"
	"path"
	"strings"

	"github.com/kucukkanat/bunker/internal/core"
)

// AppMimeType is the MCP Apps HTML resource type.
const AppMimeType = "text/html;profile=mcp-app"

// UIExtension is the capability key hosts and servers use for MCP Apps.
const UIExtension = "io.modelcontextprotocol/ui"

const uiPrefix = "ui://bunker/"

// openURI is the generic viewer the open_site tool declares; it reads the
// site to show from the tool result.
const openURI = uiPrefix + "_open"

func appTools() []*tool {
	return []*tool{
		{
			Name: "open_site", Title: "Open a site inside this chat",
			Description: "Show a published Bunker site as an interactive app right here (in hosts that support MCP Apps), with the signed-in person's access. Elsewhere it returns the site's link.",
			InputSchema: obj(map[string]any{"site": str("Site slug")}, "site"),
			Annotations: ann("Open a site inside this chat", true, false, true),
			Meta:        map[string]any{"ui": map[string]any{"resourceUri": openURI, "visibility": []string{"model", "app"}}, "ui/resourceUri": openURI},
			fn:          openSite,
		},
		{
			Name: "site_api", Title: "Site data call (used by embedded sites)",
			Description: "Used by an embedded Bunker site to reach its own data (bunker.db, bunker.files, bunker.me...). Only page-safe operations of that one site are allowed.",
			InputSchema: obj(map[string]any{
				"site":   str("Site slug"),
				"method": map[string]any{"type": "string", "enum": []string{"GET", "POST", "PUT", "PATCH", "DELETE"}},
				"path":   str("Path relative to the site's API, e.g. /db/orders or /me"),
				"body":   map[string]any{"description": "JSON body"},
			}, "site", "path"),
			Annotations: ann("Site data call", false, false, false),
			Meta:        map[string]any{"ui": map[string]any{"visibility": []string{"app"}}},
			fn:          siteAPI,
		},
	}
}

type embedInfo struct {
	URL       string         `json:"url"`
	FilesBase string         `json:"filesBase"`
	Origin    string         `json:"origin"`
	AppURL    string         `json:"appUrl"`
	ExpiresAt int64          `json:"expiresAt"`
	Site      map[string]any `json:"site"`
}

func (s *Server) embed(ctx context.Context, site string) (*embedInfo, *Response, error) {
	resp, err := s.Backend.Do(ctx, &Request{Method: "GET", Path: "/api/v1/sites/" + url.PathEscape(site) + "/embed"})
	if err != nil {
		return nil, nil, err
	}
	if resp.Status >= 400 {
		return nil, resp, nil
	}
	var e embedInfo
	if err := json.Unmarshal(resp.Body, &e); err != nil || e.URL == "" {
		return nil, resp, nil
	}
	return &e, resp, nil
}

func openSite(ctx context.Context, s *Server, a map[string]any) (*toolResult, error) {
	site, _ := a["site"].(string)
	site = strings.TrimSpace(site)
	if site == "" {
		return errResult("site is required"), nil
	}
	e, resp, err := s.embed(ctx, site)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return ResultFrom(resp), nil
	}
	title, _ := e.Site["title"].(string)
	res := textResult("Showing " + title + " (" + e.AppURL + "). If it doesn't appear here, open the link instead.")
	res.StructuredContent = e
	res.Meta = map[string]any{"ui": map[string]any{"resourceUri": uiPrefix + site}}
	return res, nil
}

func siteAPI(ctx context.Context, s *Server, a map[string]any) (*toolResult, error) {
	site, _ := a["site"].(string)
	p, _ := a["path"].(string)
	method, _ := a["method"].(string)
	method = strings.ToUpper(method)
	if method == "" {
		method = "GET"
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return errResult("unsupported method"), nil
	}
	if site == "" || !strings.HasPrefix(p, "/") || len(p) > 8192 {
		return errResult("site and a path starting with / are required"), nil
	}
	rawPath, rawQuery, _ := strings.Cut(p, "?")
	if dec, err := url.PathUnescape(rawPath); err != nil || strings.Contains(dec, "..") || strings.Contains(rawPath, "\\") {
		return errResult("Pages can only call their own site's API."), nil
	}
	clean := path.Clean(rawPath)
	if clean == "/" {
		clean = ""
	}
	q, _ := url.ParseQuery(rawQuery)
	req := &Request{Method: method, Path: "/api/v1/sites/" + url.PathEscape(site) + clean, Query: q, PageSafe: true}
	if b, ok := a["body"]; ok && b != nil && method != "GET" && method != "DELETE" {
		enc, err := Marshal(b)
		if err != nil {
			return errResult("that data can't be sent"), nil
		}
		req.Body, req.ContentType = enc, "application/json"
	}
	resp, err := s.Backend.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"status": resp.Status, "ok": resp.Status < 400}
	var data any
	if len(resp.Body) > 0 {
		if json.Unmarshal(resp.Body, &data) != nil {
			data = truncate(string(resp.Body))
		}
	}
	if resp.Status >= 400 {
		code, msg := APIError(resp)
		out["error"] = map[string]any{"code": code, "message": msg}
		res := errResult(msg)
		res.StructuredContent = out
		return res, nil
	}
	out["data"] = data
	res := textResult("ok")
	res.StructuredContent = out
	return res, nil
}

// appResources lists ui://bunker/<site> for published sites.
func appResource(slug, title string) resource {
	return resource{URI: uiPrefix + slug, Name: "app-" + slug, Title: title, Description: "Open " + title + " as an interactive app", MimeType: AppMimeType}
}

// readApp answers resources/read for ui://bunker/...
func (s *Server) readApp(ctx context.Context, uri string) (any, error) {
	slug := strings.TrimPrefix(uri, uiPrefix)
	if i := strings.IndexAny(slug, "/?#"); i >= 0 {
		slug = slug[:i]
	}
	if slug == "" {
		return nil, &RPCError{Code: -32002, Message: "resource not found: " + uri}
	}
	base := s.Backend.BaseURL()
	var e *embedInfo
	if slug != "_open" {
		var resp *Response
		var err error
		e, resp, err = s.embed(ctx, slug)
		if err != nil {
			return nil, err
		}
		if e == nil {
			_, msg := APIError(resp)
			return nil, &RPCError{Code: -32002, Message: msg}
		}
	}
	origin := originOf(base)
	contentOrigin := origin
	if e != nil {
		if o := originOf(e.Origin); o != "" {
			contentOrigin = o
		}
	} else if app != nil && app.Cfg.ContentURL != "" {
		contentOrigin = originOf(app.Cfg.ContentURL)
	}
	html, err := AppHTML(e)
	if err != nil {
		return nil, err
	}
	domains := uniq(origin, contentOrigin)
	return map[string]any{"contents": []map[string]any{{
		"uri": uri, "mimeType": AppMimeType, "text": html,
		"_meta": map[string]any{"ui": map[string]any{
			"csp": map[string]any{
				"connectDomains":  domains,
				"resourceDomains": domains,
				"frameDomains":    uniq(contentOrigin),
			},
			"permissions":   map[string]any{"clipboardWrite": map[string]any{}},
			"prefersBorder": true,
		}},
	}}}, nil
}

func uniq(v ...string) []string {
	out := []string{}
	for _, s := range v {
		if s == "" {
			continue
		}
		dup := false
		for _, o := range out {
			dup = dup || o == s
		}
		if !dup {
			out = append(out, s)
		}
	}
	return out
}

// AppHTML renders the MCP Apps wrapper. e may be nil (the generic viewer
// waits for the open_site tool result).
func AppHTML(e *embedInfo) (string, error) {
	var initial []byte
	if e != nil {
		initial, _ = json.Marshal(e)
	} else {
		initial = []byte("null")
	}
	var b strings.Builder
	err := appTmpl.Execute(&b, map[string]any{
		"Initial": template.JS(initial),
		"Version": core.Version,
	})
	return b.String(), err
}

// The wrapper: speaks MCP Apps JSON-RPC to the host (window.parent) and
// the Bunker SDK bridge protocol (hello/init + MessageChannel) to the site.
var appTmpl = template.Must(template.New("app").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Bunker</title>
<style>
html,body{margin:0;height:100%;font:14px system-ui,-apple-system,"Segoe UI",sans-serif;background:#F7F4EC;color:#1C211E}
@media (prefers-color-scheme:dark){html,body{background:#121714;color:#E8EBE6}}
#bar{display:flex;align-items:center;gap:8px;height:34px;padding:0 10px;border-bottom:1px solid rgba(90,99,93,.25)}
#bar strong{flex:1;font-weight:600;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
#bar button{font:inherit;border:0;background:none;color:#2F5543;cursor:pointer;text-decoration:underline}
#f{display:block;width:100%;height:calc(100% - 35px);min-height:420px;border:0;background:#fff}
#msg{padding:24px;color:#5A635D}
</style></head><body>
<div id="bar"><strong id="t">Bunker</strong><button id="open" type="button" hidden>Open in Bunker</button></div>
<div id="msg">Loading…</div>
<script>
(function(){
'use strict';
var INITIAL = {{.Initial}};
var host = window.parent, seq = 0, pending = {}, info = null, frame = null, port = null;
function rpc(method, params){
  return new Promise(function(resolve, reject){
    var id = ++seq; pending[id] = {resolve: resolve, reject: reject};
    host.postMessage({jsonrpc: '2.0', id: id, method: method, params: params || {}}, '*');
    setTimeout(function(){ if (pending[id]) { delete pending[id]; reject(new Error('The app host did not answer.')); } }, 120000);
  });
}
function notify(method, params){ host.postMessage({jsonrpc: '2.0', method: method, params: params || {}}, '*'); }
window.addEventListener('message', function(e){
  var d = e.data;
  if (frame && e.source === frame.contentWindow) { onFrame(e); return; }
  if (e.source !== host || !d || d.jsonrpc !== '2.0') return;
  if (d.id !== undefined && pending[d.id] && !d.method) {
    var p = pending[d.id]; delete pending[d.id];
    if (d.error) p.reject(new Error(d.error.message || 'error')); else p.resolve(d.result);
    return;
  }
  if (d.method === 'ui/notifications/tool-result') {
    var sc = d.params && d.params.structuredContent;
    if (sc && sc.url) mount(sc);
    else if (!info) show((d.params && d.params.content && d.params.content[0] && d.params.content[0].text) || 'This site could not be opened.');
  }
  if (d.method === 'ui/resource-teardown' && d.id !== undefined) host.postMessage({jsonrpc: '2.0', id: d.id, result: {}}, '*');
});
function show(text){ var m = document.getElementById('msg'); m.hidden = false; m.textContent = text; }
function size(){ notify('ui/notifications/size-changed', {width: document.documentElement.scrollWidth, height: Math.max(480, Math.min(window.innerHeight || 600, 900))}); }
function mount(e){
  if (info && info.url === e.url) return;
  info = e;
  var s = e.site || {};
  document.title = s.title || 'Bunker';
  document.getElementById('t').textContent = (s.icon ? s.icon + ' ' : '') + (s.title || s.slug || 'Bunker');
  var ob = document.getElementById('open');
  if (e.appUrl) { ob.hidden = false; ob.onclick = function(){ rpc('ui/open-link', {url: e.appUrl}).catch(function(){}); }; }
  document.getElementById('msg').hidden = true;
  if (frame) frame.remove();
  frame = document.createElement('iframe');
  frame.id = 'f';
  frame.title = s.title || 'Bunker site';
  frame.setAttribute('sandbox', 'allow-scripts allow-forms allow-modals allow-downloads');
  frame.setAttribute('allow', 'clipboard-write; fullscreen');
  frame.src = e.url;
  frame.addEventListener('load', function(){ if (!port) connect(null); });
  document.body.appendChild(frame);
  size();
}
function connect(nonce){
  if (port) try { port.close(); } catch (_) {}
  var ch = new MessageChannel(); port = ch.port1;
  port.onmessage = function(ev){ onPort(ev.data); };
  frame.contentWindow.postMessage({bk: 1, type: 'init', nonce: nonce, site: info.site, filesBase: info.filesBase}, '*', [ch.port2]);
}
function onFrame(e){
  var d = e.data;
  if (d && d.bk === 1 && d.type === 'hello') { port = null; connect(typeof d.nonce === 'string' ? d.nonce.slice(0, 64) : null); }
}
function reply(m){ m.bk = 1; try { port.postMessage(m); } catch (_) {} }
function onPort(d){
  if (!d || d.bk !== 1) return;
  if (d.type === 'title' && typeof d.title === 'string') { document.title = d.title.slice(0, 200); return; }
  if (d.type === 'open' && typeof d.url === 'string' && /^https:/.test(d.url)) { rpc('ui/open-link', {url: d.url}).catch(function(){}); return; }
  if (d.type !== 'api') return;
  if (d.stream || (d.body && (d.body.__multipart || d.body instanceof Blob))) {
    reply({id: d.id, ok: false, status: 501, error: {code: 'unavailable', message: 'Open this app in Bunker to use this feature.'}});
    return;
  }
  rpc('tools/call', {name: 'site_api', arguments: {site: info.site.slug, method: d.method || 'GET', path: d.path, body: d.body}}).then(function(r){
    var sc = (r && r.structuredContent) || {};
    if (sc.ok) reply({id: d.id, ok: true, status: sc.status, data: sc.data === undefined ? null : sc.data});
    else reply({id: d.id, ok: false, status: sc.status || 0, error: sc.error || {code: 'error', message: 'Something went wrong'}});
  }, function(err){
    reply({id: d.id, ok: false, status: 0, error: {code: 'unavailable', message: err.message}});
  });
}
window.addEventListener('resize', size);
var started = rpc('ui/initialize', {protocolVersion: '2026-01-26', appInfo: {name: 'bunker', version: '{{.Version}}'}, clientInfo: {name: 'bunker', version: '{{.Version}}'}, appCapabilities: {}, capabilities: {}})
  .then(function(){ notify('ui/notifications/initialized', {}); size(); }, function(){});
Promise.race([started, new Promise(function(r){ setTimeout(r, 3000); })]).then(function(){
  if (INITIAL && !info) mount(INITIAL); else if (!info) show('Waiting for the site…');
});
})();
</script>
</body></html>`))
