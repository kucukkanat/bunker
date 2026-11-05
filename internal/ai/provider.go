package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---- normalized, provider-neutral types ----

// Message is one conversation turn. Role is "user" or "assistant".
// A user turn may carry ToolResults (answers to the previous assistant
// turn's ToolCalls); an assistant turn may carry ToolCalls.
type Message struct {
	Role        string       `json:"role"`
	Content     string       `json:"content,omitempty"`
	ToolCalls   []ToolCall   `json:"toolCalls,omitempty"`
	ToolResults []ToolResult `json:"toolResults,omitempty"`

	// raw holds the provider's own representation of an assistant turn
	// (e.g. Anthropic content blocks including thinking signatures) so it
	// can be replayed unchanged inside the same agent loop.
	raw         json.RawMessage
	rawProvider string
}

// ToolCall is a model's request to run a tool.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult answers a ToolCall.
type ToolResult struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content"`
	IsError bool   `json:"isError,omitempty"`
}

// Tool is a function the model may call. Schema is a JSON Schema object.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
}

// Request is a normalized model call.
type Request struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []Tool
	MaxTokens int
	// JSON asks for a JSON answer; Schema optionally constrains it.
	JSON   bool
	Schema map[string]any
	// Effort is a hint ("low", "medium", "high") for providers that support it.
	Effort string
	// Cache marks the prompt prefix as cacheable (Anthropic prompt caching).
	Cache bool
}

// Stop reasons, normalized.
const (
	StopEnd       = "end"
	StopToolUse   = "tool_use"
	StopMaxTokens = "max_tokens"
	StopRefusal   = "refusal"
)

