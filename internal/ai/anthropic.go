package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// anthropicProvider calls the Anthropic Messages API (POST /v1/messages).
type anthropicProvider struct {
	endpoint string // base URL, default https://api.anthropic.com
	key      string
	version  string // anthropic-version header
}

func (p *anthropicProvider) Name() string { return "anthropic" }

type aBlock = map[string]any

func (p *anthropicProvider) body(req *Request, stream bool) map[string]any {
	system := req.System
	var outputConfig map[string]any
	if req.JSON {
		if req.Schema != nil {
			outputConfig = map[string]any{"format": map[string]any{"type": "json_schema", "schema": req.Schema}}
		} else {
			system += jsonInstruction(nil)
		}
	}
	if req.Effort != "" && anthropicSupportsEffort(req.Model) {
		if outputConfig == nil {
			outputConfig = map[string]any{}
		}
		outputConfig["effort"] = req.Effort
	}
	msgs := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, map[string]any{"role": m.Role, "content": p.content(m)})
	}
	if req.Cache && len(msgs) > 0 {
		// Cache the growing conversation prefix: mark the last block of the
		// last message (system is marked separately below).
		if blocks, ok := msgs[len(msgs)-1]["content"].([]aBlock); ok && len(blocks) > 0 {
			last := map[string]any{}
			for k, v := range blocks[len(blocks)-1] {
				last[k] = v
			}
			last["cache_control"] = map[string]any{"type": "ephemeral"}
			cp := append([]aBlock(nil), blocks...)
			cp[len(cp)-1] = last
			msgs[len(msgs)-1]["content"] = cp
		}
	}
	b := map[string]any{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
		"messages":   msgs,
	}
	if system != "" {
		sb := aBlock{"type": "text", "text": system}
		if req.Cache {
			sb["cache_control"] = map[string]any{"type": "ephemeral"}
		}
		b["system"] = []aBlock{sb}
	}
	if len(req.Tools) > 0 {
		tools := make([]aBlock, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, aBlock{"name": t.Name, "description": t.Description, "input_schema": t.Schema})
		}
		b["tools"] = tools
	}
	if outputConfig != nil {
		b["output_config"] = outputConfig
	}
	if stream {
		b["stream"] = true
	}
	return b
}

// content renders a normalized message as Anthropic content blocks.
func (p *anthropicProvider) content(m Message) any {
	if m.Role == "assistant" && m.rawProvider == "anthropic" && len(m.raw) > 0 {
		var blocks []aBlock
		if json.Unmarshal(m.raw, &blocks) == nil {
			return blocks
		}
	}
	var blocks []aBlock
	for _, r := range m.ToolResults {
		c := r.Content
		if c == "" {
			c = "(empty)"
		}
		tb := aBlock{"type": "tool_result", "tool_use_id": r.ID, "content": c}
		if r.IsError {
			tb["is_error"] = true
		}
		blocks = append(blocks, tb)
	}
	if m.Content != "" {
		blocks = append(blocks, aBlock{"type": "text", "text": m.Content})
	}
	for _, c := range m.ToolCalls {
		var in any = map[string]any{}
		if len(c.Input) > 0 {
			_ = json.Unmarshal(c.Input, &in)
		}
		blocks = append(blocks, aBlock{"type": "tool_use", "id": c.ID, "name": c.Name, "input": in})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, aBlock{"type": "text", "text": "(no content)"})
	}
	return blocks
}

func (p *anthropicProvider) headers() map[string]string {
	v := p.version
	if v == "" {
		v = "2023-06-01"
	}
	return map[string]string{"x-api-key": p.key, "anthropic-version": v}
}

func (p *anthropicProvider) url() string {
	base := strings.TrimRight(p.endpoint, "/")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	if strings.HasSuffix(base, "/v1/messages") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/messages"
	}
	return base + "/v1/messages"
}

