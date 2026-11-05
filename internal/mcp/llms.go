package mcp

import (
	"net/http"

	"github.com/kucukkanat/bunker/internal/core"
)

func registerDocs(a *core.App) {
	a.Public("/llms.txt")
	a.Public("/llms-full.txt")
	serve := func(full bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Write([]byte(AgentGuide(BaseURL(r), a.Ops(), full)))
		}
	}
	a.Mux.HandleFunc("GET /llms.txt", serve(false))
	a.Mux.HandleFunc("GET /llms-full.txt", serve(true))
	a.Handle(core.Op{Name: "docs.agent_guide", Method: "GET", Path: "/api/v1/agent-guide", Summary: "Documentation for AI agents (auth, deploy, sandbox, SDK, operations)", Access: core.AccessPublic, Hidden: true,
		Fn: func(r *http.Request) (any, error) {
			base := BaseURL(r)
			return map[string]any{
				"server":       base,
				"intro":        Intro,
				"auth":         AuthGuide(base),
				"deploy":       DeployGuide(base),
				"sandbox":      SandboxRules,
				"sdk":          SDKReference,
				"markdown":     AgentGuide(base, a.Ops(), true),
				"mcp":          base + "/mcp",
				"ops":          base + "/api/v1/ops",
				"openapi":      base + "/api/v1/openapi.json",
				"instructions": Instructions,
			}, nil
		}})
}
