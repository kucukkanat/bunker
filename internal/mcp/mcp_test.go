package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/kucukkanat/bunker/internal/core"
)

// fakeBackend records requests and answers from a script.
type fakeBackend struct {
	mu   sync.Mutex
	reqs []*Request
	ops  []core.Op
}

func (f *fakeBackend) BaseURL() string { return "https://bunker.test" }
func (f *fakeBackend) Ops(ctx context.Context) ([]core.Op, error) {
	return f.ops, nil
}
func (f *fakeBackend) Do(ctx context.Context, r *Request) (*Response, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	f.mu.Unlock()
	h := http.Header{"Content-Type": {"application/json"}}
	switch {
	case r.Path == "/api/v1/drop":
		return &Response{Status: 201, Header: h, Body: []byte(`{"slug":"hello","url":"https://bunker.test/s/hello/","version":1}`)}, nil
	case strings.HasSuffix(r.Path, "/builder"):
		return &Response{Status: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: []byte("event: status\ndata: {\"status\":\"Planning\"}\n\nevent: text\ndata: {\"text\":\"Added a button.\"}\n\nevent: file\ndata: {\"path\":\"index.html\"}\n\nevent: done\ndata: {\"ok\":true}\n\n")}, nil
	case r.Path == "/api/v1/sites/nope":
		return &Response{Status: 404, Header: h, Body: []byte(`{"error":{"code":"not_found","message":"site not found"}}`)}, nil
	}
	return &Response{Status: 200, Header: h, Body: []byte(`{"ok":true}`)}, nil
}

var testOps = []core.Op{
	{Name: "sites.list", Method: "GET", Path: "/api/v1/sites", Summary: "List sites", Params: []core.Param{{Name: "filter", In: "query", Enum: []string{"all", "mine"}}}},
	{Name: "sites.get", Method: "GET", Path: "/api/v1/sites/{site}", Summary: "Get", Params: []core.Param{{Name: "site", In: "path", Required: true}}},
	{Name: "sites.delete", Method: "DELETE", Path: "/api/v1/sites/{site}", Summary: "Delete", Destructive: true, Body: "none", Params: []core.Param{{Name: "site", In: "path", Required: true}}},
	{Name: "versions.restore", Method: "POST", Path: "/api/v1/sites/{site}/versions/{n}/restore", Summary: "Restore", Body: "json", Params: []core.Param{{Name: "site", In: "path"}, {Name: "n", In: "path", Type: "integer"}}},
	{Name: "files.write", Method: "PUT", Path: "/api/v1/sites/{site}/files/{path...}", Body: "raw", Params: []core.Param{{Name: "site", In: "path"}, {Name: "path", In: "path"}, {Name: "content", In: "body", Type: "file", Required: true}}},
	{Name: "sites.drop", Method: "POST", Path: "/api/v1/drop", Body: "multipart", Params: []core.Param{{Name: "files", In: "body", Type: "file", Required: true}, {Name: "title", In: "body"}}},
	{Name: "data.events", Method: "GET", Path: "/api/v1/sites/{site}/events", Stream: true},
	{Name: "users.list", Method: "GET", Path: "/api/v1/admin/users", Access: core.AccessAdmin},
}

func rpc(t *testing.T, s *Server, msg string) map[string]any {
	t.Helper()
	out := s.HandleRaw(context.Background(), []byte(msg))
	if out == nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("bad reply %s: %v", out, err)
	}
	return m
}

