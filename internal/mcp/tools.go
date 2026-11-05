package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kucukkanat/bunker/internal/core"
)

type tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"`

	op    *core.Op
	admin bool
	fn    func(ctx context.Context, s *Server, args map[string]any) (*toolResult, error)
}

type content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Resource any    `json:"resource,omitempty"`
}

type toolResult struct {
	Content           []content `json:"content"`
	StructuredContent any       `json:"structuredContent,omitempty"`
	IsError           bool      `json:"isError,omitempty"`
	Meta              any       `json:"_meta,omitempty"`
}

func textResult(t string) *toolResult {
	return &toolResult{Content: []content{{Type: "text", Text: t}}}
}
func errResult(t string) *toolResult {
	return &toolResult{Content: []content{{Type: "text", Text: t}}, IsError: true}
}

// ToolName maps an op name to an MCP tool name (dots → underscores).
func ToolName(op string) string {
	var b strings.Builder
	for _, r := range op {
		switch {
		case r == '.':
			b.WriteByte('_')
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	n := b.String()
	if len(n) > 64 {
		n = n[:64]
	}
	return n
}

// skipOp drops ops that make no sense as a single tool call.
func skipOp(o core.Op) bool {
	if o.Hidden {
		return true
	}
	// Long-lived subscriptions (SSE event feeds) never finish.
	if o.Stream && (strings.HasSuffix(o.Path, "/events") || strings.Contains(o.Path, "/events/")) {
		return true
	}
	return false
}

func schemaFor(p core.Param, op core.Op) map[string]any {
	s := map[string]any{}
	switch p.Type {
	case "integer", "boolean", "number":
		s["type"] = p.Type
	case "object":
		s["type"] = "object"
		s["additionalProperties"] = true
	case "array":
		s["type"] = "array"
		s["items"] = map[string]any{"type": "string"}
	case "file":
		if op.Body == "multipart" {
			s["type"] = "object"
			s["additionalProperties"] = map[string]any{}
			if p.Desc == "" {
				p.Desc = "Files to upload"
			}
			p.Desc += ". Map of file path to text content, or to {\"base64\":\"...\"} for binary files. Include index.html."
		} else {
			s["type"] = "string"
			if p.Desc == "" {
				p.Desc = "File content (text)"
			}
		}
	default:
		s["type"] = "string"
	}
	if len(p.Enum) > 0 {
		s["enum"] = p.Enum
	}
	if p.Desc != "" {
		s["description"] = p.Desc
	}
	return s
}

// opGuidance adds usage notes to generated tools whose summary alone
// doesn't tell an agent when or how to use them.
var opGuidance = map[string]string{
	"fn.list":        "Server functions are api/<name>.js|ts files in the site with an export default async function (input, ctx). Shows each function's name, whether it runs as the owner (elevated), its schedule and onWrite triggers, any bundling error, and whether functions are turned off for this site and why.",
	"fn.call":        "Runs one server function. `name` is the file path under api/ without the extension (api/orders/refund.ts → orders/refund). `input` is any JSON value passed as the first argument; set draft=true to run the unpublished draft code. Returns {output, durationMs}. Error codes: functions_off, not_found, busy, timeout, function_error (with the stack for editors).",
	"fn.runs":        "Recent runs of the site's server functions (newest first, last 200 kept) with trigger, user, status, duration, error and console output. Use it to debug a function after calling it.",
	"secrets.list":   "Lists the NAMES of secrets server functions can read with bunker.secrets.get(NAME). Values are never returned.",
	"secrets.set":    "Stores a secret (API key, token, webhook URL) for the site's server functions. Names match ^[A-Z][A-Z0-9_]{0,63}$; values up to 16 KB, encrypted at rest and never readable back. Ask the person to provide the value (or to set it themselves with `bunker secrets set <site> NAME`); never invent one and never put secrets in site files.",
	"secrets.delete": "Functions that read this secret get null afterwards.",
}

// OpTool converts an op into a tool definition.
func OpTool(o core.Op) *tool {
	props := map[string]any{}
	req := []string{}
	for _, p := range o.Params {
		props[p.Name] = schemaFor(p, o)
		if p.Required || p.In == "path" {
			req = append(req, p.Name)
		}
	}
	ro := o.Method == "GET"
	ann := map[string]any{
		"title":           o.Summary,
		"readOnlyHint":    ro,
		"destructiveHint": !ro && o.Destructive,
		"idempotentHint":  ro || o.Method == "PUT" || o.Method == "DELETE",
		"openWorldHint":   false,
	}
	desc := o.Summary + " (" + o.Method + " " + o.Path + ")"
	if o.Access == core.AccessAdmin {
		desc += ". Bunker admins only."
	}
	if o.Destructive {
		desc += ". Destructive: confirm with the person first."
	}
	if g := opGuidance[o.Name]; g != "" {
		desc += ". " + g
	}
	op := o
	return &tool{
		Name: ToolName(o.Name), Title: o.Summary, Description: desc,
		InputSchema: map[string]any{"type": "object", "properties": props, "required": req},
		Annotations: ann, op: &op, admin: o.Access == core.AccessAdmin,
	}
}

func (s *Server) loadTools(ctx context.Context) ([]*tool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tools != nil {
		return s.tools, nil
	}
	ops, err := s.Backend.Ops(ctx)
	if err != nil {
		return nil, err
	}
	t := curatedTools()
	for _, o := range ops {
		if skipOp(o) {
			continue
		}
		t = append(t, OpTool(o))
	}
	s.tools = t
	return t, nil
}

func (s *Server) visible(t *tool) bool {
	return !(t.admin && s.IsAdmin != nil && !*s.IsAdmin)
}

func (s *Server) listTools(ctx context.Context) (any, error) {
	tools, err := s.loadTools(ctx)
	if err != nil {
		return nil, &RPCError{Code: codeInternal, Message: "could not load Bunker operations: " + err.Error()}
	}
	out := make([]*tool, 0, len(tools))
	for _, t := range tools {
		if s.visible(t) {
			out = append(out, t)
		}
	}
	return map[string]any{"tools": out}, nil
}

func (s *Server) callTool(ctx context.Context, name string, args map[string]any) (any, error) {
	tools, err := s.loadTools(ctx)
	if err != nil {
		return nil, &RPCError{Code: codeInternal, Message: "could not load Bunker operations: " + err.Error()}
	}
	if args == nil {
		args = map[string]any{}
	}
	for _, t := range tools {
		if t.Name != name {
			continue
		}
		var res *toolResult
		if t.fn != nil {
			res, err = t.fn(ctx, s, args)
		} else {
			var resp *Response
			resp, err = CallOp(ctx, s.Backend, *t.op, args)
			if err == nil {
				res = ResultFrom(resp)
			}
		}
		if err != nil {
			return errResult(err.Error()), nil
		}
		return res, nil
	}
	return nil, &RPCError{Code: codeInvalidParams, Message: "unknown tool: " + name}
}

// Stringify renders an argument for a path or query string.
func Stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case json.Number:
		return x.String()
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = Stringify(e)
		}
		return strings.Join(parts, ",")
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// BuildRequest maps tool/CLI arguments onto an op's HTTP request.
func BuildRequest(o core.Op, args map[string]any) (*Request, error) {
	p := o.Path
	q := url.Values{}
	body := map[string]any{}
	var raw []byte
	rawSet := false
	known := map[string]bool{}
	for _, pa := range o.Params {
		known[pa.Name] = true
		v, ok := args[pa.Name]
		if !ok || v == nil {
			if pa.In == "path" {
				return nil, fmt.Errorf("missing required parameter %q", pa.Name)
			}
			if pa.Required && pa.In == "body" && o.Body != "raw" {
				return nil, fmt.Errorf("missing required parameter %q", pa.Name)
			}
			continue
		}
		switch pa.In {
		case "path":
			str := Stringify(v)
			if strings.Contains(p, "{"+pa.Name+"...}") {
				segs := strings.Split(strings.Trim(str, "/"), "/")
				for i := range segs {
					segs[i] = url.PathEscape(segs[i])
				}
				p = strings.Replace(p, "{"+pa.Name+"...}", strings.Join(segs, "/"), 1)
			} else {
				p = strings.Replace(p, "{"+pa.Name+"}", url.PathEscape(str), 1)
			}
		case "query":
			q.Set(pa.Name, Stringify(v))
		default:
			if o.Body == "raw" {
				switch x := v.(type) {
				case string:
					raw = []byte(x)
				case []byte:
					raw = x
				default:
					raw, _ = json.Marshal(x)
				}
				rawSet = true
				continue
			}
			body[pa.Name] = v
			if o.Body == "multipart" && pa.Type != "file" {
				// Deploy-style endpoints read metadata (title, slug...) from the query too.
				q.Set(pa.Name, Stringify(v))
			}
		}
	}
	// Pass through extra arguments for JSON bodies (ops may accept more
	// fields than they document).
	if o.Body == "json" || o.Body == "multipart" {
		for k, v := range args {
			if !known[k] {
				body[k] = v
			}
		}
	}
	if strings.Contains(p, "{") {
		return nil, fmt.Errorf("missing a path parameter for %s", o.Path)
	}
	req := &Request{Method: o.Method, Path: p, Query: q}
	switch {
	case o.Body == "raw":
		if rawSet {
			req.Body = raw
			req.ContentType = "application/octet-stream"
		}
	case o.Body == "none" || o.Method == "GET":
		// Fold any body args into the query for GET/DELETE.
		for k, v := range body {
			q.Set(k, Stringify(v))
		}
	default:
		b, err := Marshal(body)
		if err != nil {
			return nil, err
		}
		req.Body = b
		req.ContentType = "application/json"
	}
	return req, nil
}

// CallOp runs an op with arguments through a backend.
func CallOp(ctx context.Context, b Backend, o core.Op, args map[string]any) (*Response, error) {
	req, err := BuildRequest(o, args)
	if err != nil {
		return nil, err
	}
	return b.Do(ctx, req)
}

// APIError extracts the plain-English message from an error response.
func APIError(r *Response) (code, msg string) {
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(r.Body, &e) == nil && e.Error.Message != "" {
		return e.Error.Code, e.Error.Message
	}
	t := strings.TrimSpace(string(r.Body))
	if len(t) > 300 {
		t = t[:300]
	}
	if t == "" {
		t = fmt.Sprintf("request failed with status %d", r.Status)
	}
	return "http_" + strconv.Itoa(r.Status), t
}

const maxText = 512 << 10

// ResultFrom converts an API response into a tool result.
func ResultFrom(r *Response) *toolResult {
	if r.Status >= 400 {
		code, msg := APIError(r)
		return errResult(msg + " (" + code + ")")
	}
	if r.Status == 204 || len(r.Body) == 0 {
		return &toolResult{Content: []content{{Type: "text", Text: `{"ok":true}`}}, StructuredContent: map[string]any{"ok": true}}
	}
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch {
	case mt == "text/event-stream":
		sum := SummarizeSSE(bytes.NewReader(r.Body))
		res := textResult(sum.Text())
		if sum.Error != "" {
			res.IsError = true
		}
		res.StructuredContent = sum.Map()
		return res
	case mt == "application/json" || json.Valid(r.Body) && (r.Body[0] == '{' || r.Body[0] == '['):
		res := textResult(truncate(string(r.Body)))
		var obj map[string]any
		if json.Unmarshal(r.Body, &obj) == nil {
			res.StructuredContent = obj
		}
		return res
	case utf8.Valid(r.Body):
		return textResult(truncate(string(r.Body)))
	}
	b64 := base64.StdEncoding.EncodeToString(r.Body)
	if len(b64) > maxText {
		return textResult(fmt.Sprintf("(binary content, %d bytes, too large to return inline)", len(r.Body)))
	}
	return &toolResult{Content: []content{
		{Type: "text", Text: fmt.Sprintf("(binary content, %d bytes, %s)", len(r.Body), mt)},
		{Type: "resource", Resource: map[string]any{"uri": "bunker://blob", "mimeType": firstNonEmpty(mt, "application/octet-stream"), "blob": b64}},
	}}
}

func truncate(s string) string {
	if len(s) > maxText {
		return s[:maxText] + "\n…(truncated)"
	}
	return s
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// ParseSSE reads a text/event-stream and calls fn for each event.
func ParseSSE(r io.Reader, fn func(event, data string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	ev, data := "", []string{}
	flush := func() error {
		if len(data) == 0 && ev == "" {
			return nil
		}
		e := ev
		if e == "" {
			e = "message"
		}
		d := strings.Join(data, "\n")
		ev, data = "", data[:0]
		return fn(e, d)
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		k, v, _ := strings.Cut(line, ":")
		v = strings.TrimPrefix(v, " ")
		switch k {
		case "event":
			ev = v
		case "data":
			data = append(data, v)
		}
	}
	if err := flush(); err != nil {
		return err
	}
	return sc.Err()
}

// SSESummary collects an AI-builder style event stream
// (status | text | file | done | error).
type SSESummary struct {
	Status []string
	Body   strings.Builder
	Files  []string
	Done   map[string]any
	Error  string
}

// Text renders the summary for humans and models.
func (s *SSESummary) Text() string {
	var b strings.Builder
	if t := strings.TrimSpace(s.Body.String()); t != "" {
		b.WriteString(t + "\n")
	}
	if len(s.Files) > 0 {
		b.WriteString("Files changed: " + strings.Join(s.Files, ", ") + "\n")
	}
	if s.Done != nil {
		j, _ := json.Marshal(s.Done)
		b.WriteString("Done: " + string(j) + "\n")
	}
	if s.Error != "" {
		b.WriteString("Error: " + s.Error + "\n")
	}
	if b.Len() == 0 && len(s.Status) > 0 {
		b.WriteString(s.Status[len(s.Status)-1])
	}
	return strings.TrimSpace(b.String())
}

// Map renders the summary as structured content.
func (s *SSESummary) Map() map[string]any {
	m := map[string]any{"text": strings.TrimSpace(s.Body.String()), "files": s.Files}
	if s.Done != nil {
		m["done"] = s.Done
	}
	if s.Error != "" {
		m["error"] = s.Error
	}
	return m
}

// SSEField pulls a human string out of an event payload.
func SSEField(data string, keys ...string) string {
	var m map[string]any
	if json.Unmarshal([]byte(data), &m) != nil {
		var s string
		if json.Unmarshal([]byte(data), &s) == nil {
			return s
		}
		return data
	}
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
		if v, ok := m[k].(map[string]any); ok {
			if s, ok := v["message"].(string); ok {
				return s
			}
		}
	}
	return ""
}

// SummarizeSSE reads a whole builder stream.
func SummarizeSSE(r io.Reader) *SSESummary {
	s := &SSESummary{}
	_ = ParseSSE(r, func(ev, data string) error {
		switch ev {
		case "status":
			s.Status = append(s.Status, SSEField(data, "status", "message", "text"))
		case "text", "message":
			s.Body.WriteString(SSEField(data, "text", "delta", "content"))
		case "file":
			s.Files = append(s.Files, SSEField(data, "path", "name"))
		case "done":
			var m map[string]any
			if json.Unmarshal([]byte(data), &m) != nil {
				m = map[string]any{"message": data}
			}
			s.Done = m
		case "error":
			s.Error = SSEField(data, "message", "error")
		}
		return nil
	})
	return s
}

// Marshal encodes JSON without HTML escaping (smaller, readable bodies).
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
