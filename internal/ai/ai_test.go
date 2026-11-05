package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/kucukkanat/bunker/internal/auth"
	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/server"
	_ "github.com/kucukkanat/bunker/internal/sites"
)

var (
	testApp *core.App
	handler http.Handler
)

func TestMain(m *testing.M) {
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "BUNKER_AI_") {
			os.Unsetenv(strings.SplitN(e, "=", 2)[0])
		}
	}
	dir, err := os.MkdirTemp("", "bunker-ai-test")
	if err != nil {
		panic(err)
	}
	cfg := &core.Config{Dev: true, DataDir: dir, URL: "http://bunker.test", TLS: "off"}
	a, err := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		panic(err)
	}
	testApp, handler = a, server.Handler(a)
	retryBase, retryMaxWait = 5*time.Millisecond, 20*time.Millisecond
	code := m.Run()
	a.DB.Close()
	os.RemoveAll(dir)
	os.Exit(code)
}

// call performs an API request as the dev admin.
func call(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("X-Bunker", "1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("bad json (%d): %s", rec.Code, rec.Body.String())
	}
	return v
}

// sseEvents parses a recorded SSE body into data payloads.
func sseEvents(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	readSSE(bytes.NewReader(body), func(ev sseEvent) bool {
		var v map[string]any
		if json.Unmarshal(ev.Data, &v) == nil {
			out = append(out, v)
		}
		return true
	})
	return out
}

func newSite(t *testing.T, title string) string {
	t.Helper()
	rec := call(t, "POST", "/api/v1/sites", map[string]any{"title": title})
	if rec.Code != 201 && rec.Code != 200 {
		t.Fatalf("create site: %d %s", rec.Code, rec.Body.String())
	}
	return decode(t, rec)["slug"].(string)
}

func configure(t *testing.T, body map[string]any) {
	t.Helper()
	rec := call(t, "PUT", "/api/v1/admin/ai", body)
	if rec.Code != 200 {
		t.Fatalf("configure: %d %s", rec.Code, rec.Body.String())
	}
}

func resetUsage() {
	testApp.DB.Exec(`DELETE FROM ai_usage`)
	invalidateConfig()
}

// ---- fake providers ----

// fakeServer replays scripted handlers in order and records requests.
type fakeServer struct {
	*httptest.Server
	mu       sync.Mutex
	script   []http.HandlerFunc
	requests []map[string]any
	headers  []http.Header
	paths    []string
}

func newFake(t *testing.T) *fakeServer {
	f := &fakeServer{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var v map[string]any
		json.Unmarshal(b, &v)
		f.mu.Lock()
		f.requests = append(f.requests, v)
		f.headers = append(f.headers, r.Header.Clone())
		f.paths = append(f.paths, r.URL.RequestURI())
		var h http.HandlerFunc
		if len(f.script) > 0 {
			h, f.script = f.script[0], f.script[1:]
		}
		f.mu.Unlock()
		if h == nil {
			http.Error(w, `{"error":{"type":"test","message":"no more scripted responses"}}`, 500)
			return
		}
		h(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeServer) push(h ...http.HandlerFunc) {
	f.mu.Lock()
	f.script = append(f.script, h...)
	f.mu.Unlock()
}

func (f *fakeServer) req(i int) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i]
}

func (f *fakeServer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func sse(events ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			io.WriteString(w, e)
			w.(http.Flusher).Flush()
		}
	}
}

func jsonResp(status int, v any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
}

func aEv(typ string, v map[string]any) string {
	v["type"] = typ
	b, _ := json.Marshal(v)
	return "event: " + typ + "\ndata: " + string(b) + "\n\n"
}

type aTool struct {
	id, name string
	input    map[string]any
}

