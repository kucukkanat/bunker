package fnrt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const wasmSHA256 = "42a732a676ec2d93488c19411e0fad283bf72658fdad746f089914b523c783b1"

var (
	sharedOnce sync.Once
	shared     *Engine
)

func engine(t testing.TB) *Engine {
	sharedOnce.Do(func() { shared = New(Config{MaxConcurrent: 16}) })
	return shared
}

func run(t testing.TB, code string, opts ...func(*Job)) (*Result, error) {
	t.Helper()
	job := Job{Code: code, Limits: Limits{Timeout: 10 * time.Second}}
	for _, o := range opts {
		o(&job)
	}
	return engine(t).Run(context.Background(), job)
}

func mustRun(t *testing.T, code string, opts ...func(*Job)) string {
	t.Helper()
	res, err := run(t, code, opts...)
	if err != nil {
		var fe *Error
		if errors.As(err, &fe) {
			t.Fatalf("run: %v\n%s", err, fe.Stack)
		}
		t.Fatalf("run: %v", err)
	}
	return string(res.Output)
}

func wantCode(t *testing.T, err error, code string) *Error {
	t.Helper()
	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatalf("want *Error %q, got %v", code, err)
	}
	if fe.Code != code {
		t.Fatalf("want code %q, got %q (%s)", code, fe.Code, fe.Message)
	}
	return fe
}

// fakeHost records requests and answers them with fn.
type fakeHost struct {
	mu   sync.Mutex
	msgs []map[string]any
	fn   func(m map[string]any) any
}

func (h *fakeHost) host(_ context.Context, raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(err)
	}
	h.mu.Lock()
	h.msgs = append(h.msgs, m)
	h.mu.Unlock()
	var rep any = map[string]any{"ok": true, "status": 200, "data": nil}
	if h.fn != nil {
		rep = h.fn(m)
	}
	b, _ := json.Marshal(rep)
	return b
}

func ok(data any) any { return map[string]any{"ok": true, "status": 200, "data": data} }

func withHost(h *fakeHost) func(*Job) { return func(j *Job) { j.Host = h.host } }

func TestWasmChecksum(t *testing.T) {
	sum := sha256.Sum256(qjsWasm)
	if got := hex.EncodeToString(sum[:]); got != wasmSHA256 {
		t.Fatalf("qjs-wasi.wasm sha256 = %s, want %s", got, wasmSHA256)
	}
}

func TestHello(t *testing.T) {
	out := mustRun(t, `export default function (input, ctx) { return { hello: input.name, user: ctx.user.id, n: 1 + 1 }; }`,
		func(j *Job) {
			j.Input = json.RawMessage(`{"name":"world"}`)
			j.Context = json.RawMessage(`{"user":{"id":"u1"},"site":{"slug":"demo"}}`)
		})
	if out != `{"hello":"world","user":"u1","n":2}` {
		t.Fatalf("output = %s", out)
	}
}

func TestUndefinedReturnIsNull(t *testing.T) {
	if out := mustRun(t, `export default function () {}`); out != "null" {
		t.Fatalf("output = %s", out)
	}
}

func TestAsyncAwait(t *testing.T) {
	out := mustRun(t, `
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
export default async function () {
  const t0 = Date.now();
  await sleep(30);
  let cleared = true;
  const id = setTimeout(() => { cleared = false; }, 5);
  clearTimeout(id);
  await sleep(20);
  return { waited: Date.now() - t0 >= 25, cleared, v: await Promise.resolve(7) };
}`)
	if out != `{"waited":true,"cleared":true,"v":7}` {
		t.Fatalf("output = %s", out)
	}
}

