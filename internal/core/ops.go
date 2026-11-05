package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"sort"
	"strings"
)

// Access levels for operations.
const (
	AccessPublic = "public" // no sign-in needed
	AccessUser   = "user"   // any signed-in user (site-level checks happen inside)
	AccessAdmin  = "admin"  // Bunker admins only
)

// Token scopes.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"
	ScopeAdmin = "admin"
)

// Param describes one input of an operation. The same metadata drives
// the OpenAPI document, CLI flags and MCP tool schemas.
type Param struct {
	Name     string `json:"name"`
	In       string `json:"in"`   // path | query | body
	Type     string `json:"type"` // string | integer | boolean | object | array | file
	Required bool   `json:"required,omitempty"`
	Desc     string `json:"description,omitempty"`
	Enum     []string `json:"enum,omitempty"`
}

// Op is a single API operation. Every action available in the web UI
// must be an Op so the CLI, MCP server and agents get it for free.
type Op struct {
	Name    string  `json:"name"`    // dotted, e.g. "sites.create"
	Method  string  `json:"method"`  // GET POST PATCH PUT DELETE
	Path    string  `json:"path"`    // Go 1.22 mux pattern path, e.g. /api/v1/sites/{site}
	Summary string  `json:"summary"`
	Params  []Param `json:"params,omitempty"`
	Access  string  `json:"access"`
	Scope   string  `json:"scope"`
	// Body is "json" (default), "multipart", "raw" or "none".
	Body string `json:"body,omitempty"`
	// Stream marks SSE/streaming responses.
	Stream bool `json:"stream,omitempty"`
	// Hidden ops are not offered to CLI/MCP (e.g. OAuth callbacks).
	Hidden bool `json:"hidden,omitempty"`
	// Destructive ops ask for confirmation in CLI/MCP clients.
	Destructive bool `json:"destructive,omitempty"`

	// Fn handles the request and returns a JSON-able value.
	Fn func(r *http.Request) (any, error) `json:"-"`
	// Handler is used instead of Fn when the op writes its own response.
	Handler http.HandlerFunc `json:"-"`
}

// Handle registers an operation on the mux.
func (a *App) Handle(op Op) {
	if op.Access == "" {
		op.Access = AccessUser
	}
	if op.Scope == "" {
		if op.Method == http.MethodGet {
			op.Scope = ScopeRead
		} else {
			op.Scope = ScopeWrite
		}
		if op.Access == AccessAdmin {
			op.Scope = ScopeAdmin
		}
	}
	if op.Body == "" {
		if op.Method == http.MethodGet || op.Method == http.MethodDelete {
			op.Body = "none"
		} else {
			op.Body = "json"
		}
	}
	if op.Access == AccessPublic {
		a.Public(op.Path)
	}
	a.opsMu.Lock()
	a.ops = append(a.ops, op)
	a.opsMu.Unlock()
	o := op
	a.Mux.HandleFunc(op.Method+" "+op.Path, func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				a.Log.Error("panic", "op", o.Name, "err", rec, "stack", string(debug.Stack()))
				Err(w, Internal("internal error"))
			}
		}()
		if o.Access != AccessPublic {
			u := UserFrom(r.Context())
			if u == nil {
				Err(w, Unauthorized("sign in required"))
				return
			}
			if o.Access == AccessAdmin && !u.IsAdmin {
				Err(w, Forbidden("admins only"))
				return
			}
			if !u.HasScope(o.Scope) {
				Err(w, Forbidden("token lacks scope "+o.Scope))
				return
			}
		}
		if o.Handler != nil {
			o.Handler(w, r)
			return
		}
		v, err := o.Fn(r)
		if err != nil {
			Err(w, err)
			return
		}
		if v == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		status := http.StatusOK
		if r.Method == http.MethodPost {
			if s, ok := v.(interface{ StatusCode() int }); ok {
				status = s.StatusCode()
			}
		}
		JSON(w, status, v)
	})
}

