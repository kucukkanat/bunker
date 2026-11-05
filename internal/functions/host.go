package functions

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/kucukkanat/bunker/internal/connections"
	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/sites"
)

// host answers a running function's requests.
type host struct {
	site *sites.SiteRecord
	as   *core.User
	st   Settings
	ctx  context.Context
}

type hostMsg struct {
	T      string          `json:"t"`
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body"`
	Name   string          `json:"name"`
}

type fetchMsg struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    *string           `json:"body"`
	BodyB64 string            `json:"bodyB64"`
}

func reply(status int, data any) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"ok": true, "status": status, "data": data})
	return b
}

func fail(status int, code, msg string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"ok": false, "status": status, "error": map[string]any{"code": code, "message": msg}})
	return b
}

func (h *host) handle(ctx context.Context, raw json.RawMessage) json.RawMessage {
	var m hostMsg
	if err := json.Unmarshal(raw, &m); err != nil {
		return fail(400, "invalid", "bad host request")
	}
	switch m.T {
	case "api":
		return h.api(ctx, &m)
	case "secret":
		v, ok := secret(ctx, h.site.ID, m.Name)
		if !ok {
			return reply(200, nil)
		}
		return reply(200, v)
	case "fetch":
		var f fetchMsg
		if err := json.Unmarshal(raw, &f); err != nil {
			return fail(400, "invalid", "fetch: headers must be strings and the body a string")
		}
		return h.fetch(ctx, &f)
	}
	return fail(400, "invalid", "unknown host request "+m.T)
}

var apiMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}

// api dispatches to the site's own page-safe API, in-process, as h.as.
func (h *host) api(ctx context.Context, m *hostMsg) json.RawMessage {
	method := strings.ToUpper(m.Method)
	if method == "" {
		method = "GET"
	}
	if !apiMethods[method] || !strings.HasPrefix(m.Path, "/") || len(m.Path) > 8192 {
		return fail(400, "forbidden", "functions can only call their own app's API")
	}
	base := "/api/v1/sites/" + url.PathEscape(h.site.Slug)
	u, err := url.Parse(base + m.Path)
	if err != nil {
		return fail(400, "invalid", "bad path")
	}
	if clean := path.Clean(u.Path); clean != u.Path && clean+"/" != u.Path || !strings.HasPrefix(u.Path, base+"/") {
		return fail(400, "forbidden", "functions can only call their own app's API")
	}
	if strings.HasPrefix(u.Path, base+"/functions") {
		return fail(400, "forbidden", "functions can't call other functions through the API; import the shared code from api/_lib.js instead")
	}
	var body io.Reader
	ctype := "application/json"
	if method != "GET" && method != "DELETE" && len(m.Body) > 0 && string(m.Body) != "null" {
		b, ct, err := encodeBody(m.Body)
		if err != nil {
			return fail(400, "invalid", err.Error())
		}
		body, ctype = bytes.NewReader(b), ct
	}
	rctx := core.WithUser(ctx, h.as)
	req := httptest.NewRequest(method, u.String(), body).WithContext(rctx)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Bunker", "1")
	req.Header.Set("X-Bunker-Page", "1")
	rec := httptest.NewRecorder()
	app.Mux.ServeHTTP(rec, req)
	res := rec.Result()
	b := rec.Body.Bytes()
	ct := res.Header.Get("Content-Type")
	if res.StatusCode >= 300 {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		json.Unmarshal(b, &e)
		if e.Error.Code == "" {
			e.Error.Code, e.Error.Message = "error", fmt.Sprintf("the request failed (%d)", res.StatusCode)
		}
		return fail(res.StatusCode, e.Error.Code, e.Error.Message)
	}
	switch {
	case res.StatusCode == http.StatusNoContent || len(b) == 0:
		return reply(res.StatusCode, nil)
	case strings.HasPrefix(ct, "application/json"):
		out, _ := json.Marshal(map[string]any{"ok": true, "status": res.StatusCode, "data": json.RawMessage(b)})
		return out
	case strings.HasPrefix(ct, "text/"):
		return reply(res.StatusCode, string(b))
	default:
		return reply(res.StatusCode, map[string]any{"bodyB64": base64.StdEncoding.EncodeToString(b), "type": ct})
	}
}