func TestSDKShapes(t *testing.T) {
	h := &fakeHost{fn: func(m map[string]any) any {
		path, _ := m["path"].(string)
		switch {
		case m["method"] == "POST" && path == "/db/orders":
			return ok(map[string]any{"_id": "o1", "total": 5})
		case m["method"] == "GET" && path == "/db/orders/missing":
			return map[string]any{"ok": false, "status": 404, "error": map[string]any{"code": "not_found", "message": "no"}}
		case strings.HasPrefix(path, "/db/orders/_query"):
			return ok(map[string]any{"items": []any{map[string]any{"_id": "a"}}, "next": nil})
		case strings.HasPrefix(path, "/db/orders/_count"):
			return ok(map[string]any{"count": 3})
		case path == "/kv/k1":
			return ok(map[string]any{"exists": true, "value": map[string]any{"x": 1}})
		case path == "/me":
			return ok(map[string]any{"id": "u1", "email": "a@b"})
		case path == "/uploads/f1/content":
			return ok(map[string]any{"bodyB64": "aGVsbG8=", "type": "text/plain"})
		case path == "/uploads":
			return ok(map[string]any{"items": []any{map[string]any{"id": "f9"}}})
		}
		return ok(map[string]any{})
	}}
	out := mustRun(t, `
export default async function () {
  const c = bunker.db.collection("orders");
  const o = await c.add({ total: 5 });
  const missing = await c.get("missing");
  await c.update("o1", { total: 6 });
  await c.set("o1", { total: 7 });
  await c.delete("o1");
  const q = await c.query({ status: "open" });
  const n = await c.count({ status: "open" });
  await bunker.storage.set("k1", { x: 1 });
  const kv = await bunker.storage.get("k1");
  const me = await bunker.me();
  const text = await bunker.files.read("f1", "text");
  const bytes = await bunker.files.read("f1");
  const up = await bunker.files.upload("a.txt", "hi", { folder: "docs" });
  await bunker.rooms("lobby").send({ hi: 1 });
  await bunker.notify({ to: "u2", title: "T" });
  await bunker.ai.chat("hello");
  await bunker.m365.graph("/me/messages", { top: 5 });
  await bunker.connections.fetch("crm", { path: "/x" });
  return { id: o.id, missing, q: q.length, qid: q[0].id, n, kv, me: me.id, text, bytes: bytes.length, up: up.id };
}`, withHost(h))
	want := `{"id":"o1","missing":null,"q":1,"qid":"a","n":3,"kv":{"x":1},"me":"u1","text":"hello","bytes":5,"up":"f9"}`
	if out != want {
		t.Fatalf("output = %s\nwant     %s", out, want)
	}
	type req struct{ method, path, body string }
	var got []req
	for _, m := range h.msgs {
		if m["t"] != "api" {
			t.Fatalf("unexpected message %v", m)
		}
		b, _ := json.Marshal(m["body"])
		got = append(got, req{m["method"].(string), m["path"].(string), string(b)})
	}
	want2 := []req{
		{"POST", "/db/orders", `{"total":5}`},
		{"GET", "/db/orders/missing", `null`},
		{"PATCH", "/db/orders/o1", `{"total":6}`},
		{"PUT", "/db/orders/o1", `{"total":7}`},
		{"DELETE", "/db/orders/o1", `null`},
		{"POST", "/db/orders/_query", `{"where":{"status":"open"}}`},
		{"GET", "/db/orders/_count?where=%7B%22status%22%3A%22open%22%7D", `null`},
		{"PUT", "/kv/k1", `{"value":{"x":1}}`},
		{"GET", "/kv/k1", `null`},
		{"GET", "/me", `null`},
		{"GET", "/uploads/f1/content", `null`},
		{"GET", "/uploads/f1/content", `null`},
		{"POST", "/uploads", `{"__multipart":true,"file":{"bodyB64":"aGk=","name":"a.txt","type":"text/plain"},"folder":"docs"}`},
		{"POST", "/rooms/lobby", `{"data":{"hi":1}}`},
		{"POST", "/notify", `{"title":"T","to":["u2"]}`},
		{"POST", "/ai/chat", `{"messages":[{"content":"hello","role":"user"}]}`},
		{"GET", "/m365/graph?top=5&path=%2Fme%2Fmessages", `null`},
		{"POST", "/connections/crm/fetch", `{"method":"GET","path":"/x"}`},
	}
	if len(got) != len(want2) {
		t.Fatalf("got %d requests, want %d: %v", len(got), len(want2), got)
	}
	for i := range want2 {
		if got[i] != want2[i] {
			t.Errorf("request %d = %+v, want %+v", i, got[i], want2[i])
		}
	}
}

