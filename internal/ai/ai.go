// Package ai is Bunker's AI layer: pluggable model providers (Anthropic,
// OpenAI, Azure OpenAI, OpenAI-compatible servers), per-user/per-site
// daily token quotas, the page-facing bunker.ai API, and the AI builder
// agent that edits a site's draft through tools.
package ai

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

func init() {
	core.Register(core.Module{
		Name:  "ai",
		Order: 60,
		Migrations: []string{`
CREATE TABLE ai_usage (
  day TEXT NOT NULL,
  user_id INTEGER NOT NULL,
  site_id INTEGER NOT NULL DEFAULT 0,
  kind TEXT NOT NULL,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  billable_tokens INTEGER NOT NULL DEFAULT 0,
  calls INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(day, user_id, site_id, kind)
);
CREATE INDEX ai_usage_site ON ai_usage(site_id, day);
CREATE INDEX ai_usage_user ON ai_usage(user_id, day);
CREATE TABLE builder_messages (
  id INTEGER PRIMARY KEY,
  site_id INTEGER NOT NULL,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  user_id INTEGER,
  created_at INTEGER NOT NULL
);
CREATE INDEX builder_messages_site ON builder_messages(site_id, id);
`},
		Init: initAI,
	})
}

var app *core.App

func initAI(a *core.App) error {
	app = a
	registerAdminOps(a)
	registerChatOps(a)
	registerBuilderOps(a)
	a.On("site.purge", func(ctx context.Context, payload any) {
		if id, ok := payload.(int64); ok {
			a.DB.ExecContext(ctx, `DELETE FROM builder_messages WHERE site_id=?`, id)
		}
	})
	return nil
}

// ---- configuration ----

// Provider kinds.
const (
	ProviderNone       = "none"
	ProviderAnthropic  = "anthropic"
	ProviderOpenAI     = "openai"
	ProviderAzure      = "azure-openai"
	ProviderCompatible = "openai-compatible"
)

var providerLabels = map[string]string{
	ProviderNone:       "Off",
	ProviderAnthropic:  "Claude (Anthropic)",
	ProviderOpenAI:     "OpenAI",
	ProviderAzure:      "Azure OpenAI",
	ProviderCompatible: "OpenAI-compatible (Ollama, LM Studio, vLLM)",
}

type modelDefaults struct{ Builder, Page, Endpoint, APIVersion string }

var defaultsFor = map[string]modelDefaults{
	ProviderAnthropic:  {Builder: "claude-sonnet-5-5", Page: "claude-haiku-5-5", Endpoint: "https://api.anthropic.com", APIVersion: "2023-06-01"},
	ProviderOpenAI:     {Builder: "gpt-5", Page: "gpt-5-mini", Endpoint: "https://api.openai.com/v1"},
	ProviderAzure:      {APIVersion: "2024-10-21"},
	ProviderCompatible: {Endpoint: "http://localhost:11434/v1"},
}

// Default quotas (tokens). 0 means unlimited.
const (
	defUserDaily  = 2_000_000
	defSiteDaily  = 5_000_000
	defMaxRequest = 16_000
)

// Config is the effective AI configuration.
type Config struct {
	Provider         string
	APIKey           string
	Endpoint         string
	APIVersion       string
	BuilderModel     string
	PageModel        string
	UserDailyTokens  int64
	SiteDailyTokens  int64
	MaxRequestTokens int64
	LogContent       bool
}

// Setting keys (env overrides: BUNKER_AI_PROVIDER, BUNKER_AI_API_KEY, ...).
const (
	kProvider   = "ai.provider"
	kAPIKey     = "ai.api_key"
	kEndpoint   = "ai.endpoint"
	kAPIVersion = "ai.api_version"
	kBuilder    = "ai.builder_model"
	kPage       = "ai.page_model"
	kUserDaily  = "ai.user_daily_tokens"
	kSiteDaily  = "ai.site_daily_tokens"
	kMaxRequest = "ai.max_request_tokens"
	kLogContent = "ai.log_content"
)

