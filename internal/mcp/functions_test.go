package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/core"
)

var fnOps = []core.Op{
	{Name: "fn.list", Method: "GET", Path: "/api/v1/sites/{site}/functions", Summary: "List server functions", Params: []core.Param{{Name: "site", In: "path", Required: true}}},
	{Name: "fn.call", Method: "POST", Path: "/api/v1/sites/{site}/functions/{name...}", Summary: "Call a server function", Body: "json", PageSafe: true,
		Params: []core.Param{{Name: "site", In: "path", Required: true}, {Name: "name", In: "path", Required: true}, {Name: "draft", In: "body", Type: "boolean"}}},
	{Name: "secrets.set", Method: "PUT", Path: "/api/v1/sites/{site}/secrets/{name}", Summary: "Set a secret", Body: "json",
		Params: []core.Param{{Name: "site", In: "path", Required: true}, {Name: "name", In: "path", Required: true}, {Name: "value", In: "body", Required: true}}},
}

func TestFunctionTools(t *testing.T) {
	be := &fakeBackend{ops: fnOps}
	s := &Server{Backend: be}
	rpc(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	res := rpc(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	b, _ := json.Marshal(res)
	for _, want := range []string{`"fn_call"`, `"fn_list"`, `"secrets_set"`, "api/orders/refund.ts", "never invent one", "server functions (api/*.js"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("tools/list misses %q", want)
		}
	}
	// Nested names keep their slashes; input passes through as JSON.
	rpc(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fn_call","arguments":{"site":"lunch","name":"orders/refund","input":{"id":"d1"},"draft":true}}}`)
	r := be.reqs[len(be.reqs)-1]
	if r.Method != "POST" || r.Path != "/api/v1/sites/lunch/functions/orders/refund" || !strings.Contains(string(r.Body), `"input":{"id":"d1"}`) || !strings.Contains(string(r.Body), `"draft":true`) {
		t.Fatalf("fn_call request: %s %s %s", r.Method, r.Path, r.Body)
	}
	// The docs tool and resource serve the functions section.
	res = rpc(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_sdk_docs","arguments":{"topic":"functions"}}}`)
	b, _ = json.Marshal(res)
	if !strings.Contains(string(b), "bunker.secrets.get") || !strings.Contains(string(b), "export const config") {
		t.Fatalf("functions docs: %s", b)
	}
	res = rpc(t, s, `{"jsonrpc":"2.0","id":5,"method":"resources/read","params":{"uri":"bunker://docs/functions"}}`)
	b, _ = json.Marshal(res)
	if !strings.Contains(string(b), "config.onWrite") {
		t.Fatalf("functions resource: %s", b)
	}
}

func TestGuidesMentionFunctions(t *testing.T) {
	g := AgentGuide("https://b.test", fnOps, false)
	for _, want := range []string{"## Server functions", "bunker.fn(", "bunker secrets set", "`fn.call`"} {
		if !strings.Contains(g, want) {
			t.Fatalf("llms.txt misses %q", want)
		}
	}
	if strings.Contains(ServerFunctions, "¦") {
		t.Fatal("placeholder backticks left in ServerFunctions")
	}
	if !strings.Contains(AgentsMD("https://b.test", "lunch"), "## Server functions") {
		t.Fatal("AGENTS.md misses the server functions section")
	}
}