func TestHostErrorThrowsBunkerError(t *testing.T) {
	h := &fakeHost{fn: func(m map[string]any) any {
		return map[string]any{"ok": false, "status": 403, "error": map[string]any{"code": "forbidden", "message": "nope"}}
	}}
	out := mustRun(t, `
export default async function () {
  try { await bunker.db.collection("x").add({}); return "no"; }
  catch (e) { return { isBE: e instanceof BunkerError, name: e.name, code: e.code, message: e.message, status: e.status }; }
}`, withHost(h))
	if out != `{"isBE":true,"name":"BunkerError","code":"forbidden","message":"nope","status":403}` {
		t.Fatalf("output = %s", out)
	}
	// Uncaught, it becomes a script error.
	_, err := run(t, `export default async () => { await bunker.storage.get("a"); }`, withHost(h))
	fe := wantCode(t, err, CodeScript)
	if !strings.Contains(fe.Message, "nope") {
		t.Fatalf("message = %q", fe.Message)
	}
}

func TestSecrets(t *testing.T) {
	h := &fakeHost{fn: func(m map[string]any) any {
		if m["t"] != "secret" {
			t.Errorf("message %v", m)
		}
		if m["name"] == "API_KEY" {
			return ok("s3cret")
		}
		return ok(nil)
	}}
	out := mustRun(t, `export default async () => [await bunker.secrets.get("API_KEY"), await bunker.secrets.get("NOPE")]`, withHost(h))
	if out != `["s3cret",null]` {
		t.Fatalf("output = %s", out)
	}
}

func TestFetch(t *testing.T) {
	h := &fakeHost{fn: func(m map[string]any) any {
		if m["url"] == "https://bin.example/b" {
			return ok(map[string]any{"status": 201, "headers": map[string]any{"x-a": []any{"1", "2"}}, "bodyB64": "AAEC"})
		}
		if m["url"] == "https://down.example/" {
			return map[string]any{"ok": false, "status": 403, "error": map[string]any{"code": "outbound_refused", "message": "blocked"}}
		}
		return ok(map[string]any{"status": 200, "headers": map[string]any{"content-type": "application/json"}, "body": `{"a":1}`})
	}}
	out := mustRun(t, `
export default async function () {
  const r = await fetch("https://api.example/x", { method: "post", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ q: 1 }) });
  const j = await r.json();
  const b = await fetch("https://bin.example/b", { body: new Uint8Array([9, 8]) });
  const buf = new Uint8Array(await b.arrayBuffer());
  let err = null;
  try { await fetch("https://down.example/"); } catch (e) { err = e.code; }
  return { ok: r.ok, status: r.status, ct: r.headers.get("Content-Type"), j, bok: b.ok, bs: b.status, xa: b.headers.get("x-a"), buf: Array.from(buf), err };
}`, withHost(h))
	if out != `{"ok":true,"status":200,"ct":"application/json","j":{"a":1},"bok":true,"bs":201,"xa":"1, 2","buf":[0,1,2],"err":"outbound_refused"}` {
		t.Fatalf("output = %s", out)
	}
	m := h.msgs[0]
	if m["t"] != "fetch" || m["method"] != "POST" || m["body"] != `{"q":1}` || m["headers"].(map[string]any)["content-type"] != "application/json" {
		t.Fatalf("fetch request = %v", m)
	}
	if h.msgs[1]["bodyB64"] != "CQg=" || h.msgs[1]["method"] != "GET" {
		t.Fatalf("binary fetch request = %v", h.msgs[1])
	}
}