// anthropicTurn emits a full streamed Messages response: a thinking block
// with a signature, narration text (split in two deltas), and tool calls
// whose JSON input is split across deltas.
func anthropicTurn(text string, tools ...aTool) http.HandlerFunc {
	evs := []string{aEv("message_start", map[string]any{"message": map[string]any{"id": "msg_1", "model": "claude-sonnet-5-5", "role": "assistant", "content": []any{}, "usage": map[string]any{"input_tokens": 1000, "output_tokens": 1, "cache_read_input_tokens": 500}}})}
	evs = append(evs, ": keepalive comment\n\n")
	evs = append(evs, aEv("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}}),
		aEv("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "sig-abc"}}),
		aEv("content_block_stop", map[string]any{"index": 0}))
	idx := 1
	if text != "" {
		half := len(text) / 2
		evs = append(evs, aEv("content_block_start", map[string]any{"index": idx, "content_block": map[string]any{"type": "text", "text": ""}}),
			aEv("content_block_delta", map[string]any{"index": idx, "delta": map[string]any{"type": "text_delta", "text": text[:half]}}),
			aEv("ping", map[string]any{}),
			aEv("content_block_delta", map[string]any{"index": idx, "delta": map[string]any{"type": "text_delta", "text": text[half:]}}),
			aEv("content_block_stop", map[string]any{"index": idx}))
		idx++
	}
	for _, tl := range tools {
		in, _ := json.Marshal(tl.input)
		cut := len(in) / 3
		evs = append(evs, aEv("content_block_start", map[string]any{"index": idx, "content_block": map[string]any{"type": "tool_use", "id": tl.id, "name": tl.name, "input": map[string]any{}}}),
			aEv("content_block_delta", map[string]any{"index": idx, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(in[:cut])}}),
			aEv("content_block_delta", map[string]any{"index": idx, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(in[cut:])}}),
			aEv("content_block_stop", map[string]any{"index": idx}))
		idx++
	}
	stop := "end_turn"
	if len(tools) > 0 {
		stop = "tool_use"
	}
	evs = append(evs, aEv("message_delta", map[string]any{"delta": map[string]any{"stop_reason": stop}, "usage": map[string]any{"output_tokens": 200}}),
		aEv("message_stop", map[string]any{}))
	return sse(evs...)
}

func oaChunk(v map[string]any) string {
	b, _ := json.Marshal(v)
	return "data: " + string(b) + "\n\n"
}

// ---- provider unit tests ----