func TestInitializeNegotiation(t *testing.T) {
	s := &Server{Backend: &fakeBackend{ops: testOps}}
	for req, want := range map[string]string{"2025-06-18": "2025-06-18", "2025-03-26": "2025-03-26", "2025-11-25": "2025-11-25", "1999-01-01": VersionLatestLegacy, "": VersionLatestLegacy} {
		m := rpc(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+req+`","capabilities":{}}}`)
		res := m["result"].(map[string]any)
		if res["protocolVersion"] != want {
			t.Errorf("requested %q got %v want %s", req, res["protocolVersion"], want)
		}
		if res["capabilities"].(map[string]any)["tools"] == nil {
			t.Error("missing tools capability")
		}
	}
	if m := rpc(t, s, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); m != nil {
		t.Errorf("notification must not be answered: %v", m)
	}
	d := rpc(t, s, `{"jsonrpc":"2.0","id":"x","method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	if d["id"] != "x" || d["result"].(map[string]any)["supportedVersions"] == nil {
		t.Errorf("discover: %v", d)
	}
	bad := rpc(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2099-01-01"}}}`)
	if e := bad["error"].(map[string]any); e["code"].(float64) != CodeUnsupportedVer {
		t.Errorf("unsupported version: %v", bad)
	}
	if e := rpc(t, s, `{"jsonrpc":"2.0","id":3,"method":"nope"}`)["error"].(map[string]any); e["code"].(float64) != codeNoMethod {
		t.Errorf("unknown method: %v", e)
	}
	if e := rpc(t, s, `{not json`)["error"].(map[string]any); e["code"].(float64) != codeParse {
		t.Errorf("parse error: %v", e)
	}
}

func TestToolsListAndAnnotations(t *testing.T) {
	no := false
	s := &Server{Backend: &fakeBackend{ops: testOps}, IsAdmin: &no}
	m := rpc(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := m["result"].(map[string]any)["tools"].([]any)
	byName := map[string]map[string]any{}
	for _, x := range tools {
		tm := x.(map[string]any)
		byName[tm["name"].(string)] = tm
	}
	for _, n := range []string{"create_site_from_html", "deploy_files", "get_sdk_docs", "build_with_ai", "edit_file", "sites_list", "sites_delete", "files_write"} {
		if byName[n] == nil {
			t.Errorf("missing tool %s", n)
		}
	}
	if byName["data_events"] != nil {
		t.Error("event streams must not become tools")
	}
	if byName["users_list"] != nil {
		t.Error("admin tools hidden from non-admins")
	}
	if a := byName["sites_list"]["annotations"].(map[string]any); a["readOnlyHint"] != true {
		t.Error("GET should be readOnly")
	}
	if a := byName["sites_delete"]["annotations"].(map[string]any); a["destructiveHint"] != true {
		t.Error("delete should be destructive")
	}
	req := byName["sites_get"]["inputSchema"].(map[string]any)["required"].([]any)
	if len(req) != 1 || req[0] != "site" {
		t.Errorf("required: %v", req)
	}
	if ty := byName["sites_drop"]["inputSchema"].(map[string]any)["properties"].(map[string]any)["files"].(map[string]any)["type"]; ty != "object" {
		t.Errorf("multipart files should be an object, got %v", ty)
	}
}

func TestBuildRequest(t *testing.T) {
	r, err := BuildRequest(testOps[3], map[string]any{"site": "a b", "n": float64(3)})
	if err != nil || r.Method != "POST" || r.Path != "/api/v1/sites/a%20b/versions/3/restore" {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = BuildRequest(testOps[4], map[string]any{"site": "s", "path": "css/app.css", "content": "body{}"})
	if err != nil || r.Path != "/api/v1/sites/s/files/css/app.css" || string(r.Body) != "body{}" {
		t.Fatalf("raw: %+v %v", r, err)
	}
	r, _ = BuildRequest(testOps[0], map[string]any{"filter": "mine"})
	if r.Query.Get("filter") != "mine" || r.Body != nil {
		t.Fatalf("query: %+v", r)
	}
	r, _ = BuildRequest(testOps[5], map[string]any{"files": map[string]any{"index.html": "<h1>"}, "title": "T"})
	if r.Query.Get("title") != "T" || !strings.Contains(string(r.Body), `"index.html"`) {
		t.Fatalf("multipart as json: %+v %s", r, r.Body)
	}
	if _, err := BuildRequest(testOps[1], map[string]any{}); err == nil {
		t.Fatal("missing path param must fail")
	}
}

func TestToolsCall(t *testing.T) {
	fb := &fakeBackend{ops: testOps}
	s := &Server{Backend: fb}
	m := rpc(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_site_from_html","arguments":{"title":"Hello","html":"<h1>hi</h1>"}}}`)
	res := m["result"].(map[string]any)
	if res["isError"] == true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), "hello") {
		t.Fatalf("create_site_from_html: %v", m)
	}
	if last := fb.reqs[len(fb.reqs)-1]; last.Path != "/api/v1/drop" || last.Query.Get("title") != "Hello" || !strings.Contains(string(last.Body), "<h1>hi</h1>") {
		t.Fatalf("drop request: %+v", last)
	}
	m = rpc(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sites_get","arguments":{"site":"nope"}}}`)
	if m["result"].(map[string]any)["isError"] != true {
		t.Fatalf("404 should be a tool error: %v", m)
	}
	m = rpc(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"build_with_ai","arguments":{"site":"x","prompt":"add a button"}}}`)
	txt := m["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(txt, "Added a button.") || !strings.Contains(txt, "index.html") {
		t.Fatalf("builder SSE summary: %q", txt)
	}
	m = rpc(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"no_such_tool"}}`)
	if m["error"] == nil {
		t.Fatal("unknown tool must be a protocol error")
	}
	// Batch.
	out := s.HandleRaw(context.Background(), []byte(`[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":2,"method":"prompts/list"}]`))
	var arr []any
	if json.Unmarshal(out, &arr) != nil || len(arr) != 2 {
		t.Fatalf("batch: %s", out)
	}
}

