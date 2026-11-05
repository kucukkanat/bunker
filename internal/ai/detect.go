package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

// Local model servers Bunker can use with one click (OpenAI-compatible APIs).
var localServers = []struct{ Name, Endpoint string }{
	{"Ollama", "http://127.0.0.1:11434/v1"},
	{"LM Studio", "http://127.0.0.1:1234/v1"},
}

func registerDetect(a *core.App) {
	a.Handle(core.Op{Name: "ai.detect", Method: "GET", Path: "/api/v1/admin/ai/detect", Summary: "Find AI options that need no setup (API keys in the environment, local model servers)", Access: core.AccessAdmin,
		Fn: func(r *http.Request) (any, error) {
			out := []map[string]any{}
			if os.Getenv("ANTHROPIC_API_KEY") != "" {
				out = append(out, map[string]any{"name": "Claude (ANTHROPIC_API_KEY)", "provider": ProviderAnthropic, "inUse": currentConfig().Provider == ProviderAnthropic})
			}
			if os.Getenv("OPENAI_API_KEY") != "" {
				out = append(out, map[string]any{"name": "OpenAI (OPENAI_API_KEY)", "provider": ProviderOpenAI})
			}
			ctx, cancel := context.WithTimeout(r.Context(), 1500*time.Millisecond)
			defer cancel()
			for _, s := range localServers {
				req, _ := http.NewRequestWithContext(ctx, "GET", s.Endpoint+"/models", nil)
				res, err := http.DefaultClient.Do(req)
				if err != nil {
					continue
				}
				var body struct {
					Data []struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				json.NewDecoder(res.Body).Decode(&body)
				res.Body.Close()
				if res.StatusCode != 200 || len(body.Data) == 0 {
					continue
				}
				models := []string{}
				for _, m := range body.Data {
					models = append(models, m.ID)
				}
				out = append(out, map[string]any{"name": s.Name + " on this machine", "provider": ProviderCompatible, "endpoint": s.Endpoint, "models": models})
			}
			return map[string]any{"options": out}, nil
		}})
}