// encodeBody turns {__multipart:true, file:{name,type,text|bodyB64}, folder:"x"}
// into a multipart form; anything else is sent as JSON.
func encodeBody(raw json.RawMessage) ([]byte, string, error) {
	var probe map[string]json.RawMessage
	if json.Unmarshal(raw, &probe) != nil || string(probe["__multipart"]) != "true" {
		return raw, "application/json", nil
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range probe {
		if k == "__multipart" {
			continue
		}
		var files []struct {
			Name string  `json:"name"`
			Type string  `json:"type"`
			Text *string `json:"text"`
			B64  string  `json:"bodyB64"`
		}
		if json.Unmarshal(v, &files) != nil {
			var one struct {
				Name string  `json:"name"`
				Type string  `json:"type"`
				Text *string `json:"text"`
				B64  string  `json:"bodyB64"`
			}
			if json.Unmarshal(v, &one) == nil && one.Name != "" {
				files = append(files, one)
			}
		}
		if len(files) > 0 && files[0].Name != "" {
			for _, f := range files {
				var data []byte
				if f.Text != nil {
					data = []byte(*f.Text)
				} else {
					d, err := base64.StdEncoding.DecodeString(f.B64)
					if err != nil {
						return nil, "", errors.New("file data must be text or a Uint8Array")
					}
					data = d
				}
				hdr := make(map[string][]string)
				hdr["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name=%q; filename=%q`, k, f.Name)}
				if f.Type != "" {
					hdr["Content-Type"] = []string{f.Type}
				} else {
					hdr["Content-Type"] = []string{"application/octet-stream"}
				}
				w, _ := mw.CreatePart(hdr)
				w.Write(data)
			}
			continue
		}
		var s string
		if json.Unmarshal(v, &s) != nil {
			s = string(v)
		}
		mw.WriteField(k, s)
	}
	mw.Close()
	return buf.Bytes(), mw.FormDataContentType(), nil
}

// ---- secrets ----

func secret(ctx context.Context, siteID int64, name string) (string, bool) {
	var v string
	if err := app.DB.QueryRowContext(ctx, `SELECT value FROM fn_secrets WHERE site_id=? AND name=?`, siteID, name).Scan(&v); err != nil {
		return "", false
	}
	p, err := core.Decrypt(v)
	if err != nil {
		return "", false
	}
	return string(p), true
}

// ---- outbound fetch ----

const maxFetchBody = 5 << 20

var fetchClient = func() *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	// Checked on the exact IP being dialed, after DNS, so rebinding can't
	// reach private addresses.
	d.Control = func(network, address string, _ syscall.RawConn) error {
		h, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(h); ip == nil || connections.BlockedIP(ip) {
			return fmt.Errorf("private address %s is blocked", h)
		}
		return nil
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           d.DialContext,
			TLSClientConfig:       connections.TLSConfig,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			MaxIdleConns:          64,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       60 * time.Second,
			ForceAttemptHTTP2:     true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return checkHost(req.Context(), req.URL)
		},
	}
}()

type policyKey struct{}

func hostAllowed(st Settings, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	switch st.Outbound {
	case "off":
		return false
	case "allowlist":
		for _, a := range st.Allowlist {
			if a == host || strings.HasPrefix(a, "*.") && strings.HasSuffix(host, a[1:]) {
				return true
			}
		}
		return false
	}
	return true
}

func checkHost(ctx context.Context, u *url.URL) error {
	st, _ := ctx.Value(policyKey{}).(Settings)
	if u.Scheme != "https" && u.Scheme != "http" {
		return errors.New("only http and https URLs can be fetched")
	}
	if !hostAllowed(st, u.Hostname()) {
		return &refused{host: u.Hostname(), policy: st.Outbound}
	}
	return nil
}

type refused struct{ host, policy string }

func (r *refused) Error() string {
	if r.policy == "off" {
		return "this Bunker doesn't let server functions reach the internet. Ask an admin for a connection (bunker.connections.fetch)"
	}
	return r.host + " isn't on this Bunker's list of sites server functions may reach. Ask an admin to add it"
}

// Request headers a function may not set.
var dropHeaders = map[string]bool{"host": true, "content-length": true, "connection": true, "transfer-encoding": true, "cookie": true}

func (h *host) fetch(ctx context.Context, m *fetchMsg) json.RawMessage {
	u, err := url.Parse(m.URL)
	if err != nil || u.Host == "" {
		return fail(400, "invalid", "fetch needs an absolute http(s) URL")
	}
	pctx := context.WithValue(ctx, policyKey{}, h.st)
	if err := checkHost(pctx, u); err != nil {
		var rf *refused
		if errors.As(err, &rf) {
			app.Audit(core.WithUser(ctx, h.as), "functions.outbound.refused", h.site.ID, u.Hostname(), map[string]any{"policy": h.st.Outbound})
		}
		return fail(403, "forbidden", err.Error())
	}
	method := strings.ToUpper(m.Method)
	if method == "" {
		method = "GET"
	}
	var body io.Reader
	if m.Body != nil {
		body = strings.NewReader(*m.Body)
	} else if m.BodyB64 != "" {
		b, err := base64.StdEncoding.DecodeString(m.BodyB64)
		if err != nil {
			return fail(400, "invalid", "bad body")
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(pctx, method, u.String(), body)
	if err != nil {
		return fail(400, "invalid", err.Error())
	}
	for k, v := range m.Headers {
		if !dropHeaders[strings.ToLower(k)] {
			req.Header.Set(k, v)
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Bunker-Functions/"+core.Version)
	}
	res, err := fetchClient.Do(req)
	if err != nil {
		msg := err.Error()
		var rf *refused
		if errors.As(err, &rf) {
			msg = rf.Error()
		}
		return fail(502, "fetch_failed", msg)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxFetchBody+1))
	if err != nil {
		return fail(502, "fetch_failed", err.Error())
	}
	if len(b) > maxFetchBody {
		return fail(502, "too_large", "the response is larger than 5 MB")
	}
	hdr := map[string]string{}
	for k := range res.Header {
		hdr[strings.ToLower(k)] = res.Header.Get(k)
	}
	out := map[string]any{"status": res.StatusCode, "statusText": http.StatusText(res.StatusCode), "headers": hdr, "url": res.Request.URL.String()}
	ct := res.Header.Get("Content-Type")
	if isText(ct) {
		out["body"] = string(b)
	} else {
		out["bodyB64"] = base64.StdEncoding.EncodeToString(b)
	}
	return reply(200, out)
}

func isText(ct string) bool {
	ct = strings.ToLower(ct)
	return ct == "" || strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") || strings.Contains(ct, "xml") || strings.Contains(ct, "javascript") || strings.Contains(ct, "x-www-form-urlencoded")
}