// Usage counts tokens for one call.
type Usage struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	// CacheReadTokens are prompt tokens served from cache (billed ~10%).
	CacheReadTokens  int64 `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int64 `json:"cacheWriteTokens,omitempty"`
}

// Billable is the number of tokens counted against quotas: cache reads
// count a tenth, like providers bill them.
func (u Usage) Billable() int64 {
	return u.InputTokens + u.CacheWriteTokens + u.CacheReadTokens/10 + u.OutputTokens
}

func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.CacheReadTokens += o.CacheReadTokens
	u.CacheWriteTokens += o.CacheWriteTokens
}

// Response is a normalized model answer.
type Response struct {
	Text       string
	ToolCalls  []ToolCall
	StopReason string
	Usage      Usage
	Model      string
	// Message is the assistant turn to append to the conversation.
	Message Message
}

// Provider talks to one model API. When onText is non-nil the call
// streams and onText receives text deltas as they arrive.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req *Request, onText func(string)) (*Response, error)
}

// ProviderError is an error returned by the upstream API.
type ProviderError struct {
	Status  int
	Type    string
	Message string
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("ai provider: %d %s: %s", e.Status, e.Type, e.Message)
}

func (e *ProviderError) retryable() bool {
	return e.Status == 408 || e.Status == 409 || e.Status == 429 || e.Status >= 500
}

// ---- HTTP plumbing shared by providers ----

var httpClient = &http.Client{Transport: &http.Transport{
	Proxy:               http.ProxyFromEnvironment,
	DialContext:         (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout: 15 * time.Second,
	MaxIdleConns:        64,
	MaxIdleConnsPerHost: 16,
	IdleConnTimeout:     90 * time.Second,
	ForceAttemptHTTP2:   true,
}}

// Tunables (tests shorten them).
var (
	maxRetries       = 3
	retryBase        = 800 * time.Millisecond
	retryMaxWait     = 20 * time.Second
	nonStreamTimeout = 3 * time.Minute
	streamIdle       = 2 * time.Minute
)

// doJSON POSTs body to url with headers, retrying 408/409/429/5xx and
// network errors with exponential backoff (honoring Retry-After). The
// caller must close the returned response body. On a non-2xx final
// answer it returns a *ProviderError built by parseErr.
func doJSON(ctx context.Context, url string, headers map[string]string, body any, parseErr func(status int, b []byte) *ProviderError) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			if v != "" {
				req.Header.Set(k, v)
			}
		}
		resp, err := httpClient.Do(req)
		var wait time.Duration
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
		} else if resp.StatusCode/100 == 2 {
			return resp, nil
		} else {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			pe := parseErr(resp.StatusCode, b)
			if !pe.retryable() {
				return nil, pe
			}
			lastErr = pe
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if s, err := strconv.Atoi(ra); err == nil {
					wait = time.Duration(s) * time.Second
				}
			}
		}
		if attempt >= maxRetries {
			return nil, lastErr
		}
		if wait == 0 {
			wait = retryBase << attempt
			wait += time.Duration(rand.Int64N(int64(wait)/2 + 1))
		}
		if wait > retryMaxWait {
			wait = retryMaxWait
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

// genericErr parses {"error":{"type","message"}} / {"error":"..."} / {"message"} bodies.
func genericErr(status int, b []byte) *ProviderError {
	pe := &ProviderError{Status: status}
	var v struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Type    string          `json:"type"`
	}
	if json.Unmarshal(b, &v) == nil {
		pe.Message, pe.Type = v.Message, v.Type
		if len(v.Error) > 0 {
			var e struct {
				Type    string `json:"type"`
				Code    any    `json:"code"`
				Message string `json:"message"`
			}
			var s string
			if json.Unmarshal(v.Error, &e) == nil {
				pe.Message = e.Message
				pe.Type = e.Type
				if pe.Type == "" && e.Code != nil {
					pe.Type = fmt.Sprint(e.Code)
				}
			} else if json.Unmarshal(v.Error, &s) == nil {
				pe.Message = s
			}
		}
	}
	if pe.Message == "" {
		pe.Message = strings.TrimSpace(string(b))
		if len(pe.Message) > 300 {
			pe.Message = pe.Message[:300]
		}
	}
	if pe.Message == "" {
		pe.Message = http.StatusText(status)
	}
	return pe
}

// ---- SSE ----

// sseEvent is one server-sent event.
type sseEvent struct {
	Event string
	Data  []byte
}

// readSSE calls fn for every event in r until EOF, an error, or fn
// returns false. Comment lines and unknown fields are ignored.
func readSSE(r io.Reader, fn func(ev sseEvent) bool) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var ev sseEvent
	var data bytes.Buffer
	for {
		line, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// Long line: accumulate the rest of it.
			buf := append([]byte(nil), line...)
			for errors.Is(err, bufio.ErrBufferFull) {
				line, err = br.ReadSlice('\n')
				buf = append(buf, line...)
				if len(buf) > 16<<20 {
					return errors.New("sse line too long")
				}
			}
			line = buf
		}
		if len(line) == 0 && err != nil {
			if data.Len() > 0 {
				ev.Data = data.Bytes()
				fn(ev)
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			if data.Len() > 0 || ev.Event != "" {
				ev.Data = append([]byte(nil), data.Bytes()...)
				if !fn(ev) {
					return nil
				}
			}
			ev = sseEvent{}
			data.Reset()
		} else if line[0] != ':' {
			field, val, _ := bytes.Cut(line, []byte(":"))
			val = bytes.TrimPrefix(val, []byte(" "))
			switch string(field) {
			case "event":
				ev.Event = string(val)
			case "data":
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.Write(val)
			}
		}
		if err != nil {
			if data.Len() > 0 {
				ev.Data = data.Bytes()
				fn(ev)
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// idleReader cancels the request when no bytes arrive for d.
type idleReader struct {
	r     io.Reader
	d     time.Duration
	timer *time.Timer
}

func newIdleReader(r io.Reader, d time.Duration, cancel context.CancelFunc) *idleReader {
	return &idleReader{r: r, d: d, timer: time.AfterFunc(d, cancel)}
}

func (i *idleReader) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	if n > 0 {
		i.timer.Reset(i.d)
	}
	return n, err
}

func (i *idleReader) stop() { i.timer.Stop() }

// ---- helpers ----

// toolSeq assigns unique ids to tool calls that arrive without one.
var toolSeq struct {
	sync.Mutex
	n int
}

func newToolID() string {
	toolSeq.Lock()
	defer toolSeq.Unlock()
	toolSeq.n++
	return "call_" + strconv.Itoa(toolSeq.n)
}

// jsonInstruction is appended to the system prompt when JSON output is
// requested without native schema support.
func jsonInstruction(schema map[string]any) string {
	s := "\n\nRespond with only a valid JSON value. No prose, no explanations, no code fences."
	if schema != nil {
		b, _ := json.Marshal(schema)
		s += " The JSON must match this JSON Schema: " + string(b)
	}
	return s
}

// extractJSON pulls the first JSON value out of model text (tolerating
// code fences and chatter around it).
func extractJSON(text string) (json.RawMessage, bool) {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "```") {
		t = strings.TrimPrefix(t, "```json")
		t = strings.TrimPrefix(t, "```JSON")
		t = strings.TrimPrefix(t, "```")
		if i := strings.LastIndex(t, "```"); i >= 0 {
			t = t[:i]
		}
		t = strings.TrimSpace(t)
	}
	if json.Valid([]byte(t)) && t != "" {
		return json.RawMessage(t), true
	}
	start := strings.IndexAny(t, "{[")
	if start < 0 {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(t[start:]))
	var v json.RawMessage
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// validJSONObject reports whether b is a JSON object (tool inputs must be).
func validJSONObject(b []byte) bool {
	b = bytes.TrimSpace(b)
	return len(b) > 1 && b[0] == '{' && json.Valid(b)
}
