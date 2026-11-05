package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeFnServer answers the server-function and secrets ops the way the
// contract describes, and records what the CLI sent.
func fakeFnServer(t *testing.T) (*httptest.Server, *sync.Map) {
	t.Helper()
	got := &sync.Map{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.Store(r.Method+" "+r.URL.EscapedPath(), string(b))
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.EscapedPath() {
		case "GET /api/v1/sites/lunch/functions":
			w.Write([]byte(`{"enabled":true,"blocked":null,"functions":[{"name":"orders","path":"api/orders.js","elevated":false,"schedule":"","onWrite":[]},{"name":"digest","path":"api/digest.ts","elevated":true,"schedule":"0 8 * * 1","onWrite":[]}],"limits":{}}`))
		case "POST /api/v1/sites/lunch/functions/orders", "POST /api/v1/sites/lunch/functions/orders/refund":
			w.Write([]byte(`{"output":{"id":"d1"},"durationMs":7}`))
		case "POST /api/v1/sites/lunch/functions/boom":
			w.WriteHeader(500)
			w.Write([]byte(`{"error":{"code":"function_error","message":"x is not defined","stack":"at default (api/boom.js:3)"}}`))
		case "GET /api/v1/sites/lunch/functions/_runs":
			got.Store("runs-query", r.URL.RawQuery)
			w.Write([]byte(`{"items":[{"id":2,"name":"orders","trigger":{"type":"call"},"user":{"email":"ada@corp.com"},"status":"ok","durationMs":5,"logs":"saved d1\n","at":1760000000000}]}`))
		case "GET /api/v1/sites/lunch/events":
			got.Store("events-query", r.URL.RawQuery)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("retry: 3000\n: connected\n\n"))
			w.Write([]byte(`data: {"ch":"logs","data":{"run":3,"name":"orders","level":"info","line":"hello from fn","at":1760000000000}}` + "\n\n"))
			w.Write([]byte(`data: {"ch":"logs","data":{"run":4,"name":"other","level":"info","line":"skip me","at":1760000000000}}` + "\n\n"))
			w.Write([]byte("event: bye\ndata: {}\n\n"))
		case "GET /api/v1/sites/lunch/secrets":
			w.Write([]byte(`{"items":[{"name":"SLACK_WEBHOOK","updatedAt":1760000000,"updatedBy":"ada@corp.com"}]}`))
		case "PUT /api/v1/sites/lunch/secrets/SLACK_WEBHOOK", "DELETE /api/v1/sites/lunch/secrets/SLACK_WEBHOOK":
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"error":{"code":"not_found","message":"nope"}}`))
		}
	}))
	t.Cleanup(ts.Close)
	return ts, got
}

func TestFnCommands(t *testing.T) {
	ts, got := fakeFnServer(t)
	env := map[string]string{"BUNKER_CONFIG": filepath.Join(t.TempDir(), "c.json"), "BUNKER_SERVER": ts.URL, "BUNKER_TOKEN": "bk_test"}
	t.Chdir(t.TempDir())

	out, e, code := run(t, env, "", "fn", "ls", "lunch")
	if code != 0 || !strings.Contains(out, "orders") || !strings.Contains(out, "0 8 * * 1") || !strings.Contains(out, "owner") {
		t.Fatalf("fn ls: %d %s %s", code, out, e)
	}

	out, e, code = run(t, env, "", "fn", "call", "lunch", "orders", `{"meal":"Pasta"}`, "--draft")
	if code != 0 || !strings.Contains(out, `"id": "d1"`) {
		t.Fatalf("fn call: %d %s %s", code, out, e)
	}
	v, _ := got.Load("POST /api/v1/sites/lunch/functions/orders")
	var body struct {
		Input map[string]any `json:"input"`
		Draft bool           `json:"draft"`
	}
	json.Unmarshal([]byte(v.(string)), &body)
	if body.Input["meal"] != "Pasta" || !body.Draft {
		t.Fatalf("call body: %v", v)
	}

	// Stdin input, nested name, file extension stripped.
	if out, e, code := run(t, env, `{"a":1}`, "fn", "call", "lunch", "api/orders/refund.js", "-", "--json"); code != 0 || !strings.Contains(out, `"durationMs":7`) {
		t.Fatalf("fn call stdin: %d %s %s", code, out, e)
	}
	v, _ = got.Load("POST /api/v1/sites/lunch/functions/orders/refund")
	if !strings.Contains(v.(string), `"input":{"a":1}`) {
		t.Fatalf("stdin body: %v", v)
	}

	// Function errors keep the stack for editors.
	if _, e, code := run(t, env, "", "fn", "call", "lunch", "boom"); code != 1 || !strings.Contains(e, "is not defined") || !strings.Contains(e, "api/boom.js:3") {
		t.Fatalf("fn error: %d %s", code, e)
	}

	out, _, code = run(t, env, "", "fn", "logs", "lunch", "--name", "orders")
	if code != 0 || !strings.Contains(out, "saved d1") {
		t.Fatalf("fn logs: %s", out)
	}
	if q, _ := got.Load("runs-query"); !strings.Contains(q.(string), "name=orders") {
		t.Fatalf("runs query: %v", q)
	}

	out, e, code = run(t, env, "", "fn", "logs", "lunch", "--follow", "--name", "orders")
	if code != 0 || !strings.Contains(out, "hello from fn") || strings.Contains(out, "skip me") {
		t.Fatalf("fn logs --follow: %d %s %s", code, out, e)
	}
	if q, _ := got.Load("events-query"); q.(string) != "ch=logs" {
		t.Fatalf("events query: %v", q)
	}
}

func TestSecretsCommands(t *testing.T) {
	ts, got := fakeFnServer(t)
	env := map[string]string{"BUNKER_CONFIG": filepath.Join(t.TempDir(), "c.json"), "BUNKER_SERVER": ts.URL, "BUNKER_TOKEN": "bk_test"}
	t.Chdir(t.TempDir())

	if out, e, code := run(t, env, "", "secrets", "ls", "lunch"); code != 0 || !strings.Contains(out, "SLACK_WEBHOOK") {
		t.Fatalf("secrets ls: %d %s %s", code, out, e)
	}
	// The value comes from stdin, never argv.
	if _, e, code := run(t, env, "https://hooks.example/abc\n", "secrets", "set", "lunch", "SLACK_WEBHOOK"); code != 0 {
		t.Fatalf("secrets set: %s", e)
	}
	v, _ := got.Load("PUT /api/v1/sites/lunch/secrets/SLACK_WEBHOOK")
	if v.(string) != `{"value":"https://hooks.example/abc"}` {
		t.Fatalf("set body: %v", v)
	}
	if _, e, code := run(t, env, "", "secrets", "set", "lunch", "bad-name"); code != 2 || !strings.Contains(e, "capital letters") {
		t.Fatalf("bad name: %d %s", code, e)
	}
	if _, e, code := run(t, env, "", "secrets", "set", "lunch", "EMPTY"); code != 2 || !strings.Contains(e, "empty") {
		t.Fatalf("empty value: %d %s", code, e)
	}
	if _, e, code := run(t, env, "", "secrets", "rm", "lunch", "SLACK_WEBHOOK", "--yes"); code != 0 {
		t.Fatalf("secrets rm: %s", e)
	}
	if _, ok := got.Load("DELETE /api/v1/sites/lunch/secrets/SLACK_WEBHOOK"); !ok {
		t.Fatal("rm did not call the API")
	}
}

func TestDeployPrintsFunctions(t *testing.T) {
	var x App
	var errb strings.Builder
	x.Stderr = &errb
	x.printDeployedFunctions(&deployResult{Functions: []fnInfo{{Name: "orders", Path: "api/orders.js"}, {Name: "nightly", Elevated: true, Schedule: "0 2 * * *"}, {Name: "bad", Error: "SyntaxError"}}})
	s := errb.String()
	for _, want := range []string{"3 server functions", "orders", "api/orders.js", "runs as the owner", "schedule 0 2 * * *", "SyntaxError", "won't run"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in %s", want, s)
		}
	}
	errb.Reset()
	x.printDeployedFunctions(&deployResult{})
	if errb.Len() != 0 {
		t.Fatalf("no functions should print nothing: %q", errb.String())
	}
}
