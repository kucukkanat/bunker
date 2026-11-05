package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strings"
)

// openaiProvider speaks the Chat Completions API. It serves three
// flavors: "openai" (api.openai.com), "azure-openai" (deployment
// endpoints with api-key + api-version) and "openai-compatible"
// (Ollama, LM Studio, vLLM... base URL with optional key).
type openaiProvider struct {
	flavor     string
	endpoint   string
	key        string
	apiVersion string // azure only; "v1" uses Azure's /openai/v1 API
}

func (p *openaiProvider) Name() string { return p.flavor }

func (p *openaiProvider) url(model string) string {
	base := strings.TrimRight(p.endpoint, "/")
	switch p.flavor {
	case "azure-openai":
		if strings.Contains(base, "/chat/completions") {
			return base
		}
		if p.apiVersion == "v1" || strings.HasSuffix(base, "/openai/v1") {
			if !strings.HasSuffix(base, "/openai/v1") {
				base += "/openai/v1"
			}
			return base + "/chat/completions"
		}
		v := p.apiVersion
		if v == "" {
			v = "2024-10-21"
		}
		base = strings.TrimSuffix(base, "/openai")
		return base + "/openai/deployments/" + url.PathEscape(model) + "/chat/completions?api-version=" + url.QueryEscape(v)
	case "openai":
		if base == "" {
			base = "https://api.openai.com/v1"
		}
	}
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	if p.flavor == "openai-compatible" && !strings.HasSuffix(base, "/v1") && !strings.Contains(base, "/v1/") {
		// Ollama / LM Studio / vLLM all serve the OpenAI API under /v1.
		base += "/v1"
	}
	return base + "/chat/completions"
}

func (p *openaiProvider) headers() map[string]string {
	if p.flavor == "azure-openai" {
		return map[string]string{"api-key": p.key}
	}
	if p.key == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + p.key}
}

func (p *openaiProvider) body(req *Request, stream bool) map[string]any {
	system := req.System
	var respFormat map[string]any
	if req.JSON {
		if req.Schema != nil {
			respFormat = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "output", "schema": req.Schema}}
		} else {
			respFormat = map[string]any{"type": "json_object"}
		}
		// json_object mode requires the word "JSON" in the prompt.
		system += jsonInstruction(nil)
	}
	msgs := make([]map[string]any, 0, len(req.Messages)+1)
	if system != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": system})
	}
	for _, m := range req.Messages {
		if m.Role == "assistant" {
			am := map[string]any{"role": "assistant"}
			if m.Content != "" {
				am["content"] = m.Content
			} else {
				am["content"] = nil
			}
			if len(m.ToolCalls) > 0 {
				tcs := make([]map[string]any, 0, len(m.ToolCalls))
				for _, c := range m.ToolCalls {
					args := string(c.Input)
					if args == "" {
						args = "{}"
					}
					tcs = append(tcs, map[string]any{"id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": args}})
				}
				am["tool_calls"] = tcs
			} else if m.Content == "" {
				am["content"] = ""
			}
			msgs = append(msgs, am)
			continue
		}
		for _, r := range m.ToolResults {
			c := r.Content
			if r.IsError {
				c = "Error: " + c
			}
			msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": r.ID, "content": c})
		}
		if m.Content != "" || len(m.ToolResults) == 0 {
			msgs = append(msgs, map[string]any{"role": "user", "content": m.Content})
		}
	}
	b := map[string]any{"messages": msgs}
	if p.flavor != "azure-openai" || p.apiVersion == "v1" {
		b["model"] = req.Model
	}
	if req.MaxTokens > 0 {
		if p.flavor == "openai-compatible" {
			b["max_tokens"] = req.MaxTokens
		} else {
			b["max_completion_tokens"] = req.MaxTokens
		}
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Schema}})
		}
		b["tools"] = tools
	}
	if respFormat != nil {
		b["response_format"] = respFormat
	}
	if req.Effort != "" && p.flavor == "openai" && openaiReasoning(req.Model) {
		b["reasoning_effort"] = req.Effort
	}
	if stream {
		b["stream"] = true
		b["stream_options"] = map[string]any{"include_usage": true}
	}
	return b
}

func openaiReasoning(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4") || strings.HasPrefix(m, "gpt-5")
}

type oaUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func (u *oaUsage) norm() Usage {
	if u == nil {
		return Usage{}
	}
	out := Usage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens}
	if u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 && u.PromptTokensDetails.CachedTokens <= u.PromptTokens {
		out.CacheReadTokens = u.PromptTokensDetails.CachedTokens
		out.InputTokens -= u.PromptTokensDetails.CachedTokens
	}
	return out
}

type oaToolCall struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func normOpenAIStop(s string) string {
	switch s {
	case "tool_calls", "function_call":
		return StopToolUse
	case "length":
		return StopMaxTokens
	case "content_filter":
		return StopRefusal
	}
	return StopEnd
}