func (p *anthropicProvider) Complete(ctx context.Context, req *Request, onText func(string)) (*Response, error) {
	resp, err := p.call(ctx, req, onText)
	if err != nil {
		// Some models/proxies don't support structured outputs or effort:
		// retry once without them.
		var pe *ProviderError
		if errors.As(err, &pe) && pe.Status == 400 && (req.Schema != nil || req.Effort != "") &&
			(strings.Contains(pe.Message, "output_config") || strings.Contains(pe.Message, "effort") || strings.Contains(pe.Message, "schema") || strings.Contains(pe.Message, "format")) {
			r2 := *req
			if r2.Schema != nil {
				r2.System += jsonInstruction(r2.Schema)
				r2.Schema = nil
				r2.JSON = false
			}
			r2.Effort = ""
			return p.call(ctx, &r2, onText)
		}
	}
	return resp, err
}

func (p *anthropicProvider) call(ctx context.Context, req *Request, onText func(string)) (*Response, error) {
	stream := onText != nil
	if !stream {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, nonStreamTimeout)
		defer cancel()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	hr, err := doJSON(ctx, p.url(), p.headers(), p.body(req, stream), genericErr)
	if err != nil {
		return nil, err
	}
	defer hr.Body.Close()
	if !stream {
		var m anthropicMessage
		if err := json.NewDecoder(io.LimitReader(hr.Body, 32<<20)).Decode(&m); err != nil {
			return nil, err
		}
		return m.normalize(), nil
	}
	ir := newIdleReader(hr.Body, streamIdle, cancel)
	defer ir.stop()
	return p.parseStream(ir, onText)
}

// anthropicMessage is a (non-streaming) Messages API response.
type anthropicMessage struct {
	Model      string            `json:"model"`
	Content    []json.RawMessage `json:"content"`
	StopReason string            `json:"stop_reason"`
	Usage      anthropicUsage    `json:"usage"`
}

type anthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

func (u anthropicUsage) norm() Usage {
	return Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadInputTokens, CacheWriteTokens: u.CacheCreationInputTokens}
}

func (m *anthropicMessage) normalize() *Response {
	r := &Response{Model: m.Model, Usage: m.Usage.norm(), StopReason: normAnthropicStop(m.StopReason)}
	var text strings.Builder
	blocks := make([]json.RawMessage, 0, len(m.Content))
	for _, raw := range m.Content {
		var b struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if json.Unmarshal(raw, &b) != nil {
			continue
		}
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			in := b.Input
			if len(in) == 0 || string(in) == "null" {
				in = json.RawMessage("{}")
			}
			r.ToolCalls = append(r.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Input: in})
		}
		blocks = append(blocks, raw)
	}
	r.Text = text.String()
	raw, _ := json.Marshal(blocks)
	r.Message = Message{Role: "assistant", Content: r.Text, ToolCalls: r.ToolCalls, raw: raw, rawProvider: "anthropic"}
	return r
}

func normAnthropicStop(s string) string {
	switch s {
	case "tool_use":
		return StopToolUse
	case "max_tokens", "model_context_window_exceeded":
		return StopMaxTokens
	case "refusal":
		return StopRefusal
	}
	return StopEnd
}