var allKeys = []string{kProvider, kAPIKey, kEndpoint, kAPIVersion, kBuilder, kPage, kUserDaily, kSiteDaily, kMaxRequest, kLogContent}

var cfgCache struct {
	sync.Mutex
	c   *Config
	exp time.Time
}

func invalidateConfig() {
	cfgCache.Lock()
	cfgCache.c = nil
	cfgCache.Unlock()
}

// currentConfig returns the effective configuration (cached for 10s).
func currentConfig() *Config {
	cfgCache.Lock()
	defer cfgCache.Unlock()
	if cfgCache.c != nil && time.Now().Before(cfgCache.exp) {
		return cfgCache.c
	}
	c := loadConfig()
	cfgCache.c, cfgCache.exp = c, time.Now().Add(10*time.Second)
	return c
}

func loadConfig() *Config {
	c := &Config{
		Provider:     strings.ToLower(strings.TrimSpace(app.Setting(kProvider))),
		APIKey:       strings.TrimSpace(app.SecretSetting(kAPIKey)),
		Endpoint:     strings.TrimSpace(app.Setting(kEndpoint)),
		APIVersion:   strings.TrimSpace(app.Setting(kAPIVersion)),
		BuilderModel: strings.TrimSpace(app.Setting(kBuilder)),
		PageModel:    strings.TrimSpace(app.Setting(kPage)),
	}
	c.UserDailyTokens = intSetting(kUserDaily, defUserDaily)
	c.SiteDailyTokens = intSetting(kSiteDaily, defSiteDaily)
	c.MaxRequestTokens = intSetting(kMaxRequest, defMaxRequest)
	c.LogContent, _ = strconv.ParseBool(app.Setting(kLogContent))
	if c.Provider == "" && c.APIKey != "" {
		// A key alone is enough when its shape gives the provider away.
		switch {
		case strings.HasPrefix(c.APIKey, "sk-ant-"):
			c.Provider = ProviderAnthropic
		case strings.HasPrefix(c.APIKey, "sk-"):
			c.Provider = ProviderOpenAI
		}
	}
	if c.Provider == "" {
		c.Provider = ProviderNone
	}
	c.applyDefaults()
	return c
}

func (c *Config) applyDefaults() {
	d := defaultsFor[c.Provider]
	if c.BuilderModel == "" {
		c.BuilderModel = d.Builder
	}
	if c.PageModel == "" {
		c.PageModel = d.Page
	}
	if c.PageModel == "" {
		c.PageModel = c.BuilderModel
	}
	if c.BuilderModel == "" {
		c.BuilderModel = c.PageModel
	}
	if c.Endpoint == "" {
		c.Endpoint = d.Endpoint
	}
	if c.APIVersion == "" {
		c.APIVersion = d.APIVersion
	}
}

