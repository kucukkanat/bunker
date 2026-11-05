package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/kucukkanat/bunker/internal/core"
)

func init() {
	core.Register(core.Module{
		Name:       "mcp",
		Order:      800,
		Migrations: []string{oauthSchema},
		Init:       initRemote,
	})
}

var app *core.App

func initRemote(a *core.App) error {
	app = a
	// /mcp authenticates bearer tokens itself (and answers 401 with OAuth
	// discovery hints), so it skips the cookie CSRF middleware: cookies are
	// never accepted there.
	a.Public("/mcp")
	a.Mux.HandleFunc("POST /mcp", handleMCP)
	a.Mux.HandleFunc("GET /mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "This MCP server does not offer a GET event stream. Send JSON-RPC with POST.", http.StatusMethodNotAllowed)
	})
	a.Mux.HandleFunc("DELETE /mcp", func(w http.ResponseWriter, r *http.Request) {
		// Sessions are stateless; nothing to tear down.
		w.WriteHeader(http.StatusNoContent)
	})
	registerOAuth(a)
	go sweepOAuthLoop()
	registerDocs(a)
	return nil
}

// BaseURL returns the server's public URL, falling back to the request.
func BaseURL(r *http.Request) string {
	if app != nil && app.Cfg.PublicURL() != "" {
		return app.Cfg.PublicURL()
	}
	scheme := "http"
	if r.TLS != nil || (app != nil && app.Cfg.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func originOf(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return ""
	}
	return p.Scheme + "://" + p.Host
}

func isLocalOrigin(o string) bool {
	p, err := url.Parse(o)
	if err != nil {
		return false
	}
	h := p.Hostname()
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

func mcpError(w http.ResponseWriter, status int, id json.RawMessage, code int, msg string, data any) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(response{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg, Data: data}})
}

func challenge(w http.ResponseWriter, r *http.Request, errCode, desc string) {
	base := BaseURL(r)
	v := `Bearer resource_metadata="` + base + `/.well-known/oauth-protected-resource/mcp", scope="bunker"`
	if errCode != "" {
		v += `, error="` + errCode + `", error_description="` + desc + `"`
	}
	w.Header().Set("WWW-Authenticate", v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "unauthorized", "message": "Connect with OAuth or send Authorization: Bearer <token>. " + desc}})
}