// parseStream reassembles a streamed Messages API response, forwarding
// text deltas. Content blocks are rebuilt exactly (including thinking
// signatures) so the assistant turn can be replayed in a tool loop.
func (p *anthropicProvider) parseStream(r io.Reader, onText func(string)) (*Response, error) {
	type blockState struct {
		block    aBlock
		partial  strings.Builder // tool_use input JSON
		text     strings.Builder
		thinking strings.Builder
	}
	var blocks []*blockState
	var model, stop string
	var usage anthropicUsage
	var streamErr error
	done := false
	err := readSSE(r, func(ev sseEvent) bool {
		var e struct {
			Type    string          `json:"type"`
			Index   int             `json:"index"`
			Message json.RawMessage `json:"message"`
			Block   aBlock          `json:"content_block"`
			Delta   struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage *anthropicUsage `json:"usage"`
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(ev.Data, &e) != nil {
			return true
		}
		switch e.Type {
		case "message_start":
			var m anthropicMessage
			if json.Unmarshal(e.Message, &m) == nil {
				model = m.Model
				usage = m.Usage
			}
		case "content_block_start":
			for len(blocks) <= e.Index {
				blocks = append(blocks, &blockState{})
			}
			bs := blocks[e.Index]
			bs.block = e.Block
			if t, _ := e.Block["text"].(string); t != "" {
				bs.text.WriteString(t)
				onText(t)
			}
		case "content_block_delta":
			if e.Index >= len(blocks) || blocks[e.Index].block == nil {
				return true
			}
			bs := blocks[e.Index]
			switch e.Delta.Type {
			case "text_delta":
				bs.text.WriteString(e.Delta.Text)
				if e.Delta.Text != "" {
					onText(e.Delta.Text)
				}
			case "thinking_delta":
				bs.thinking.WriteString(e.Delta.Thinking)
			case "signature_delta":
				bs.block["signature"] = e.Delta.Signature
			case "input_json_delta":
				bs.partial.WriteString(e.Delta.PartialJSON)
			}
		case "message_delta":
			if e.Delta.StopReason != "" {
				stop = e.Delta.StopReason
			}
			if e.Usage != nil {
				if e.Usage.OutputTokens > usage.OutputTokens {
					usage.OutputTokens = e.Usage.OutputTokens
				}
				if e.Usage.InputTokens > usage.InputTokens {
					usage.InputTokens = e.Usage.InputTokens
				}
				if e.Usage.CacheReadInputTokens > usage.CacheReadInputTokens {
					usage.CacheReadInputTokens = e.Usage.CacheReadInputTokens
				}
				if e.Usage.CacheCreationInputTokens > usage.CacheCreationInputTokens {
					usage.CacheCreationInputTokens = e.Usage.CacheCreationInputTokens
				}
			}
		case "message_stop":
			done = true
			return false
		case "error":
			pe := &ProviderError{Status: 500, Type: "stream_error", Message: "stream error"}
			if e.Error != nil {
				pe.Type, pe.Message = e.Error.Type, e.Error.Message
				if e.Error.Type == "overloaded_error" {
					pe.Status = 529
				} else if e.Error.Type == "rate_limit_error" {
					pe.Status = 429
				}
			}
			streamErr = pe
			return false
		}
		return true
	})
	if streamErr != nil {
		return nil, streamErr
	}
	if err != nil {
		return nil, err
	}
	if !done && stop == "" {
		return nil, io.ErrUnexpectedEOF
	}
	msg := anthropicMessage{Model: model, StopReason: stop, Usage: usage}
	for _, bs := range blocks {
		if bs == nil || bs.block == nil {
			continue
		}
		b := bs.block
		switch b["type"] {
		case "text":
			b["text"] = bs.text.String()
		case "thinking":
			b["thinking"] = bs.thinking.String()
		case "tool_use":
			in := strings.TrimSpace(bs.partial.String())
			if in == "" {
				in = "{}"
			}
			var v any
			if json.Unmarshal([]byte(in), &v) != nil {
				// Truncated tool input (e.g. max_tokens): keep it as an
				// invalid marker so the loop can report it to the model.
				v = map[string]any{"__invalid_json": true}
			}
			b["input"] = v
		}
		raw, _ := json.Marshal(b)
		msg.Content = append(msg.Content, raw)
	}
	return msg.normalize(), nil
}

var claudeVer = regexp.MustCompile(`claude-(opus|sonnet|haiku|fable|mythos)-(\d+)(?:-(\d+))?`)

// anthropicSupportsEffort reports whether a Claude model accepts
// output_config.effort (Opus 4.5+, Sonnet 4.6+, and every 5.x model).
func anthropicSupportsEffort(model string) bool {
	m := claudeVer.FindStringSubmatch(model)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[2])
	minor := 0
	if m[3] != "" && len(m[3]) <= 2 {
		minor, _ = strconv.Atoi(m[3])
	}
	if major >= 5 {
		return true
	}
	if major == 4 {
		switch m[1] {
		case "opus":
			return minor >= 5
		case "sonnet":
			return minor >= 6
		}
	}
	return false
}