type logRec struct {
	mu    sync.Mutex
	lines [][2]string
	size  int
}

func (l *logRec) log(level, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, [2]string{level, line})
	l.size += len(line)
}

func TestConsoleLevels(t *testing.T) {
	l := &logRec{}
	mustRun(t, `export default () => {
  console.log("a", 1, { b: 2 });
  console.info("info\nline2");
  console.warn("w");
  console.error(new Error("boom"));
  print("p");
}`, func(j *Job) { j.Log = l.log })
	if len(l.lines) != 5 {
		t.Fatalf("lines = %q", l.lines)
	}
	want := [][2]string{{"log", `a 1 {"b":2}`}, {"info", "info\nline2"}, {"warn", "w"}}
	for i, w := range want {
		if l.lines[i] != w {
			t.Errorf("line %d = %q, want %q", i, l.lines[i], w)
		}
	}
	if l.lines[3][0] != "error" || !strings.HasPrefix(l.lines[3][1], "Error: boom") {
		t.Errorf("error line = %q", l.lines[3])
	}
	if l.lines[4] != [2]string{"log", "p"} {
		t.Errorf("print line = %q", l.lines[4])
	}
}

func TestLogCap(t *testing.T) {
	l := &logRec{}
	mustRun(t, `export default () => { for (let i = 0; i < 1000; i++) console.log("x".repeat(100)); return 1; }`,
		func(j *Job) { j.Log = l.log; j.Limits.MaxLogBytes = 2000 })
	last := l.lines[len(l.lines)-1]
	if !strings.Contains(last[1], "truncated") {
		t.Fatalf("last line = %q", last)
	}
	if l.size > 2000+len(last[1]) {
		t.Fatalf("logged %d bytes", l.size)
	}
}

func TestTimeout(t *testing.T) {
	start := time.Now()
	_, err := run(t, `export default () => { while (true) {} }`, func(j *Job) { j.Limits.Timeout = 200 * time.Millisecond })
	wantCode(t, err, CodeTimeout)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v", d)
	}
	// A pending timer far in the future is also cut off.
	_, err = run(t, `export default () => new Promise((r) => setTimeout(r, 3600e3))`, func(j *Job) { j.Limits.Timeout = 200 * time.Millisecond })
	wantCode(t, err, CodeTimeout)
	// Parent context deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err = engine(t).Run(ctx, Job{Code: `export default () => { for(;;){} }`})
	wantCode(t, err, CodeTimeout)
}

func TestTimeoutWhileWaitingForHost(t *testing.T) {
	h := &fakeHost{fn: func(m map[string]any) any { time.Sleep(50 * time.Millisecond); return ok(nil) }}
	_, err := run(t, `export default async () => { for (;;) await bunker.storage.get("a"); }`,
		withHost(h), func(j *Job) { j.Limits.Timeout = 200 * time.Millisecond; j.Limits.MaxHostCalls = 1e6 })
	wantCode(t, err, CodeTimeout)
}

func TestMemoryCap(t *testing.T) {
	_, err := run(t, `export default () => { const a = []; for (;;) a.push(new Array(100000).fill(1.5)); }`,
		func(j *Job) { j.Limits.MemoryBytes = 16 << 20 })
	wantCode(t, err, CodeMemory)
	_, err = run(t, `export default () => { const m = new Map(); for (let i = 0; ; i++) m.set(i, { i, s: "v" + i }); }`, func(j *Job) { j.Limits.MemoryBytes = 16 << 20 })
	wantCode(t, err, CodeMemory)
	_, err = run(t, `export default () => { const a = []; for (;;) a.push(new Uint8Array(1 << 20)); }`, func(j *Job) { j.Limits.MemoryBytes = 16 << 20 })
	wantCode(t, err, CodeMemory)
	// The engine keeps working afterwards.
	if out := mustRun(t, `export default () => 1`); out != "1" {
		t.Fatal(out)
	}
}