func handleMCP(w http.ResponseWriter, r *http.Request) {
	base := BaseURL(r)
	// DNS-rebinding guard: browsers always send Origin; it must be ours.
	if o := r.Header.Get("Origin"); o != "" && o != originOf(base) && !(app.Cfg.Dev && isLocalOrigin(o)) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	u := core.UserFrom(r.Context())
	if u == nil || !u.ViaToken {
		if r.Header.Get("Authorization") != "" {
			challenge(w, r, "invalid_token", "The token is invalid, expired or revoked.")
		} else {
			challenge(w, r, "", "")
		}
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
	if err != nil {
		mcpError(w, http.StatusRequestEntityTooLarge, nil, codeInvalidRequest, "request too large", nil)
		return
	}
	msgs, _, perr := ParseMessages(raw)
	if perr != nil {
		mcpError(w, http.StatusBadRequest, nil, perr.Code, perr.Message, nil)
		return
	}
	hv := r.Header.Get("MCP-Protocol-Version")
	if hv != "" && !slices.Contains(SupportedVersions, hv) {
		mcpError(w, http.StatusBadRequest, msgs[0].ID, CodeUnsupportedVer, "unsupported MCP-Protocol-Version "+hv, map[string]any{"supported": SupportedVersions})
		return
	}
	// Stateless (2026-07-28+) requests: headers must mirror the body.
	if len(msgs) == 1 && msgs[0].Method != "" {
		m := msgs[0]
		if mv := MetaVersion(m.Params); mv != "" {
			if !slices.Contains(SupportedVersions, mv) {
				mcpError(w, http.StatusBadRequest, m.ID, CodeUnsupportedVer, "unsupported protocol version "+mv, map[string]any{"supported": SupportedVersions})
				return
			}
			if msg := headerMismatch(r, m, mv); msg != "" {
				mcpError(w, http.StatusBadRequest, m.ID, CodeHeaderMismatch, msg, nil)
				return
			}
		}
	}
	admin := u.IsAdmin
	srv := &Server{Backend: &inproc{a: app, u: u, ip: core.IPFrom(r.Context()), base: base}, IsAdmin: &admin}
	var outs []json.RawMessage
	initialize := false
	for _, m := range msgs {
		if m.Method == "initialize" {
			initialize = true
		}
		if out := srv.Handle(r.Context(), m); out != nil {
			outs = append(outs, out)
		}
	}
	if initialize {
		w.Header().Set("Mcp-Session-Id", core.RandomToken(18))
	}
	if len(outs) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if len(msgs) == 1 && raw[0] != '[' {
		w.Write(outs[0])
		return
	}
	b, _ := json.Marshal(outs)
	w.Write(b)
}

func headerMismatch(r *http.Request, m *Message, version string) string {
	if h := r.Header.Get("MCP-Protocol-Version"); h != version {
		return "MCP-Protocol-Version header must equal _meta protocolVersion " + version
	}
	if h := r.Header.Get("Mcp-Method"); h != m.Method {
		return "Mcp-Method header must equal the request method " + m.Method
	}
	var p struct {
		Name string `json:"name"`
		URI  string `json:"uri"`
	}
	json.Unmarshal(m.Params, &p)
	want := ""
	switch m.Method {
	case "tools/call", "prompts/get":
		want = p.Name
	case "resources/read":
		want = p.URI
	default:
		return ""
	}
	if h := r.Header.Get("Mcp-Name"); h != want {
		return "Mcp-Name header must equal " + want
	}
	return ""
}

// inproc runs API calls inside this process as user u, through the same
// mux and op wrappers (so access, scopes and page-safety checks apply).
type inproc struct {
	a    *core.App
	u    *core.User
	ip   string
	base string
}

func (b *inproc) BaseURL() string { return b.base }

func (b *inproc) Ops(ctx context.Context) ([]core.Op, error) {
	var out []core.Op
	for _, o := range b.a.Ops() {
		if !o.Hidden {
			out = append(out, o)
		}
	}
	return out, nil
}

func (b *inproc) Do(ctx context.Context, req *Request) (*Response, error) {
	target := req.Path
	if len(req.Query) > 0 {
		target += "?" + req.Query.Encode()
	}
	ctx = core.WithUser(core.WithIP(ctx, b.ip), b.u)
	hr, err := http.NewRequestWithContext(ctx, req.Method, target, bytes.NewReader(req.Body))
	if err != nil {
		return nil, err
	}
	if req.ContentType != "" {
		hr.Header.Set("Content-Type", req.ContentType)
	}
	hr.Header.Set("Accept", "application/json, text/event-stream")
	hr.Header.Set("User-Agent", "bunker-mcp")
	if req.PageSafe {
		hr.Header.Set("X-Bunker-Page", "1")
	}
	hr.RemoteAddr = b.ip + ":0"
	if p, err := url.Parse(b.base); err == nil {
		hr.Host = p.Host
	}
	rec := &recorder{h: http.Header{}, status: 200}
	b.a.Mux.ServeHTTP(rec, hr)
	return &Response{Status: rec.status, Header: rec.h, Body: rec.buf.Bytes()}, nil
}

// recorder is a minimal in-memory ResponseWriter (bounded).
type recorder struct {
	h      http.Header
	status int
	wrote  bool
	buf    bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.h }
func (r *recorder) WriteHeader(s int) {
	if !r.wrote {
		r.status, r.wrote = s, true
	}
}
func (r *recorder) Write(p []byte) (int, error) {
	r.wrote = true
	if r.buf.Len()+len(p) > 64<<20 {
		return 0, io.ErrShortWrite
	}
	return r.buf.Write(p)
}
func (r *recorder) Flush() {}

// SplitScopes parses an OAuth scope string.
func SplitScopes(s string) []string { return strings.Fields(s) }