func TestSSEParser(t *testing.T) {
	in := ": comment\r\nevent: a\r\ndata: line1\r\ndata: line2\r\n\r\ndata: {\"x\":1}\n\ndata: tail-without-blank-line"
	var got []sseEvent
	if err := readSSE(strings.NewReader(in), func(ev sseEvent) bool { got = append(got, ev); return true }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Event != "a" || string(got[0].Data) != "line1\nline2" || string(got[1].Data) != `{"x":1}` || string(got[2].Data) != "tail-without-blank-line" {
		t.Fatalf("unexpected events: %+v", got)
	}
	long := strings.Repeat("x", 200<<10)
	got = nil
	readSSE(strings.NewReader("data: "+long+"\n\n"), func(ev sseEvent) bool { got = append(got, ev); return true })
	if len(got) != 1 || len(got[0].Data) != len(long) {
		t.Fatalf("long line not parsed: %d", len(got))
	}
}

func TestAnthropicNonStream(t *testing.T) {
	f := newFake(t)
	f.push(jsonResp(200, map[string]any{
		"model": "claude-haiku-5-5", "stop_reason": "tool_use",
		"content": []any{
			map[string]any{"type": "text", "text": "Let me check."},
			map[string]any{"type": "tool_use", "id": "tu_1", "name": "list_files", "input": map[string]any{}},
		},
		"usage": map[string]any{"input_tokens": 12, "output_tokens": 7},
	}))
	p := &anthropicProvider{endpoint: f.URL, key: "sk-ant-test"}
	resp, err := p.Complete(context.Background(), &Request{Model: "claude-haiku-5-5", System: "sys", MaxTokens: 100, Effort: "low",
		Tools:    []Tool{{Name: "list_files", Description: "d", Schema: obj(map[string]any{})}},
		Messages: []Message{{Role: "user", Content: "hi"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "Let me check." || resp.StopReason != StopToolUse || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "list_files" || resp.Usage.InputTokens != 12 {
		t.Fatalf("bad normalization: %+v", resp)
	}
	h := f.headers[0]
	if h.Get("x-api-key") != "sk-ant-test" || h.Get("anthropic-version") != "2023-06-01" || f.paths[0] != "/v1/messages" {
		t.Fatalf("bad request headers/path: %v %s", h, f.paths[0])
	}
	body := f.req(0)
	tools := body["tools"].([]any)
	if tools[0].(map[string]any)["input_schema"] == nil || body["system"] == nil || body["stream"] != nil {
		t.Fatalf("bad body: %v", body)
	}
	if body["output_config"].(map[string]any)["effort"] != "low" {
		t.Fatalf("effort missing: %v", body["output_config"])
	}
}

func TestAnthropicStreamAndReplay(t *testing.T) {
	f := newFake(t)
	f.push(anthropicTurn("Hello there", aTool{"tu_9", "read_file", map[string]any{"path": "index.html"}}))
	f.push(jsonResp(200, map[string]any{"content": []any{map[string]any{"type": "text", "text": "done"}}, "stop_reason": "end_turn", "usage": map[string]any{}}))
	p := &anthropicProvider{endpoint: f.URL, key: "k"}
	var streamed strings.Builder
	req := &Request{Model: "claude-sonnet-5-5", MaxTokens: 100, Messages: []Message{{Role: "user", Content: "hi"}}, Cache: true}
	resp, err := p.Complete(context.Background(), req, func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if streamed.String() != "Hello there" || resp.Text != "Hello there" {
		t.Fatalf("streamed %q text %q", streamed.String(), resp.Text)
	}
	if len(resp.ToolCalls) != 1 || string(resp.ToolCalls[0].Input) != `{"path":"index.html"}` || resp.StopReason != StopToolUse {
		t.Fatalf("tool call not reassembled: %+v", resp.ToolCalls)
	}
	if resp.Usage.InputTokens != 1000 || resp.Usage.OutputTokens != 200 || resp.Usage.CacheReadTokens != 500 {
		t.Fatalf("usage: %+v", resp.Usage)
	}
	if f.req(0)["stream"] != true {
		t.Fatal("stream flag not sent")
	}
	// Replay: the assistant turn (with its thinking signature) goes back unchanged.
	req.Messages = append(req.Messages, resp.Message, Message{Role: "user", ToolResults: []ToolResult{{ID: "tu_9", Content: "<html>"}}})
	if _, err := p.Complete(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	msgs := f.req(1)["messages"].([]any)
	asst := msgs[1].(map[string]any)["content"].([]any)
	first := asst[0].(map[string]any)
	if first["type"] != "thinking" || first["signature"] != "sig-abc" {
		t.Fatalf("thinking block not replayed: %v", asst)
	}
	tr := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if tr["type"] != "tool_result" || tr["tool_use_id"] != "tu_9" || tr["cache_control"] == nil {
		t.Fatalf("tool result/cache marker wrong: %v", tr)
	}
}

func TestAnthropicRetriesOn529(t *testing.T) {
	f := newFake(t)
	f.push(jsonResp(529, map[string]any{"type": "error", "error": map[string]any{"type": "overloaded_error", "message": "Overloaded"}}),
		jsonResp(200, map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}, "stop_reason": "end_turn"}))
	p := &anthropicProvider{endpoint: f.URL, key: "k"}
	resp, err := p.Complete(context.Background(), &Request{Model: "m", MaxTokens: 5, Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	if err != nil || resp.Text != "ok" || f.count() != 2 {
		t.Fatalf("retry failed: %v %v %d", resp, err, f.count())
	}
	// 400s are not retried and become friendly errors.
	f.push(jsonResp(400, map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": "bad thing"}}))
	_, err = p.Complete(context.Background(), &Request{Model: "m", MaxTokens: 5, Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	he, ok := friendly(err).(*core.HTTPError)
	if !ok || he.Status != 400 || !strings.Contains(he.Message, "bad thing") || f.count() != 3 {
		t.Fatalf("400 handling: %v", err)
	}
}

func TestOpenAIStreamToolCalls(t *testing.T) {
	f := newFake(t)
	idx0 := 0
	f.push(sse(
		oaChunk(map[string]any{"model": "gpt-5", "choices": []any{map[string]any{"delta": map[string]any{"role": "assistant", "content": "Working"}}}}),
		oaChunk(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": " on it"}}}}),
		oaChunk(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": idx0, "id": "call_a", "type": "function", "function": map[string]any{"name": "write_file", "arguments": `{"path":"ind`}}}}}}}),
		oaChunk(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": idx0, "function": map[string]any{"arguments": `ex.html","content":"<p>hi</p>"}`}}}}}}}),
		oaChunk(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "tool_calls"}}}),
		oaChunk(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 50, "completion_tokens": 20, "prompt_tokens_details": map[string]any{"cached_tokens": 10}}}),
		"data: [DONE]\n\n",
	))
	p := &openaiProvider{flavor: ProviderOpenAI, endpoint: f.URL + "/v1", key: "sk-x"}
	var got strings.Builder
	resp, err := p.Complete(context.Background(), &Request{Model: "gpt-5", System: "sys", MaxTokens: 50, Tools: builderTools,
		Messages: []Message{
			{Role: "user", Content: "build"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "c0", Name: "list_files", Input: json.RawMessage(`{}`)}}},
			{Role: "user", ToolResults: []ToolResult{{ID: "c0", Content: "index.html (5 bytes)"}}},
		}}, func(s string) { got.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "Working on it" || resp.StopReason != StopToolUse || len(resp.ToolCalls) != 1 {
		t.Fatalf("bad stream: %q %+v", got.String(), resp)
	}
	var in map[string]string
	json.Unmarshal(resp.ToolCalls[0].Input, &in)
	if in["path"] != "index.html" || in["content"] != "<p>hi</p>" || resp.ToolCalls[0].ID != "call_a" {
		t.Fatalf("tool args: %s", resp.ToolCalls[0].Input)
	}
	if resp.Usage.InputTokens != 40 || resp.Usage.CacheReadTokens != 10 || resp.Usage.OutputTokens != 20 {
		t.Fatalf("usage: %+v", resp.Usage)
	}
	body := f.req(0)
	if f.headers[0].Get("Authorization") != "Bearer sk-x" || f.paths[0] != "/v1/chat/completions" {
		t.Fatalf("auth/path: %v %s", f.headers[0], f.paths[0])
	}
	msgs := body["messages"].([]any)
	if msgs[0].(map[string]any)["role"] != "system" || msgs[2].(map[string]any)["tool_calls"] == nil || msgs[3].(map[string]any)["role"] != "tool" || msgs[3].(map[string]any)["tool_call_id"] != "c0" {
		t.Fatalf("messages: %v", msgs)
	}
	if body["max_completion_tokens"] == nil || body["tools"].([]any)[0].(map[string]any)["type"] != "function" {
		t.Fatalf("body: %v", body)
	}
}

func TestOpenAIFlavorsURLAndJSON(t *testing.T) {
	az := &openaiProvider{flavor: ProviderAzure, endpoint: "https://res.openai.azure.com/", key: "k", apiVersion: "2024-10-21"}
	if u := az.url("my-deploy"); u != "https://res.openai.azure.com/openai/deployments/my-deploy/chat/completions?api-version=2024-10-21" {
		t.Fatalf("azure url: %s", u)
	}
	if az.headers()["api-key"] != "k" {
		t.Fatal("azure key header")
	}
	if _, ok := az.body(&Request{Model: "my-deploy"}, false)["model"]; ok {
		t.Fatal("azure deployment API must not send model")
	}
	az1 := &openaiProvider{flavor: ProviderAzure, endpoint: "https://res.openai.azure.com", apiVersion: "v1"}
	if u := az1.url("x"); u != "https://res.openai.azure.com/openai/v1/chat/completions" {
		t.Fatalf("azure v1 url: %s", u)
	}
	ol := &openaiProvider{flavor: ProviderCompatible, endpoint: "http://localhost:11434"}
	if u := ol.url("llama"); u != "http://localhost:11434/v1/chat/completions" || ol.headers() != nil {
		t.Fatalf("compatible url: %s", u)
	}
	b := ol.body(&Request{Model: "llama", MaxTokens: 9, JSON: true, Schema: map[string]any{"type": "object"}, Messages: []Message{{Role: "user", Content: "x"}}}, false)
	if b["max_tokens"] != 9 || b["response_format"].(map[string]any)["type"] != "json_schema" {
		t.Fatalf("compatible body: %v", b)
	}
	// Non-stream normalization incl. text-only and JSON extraction.
	f := newFake(t)
	f.push(jsonResp(200, map[string]any{"model": "llama", "choices": []any{map[string]any{"message": map[string]any{"content": "```json\n{\"a\":1}\n```"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 4}}))
	p := &openaiProvider{flavor: ProviderCompatible, endpoint: f.URL + "/v1"}
	resp, err := p.Complete(context.Background(), &Request{Model: "llama", Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := extractJSON(resp.Text); !ok || string(v) != `{"a":1}` || resp.Usage.OutputTokens != 4 {
		t.Fatalf("json extract: %q %v", resp.Text, ok)
	}
}

// ---- API tests ----

func TestNotConfigured(t *testing.T) {
	configure(t, map[string]any{"provider": "none"})
	slug := newSite(t, "Off Test")
	st := decode(t, call(t, "GET", "/api/v1/ai/status", nil))
	if st["configured"] != false {
		t.Fatalf("status: %v", st)
	}
	for _, rec := range []*httptest.ResponseRecorder{
		call(t, "POST", "/api/v1/sites/"+slug+"/builder", map[string]any{"message": "hi"}),
		call(t, "POST", "/api/v1/sites/"+slug+"/ai/chat", map[string]any{"prompt": "hi"}),
		call(t, "POST", "/api/v1/ai/create", map[string]any{"prompt": "a todo app"}),
	} {
		if rec.Code != 503 || !strings.Contains(rec.Body.String(), "AI isn't set up yet") {
			t.Fatalf("expected 503, got %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestAdminConfigMasksKey(t *testing.T) {
	configure(t, map[string]any{"provider": "anthropic", "apiKey": "sk-ant-api03-secretsecretsecret-WXYZ", "quotas": map[string]any{"userDailyTokens": 1234}})
	v := decode(t, call(t, "GET", "/api/v1/admin/ai", nil))
	if strings.Contains(fmt.Sprint(v), "secretsecret") {
		t.Fatalf("key leaked: %v", v)
	}
	if v["apiKey"] != "sk-a…WXYZ" || v["builderModel"] != "claude-sonnet-5-5" || v["pageModel"] != "claude-haiku-5-5" || v["configured"] != true {
		t.Fatalf("view: %v", v)
	}
	if v["quotas"].(map[string]any)["userDailyTokens"].(float64) != 1234 {
		t.Fatalf("quota: %v", v["quotas"])
	}
	// Sending the mask back keeps the key.
	configure(t, map[string]any{"apiKey": "sk-a…WXYZ", "builderModel": "claude-opus-5-5"})
	if currentConfig().APIKey != "sk-ant-api03-secretsecretsecret-WXYZ" || currentConfig().BuilderModel != "claude-opus-5-5" {
		t.Fatal("mask overwrote the key")
	}
	// Switching provider drops the old key and models.
	configure(t, map[string]any{"provider": "openai"})
	c := currentConfig()
	if c.APIKey != "" || c.BuilderModel != "gpt-5" || c.Configured() {
		t.Fatalf("switch: %+v", c)
	}
	if rec := call(t, "PUT", "/api/v1/admin/ai", map[string]any{"provider": "bogus"}); rec.Code != 400 {
		t.Fatalf("bad provider accepted: %d", rec.Code)
	}
	resetUsage()
	configure(t, map[string]any{"quotas": map[string]any{"userDailyTokens": defUserDaily}})
}

func TestAdminTestOp(t *testing.T) {
	f := newFake(t)
	f.push(jsonResp(200, map[string]any{"model": "claude-haiku-5-5", "content": []any{map[string]any{"type": "text", "text": "ok"}}, "stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 5, "output_tokens": 1}}))
	configure(t, map[string]any{"provider": "anthropic", "apiKey": "sk-ant-test-key-123456", "endpoint": f.URL})
	v := decode(t, call(t, "POST", "/api/v1/admin/ai/test", map[string]any{"model": "page"}))
	if v["ok"] != true || v["reply"] != "ok" || v["model"] != "claude-haiku-5-5" {
		t.Fatalf("test op: %v", v)
	}
	f.push(jsonResp(401, map[string]any{"type": "error", "error": map[string]any{"type": "authentication_error", "message": "invalid x-api-key"}}))
	v = decode(t, call(t, "POST", "/api/v1/admin/ai/test", nil))
	if v["ok"] != false || !strings.Contains(v["error"].(string), "key") {
		t.Fatalf("failing test op: %v", v)
	}
}

func TestPageChatAndQuota(t *testing.T) {
	resetUsage()
	f := newFake(t)
	configure(t, map[string]any{"provider": "openai-compatible", "endpoint": f.URL + "/v1", "builderModel": "llama3.1", "pageModel": "llama3.1", "apiKey": "", "quotas": map[string]any{"userDailyTokens": 100, "maxRequestTokens": 16000}})
	t.Cleanup(func() {
		configure(t, map[string]any{"quotas": map[string]any{"userDailyTokens": defUserDaily, "maxRequestTokens": defMaxRequest}})
		resetUsage()
	})
	slug := newSite(t, "Chat Test")

	// Non-stream JSON.
	f.push(jsonResp(200, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `Sure: {"sentiment":"positive"}`}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 30, "completion_tokens": 10}}))
	rec := call(t, "POST", "/api/v1/sites/"+slug+"/ai/chat", map[string]any{"messages": []any{map[string]any{"role": "system", "content": "classify"}, map[string]any{"role": "user", "content": "I love it"}}, "json": true})
	if rec.Code != 200 {
		t.Fatalf("chat: %d %s", rec.Code, rec.Body.String())
	}
	v := decode(t, rec)
	if v["json"].(map[string]any)["sentiment"] != "positive" || v["usage"].(map[string]any)["outputTokens"].(float64) != 10 {
		t.Fatalf("chat result: %v", v)
	}
	sent := f.req(0)
	if sent["model"] != "llama3.1" || sent["messages"].([]any)[0].(map[string]any)["role"] != "system" || sent["max_tokens"].(float64) != defPageMaxTokens {
		t.Fatalf("sent: %v", sent)
	}

	// Stream.
	f.push(sse(
		oaChunk(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "Hel"}}}}),
		oaChunk(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "lo"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 40, "completion_tokens": 30}}),
		"data: [DONE]\n\n"))
	rec = call(t, "POST", "/api/v1/sites/"+slug+"/ai/chat", map[string]any{"prompt": "hi", "stream": true})
	evs := sseEvents(t, rec.Body.Bytes())
	if len(evs) != 3 || evs[0]["text"] != "Hel" || evs[1]["text"] != "lo" || evs[2]["type"] != "done" {
		t.Fatalf("stream events: %v", evs)
	}

	// 30+10+40+30 = 110 tokens used > 100 daily limit → quota error.
	rec = call(t, "POST", "/api/v1/sites/"+slug+"/ai/chat", map[string]any{"prompt": "again"})
	if rec.Code != 429 || !strings.Contains(rec.Body.String(), "quota_exceeded") || !strings.Contains(rec.Body.String(), "allowance") {
		t.Fatalf("quota not enforced: %d %s", rec.Code, rec.Body.String())
	}
	st := decode(t, call(t, "GET", "/api/v1/ai/status", nil))
	b := st["budget"].(map[string]any)
	if b["remaining"].(float64) != 0 || b["used"].(float64) != 110 {
		t.Fatalf("budget: %v", b)
	}
	// Page requests can call ai.chat but not the builder.
	req := httptest.NewRequest("POST", "/api/v1/sites/"+slug+"/builder", strings.NewReader(`{"message":"x"}`))
	req.Header.Set("X-Bunker", "1")
	req.Header.Set("X-Bunker-Page", "1")
	prec := httptest.NewRecorder()
	handler.ServeHTTP(prec, req)
	if prec.Code != 403 {
		t.Fatalf("builder must not be PageSafe: %d", prec.Code)
	}
	// Request size cap.
	resetUsage()
	configure(t, map[string]any{"quotas": map[string]any{"userDailyTokens": defUserDaily, "maxRequestTokens": 2000}})
	rec = call(t, "POST", "/api/v1/sites/"+slug+"/ai/chat", map[string]any{"prompt": strings.Repeat("word ", 4000)})
	if rec.Code != 413 {
		t.Fatalf("size cap: %d %s", rec.Code, rec.Body.String())
	}
	// Admin usage report.
	testApp.DB.Exec(`INSERT INTO ai_usage(day,user_id,site_id,kind,input_tokens,output_tokens,billable_tokens,calls) VALUES(?,1,0,'page',5,5,10,1)`, today())
	u := decode(t, call(t, "GET", "/api/v1/admin/ai/usage?days=7", nil))
	if len(u["byUser"].([]any)) == 0 || u["totals"].(map[string]any)["calls"].(float64) < 1 {
		t.Fatalf("usage report: %v", u)
	}
	configure(t, map[string]any{"quotas": map[string]any{"maxRequestTokens": defMaxRequest}})
}

func TestBuilderLoop(t *testing.T) {
	resetUsage()
	f := newFake(t)
	configure(t, map[string]any{"provider": "anthropic", "apiKey": "sk-ant-test-key-123456", "endpoint": f.URL, "builderModel": "", "pageModel": ""})
	slug := newSite(t, "Lunch Orders")

	page := `<!doctype html><html><head><meta charset="utf-8"><title>Lunch</title><link rel="stylesheet" href="/_/kit.css"></head><body><button class="go">Order</button></body></html>`
	f.push(
		anthropicTurn("I'll make a lunch order page.", aTool{"t1", "write_file", map[string]any{"path": "index.html", "content": page}}),
		anthropicTurn("", aTool{"t2", "edit_file", map[string]any{"path": "index.html", "old_text": `class="go"`, "new_text": `class="go green"`}},
			aTool{"t3", "write_file", map[string]any{"path": "../evil.html", "content": "x"}}),
		anthropicTurn("Your lunch order page is ready. Try it in the preview."),
	)
	rec := call(t, "POST", "/api/v1/sites/"+slug+"/builder", map[string]any{"message": "a page to order lunch", "files": []any{map[string]any{"name": "menu.csv", "content": "meal,price\nPasta,8"}}})
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("builder: %d %s", rec.Code, rec.Body.String())
	}
	evs := sseEvents(t, rec.Body.Bytes())
	var types []string
	var files []string
	var text strings.Builder
	var done map[string]any
	for _, e := range evs {
		types = append(types, e["type"].(string))
		switch e["type"] {
		case "file":
			files = append(files, e["path"].(string)+":"+e["action"].(string))
		case "text":
			text.WriteString(e["text"].(string))
		case "done":
			done = e
		case "error":
			t.Fatalf("error event: %v", e)
		}
	}
	if done == nil {
		t.Fatalf("no done event: %v", types)
	}
	if strings.Join(files, ",") != "index.html:write,index.html:write" {
		t.Fatalf("file events: %v", files)
	}
	if !strings.Contains(text.String(), "lunch order page") || !strings.Contains(done["summary"].(string), "ready") || done["draft"] != true {
		t.Fatalf("narration/summary: %q %v", text.String(), done)
	}
	if done["budget"] == nil || done["usage"].(map[string]any)["turns"].(float64) != 3 {
		t.Fatalf("done payload: %v", done)
	}
	// The draft has the edited file.
	frec := call(t, "GET", "/api/v1/sites/"+slug+"/files/index.html", nil)
	if frec.Code != 200 || !strings.Contains(frec.Body.String(), `class="go green"`) {
		t.Fatalf("draft content: %d %s", frec.Code, frec.Body.String())
	}
	// The bad path was rejected with an error tool_result, the other call succeeded.
	msgs := f.req(2)["messages"].([]any)
	results := msgs[len(msgs)-1].(map[string]any)["content"].([]any)
	if len(results) != 2 || results[0].(map[string]any)["is_error"] != nil || results[1].(map[string]any)["is_error"] != true {
		t.Fatalf("tool results: %v", results)
	}
	// The first prompt carried the attachment and the system prompt.
	first := f.req(0)
	if !strings.Contains(fmt.Sprint(first["messages"]), "menu.csv") || !strings.Contains(fmt.Sprint(first["system"]), "Bunker sandbox rules") || first["tools"] == nil {
		t.Fatalf("first request: %v", first["messages"])
	}

	// History persisted.
	h := decode(t, call(t, "GET", "/api/v1/sites/"+slug+"/builder/history", nil))
	hm := h["messages"].([]any)
	if len(hm) != 2 || hm[0].(map[string]any)["role"] != "user" || !strings.Contains(hm[0].(map[string]any)["content"].(string), "menu.csv") ||
		!strings.Contains(hm[1].(map[string]any)["content"].(string), "Changed: index.html") {
		t.Fatalf("history: %v", hm)
	}

	// A follow-up includes previous turns and the current (small) site inline.
	f.push(anthropicTurn("Done, the button is green now."))
	rec = call(t, "POST", "/api/v1/sites/"+slug+"/builder", map[string]any{"message": "make the button green"})
	if !strings.Contains(rec.Body.String(), `"type":"done"`) {
		t.Fatalf("follow-up: %s", rec.Body.String())
	}
	fm := f.req(3)["messages"].([]any)
	if len(fm) != 3 || !strings.Contains(fmt.Sprint(fm[0]), "order lunch") || !strings.Contains(fmt.Sprint(fm[2]), `class=\"go green\"`) && !strings.Contains(fmt.Sprint(fm[2]), `class="go green"`) {
		t.Fatalf("follow-up messages: %v", fm)
	}

	// One build per site at a time.
	s, _ := testSite(t, slug)
	lockSite(s)
	rec = call(t, "POST", "/api/v1/sites/"+slug+"/builder", map[string]any{"message": "again"})
	unlockSite(s)
	if rec.Code != 409 {
		t.Fatalf("expected 409, got %d", rec.Code)
	}

	// Reset history.
	if rec := call(t, "DELETE", "/api/v1/sites/"+slug+"/builder/history", nil); rec.Code != 200 {
		t.Fatalf("reset: %d", rec.Code)
	}
	if hm := decode(t, call(t, "GET", "/api/v1/sites/"+slug+"/builder/history", nil))["messages"].([]any); len(hm) != 0 {
		t.Fatalf("history not cleared: %v", hm)
	}
	// Usage was recorded as builder usage.
	var n int64
	testApp.DB.QueryRow(`SELECT COALESCE(SUM(billable_tokens),0) FROM ai_usage WHERE kind='builder'`).Scan(&n)
	if n == 0 {
		t.Fatal("builder usage not recorded")
	}
}

func TestBuilderStopsRepeatingFailures(t *testing.T) {
	resetUsage()
	f := newFake(t)
	configure(t, map[string]any{"provider": "anthropic", "apiKey": "sk-ant-test-key-123456", "endpoint": f.URL})
	slug := newSite(t, "Loop Test")
	bad := aTool{"x", "edit_file", map[string]any{"path": "missing.html", "old_text": "a", "new_text": "b"}}
	for i := 0; i < 10; i++ {
		f.push(anthropicTurn("", bad))
	}
	rec := call(t, "POST", "/api/v1/sites/"+slug+"/builder", map[string]any{"message": "fix it"})
	evs := sseEvents(t, rec.Body.Bytes())
	last := evs[len(evs)-1]
	if last["type"] != "done" || f.count() != 4 {
		t.Fatalf("loop guard: %d calls, last %v", f.count(), last)
	}
	if !strings.Contains(fmt.Sprint(evs), "kept failing") {
		t.Fatalf("expected a plain stop message: %v", evs)
	}
}

func TestCreateApp(t *testing.T) {
	resetUsage()
	f := newFake(t)
	configure(t, map[string]any{"provider": "anthropic", "apiKey": "sk-ant-test-key-123456", "endpoint": f.URL})
	f.push(jsonResp(200, map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"title":"Team Lunch Poll","icon":"🥗"}`}}, "stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 50, "output_tokens": 10}}),
		anthropicTurn("Here's your poll.", aTool{"w", "write_file", map[string]any{"path": "index.html", "content": "<!doctype html><title>Poll</title>"}}),
		anthropicTurn("All set."))
	rec := call(t, "POST", "/api/v1/ai/create", map[string]any{"prompt": "a poll for where the team goes for lunch"})
	evs := sseEvents(t, rec.Body.Bytes())
	if len(evs) == 0 || evs[0]["type"] != "site" || evs[0]["title"] != "Team Lunch Poll" || evs[0]["icon"] != "🥗" {
		t.Fatalf("site event: %v", evs)
	}
	if evs[len(evs)-1]["type"] != "done" {
		t.Fatalf("no done: %v", evs[len(evs)-1])
	}
	slug := evs[0]["slug"].(string)
	if rec := call(t, "GET", "/api/v1/sites/"+slug+"/files/index.html", nil); rec.Code != 200 {
		t.Fatalf("created site draft: %d", rec.Code)
	}
	if f.req(0)["output_config"] == nil {
		t.Fatalf("title suggestion should use structured output: %v", f.req(0))
	}
}

func testSite(t *testing.T, slug string) (int64, error) {
	var id int64
	err := testApp.DB.QueryRow(`SELECT id FROM sites WHERE slug=?`, slug).Scan(&id)
	return id, err
}

func TestEffortGating(t *testing.T) {
	for m, want := range map[string]bool{"claude-sonnet-5-5": true, "claude-haiku-5-5": true, "claude-opus-4-8": true, "claude-haiku-4-5": false, "claude-sonnet-4-5": false, "gpt-5": false} {
		if anthropicSupportsEffort(m) != want {
			t.Errorf("%s: want %v", m, want)
		}
	}
}

func TestBuilderCancelsOnDisconnect(t *testing.T) {
	resetUsage()
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, aEv("message_start", map[string]any{"message": map[string]any{"model": "m", "usage": map[string]any{"input_tokens": 1}}}))
		io.WriteString(w, aEv("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text", "text": ""}}))
		io.WriteString(w, aEv("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": "Working"}}))
		w.(http.Flusher).Flush()
		select { // hang until Bunker gives up on us
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer upstream.Close()
	defer close(release)
	configure(t, map[string]any{"provider": "anthropic", "apiKey": "sk-ant-test-key-123456", "endpoint": upstream.URL})
	slug := newSite(t, "Disconnect Test")
	id, _ := testSite(t, slug)

	srv := httptest.NewServer(handler)
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/api/v1/sites/"+slug+"/builder", strings.NewReader(`{"message":"build"}`))
	req.Header.Set("X-Bunker", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	var got strings.Builder
	for !strings.Contains(got.String(), "Working") {
		n, err := resp.Body.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			t.Fatalf("stream ended early: %v %s", err, got.String())
		}
	}
	if !building(id) {
		t.Fatal("site should be locked while building")
	}
	cancel()
	resp.Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for building(id) {
		if time.Now().After(deadline) {
			t.Fatal("build did not stop after the client disconnected")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
