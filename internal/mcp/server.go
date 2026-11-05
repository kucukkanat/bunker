// Package mcp implements Bunker's Model Context Protocol tool layer once
// and serves it two ways: as a local stdio server (`bunker mcp`, which
// calls a remote Bunker over HTTP with the CLI's token) and as the remote
// Streamable HTTP endpoint POST /mcp inside the server (which dispatches
// in-process as the token's user). It also hosts the OAuth 2.1
// authorization server MCP clients use to connect with just a URL, and
// the agent docs (/llms.txt).
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"sync"

	"github.com/kucukkanat/bunker/internal/core"
)

// Protocol versions we speak, newest first. 2026-07-28 is the stateless
// revision (no initialize; version travels in _meta and headers).
const (
	VersionStateless    = "2026-07-28"
	VersionLatestLegacy = "2025-11-25"
)

var SupportedVersions = []string{VersionStateless, "2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeNoMethod       = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
	CodeHeaderMismatch = -32020
	CodeUnsupportedVer = -32022
)

const metaVersionKey = "io.modelcontextprotocol/protocolVersion"

// Request is an API call issued by a tool.
type Request struct {
	Method      string
	Path        string
	Query       url.Values
	Body        []byte
	ContentType string
	// PageSafe marks a call made on behalf of a site page (an embedded MCP
	// App): it is sent with X-Bunker-Page: 1 so only page-safe ops run.
	PageSafe bool
}

// Response is the API's answer.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Backend executes API calls as the connected user. The CLI implements it
// over HTTP; the server implements it in-process.
type Backend interface {
	// Ops lists the non-hidden operations the server offers.
	Ops(ctx context.Context) ([]core.Op, error)
	// Do performs one API request.
	Do(ctx context.Context, req *Request) (*Response, error)
	// BaseURL is the server's public URL (for links in results).
	BaseURL() string
}

// Server answers JSON-RPC messages.
type Server struct {
	Backend Backend
	Version string // Bunker version reported in serverInfo
	// IsAdmin, when known, hides admin-only tools from non-admins.
	IsAdmin *bool

	mu    sync.Mutex
	tools []*tool
}

// Message is a JSON-RPC 2.0 message.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// IsRequest reports whether the message expects a response.
func (m *Message) IsRequest() bool { return m.Method != "" && len(m.ID) > 0 && string(m.ID) != "null" }

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return e.Message }

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// ParseMessages decodes a single message or a batch.
func ParseMessages(raw []byte) (msgs []*Message, batch bool, err *RPCError) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, false, &RPCError{Code: codeInvalidRequest, Message: "empty message"}
	}
	if raw[0] == '[' {
		if e := json.Unmarshal(raw, &msgs); e != nil {
			return nil, true, &RPCError{Code: codeParse, Message: "parse error: " + e.Error()}
		}
		if len(msgs) == 0 {
			return nil, true, &RPCError{Code: codeInvalidRequest, Message: "empty batch"}
		}
		return msgs, true, nil
	}
	var m Message
	if e := json.Unmarshal(raw, &m); e != nil {
		return nil, false, &RPCError{Code: codeParse, Message: "parse error: " + e.Error()}
	}
	return []*Message{&m}, false, nil
}

// HandleRaw processes one line/body (single message or batch) and returns
// the encoded reply, or nil when nothing needs to be sent back.
func (s *Server) HandleRaw(ctx context.Context, raw []byte) []byte {
	msgs, batch, perr := ParseMessages(raw)
	if perr != nil {
		b, _ := json.Marshal(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: perr})
		return b
	}
	var outs []json.RawMessage
	for _, m := range msgs {
		if r := s.Handle(ctx, m); r != nil {
			outs = append(outs, r)
		}
	}
	if len(outs) == 0 {
		return nil
	}
	if !batch {
		return outs[0]
	}
	b, _ := json.Marshal(outs)
	return b
}

// Handle processes one message. Notifications and responses return nil.
func (s *Server) Handle(ctx context.Context, m *Message) json.RawMessage {
	if m.Method == "" {
		return nil // a response to something we never send, or junk
	}
	if !m.IsRequest() {
		return nil // notifications/initialized, notifications/cancelled, ...
	}
	res, err := s.dispatch(ctx, m)
	out := response{JSONRPC: "2.0", ID: m.ID}
	if err != nil {
		var re *RPCError
		if e, ok := err.(*RPCError); ok {
			re = e
		} else {
			re = &RPCError{Code: codeInternal, Message: err.Error()}
		}
		out.Error = re
	} else {
		out.Result = res
	}
	b, e := json.Marshal(out)
	if e != nil {
		b, _ = json.Marshal(response{JSONRPC: "2.0", ID: m.ID, Error: &RPCError{Code: codeInternal, Message: "could not encode result"}})
	}
	return b
}