func TestOutputCap(t *testing.T) {
	_, err := run(t, `export default () => "x".repeat(2000)`, func(j *Job) { j.Limits.MaxOutput = 1000 })
	wantCode(t, err, CodeOutputTooLarge)
	_, err = run(t, `export default () => "x".repeat(6 << 20)`)
	wantCode(t, err, CodeOutputTooLarge)
}

func TestHostCallCap(t *testing.T) {
	h := &fakeHost{}
	_, err := run(t, `export default async () => { for (let i = 0; i < 100; i++) await bunker.storage.get("k"); }`,
		withHost(h), func(j *Job) { j.Limits.MaxHostCalls = 10 })
	wantCode(t, err, CodeTooManyCalls)
	if len(h.msgs) != 10 {
		t.Fatalf("host saw %d calls", len(h.msgs))
	}
	res, err := run(t, `export default async () => { for (let i = 0; i < 10; i++) await bunker.storage.get("k"); }`,
		withHost(&fakeHost{}), func(j *Job) { j.Limits.MaxHostCalls = 10 })
	if err != nil || res.HostCalls != 10 {
		t.Fatalf("res=%v err=%v", res, err)
	}
}

func TestNoStdOs(t *testing.T) {
	out := mustRun(t, `export default () => [typeof std, typeof os, typeof bjson, typeof scriptArgs, typeof globalThis.std, typeof bunker, typeof fetch, typeof setTimeout, typeof TextEncoder]`)
	if out != `["undefined","undefined","undefined","undefined","undefined","object","function","function","function"]` {
		t.Fatalf("output = %s", out)
	}
	// bunker can't be replaced.
	out = mustRun(t, `export default () => { try { globalThis.bunker = 1; } catch (_) {} try { bunker.db = 1; } catch (_) {} return typeof bunker.db.collection; }`)
	if out != `"function"` {
		t.Fatalf("output = %s", out)
	}
}

func TestTextEncoding(t *testing.T) {
	out := mustRun(t, `export default () => {
  const s = "héllo ✓ 😀";
  const b = new TextEncoder().encode(s);
  return [b.length, new TextDecoder().decode(b) === s];
}`)
	if out != `[15,true]` {
		t.Fatalf("output = %s", out)
	}
}

func TestScriptError(t *testing.T) {
	_, err := run(t, `export default () => { throw new TypeError("bad thing"); }`)
	fe := wantCode(t, err, CodeScript)
	if fe.Message != "TypeError: bad thing" || fe.Stack == "" {
		t.Fatalf("err = %+v", fe)
	}
	_, err = run(t, `export default async () => { throw "plain"; }`)
	wantCode(t, err, CodeScript)
	_, err = run(t, `export default () => new Promise(() => {})`)
	wantCode(t, err, CodeScript)
}

func TestBadCode(t *testing.T) {
	for _, code := range []string{
		`export default function ( {`,
		`export const x = 1;`,
		`import "./nope.js"; export default () => 1`,
		`throw new Error("top level"); export default () => 1`,
	} {
		_, err := run(t, code)
		wantCode(t, err, CodeBadCode)
	}
}