func intSetting(key string, def int64) int64 {
	v := strings.TrimSpace(app.Setting(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(v, "_", ""), 10, 64)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// Problem returns why the config can't be used ("" when it's ready).
func (c *Config) Problem() string {
	switch c.Provider {
	case ProviderNone:
		return "AI is turned off."
	case ProviderAnthropic, ProviderOpenAI:
		if c.APIKey == "" {
			return "Add an API key."
		}
	case ProviderAzure:
		if c.Endpoint == "" {
			return "Add your Azure OpenAI endpoint (https://<resource>.openai.azure.com)."
		}
		if c.APIKey == "" {
			return "Add an API key."
		}
		if c.BuilderModel == "" {
			return "Add the deployment names to use."
		}
	case ProviderCompatible:
		if c.Endpoint == "" {
			return "Add the server address (for Ollama: http://localhost:11434/v1)."
		}
		if c.BuilderModel == "" {
			return "Add the model name to use (for example llama3.1)."
		}
	default:
		return "Unknown provider " + c.Provider + "."
	}
	return ""
}

// Configured reports whether AI is ready to use.
func (c *Config) Configured() bool { return c.Problem() == "" }

// newProvider builds the client for c.
func newProvider(c *Config) Provider {
	switch c.Provider {
	case ProviderAnthropic:
		return &anthropicProvider{endpoint: c.Endpoint, key: c.APIKey, version: c.APIVersion}
	case ProviderOpenAI, ProviderAzure, ProviderCompatible:
		return &openaiProvider{flavor: c.Provider, endpoint: c.Endpoint, key: c.APIKey, apiVersion: c.APIVersion}
	}
	return nil
}

// errNotConfigured is returned by every AI entry point when AI is off.
func errNotConfigured() error {
	return core.Unavailable("AI isn't set up yet. An admin can add it in Admin → AI.")
}

// ready returns the config and provider, or a friendly 503.
func ready() (*Config, Provider, error) {
	c := currentConfig()
	if !c.Configured() {
		return c, nil, errNotConfigured()
	}
	return c, newProvider(c), nil
}

// friendly turns provider/transport errors into plain-English API errors.
func friendly(err error) error {
	if err == nil {
		return nil
	}
	var he *core.HTTPError
	if errors.As(err, &he) {
		return he
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return core.NewErr(504, "timeout", "The AI took too long to answer. Try again, or ask for something smaller.")
	}
	if errors.Is(err, context.Canceled) {
		return core.NewErr(499, "canceled", "The request was canceled.")
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		switch {
		case pe.Status == 401 || pe.Status == 403:
			return core.Unavailable("The AI service didn't accept Bunker's key. An admin should check Admin → AI.")
		case pe.Status == 404:
			return core.Unavailable("The AI service doesn't know the configured model. An admin should check the model names in Admin → AI.")
		case pe.Status == 429:
			return core.NewErr(503, "ai_busy", "The AI service is busy right now. Try again in a minute.")
		case pe.Status >= 500:
			return core.NewErr(503, "ai_unavailable", "The AI service is having trouble right now. Try again shortly.")
		default:
			msg := pe.Message
			if len(msg) > 300 {
				msg = msg[:300]
			}
			return core.BadRequest("The AI service didn't accept the request: " + msg)
		}
	}
	return core.NewErr(503, "ai_unavailable", "Bunker couldn't reach the AI service. Try again shortly.")
}

// ---- admin ops ----

func maskKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 10 {
		return "••••"
	}
	return k[:4] + "…" + k[len(k)-4:]
}

func configView(c *Config) map[string]any {
	pinned := []string{}
	for _, k := range allKeys {
		if app.SettingFromEnv(k) {
			pinned = append(pinned, k)
		}
	}
	type pinfo struct {
		ID           string `json:"id"`
		Label        string `json:"label"`
		BuilderModel string `json:"builderModel,omitempty"`
		PageModel    string `json:"pageModel,omitempty"`
		Endpoint     string `json:"endpoint,omitempty"`
		APIVersion   string `json:"apiVersion,omitempty"`
		NeedsKey     bool   `json:"needsKey"`
	}
	var providers []pinfo
	for _, id := range []string{ProviderAnthropic, ProviderOpenAI, ProviderAzure, ProviderCompatible, ProviderNone} {
		d := defaultsFor[id]
		providers = append(providers, pinfo{id, providerLabels[id], d.Builder, d.Page, d.Endpoint, d.APIVersion, id != ProviderCompatible && id != ProviderNone})
	}
	return map[string]any{
		"provider":      c.Provider,
		"providerLabel": providerLabels[c.Provider],
		"configured":    c.Configured(),
		"problem":       c.Problem(),
		"apiKey":        maskKey(c.APIKey),
		"apiKeySet":     c.APIKey != "",
		"endpoint":      c.Endpoint,
		"apiVersion":    c.APIVersion,
		"builderModel":  c.BuilderModel,
		"pageModel":     c.PageModel,
		"quotas": map[string]any{
			"userDailyTokens":  c.UserDailyTokens,
			"siteDailyTokens":  c.SiteDailyTokens,
			"maxRequestTokens": c.MaxRequestTokens,
		},
		"logContent":  c.LogContent,
		"pinnedByEnv": pinned,
		"providers":   providers,
	}
}

type configInput struct {
	Provider         *string `json:"provider"`
	APIKey           *string `json:"apiKey"`
	Endpoint         *string `json:"endpoint"`
	APIVersion       *string `json:"apiVersion"`
	BuilderModel     *string `json:"builderModel"`
	PageModel        *string `json:"pageModel"`
	UserDailyTokens  *int64  `json:"userDailyTokens"`
	SiteDailyTokens  *int64  `json:"siteDailyTokens"`
	MaxRequestTokens *int64  `json:"maxRequestTokens"`
	LogContent       *bool   `json:"logContent"`
	Quotas           *struct {
		UserDailyTokens  *int64 `json:"userDailyTokens"`
		SiteDailyTokens  *int64 `json:"siteDailyTokens"`
		MaxRequestTokens *int64 `json:"maxRequestTokens"`
	} `json:"quotas"`
}

func (in *configInput) flatten() {
	if q := in.Quotas; q != nil {
		if q.UserDailyTokens != nil {
			in.UserDailyTokens = q.UserDailyTokens
		}
		if q.SiteDailyTokens != nil {
			in.SiteDailyTokens = q.SiteDailyTokens
		}
		if q.MaxRequestTokens != nil {
			in.MaxRequestTokens = q.MaxRequestTokens
		}
	}
}

// keyIsMask reports whether the client echoed back the masked key.
func keyIsMask(s string) bool { return strings.Contains(s, "…") || strings.Contains(s, "••") }

func (in *configInput) validate() error {
	if in.Provider != nil {
		p := strings.ToLower(strings.TrimSpace(*in.Provider))
		if p == "" {
			p = ProviderNone
		}
		if _, ok := providerLabels[p]; !ok {
			return core.BadRequest("provider must be one of anthropic, openai, azure-openai, openai-compatible or none")
		}
		*in.Provider = p
	}
	if in.Endpoint != nil {
		e := strings.TrimSpace(*in.Endpoint)
		if e != "" && !strings.HasPrefix(e, "https://") && !strings.HasPrefix(e, "http://") {
			return core.BadRequest("the endpoint must start with https:// (or http:// for a server on your network)")
		}
		*in.Endpoint = e
	}
	for _, n := range []*int64{in.UserDailyTokens, in.SiteDailyTokens, in.MaxRequestTokens} {
		if n != nil && *n < 0 {
			return core.BadRequest("limits can't be negative (use 0 for no limit)")
		}
	}
	return nil
}

// overlay applies in to a copy of c without saving (used by the test op).
func (in *configInput) overlay(c *Config) *Config {
	cp := *c
	providerChanged := in.Provider != nil && *in.Provider != c.Provider
	if providerChanged {
		cp.Provider = *in.Provider
		// Drop the previous provider's defaults.
		d := defaultsFor[c.Provider]
		if cp.BuilderModel == d.Builder {
			cp.BuilderModel = ""
		}
		if cp.PageModel == d.Page || cp.PageModel == d.Builder {
			cp.PageModel = ""
		}
		if cp.Endpoint == d.Endpoint {
			cp.Endpoint = ""
		}
		if cp.APIVersion == d.APIVersion {
			cp.APIVersion = ""
		}
		cp.APIKey = ""
	}
	if in.APIKey != nil && !keyIsMask(*in.APIKey) {
		cp.APIKey = strings.TrimSpace(*in.APIKey)
	}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	set(&cp.Endpoint, in.Endpoint)
	set(&cp.APIVersion, in.APIVersion)
	set(&cp.BuilderModel, in.BuilderModel)
	set(&cp.PageModel, in.PageModel)
	cp.applyDefaults()
	return &cp
}

func (in *configInput) save(ctx context.Context) ([]string, error) {
	var changed []string
	put := func(key string, v *string) error {
		if v == nil {
			return nil
		}
		changed = append(changed, key)
		return app.SetSetting(ctx, key, strings.TrimSpace(*v))
	}
	putInt := func(key string, v *int64) error {
		if v == nil {
			return nil
		}
		changed = append(changed, key)
		return app.SetSetting(ctx, key, strconv.FormatInt(*v, 10))
	}
	if err := put(kProvider, in.Provider); err != nil {
		return nil, err
	}
	if in.APIKey != nil && !keyIsMask(*in.APIKey) {
		changed = append(changed, kAPIKey)
		if err := app.SetSecretSetting(kAPIKey, strings.TrimSpace(*in.APIKey)); err != nil {
			return nil, err
		}
	}
	for _, kv := range []struct {
		k string
		v *string
	}{{kEndpoint, in.Endpoint}, {kAPIVersion, in.APIVersion}, {kBuilder, in.BuilderModel}, {kPage, in.PageModel}} {
		if err := put(kv.k, kv.v); err != nil {
			return nil, err
		}
	}
	for _, kv := range []struct {
		k string
		v *int64
	}{{kUserDaily, in.UserDailyTokens}, {kSiteDaily, in.SiteDailyTokens}, {kMaxRequest, in.MaxRequestTokens}} {
		if err := putInt(kv.k, kv.v); err != nil {
			return nil, err
		}
	}
	if in.LogContent != nil {
		changed = append(changed, kLogContent)
		if err := app.SetSetting(ctx, kLogContent, strconv.FormatBool(*in.LogContent)); err != nil {
			return nil, err
		}
	}
	invalidateConfig()
	return changed, nil
}

func registerAdminOps(a *core.App) {
	a.Handle(core.Op{Name: "ai.config.get", Method: "GET", Path: "/api/v1/admin/ai", Summary: "Show the AI provider settings (the API key is masked)", Access: core.AccessAdmin,
		Fn: func(r *http.Request) (any, error) { return configView(currentConfig()), nil }})

	a.Handle(core.Op{Name: "ai.config.update", Method: "PUT", Path: "/api/v1/admin/ai", Summary: "Change the AI provider, key, models and limits", Access: core.AccessAdmin,
		Params: []core.Param{
			{Name: "provider", In: "body", Enum: []string{ProviderAnthropic, ProviderOpenAI, ProviderAzure, ProviderCompatible, ProviderNone}, Desc: "Which AI service to use"},
			{Name: "apiKey", In: "body", Desc: "API key (stored encrypted, never shown again; empty string removes it)"},
			{Name: "endpoint", In: "body", Desc: "Base URL (Azure resource URL, or the address of an Ollama/LM Studio/vLLM server)"},
			{Name: "apiVersion", In: "body", Desc: "API version (Azure api-version, or anthropic-version)"},
			{Name: "builderModel", In: "body", Desc: "Model (Azure: deployment) used by the AI builder"},
			{Name: "pageModel", In: "body", Desc: "Model (Azure: deployment) used by bunker.ai in pages"},
			{Name: "userDailyTokens", In: "body", Type: "integer", Desc: "Tokens per person per day (0 = no limit)"},
			{Name: "siteDailyTokens", In: "body", Type: "integer", Desc: "Tokens per site per day (0 = no limit)"},
			{Name: "maxRequestTokens", In: "body", Type: "integer", Desc: "Largest single page request in tokens (0 = no limit)"},
			{Name: "logContent", In: "body", Type: "boolean", Desc: "Keep prompts and answers in the audit log"},
		},
		Fn: func(r *http.Request) (any, error) {
			var in configInput
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			in.flatten()
			if err := in.validate(); err != nil {
				return nil, err
			}
			if in.Provider != nil && *in.Provider != currentConfig().Provider {
				// Switching providers: clear models/endpoint/version not sent so
				// the new provider's defaults apply instead of stale values.
				empty := ""
				if in.BuilderModel == nil {
					in.BuilderModel = &empty
				}
				if in.PageModel == nil {
					in.PageModel = &empty
				}
				if in.Endpoint == nil {
					in.Endpoint = &empty
				}
				if in.APIVersion == nil {
					in.APIVersion = &empty
				}
				if in.APIKey == nil || keyIsMask(*in.APIKey) {
					// Never send one provider's key to another.
					in.APIKey = &empty
				}
			}
			changed, err := in.save(r.Context())
			if err != nil {
				return nil, err
			}
			c := currentConfig()
			a.Audit(r.Context(), "ai.configure", 0, c.Provider, map[string]any{"changed": changed, "provider": c.Provider, "builderModel": c.BuilderModel, "pageModel": c.PageModel})
			return configView(c), nil
		}})

	a.Handle(core.Op{Name: "ai.config.test", Method: "POST", Path: "/api/v1/admin/ai/test", Summary: "Send a tiny prompt to check the AI settings work (accepts unsaved settings)", Access: core.AccessAdmin,
		Params: []core.Param{
			{Name: "provider", In: "body", Desc: "Test these settings instead of the saved ones (same fields as ai.config.update)"},
			{Name: "model", In: "body", Desc: "Which model to try: builder (default) or page", Enum: []string{"builder", "page"}},
		},
		Fn: func(r *http.Request) (any, error) {
			var in struct {
				configInput
				Model string `json:"model"`
			}
			if r.ContentLength != 0 {
				_ = core.Decode(r, &in)
			}
			in.flatten()
			if err := in.validate(); err != nil {
				return nil, err
			}
			c := in.overlay(currentConfig())
			model := c.BuilderModel
			if in.Model == "page" {
				model = c.PageModel
			}
			out := map[string]any{"provider": c.Provider, "model": model}
			if p := c.Problem(); p != "" {
				out["ok"], out["error"] = false, p
				return out, nil
			}
			ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
			defer cancel()
			start := time.Now()
			resp, err := newProvider(c).Complete(ctx, &Request{Model: model, MaxTokens: 256, Effort: "low",
				Messages: []Message{{Role: "user", Content: "Reply with the single word: ok"}}}, nil)
			out["latencyMs"] = time.Since(start).Milliseconds()
			if err != nil {
				out["ok"] = false
				var he *core.HTTPError
				if errors.As(friendly(err), &he) {
					out["error"] = he.Message
				}
				var pe *ProviderError
				if errors.As(err, &pe) {
					out["status"], out["detail"] = pe.Status, pe.Message
				}
				a.Audit(r.Context(), "ai.test", 0, c.Provider, map[string]any{"ok": false, "model": model})
				return out, nil
			}
			reply := strings.TrimSpace(resp.Text)
			if len(reply) > 200 {
				reply = reply[:200]
			}
			out["ok"], out["reply"], out["usage"] = true, reply, resp.Usage
			if resp.Model != "" {
				out["servedModel"] = resp.Model
			}
			a.Audit(r.Context(), "ai.test", 0, c.Provider, map[string]any{"ok": true, "model": model, "ms": out["latencyMs"]})
			return out, nil
		}})

	a.Handle(core.Op{Name: "ai.usage", Method: "GET", Path: "/api/v1/admin/ai/usage", Summary: "AI token use per person and per site",
		Access: core.AccessAdmin,
		Params: []core.Param{{Name: "days", In: "query", Type: "integer", Desc: "How many days back (default 30, max 366)"}},
		Fn: func(r *http.Request) (any, error) {
			days, _ := strconv.Atoi(r.URL.Query().Get("days"))
			if days <= 0 {
				days = 30
			}
			if days > 366 {
				days = 366
			}
			return usageReport(r.Context(), days)
		}})

	a.Handle(core.Op{Name: "ai.status", Method: "GET", Path: "/api/v1/ai/status", Summary: "Is AI available, which models, and how much of today's allowance you have left",
		Fn: func(r *http.Request) (any, error) {
			c := currentConfig()
			u := core.MustUser(r)
			out := map[string]any{
				"configured":    c.Configured(),
				"provider":      c.Provider,
				"providerLabel": providerLabels[c.Provider],
			}
			if c.Configured() {
				out["builderModel"], out["pageModel"] = c.BuilderModel, c.PageModel
				out["budget"] = userBudget(r.Context(), u.ID)
			} else if u.IsAdmin {
				out["problem"] = c.Problem()
			}
			return out, nil
		}})
}
