package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/sites"
)

// ---- server-side SSE ----

// sseWriter writes server-sent events; safe for concurrent use (the
// heartbeat goroutine and the producer share it).
type sseWriter struct {
	mu     sync.Mutex
	w      http.ResponseWriter
	rc     *http.ResponseController
	closed bool
}

func startSSE(w http.ResponseWriter) *sseWriter {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	s := &sseWriter{w: w, rc: http.NewResponseController(w)}
	s.rc.Flush()
	return s
}

// send writes one `data:` event with v as JSON.
func (s *sseWriter) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("closed")
	}
	if _, err := s.w.Write(append(append([]byte("data: "), b...), '\n', '\n')); err != nil {
		s.closed = true
		return err
	}
	return s.rc.Flush()
}

func (s *sseWriter) ping() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if _, err := s.w.Write([]byte(": ping\n\n")); err != nil {
		s.closed = true
		return
	}
	s.rc.Flush()
}

func (s *sseWriter) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

// heartbeat pings every d until ctx ends.
func (s *sseWriter) heartbeat(ctx context.Context, d time.Duration) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.ping()
		}
	}
}

var heartbeatEvery = 15 * time.Second

// errEvent is the SSE form of an error.
func errEvent(err error) map[string]any {
	he, ok := friendly(err).(*core.HTTPError)
	if !ok {
		he = core.Internal("Something went wrong.")
	}
	return map[string]any{"type": "error", "code": he.Code, "message": he.Message}
}

// ---- page API: bunker.ai ----

type chatIn struct {
	Messages  []chatMsg      `json:"messages"`
	Prompt    string         `json:"prompt"`
	System    string         `json:"system"`
	JSON      bool           `json:"json"`
	Schema    map[string]any `json:"schema"`
	MaxTokens int            `json:"maxTokens"`
	Stream    bool           `json:"stream"`
}

type chatMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// text accepts "content": "..." or [{type:"text", text:"..."}].
func (m chatMsg) text() string {
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Text != "" {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

const (
	defPageMaxTokens = 1024
	capPageMaxTokens = 8192
)

// buildChat validates the body and returns a normalized request.
func buildChat(in *chatIn, c *Config) (*Request, error) {
	req := &Request{Model: c.PageModel, System: strings.TrimSpace(in.System), JSON: in.JSON || in.Schema != nil, Schema: in.Schema, Effort: "low"}
	if in.Prompt != "" {
		req.Messages = append(req.Messages, Message{Role: "user", Content: in.Prompt})
	}
	for _, m := range in.Messages {
		t := m.text()
		switch strings.ToLower(m.Role) {
		case "system", "developer":
			if req.System != "" {
				req.System += "\n\n"
			}
			req.System += t
			continue
		case "assistant", "ai", "model":
			if t == "" {
				continue
			}
			req.Messages = appendMerged(req.Messages, Message{Role: "assistant", Content: t})
		case "user", "":
			if t == "" {
				continue
			}
			req.Messages = appendMerged(req.Messages, Message{Role: "user", Content: t})
		default:
			return nil, core.BadRequest("message roles are user, assistant or system")
		}
	}
	if len(req.Messages) == 0 {
		return nil, core.BadRequest("send a prompt or at least one user message")
	}
	if req.Messages[0].Role != "user" {
		req.Messages = append([]Message{{Role: "user", Content: "(conversation start)"}}, req.Messages...)
	}
	if req.Messages[len(req.Messages)-1].Role != "user" {
		return nil, core.BadRequest("the last message must be from the user")
	}
	mt := in.MaxTokens
	if mt <= 0 {
		mt = defPageMaxTokens
	}
	mt = min(mt, capPageMaxTokens)
	if c.MaxRequestTokens > 0 {
		mt = min(mt, int(c.MaxRequestTokens))
	}
	req.MaxTokens = max(mt, 16)
	if c.MaxRequestTokens > 0 {
		est := estimateTokens(req)
		if est+int64(req.MaxTokens) > c.MaxRequestTokens {
			return nil, core.TooLarge(friendlyTokens("That AI request is too big", est+int64(req.MaxTokens), c.MaxRequestTokens) + " Send less text, or split it into smaller requests.")
		}
	}
	return req, nil
}

func friendlyTokens(prefix string, n, limit int64) string {
	return prefix + " (about " + fmtThousands(n) + " tokens; the limit is " + fmtThousands(limit) + ")."
}

func fmtThousands(n int64) string {
	s := []byte{}
	str := strconv.FormatInt(n, 10)
	for i, c := range []byte(str) {
		if i > 0 && (len(str)-i)%3 == 0 {
			s = append(s, ',')
		}
		s = append(s, c)
	}
	return string(s)
}

// estimateTokens is a cheap upper-ish estimate (≈4 bytes per token).
func estimateTokens(req *Request) int64 {
	n := len(req.System)
	for _, m := range req.Messages {
		n += len(m.Content) + 8
		for _, r := range m.ToolResults {
			n += len(r.Content)
		}
		for _, t := range m.ToolCalls {
			n += len(t.Input)
		}
	}
	if req.Schema != nil {
		b, _ := json.Marshal(req.Schema)
		n += len(b)
	}
	return int64(n/4 + 1)
}

func appendMerged(ms []Message, m Message) []Message {
	if len(ms) > 0 && ms[len(ms)-1].Role == m.Role && len(ms[len(ms)-1].ToolCalls) == 0 && len(m.ToolResults) == 0 && len(ms[len(ms)-1].ToolResults) == 0 {
		ms[len(ms)-1].Content += "\n\n" + m.Content
		return ms
	}
	return append(ms, m)
}

func lastUserText(req *Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			return req.Messages[i].Content
		}
	}
	return ""
}