func TestInspect(t *testing.T) {
	cfg, err := engine(t).Inspect(context.Background(), `
export const config = { elevated: true, schedule: "*/5 * * * *", onWrite: ["orders"], fn: () => 1 };
console.log("loading");
export default async function () {}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(cfg) != `{"elevated":true,"schedule":"*/5 * * * *","onWrite":["orders"]}` {
		t.Fatalf("config = %s", cfg)
	}
	cfg, err = engine(t).Inspect(context.Background(), `export default () => 1`)
	if err != nil || string(cfg) != "null" {
		t.Fatalf("cfg=%s err=%v", cfg, err)
	}
	_, err = engine(t).Inspect(context.Background(), `export const config = {}`)
	wantCode(t, err, CodeBadCode)
	_, err = engine(t).Inspect(context.Background(), `export default () => 1; await bunker.db.list();`)
	wantCode(t, err, CodeBadCode)
	start := time.Now()
	_, err = engine(t).Inspect(context.Background(), `for(;;){} export default () => 1`)
	wantCode(t, err, CodeTimeout)
	if time.Since(start) > 3*time.Second {
		t.Fatal("inspect did not stop")
	}
}

func TestBusy(t *testing.T) {
	e := New(Config{MaxConcurrent: 2})
	defer e.Close(context.Background())
	if err := e.Warm(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.Run(context.Background(), Job{Code: `export default () => { for(;;){} }`, Limits: Limits{Timeout: 600 * time.Millisecond}})
		}()
	}
	time.Sleep(150 * time.Millisecond)
	start := time.Now()
	_, err := e.Run(context.Background(), Job{Code: `export default () => 1`})
	wantCode(t, err, CodeBusy)
	if d := time.Since(start); d < 80*time.Millisecond || d > 500*time.Millisecond {
		t.Fatalf("busy after %v", d)
	}
	wg.Wait()
	if _, err := e.Run(context.Background(), Job{Code: `export default () => 1`}); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentRuns(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := engine(t).Run(context.Background(), Job{Code: `export default (n) => n * 2`, Input: json.RawMessage(fmt.Sprint(i))})
			if err != nil {
				errs <- err
				return
			}
			if string(res.Output) != fmt.Sprint(i*2) {
				errs <- fmt.Errorf("run %d = %s", i, res.Output)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestNoGoroutineLeaks(t *testing.T) {
	e := engine(t)
	e.Warm()
	runtime.GC()
	before := runtime.NumGoroutine()
	h := &fakeHost{}
	codes := []struct {
		code string
		lim  Limits
	}{
		{`export default () => 1`, Limits{}},
		{`export default () => { for(;;){} }`, Limits{Timeout: 50 * time.Millisecond}},
		{`export default async () => { for(;;) await bunker.storage.get("a"); }`, Limits{Timeout: 50 * time.Millisecond, MaxHostCalls: 1e6}},
		{`export default async () => { for(;;) await bunker.storage.get("a"); }`, Limits{MaxHostCalls: 5}},
		{`export default () => new Promise((r) => setTimeout(r, 1e7))`, Limits{Timeout: 50 * time.Millisecond}},
		{`export default () => { const a = []; for (;;) a.push(new Array(1e5).fill(1)); }`, Limits{MemoryBytes: 12 << 20}},
		{`export default () => "x".repeat(5000)`, Limits{MaxOutput: 100}},
		{`export default () => { throw new Error("x") }`, Limits{}},
		{`syntax error(`, Limits{}},
	}
	for i := 0; i < 5; i++ {
		for _, c := range codes {
			e.Run(context.Background(), Job{Code: c.code, Limits: c.lim, Host: h.host})
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		runtime.GC()
		after := runtime.NumGoroutine()
		if after <= before+1 {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			t.Fatalf("goroutines: before %d, after %d\n%s", before, after, buf[:n])
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func BenchmarkTrivialCall(b *testing.B) {
	e := engine(b)
	if err := e.Warm(); err != nil {
		b.Fatal(err)
	}
	job := Job{Code: `export default (x) => ({ y: x.n + 1 })`, Input: json.RawMessage(`{"n":1}`)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Run(context.Background(), job); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHostCall(b *testing.B) {
	e := engine(b)
	e.Warm()
	h := &fakeHost{fn: func(map[string]any) any { return ok(map[string]any{"exists": true, "value": 1}) }}
	job := Job{Code: `export default async () => { let s = 0; for (let i = 0; i < 100; i++) s += await bunker.storage.get("k"); return s; }`, Host: h.host}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Run(context.Background(), job); err != nil {
			b.Fatal(err)
		}
	}
}