// Ops returns all registered operations sorted by name.
func (a *App) Ops() []Op {
	a.opsMu.RLock()
	defer a.opsMu.RUnlock()
	out := append([]Op(nil), a.ops...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// HTTPError is an error with an HTTP status and machine-readable code.
type HTTPError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *HTTPError) Error() string { return e.Code + ": " + e.Message }

func NewErr(status int, code, msg string) *HTTPError {
	return &HTTPError{Status: status, Code: code, Message: msg}
}
func BadRequest(msg string) *HTTPError   { return NewErr(400, "invalid", msg) }
func Unauthorized(msg string) *HTTPError { return NewErr(401, "unauthorized", msg) }
func Forbidden(msg string) *HTTPError    { return NewErr(403, "forbidden", msg) }
func NotFound(msg string) *HTTPError     { return NewErr(404, "not_found", msg) }
func Conflict(msg string) *HTTPError     { return NewErr(409, "conflict", msg) }
func TooLarge(msg string) *HTTPError     { return NewErr(413, "too_large", msg) }
func Quota(msg string) *HTTPError        { return NewErr(429, "quota_exceeded", msg) }
func Internal(msg string) *HTTPError     { return NewErr(500, "internal", msg) }
func Unavailable(msg string) *HTTPError  { return NewErr(503, "unavailable", msg) }

// JSON writes v as JSON.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// Err writes an error response.
func Err(w http.ResponseWriter, err error) {
	var he *HTTPError
	if !errors.As(err, &he) {
		he = Internal("internal error")
	}
	JSON(w, he.Status, map[string]any{"error": he})
}

// Decode reads a JSON body (max 8 MB) into v.
func Decode(r *http.Request, v any) error {
	if r.Body == nil {
		return BadRequest("missing body")
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<20))
	if err := dec.Decode(v); err != nil {
		return BadRequest("invalid JSON: " + err.Error())
	}
	return nil
}

// Created wraps a value so Handle responds 201.
type Created struct{ V any }

func (c Created) StatusCode() int        { return http.StatusCreated }
func (c Created) MarshalJSON() ([]byte, error) { return json.Marshal(c.V) }

// OpenAPI renders a minimal OpenAPI 3.1 document for all non-hidden ops.
func (a *App) OpenAPI() map[string]any {
	paths := map[string]map[string]any{}
	for _, op := range a.Ops() {
		if op.Hidden {
			continue
		}
		p := paths[op.Path]
		if p == nil {
			p = map[string]any{}
			paths[op.Path] = p
		}
		var params []map[string]any
		body := map[string]any{}
		var req []string
		for _, pa := range op.Params {
			schema := map[string]any{"type": oaType(pa.Type)}
			if len(pa.Enum) > 0 {
				schema["enum"] = pa.Enum
			}
			if pa.In == "body" {
				schema["description"] = pa.Desc
				body[pa.Name] = schema
				if pa.Required {
					req = append(req, pa.Name)
				}
				continue
			}
			params = append(params, map[string]any{"name": pa.Name, "in": pa.In, "required": pa.Required || pa.In == "path", "description": pa.Desc, "schema": schema})
		}
		m := map[string]any{"operationId": op.Name, "summary": op.Summary, "x-bunker-access": op.Access, "x-bunker-scope": op.Scope}
		if len(params) > 0 {
			m["parameters"] = params
		}
		if len(body) > 0 {
			ct := "application/json"
			if op.Body == "multipart" {
				ct = "multipart/form-data"
			}
			m["requestBody"] = map[string]any{"content": map[string]any{ct: map[string]any{"schema": map[string]any{"type": "object", "properties": body, "required": req}}}}
		}
		p[strings.ToLower(op.Method)] = m
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": "Bunker API", "version": Version},
		"servers": []map[string]any{{"url": a.Cfg.URL}},
		"components": map[string]any{"securitySchemes": map[string]any{"token": map[string]any{"type": "http", "scheme": "bearer"}}},
		"security":   []map[string]any{{"token": []string{}}},
		"paths":      paths,
	}
}

func oaType(t string) string {
	switch t {
	case "file":
		return "string"
	case "":
		return "string"
	}
	return t
}

// Errorf is a convenience for 400s.
func Errorf(format string, args ...any) *HTTPError { return BadRequest(fmt.Sprintf(format, args...)) }