func (p *openaiProvider) Complete(ctx context.Context, req *Request, onText func(string)) (*Response, error) {
	stream := onText != nil
	if !stream {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, nonStreamTimeout)
		defer cancel()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	hr, err := doJSON(ctx, p.url(req.Model), p.headers(), p.body(req, stream), genericErr)
	if err != nil {
		return nil, err
	}
	defer hr.Body.Close()
	if !stream {
		var v struct {
			Model   string `json:"model"`
			Choices []struct {
				Message struct {
					Content   *string      `json:"content"`
					Refusal   string       `json:"refusal"`
					ToolCalls []oaToolCall `json:"tool_calls"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *oaUsage `json:"usage"`
		}
		if err := json.NewDecoder(io.LimitReader(hr.Body, 32<<20)).Decode(&v); err != nil {
			return nil, err
		}
		r := &Response{Model: v.Model, Usage: v.Usage.norm(), StopReason: StopEnd}
		if len(v.Choices) > 0 {
			c := v.Choices[0]
			if c.Message.Content != nil {
				r.Text = *c.Message.Content
			}
			r.StopReason = normOpenAIStop(c.FinishReason)
			if c.Message.Refusal != "" {
				r.StopReason = StopRefusal
				if r.Text == "" {
					r.Text = c.Message.Refusal
				}
			}
			for _, tc := range c.Message.ToolCalls {
				r.ToolCalls = append(r.ToolCalls, normOAToolCall(tc.ID, tc.Function.Name, tc.Function.Arguments))
			}
		}
		if len(r.ToolCalls) > 0 && r.StopReason == StopEnd {
			r.StopReason = StopToolUse
		}
		r.Message = Message{Role: "assistant", Content: r.Text, ToolCalls: r.ToolCalls}
		return r, nil
	}
	ir := newIdleReader(hr.Body, streamIdle, cancel)
	defer ir.stop()
	return parseOpenAIStream(ir, onText)
}

func normOAToolCall(id, name, args string) ToolCall {
	if id == "" {
		id = newToolID()
	}
	in := json.RawMessage(strings.TrimSpace(args))
	if len(in) == 0 {
		in = json.RawMessage("{}")
	} else if !json.Valid(in) {
		in = json.RawMessage(`{"__invalid_json":true}`)
	}
	return ToolCall{ID: id, Name: name, Input: in}
}

// parseOpenAIStream reassembles a streamed Chat Completions response.
func parseOpenAIStream(r io.Reader, onText func(string)) (*Response, error) {
	type slot struct {
		id, name string
		args     strings.Builder
	}
	var slots []*slot
	byIndex := map[int]*slot{}
	var text strings.Builder
	resp := &Response{StopReason: StopEnd}
	var streamErr error
	err := readSSE(r, func(ev sseEvent) bool {
		d := strings.TrimSpace(string(ev.Data))
		if d == "[DONE]" {
			return false
		}
		var c struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content   string       `json:"content"`
					Refusal   string       `json:"refusal"`
					ToolCalls []oaToolCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *oaUsage `json:"usage"`
		}
		if json.Unmarshal(ev.Data, &c) != nil {
			return true
		}
		var eb struct {
			Error *struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		if json.Unmarshal(ev.Data, &eb) == nil && eb.Error != nil {
			streamErr = &ProviderError{Status: 500, Type: eb.Error.Type, Message: eb.Error.Message}
			return false
		}
		if c.Model != "" {
			resp.Model = c.Model
		}
		if c.Usage != nil {
			resp.Usage = c.Usage.norm()
		}
		for _, ch := range c.Choices {
			if ch.Delta.Content != "" {
				text.WriteString(ch.Delta.Content)
				onText(ch.Delta.Content)
			}
			if ch.Delta.Refusal != "" {
				text.WriteString(ch.Delta.Refusal)
				resp.StopReason = StopRefusal
			}
			for _, tc := range ch.Delta.ToolCalls {
				var s *slot
				if tc.Index != nil {
					s = byIndex[*tc.Index]
					if s == nil {
						s = &slot{}
						byIndex[*tc.Index] = s
						slots = append(slots, s)
					}
				} else if tc.ID != "" || len(slots) == 0 {
					// Servers that omit index send each call whole.
					s = &slot{}
					slots = append(slots, s)
				} else {
					s = slots[len(slots)-1]
				}
				if tc.ID != "" {
					s.id = tc.ID
				}
				if tc.Function.Name != "" {
					s.name += tc.Function.Name
				}
				s.args.WriteString(tc.Function.Arguments)
			}
			if ch.FinishReason != nil && *ch.FinishReason != "" && resp.StopReason != StopRefusal {
				resp.StopReason = normOpenAIStop(*ch.FinishReason)
			}
		}
		return true
	})
	if streamErr != nil {
		return nil, streamErr
	}
	if err != nil {
		return nil, err
	}
	resp.Text = text.String()
	for _, s := range slots {
		if s.name == "" {
			continue
		}
		resp.ToolCalls = append(resp.ToolCalls, normOAToolCall(s.id, s.name, s.args.String()))
	}
	if len(resp.ToolCalls) > 0 && resp.StopReason == StopEnd {
		resp.StopReason = StopToolUse
	}
	resp.Message = Message{Role: "assistant", Content: resp.Text, ToolCalls: resp.ToolCalls}
	return resp, nil
}