func registerChatOps(a *core.App) {
	a.Handle(core.Op{Name: "ai.chat", Method: "POST", Path: "/api/v1/sites/{site}/ai/chat", Summary: "Ask the AI from a page (bunker.ai.chat/json/stream)", PageSafe: true, Stream: true,
		Params: []core.Param{
			{Name: "site", In: "path", Required: true, Desc: "Site address (slug)"},
			{Name: "messages", In: "body", Type: "array", Desc: "Conversation: [{role: user|assistant|system, content}]"},
			{Name: "prompt", In: "body", Desc: "A single question (instead of messages)"},
			{Name: "system", In: "body", Desc: "Instructions for the AI"},
			{Name: "json", In: "body", Type: "boolean", Desc: "Answer as JSON"},
			{Name: "schema", In: "body", Type: "object", Desc: "JSON Schema the answer must follow (implies json)"},
			{Name: "maxTokens", In: "body", Type: "integer", Desc: "Longest answer in tokens (default 1024, capped)"},
			{Name: "stream", In: "body", Type: "boolean", Desc: "Stream the answer as server-sent events"},
		},
		Handler: handleChat})
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	u := core.MustUser(r)
	s, _, err := sites.Full(r.Context(), u, r.PathValue("site"), core.RoleViewer)
	if err != nil {
		core.Err(w, err)
		return
	}
	c, p, err := ready()
	if err != nil {
		core.Err(w, err)
		return
	}
	var in chatIn
	if err := core.Decode(r, &in); err != nil {
		core.Err(w, err)
		return
	}
	req, err := buildChat(&in, c)
	if err != nil {
		core.Err(w, err)
		return
	}
	if err := checkQuota(r.Context(), c, u, s.ID); err != nil {
		core.Err(w, err)
		return
	}
	start := time.Now()
	finish := func(resp *Response, calls int, usage Usage) {
		recordUsage(r.Context(), u.ID, s.ID, KindPage, usage, calls)
		answer := ""
		stop := ""
		if resp != nil {
			answer, stop = resp.Text, resp.StopReason
		}
		auditCall(r.Context(), c, s.ID, s.Slug, KindPage, req.Model, usage, time.Since(start).Milliseconds(), map[string]any{"stream": in.Stream, "stop": stop}, lastUserText(req), answer)
	}

	if !in.Stream {
		resp, err := p.Complete(r.Context(), req, nil)
		if err != nil {
			finish(nil, 1, Usage{})
			core.Err(w, friendly(err))
			return
		}
		usage := resp.Usage
		calls := 1
		out := map[string]any{"text": resp.Text, "stopReason": resp.StopReason}
		if req.JSON {
			v, ok := extractJSON(resp.Text)
			if !ok && resp.StopReason == StopEnd {
				// One repair attempt: show the model its answer and ask again.
				rep := *req
				rep.Messages = append(append([]Message(nil), req.Messages...), Message{Role: "assistant", Content: resp.Text},
					Message{Role: "user", Content: "That was not valid JSON. Reply again with only the JSON value."})
				if r2, err := p.Complete(r.Context(), &rep, nil); err == nil {
					calls++
					usage.Add(r2.Usage)
					resp = r2
					out["text"] = r2.Text
					v, ok = extractJSON(r2.Text)
				}
			}
			if ok {
				out["json"] = v
			} else {
				out["json"] = nil
				out["jsonError"] = "The AI didn't return valid JSON."
			}
		}
		if resp.StopReason == StopRefusal {
			out["refused"] = true
		}
		finish(resp, calls, usage)
		out["usage"] = map[string]any{"inputTokens": usage.InputTokens + usage.CacheReadTokens + usage.CacheWriteTokens, "outputTokens": usage.OutputTokens}
		out["model"] = req.Model
		core.JSON(w, http.StatusOK, out)
		return
	}

	sw := startSSE(w)
	defer sw.close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go sw.heartbeat(ctx, heartbeatEvery)
	resp, err := p.Complete(ctx, req, func(t string) {
		if sw.send(map[string]any{"type": "text", "text": t}) != nil {
			cancel() // client went away
		}
	})
	if err != nil {
		finish(nil, 1, Usage{})
		sw.send(errEvent(err))
		return
	}
	finish(resp, 1, resp.Usage)
	done := map[string]any{"type": "done", "stopReason": resp.StopReason, "model": req.Model,
		"usage": map[string]any{"inputTokens": resp.Usage.InputTokens + resp.Usage.CacheReadTokens + resp.Usage.CacheWriteTokens, "outputTokens": resp.Usage.OutputTokens}}
	if req.JSON {
		if v, ok := extractJSON(resp.Text); ok {
			done["json"] = v
		}
	}
	sw.send(done)
}