func TestResourcesAndPrompts(t *testing.T) {
	s := &Server{Backend: &fakeBackend{ops: testOps}}
	m := rpc(t, s, `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"bunker://docs/sdk"}}`)
	c := m["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)
	if !strings.Contains(c["text"].(string), "bunker.db") {
		t.Fatal("sdk docs resource")
	}
	m = rpc(t, s, `{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"build_app","arguments":{"description":"a poll"}}}`)
	if !strings.Contains(m["result"].(map[string]any)["messages"].([]any)[0].(map[string]any)["content"].(map[string]any)["text"].(string), "a poll") {
		t.Fatal("prompt")
	}
	m = rpc(t, s, `{"jsonrpc":"2.0","id":3,"method":"resources/list"}`)
	if len(m["result"].(map[string]any)["resources"].([]any)) < 3 {
		t.Fatal("resources/list")
	}
}

func TestStdioLoop(t *testing.T) {
	s := &Server{Backend: &fakeBackend{ops: testOps}}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeStdio(context.Background(), s, inR, outW); outW.Close() }()
	sc := bufio.NewScanner(outR)
	send := func(l string) { inW.Write([]byte(l + "\n")) }
	read := func() map[string]any {
		if !sc.Scan() {
			t.Fatal("no reply")
		}
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad line %q", sc.Text())
		}
		return m
	}
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if r := read(); r["id"].(float64) != 1 {
		t.Fatalf("init: %v", r)
	}
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(``)
	send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if r := read(); r["id"].(float64) != 2 {
		t.Fatalf("ping: %v", r)
	}
	send(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	if r := read(); len(r["result"].(map[string]any)["tools"].([]any)) == 0 {
		t.Fatal("tools/list over stdio")
	}
	inW.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestParseSSE(t *testing.T) {
	var got []string
	ParseSSE(strings.NewReader(": comment\nevent: a\ndata: 1\ndata: 2\n\ndata: plain\n\n"), func(e, d string) error {
		got = append(got, e+"="+d)
		return nil
	})
	if strings.Join(got, "|") != "a=1\n2|message=plain" {
		t.Fatalf("%q", got)
	}
}

func TestRedirectValidation(t *testing.T) {
	for _, ok := range []string{"https://claude.ai/api/mcp/auth_callback", "http://localhost:33418/callback", "http://127.0.0.1/cb", "cursor://anysphere.cursor-retrieval/oauth/callback"} {
		if err := ValidRedirectURI(ok); err != nil {
			t.Errorf("%s should be allowed: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://evil.com/cb", "javascript:alert(1)", "https://x.com/cb#frag", "/relative", "data:text/html,hi"} {
		if ValidRedirectURI(bad) == nil {
			t.Errorf("%s should be rejected", bad)
		}
	}
	if !redirectMatches([]string{"http://127.0.0.1:1234/cb"}, "http://127.0.0.1:5555/cb") {
		t.Error("loopback port may vary")
	}
	if redirectMatches([]string{"https://a.com/cb"}, "https://a.com/cb2") {
		t.Error("https must match exactly")
	}
}