// MetaVersion returns the protocol version a stateless (2026-07-28+)
// request carries in params._meta, or "".
func MetaVersion(params json.RawMessage) string {
	var p struct {
		Meta map[string]any `json:"_meta"`
	}
	if len(params) == 0 || json.Unmarshal(params, &p) != nil {
		return ""
	}
	v, _ := p.Meta[metaVersionKey].(string)
	return v
}

// Negotiate picks the version to answer an initialize request with.
func Negotiate(requested string) string {
	if requested != "" && requested != VersionStateless && slices.Contains(SupportedVersions, requested) {
		return requested
	}
	return VersionLatestLegacy
}

func (s *Server) capabilities() map[string]any {
	return map[string]any{
		"tools":     map[string]any{"listChanged": false},
		"resources": map[string]any{"listChanged": false, "subscribe": false},
		"prompts":   map[string]any{"listChanged": false},
		"logging":   map[string]any{},
		// MCP Apps (SEP-1865): open_site renders sites inside the host.
		"extensions": map[string]any{UIExtension: map[string]any{"mimeTypes": []string{AppMimeType}}},
	}
}

func (s *Server) serverInfo() map[string]any {
	v := s.Version
	if v == "" {
		v = core.Version
	}
	return map[string]any{"name": "bunker", "title": "Bunker", "version": v, "websiteUrl": s.Backend.BaseURL()}
}

// Instructions are sent to clients on initialize/discover.
const Instructions = `Bunker hosts private internal web apps ("sites") for your organization. To publish an app: write a self-contained index.html (inline CSS/JS, no CDN scripts, no fetch to outside URLs) and call create_site_from_html, or deploy_files for several files. Pages use window.bunker (db, files, ai, storage, me) for data; call get_sdk_docs first. Every deploy makes a version (versions_list, versions_restore to roll back). Share with sharing_add. open_site shows a published site as an interactive app inside hosts that support MCP Apps. You act with the signed-in person's permissions.`

func (s *Server) dispatch(ctx context.Context, m *Message) (any, error) {
	if v := MetaVersion(m.Params); v != "" && !slices.Contains(SupportedVersions, v) {
		return nil, &RPCError{Code: CodeUnsupportedVer, Message: "unsupported protocol version " + v, Data: map[string]any{"supported": SupportedVersions}}
	}
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(m.Params, &p)
		return map[string]any{
			"protocolVersion": Negotiate(p.ProtocolVersion),
			"capabilities":    s.capabilities(),
			"serverInfo":      s.serverInfo(),
			"instructions":    Instructions,
		}, nil
	case "server/discover":
		return map[string]any{
			"supportedVersions": SupportedVersions,
			"capabilities":      s.capabilities(),
			"serverInfo":        s.serverInfo(),
			"instructions":      Instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "logging/setLevel":
		return map[string]any{}, nil
	case "completion/complete":
		return map[string]any{"completion": map[string]any{"values": []string{}, "hasMore": false}}, nil
	case "tools/list":
		return s.listTools(ctx)
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil || p.Name == "" {
			return nil, &RPCError{Code: codeInvalidParams, Message: "tools/call needs a tool name"}
		}
		return s.callTool(ctx, p.Name, p.Arguments)
	case "resources/list":
		return s.listResources(ctx)
	case "resources/templates/list":
		return map[string]any{"resourceTemplates": resourceTemplates()}, nil
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil || p.URI == "" {
			return nil, &RPCError{Code: codeInvalidParams, Message: "resources/read needs a uri"}
		}
		return s.readResource(ctx, p.URI)
	case "prompts/list":
		return map[string]any{"prompts": prompts()}, nil
	case "prompts/get":
		var p struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, &RPCError{Code: codeInvalidParams, Message: "bad params"}
		}
		return getPrompt(s.Backend.BaseURL(), p.Name, p.Arguments)
	}
	return nil, &RPCError{Code: codeNoMethod, Message: "method not found: " + m.Method}
}
