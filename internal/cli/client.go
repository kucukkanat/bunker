package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/mcp"
)

// Client talks to a Bunker server's HTTP API.
type Client struct {
	Server string
	Token  string
	HTTP   *http.Client
	debug  func(string)

	opsOnce sync.Once
	ops     []core.Op
	opsErr  error
}

// APIError is an error response from the server.
type APIError struct {
	Status  int
	Code    string
	Message string
	Server  string
	Hint    string
}

func (e *APIError) Error() string { return e.Message }

func (e *APIError) hint() string {
	switch {
	case e.Hint != "":
		return e.Hint
	case e.Status == 401:
		return "Your sign-in expired or was revoked. Run `bunker login " + e.Server + "`."
	case e.Status == 403 && strings.Contains(e.Message, "scope"):
		return "This token can't do that. Create one with more scopes: `bunker login` or `bunker tokens create`."
	case e.Status == 404 && strings.Contains(strings.ToLower(e.Message), "site"):
		return "Check the address with `bunker sites`."
	case e.Status == 429:
		return "Wait a moment and try again."
	case e.Status >= 500:
		return "The server had a problem. Try again, or check `bunker doctor`."
	}
	return ""
}

var httpClient = &http.Client{
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     60 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	},
}

func (x *App) newClient(server, token string) *Client {
	c := &Client{Server: strings.TrimRight(server, "/"), Token: token, HTTP: httpClient}
	if x.Debug {
		c.debug = func(s string) { fmt.Fprintln(x.Stderr, x.errSt.dim("» "+s)) }
	}
	return c
}

var userAgent = "bunker-cli/" + core.Version + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"

// Raw performs a request and returns the response (caller closes body).
func (c *Client) Raw(ctx context.Context, method, path string, q url.Values, body io.Reader, ct string) (*http.Response, error) {
	u := c.Server + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("Accept", "application/json, text/event-stream;q=0.9, */*;q=0.5")
	req.Header.Set("User-Agent", userAgent)
	if c.debug != nil {
		c.debug(method + " " + u)
	}
	start := time.Now()
	resp, err := c.HTTP.Do(req)
	if c.debug != nil && err == nil {
		c.debug(fmt.Sprintf("%d %s (%dms)", resp.StatusCode, resp.Header.Get("Content-Type"), time.Since(start).Milliseconds()))
	}
	return resp, err
}

// errorFrom builds an APIError from a failed response (consumes body).
func (c *Client) errorFrom(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	r := &mcp.Response{Status: resp.StatusCode, Body: b}
	code, msg := mcp.APIError(r)
	if strings.HasPrefix(code, "http_") {
		// Not a Bunker JSON error (proxy page, unknown route...).
		msg = fmt.Sprintf("the server answered %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
		if resp.StatusCode == 404 {
			msg = "this server doesn't offer that (404); it may be an older Bunker version"
		}
	}
	return &APIError{Status: resp.StatusCode, Code: code, Message: msg, Server: c.Server}
}

// Call sends JSON (in may be nil) and decodes a JSON reply into out.
func (c *Client) Call(ctx context.Context, method, path string, q url.Values, in, out any) error {
	var body io.Reader
	ct := ""
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body, ct = bytes.NewReader(b), "application/json"
	}
	resp, err := c.Raw(ctx, method, path, q, body, ct)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return c.errorFrom(resp)
	}
	if out == nil || resp.StatusCode == 204 {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("unexpected reply from the server: %w", err)
	}
	return nil
}

func (c *Client) Get(ctx context.Context, path string, q url.Values, out any) error {
	return c.Call(ctx, "GET", path, q, nil, out)
}

// Ops fetches the operation catalogue (cached).
func (c *Client) Ops(ctx context.Context) ([]core.Op, error) {
	c.opsOnce.Do(func() {
		var r struct {
			Ops []core.Op `json:"ops"`
		}
		c.opsErr = c.Get(ctx, "/api/v1/ops", nil, &r)
		c.ops = r.Ops
	})
	return c.ops, c.opsErr
}

// Do implements mcp.Backend.
func (c *Client) Do(ctx context.Context, r *mcp.Request) (*mcp.Response, error) {
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}
	resp, err := c.Raw(ctx, r.Method, r.Path, r.Query, body, r.ContentType)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 401 {
		b = []byte(`{"error":{"code":"unauthorized","message":"Not signed in to ` + c.Server + `. Ask the person to run: bunker login ` + c.Server + `"}}`)
	}
	return &mcp.Response{Status: resp.StatusCode, Header: resp.Header, Body: b}, nil
}

// BaseURL implements mcp.Backend.
func (c *Client) BaseURL() string { return c.Server }

func siteURL(server, slug string) string { return server + "/s/" + slug + "/" }

func pathEsc(s string) string { return url.PathEscape(s) }
