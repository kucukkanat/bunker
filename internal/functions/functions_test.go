package functions_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/kucukkanat/bunker/internal/data"
	_ "github.com/kucukkanat/bunker/internal/files"
	_ "github.com/kucukkanat/bunker/internal/functions"
	_ "github.com/kucukkanat/bunker/internal/hooks"
	"github.com/kucukkanat/bunker/internal/testkit"
)

func drop(t *testing.T, c *testkit.Client, title string, files map[string]string) {
	t.Helper()
	c.Must("POST", "/api/v1/drop", map[string]any{"title": title, "files": files})
}

func call(c *testkit.Client, site, name string, input any) testkit.Res {
	return c.Do("POST", "/api/v1/sites/"+site+"/functions/"+name, map[string]any{"input": input})
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestCallAsViewerAndElevated(t *testing.T) {
	t.Setenv("BUNKER_ADMINS", "")
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	drop(t, ada, "Shop", map[string]string{
		"index.html":    "<h1>shop</h1>",
		"api/_lib.ts":   "export const double = (n: number): number => n * 2;",
		"api/hello.ts":  `import { double } from "./_lib"; export default async (input, ctx) => ({ hi: ctx.user.email, n: double(input.n), trig: ctx.trigger.type });`,
		"api/add.js":    `export default async (input) => (await bunker.db.collection("orders").add(input))._id;`,
		"api/count.js":  `export const config = { elevated: true }; export default async () => bunker.db.collection("orders").count();`,
		"api/mine.js":   `export default async () => bunker.db.collection("orders").count();`,
		"api/who.js":    "export const config = { elevated: true }; export default async (i, ctx) => ({ caller: ctx.user.email, actingAs: (await bunker.me()).email });",
		"api/dyn.js":    `export default async () => { const m = ["qjs", "std"].join(":"); return (await import(m)).loadFile("/fn/input.json"); };`,
		"api/client.js": `console.log("not a function")`,
		"api/data.json": `{"static":true}`,
	})
	r := call(ada, "shop", "hello", map[string]any{"n": 21})
	if r.Status != 200 {
		t.Fatalf("hello: %d %s", r.Status, r.Body)
	}
	out := r.JSON()["output"].(map[string]any)
	if out["hi"] != "ada@corp.com" || out["n"] != 42.0 || out["trig"] != "call" {
		t.Fatalf("bad output %v", out)
	}
	// Share with Bob as a viewer; his writes are his, counting with the
	// "private" rule only sees his own rows, elevated (owner) sees all.
	ada.Must("POST", "/api/v1/sites/shop/members", map[string]any{"type": "user", "id": "bob@corp.com", "role": "viewer"})
	ada.Must("PUT", "/api/v1/sites/shop/db/orders/_rule", map[string]any{"rule": "private"})
	bob := k.As("bob@corp.com", "")
	call(ada, "shop", "add", map[string]any{"item": "a"})
	if r := call(bob, "shop", "add", map[string]any{"item": "b"}); r.Status != 200 {
		t.Fatalf("bob add: %d %s", r.Status, r.Body)
	}
	if n := call(bob, "shop", "mine", nil).JSON()["output"]; n != 1.0 {
		t.Fatalf("viewer count = %v, want 1 (private rule)", n)
	}
	if n := call(bob, "shop", "count", nil).JSON()["output"]; n != 2.0 {
		t.Fatalf("elevated count = %v, want 2", n)
	}
	wr := call(bob, "shop", "who", nil)
	who, _ := wr.JSON()["output"].(map[string]any)
	if who == nil {
		t.Fatalf("who: %d %s", wr.Status, wr.Body)
	}
	if who["caller"] != "bob@corp.com" || who["actingAs"] != "ada@corp.com" {
		t.Fatalf("elevated identity: %v", who)
	}
	if r := call(ada, "shop", "dyn", nil); r.Status != 500 || !strings.Contains(string(r.Body), "computed name") {
		t.Fatalf("dynamic import: %d %s", r.Status, r.Body)
	}
	// Code never reaches browsers; static files under api/ still do.
	list := ada.Must("GET", "/api/v1/sites/shop/functions", nil)
	fns := list["functions"].([]any)
	if len(fns) != 6 {
		t.Fatalf("functions: %v", fns)
	}
	if call(ada, "shop", "client", nil).Status != 404 || call(ada, "shop", "_lib", nil).Status != 404 {
		t.Fatal("non-functions must not be callable")
	}
	// Pages may call fn.call through the bridge.
	ada.Page = true
	if r := call(ada, "shop", "hello", map[string]any{"n": 1}); r.Status != 200 {
		t.Fatalf("page call: %d %s", r.Status, r.Body)
	}
	if r := ada.Do("GET", "/api/v1/sites/shop/secrets", nil); r.Status != 403 {
		t.Fatalf("pages must not list secrets: %d", r.Status)
	}
	ada.Page = false
	// Strangers can't call.
	if r := call(k.As("eve@corp.com", ""), "shop", "hello", nil); r.Status != 404 {
		t.Fatalf("stranger: %d", r.Status)
	}
}

func TestHiddenFromBrowsers(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	drop(t, ada, "Hid", map[string]string{"index.html": "<p>x</p>", "api/x.js": "export default () => 1", "api/_y.js": "export const y = 1", "api/d.json": "{}"})
	page := ada.Do("GET", "/s/hid/", nil)
	base := regexp.MustCompile(`/s/hid/~r/[^/"']+/`).FindString(string(page.Body))
	if base == "" {
		t.Fatalf("no frame src in shell: %s", page.Body)
	}
	for p, want := range map[string]int{"api/x.js": 404, "api/_y.js": 404, "api/d.json": 200} {
		if r := ada.Do("GET", base+p, nil); r.Status != want {
			t.Errorf("%s: %d, want %d", p, r.Status, want)
		}
	}
}

func TestErrorsLimitsAndRuns(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	drop(t, ada, "Err", map[string]string{
		"index.html":  "x",
		"api/boom.js": `export default () => { console.log("about to fail"); throw new Error("kaboom"); }`,
		"api/slow.js": `export const config = { timeoutMs: 300 }; export default () => { while (true) {} }`,
		"api/bad.js":  `import x from "left-pad"; export default () => x`,
		"api/sec.js":  `export default async () => ({ k: await bunker.secrets.get("API_KEY"), none: await bunker.secrets.get("NOPE") })`,
	})
	r := call(ada, "err", "boom", nil)
	if r.Status != 500 || !strings.Contains(string(r.Body), "kaboom") {
		t.Fatalf("boom: %d %s", r.Status, r.Body)
	}
	if r := call(ada, "err", "slow", nil); r.Status != 504 {
		t.Fatalf("slow: %d %s", r.Status, r.Body)
	}
	if r := call(ada, "err", "bad", nil); r.Status != 500 || !strings.Contains(string(r.Body), "packages") {
		t.Fatalf("bad: %d %s", r.Status, r.Body)
	}
	if r := ada.Do("PUT", "/api/v1/sites/err/secrets/bad-name", map[string]any{"value": "x"}); r.Status != 400 {
		t.Fatalf("bad secret name: %d", r.Status)
	}
	ada.Must("PUT", "/api/v1/sites/err/secrets/API_KEY", map[string]any{"value": "s3cr3t"})
	out := call(ada, "err", "sec", nil).JSON()["output"].(map[string]any)
	if out["k"] != "s3cr3t" || out["none"] != nil {
		t.Fatalf("secrets: %v", out)
	}
	sl := ada.Must("GET", "/api/v1/sites/err/secrets", nil)
	if b, _ := json.Marshal(sl); strings.Contains(string(b), "s3cr3t") {
		t.Fatal("secret value leaked in list")
	}
	waitFor(t, "runs", func() bool {
		items := ada.Must("GET", "/api/v1/sites/err/functions/_runs?name=boom", nil)["items"].([]any)
		return len(items) == 1 && strings.Contains(items[0].(map[string]any)["logs"].(string), "about to fail")
	})
}

func TestBlocks(t *testing.T) {
	t.Setenv("BUNKER_ADMINS", "admin@corp.com")
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	admin := k.As("admin@corp.com", "")
	drop(t, ada, "One", map[string]string{"index.html": "x", "api/ping.js": "export default () => 'pong'"})
	drop(t, ada, "Two", map[string]string{"index.html": "x", "api/ping.js": "export default () => 'pong'"})
	me := ada.Must("GET", "/api/v1/me", nil)
	uid := strconv.FormatInt(int64(me["id"].(float64)), 10)

	if r := ada.Do("PUT", "/api/v1/admin/functions/blocks/user/"+uid, map[string]any{"reason": "x"}); r.Status != 403 {
		t.Fatalf("non-admin block: %d", r.Status)
	}
	admin.Must("PUT", "/api/v1/admin/functions/blocks/site/one", map[string]any{"reason": "Too many calls"})
	if r := call(ada, "one", "ping", nil); r.Status != 403 || !strings.Contains(string(r.Body), "Too many calls") {
		t.Fatalf("site block: %d %s", r.Status, r.Body)
	}
	if r := call(ada, "two", "ping", nil); r.Status != 200 {
		t.Fatalf("other app: %d", r.Status)
	}
	st := ada.Must("GET", "/api/v1/functions/status", nil)
	if st["user"] != nil || st["sites"].(map[string]any)["one"] == nil {
		t.Fatalf("status after site block: %v", st)
	}
	admin.Must("PUT", "/api/v1/admin/functions/blocks/user/"+uid, map[string]any{"reason": "Policy review"})
	if r := call(ada, "two", "ping", nil); r.Status != 403 {
		t.Fatalf("user block: %d", r.Status)
	}
	st = ada.Must("GET", "/api/v1/functions/status", nil)
	if u, _ := st["user"].(map[string]any); u == nil || u["reason"] != "Policy review" || len(st["sites"].(map[string]any)) != 2 {
		t.Fatalf("status after user block: %v", st)
	}
	bl := admin.Must("GET", "/api/v1/admin/functions/blocks", nil)["items"].([]any)
	if len(bl) != 2 {
		t.Fatalf("blocks: %v", bl)
	}
	admin.Must("DELETE", "/api/v1/admin/functions/blocks/user/"+uid, nil)
	admin.Must("DELETE", "/api/v1/admin/functions/blocks/site/one", nil)
	if r := call(ada, "one", "ping", nil); r.Status != 200 {
		t.Fatalf("after unblock: %d %s", r.Status, r.Body)
	}
	admin.Must("PUT", "/api/v1/admin/functions", map[string]any{"enabled": false})
	if r := call(ada, "one", "ping", nil); r.Status != 403 {
		t.Fatalf("global off: %d", r.Status)
	}
	if st := ada.Must("GET", "/api/v1/functions/status", nil); st["enabled"] != false {
		t.Fatalf("status global: %v", st)
	}
	admin.Must("PUT", "/api/v1/admin/functions", map[string]any{"enabled": true, "outbound": "allowlist", "allowlist": []string{"api.example.com", "*.stripe.com"}})
	s := admin.Must("GET", "/api/v1/admin/functions", nil)
	if s["outbound"] != "allowlist" || len(s["allowlist"].([]any)) != 2 {
		t.Fatalf("settings: %v", s)
	}
	if r := admin.Do("PUT", "/api/v1/admin/functions", map[string]any{"memoryMb": 5}); r.Status != 400 {
		t.Fatalf("bad memory accepted: %d", r.Status)
	}
}

func TestOutboundPolicy(t *testing.T) {
	t.Setenv("BUNKER_ADMINS", "admin@corp.com")
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	drop(t, ada, "Out", map[string]string{"index.html": "x",
		"api/get.js": `export default async (u) => { try { const r = await fetch(u); return r.status } catch (e) { return "err:" + e.message } }`})
	// Private addresses are always refused (the test server is on loopback).
	out := call(ada, "out", "get", k.Srv.URL+"/").JSON()["output"].(string)
	if !strings.HasPrefix(out, "err:") || !strings.Contains(out, "blocked") {
		t.Fatalf("loopback fetch: %q", out)
	}
	k.As("admin@corp.com", "").Must("PUT", "/api/v1/admin/functions", map[string]any{"outbound": "off"})
	out = call(ada, "out", "get", "https://example.com/").JSON()["output"].(string)
	if !strings.Contains(out, "connections") {
		t.Fatalf("outbound off: %q", out)
	}
}

func TestWriteTriggerAndWebhook(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	drop(t, ada, "Trig", map[string]string{"index.html": "x",
		"api/stamp.js": `export const config = { onWrite: "orders" };
export default async (ev) => { if (ev.op === "add") await bunker.db.collection("log").add({ saw: ev.doc.item, by: (await bunker.me()).email }); }`,
		"api/hook.js": `export default async (input, ctx) => ({ got: input.n * 2, via: ctx.trigger.type, hook: ctx.trigger.hook })`,
	})
	ada.Must("POST", "/api/v1/sites/trig/db/orders", map[string]any{"item": "tea"})
	waitFor(t, "write trigger", func() bool {
		items, _ := ada.Must("GET", "/api/v1/sites/trig/db/log", nil)["items"].([]any)
		return len(items) == 1 && items[0].(map[string]any)["saw"] == "tea" && items[0].(map[string]any)["by"] == "ada@corp.com"
	})
	h := ada.Must("POST", "/api/v1/sites/trig/hooks", map[string]any{"name": "Zap", "function": "hook"})
	res, err := http.Post(h["url"].(string), "application/json", strings.NewReader(`{"n":4}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.NewDecoder(res.Body).Decode(&got)
	res.Body.Close()
	if res.StatusCode != 200 || got["got"] != 8.0 || got["via"] != "webhook" || got["hook"] != "Zap" {
		t.Fatalf("webhook: %d %v", res.StatusCode, got)
	}
	if r := ada.Do("POST", "/api/v1/sites/trig/hooks", map[string]any{"name": "x", "function": "hook", "collection": "c"}); r.Status != 400 {
		t.Fatalf("both targets accepted: %d", r.Status)
	}
}

func TestDraftCode(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	drop(t, ada, "Drafty", map[string]string{"index.html": "x", "api/v.js": "export default () => 'live'"})
	ada.Must("PUT", "/api/v1/sites/drafty/files/api/v.js", "export default () => 'draft'")
	if out := call(ada, "drafty", "v", nil).JSON()["output"]; out != "live" {
		t.Fatalf("live: %v", out)
	}
	r := ada.Do("POST", "/api/v1/sites/drafty/functions/v", map[string]any{"draft": true})
	if out := r.JSON()["output"]; out != "draft" {
		t.Fatalf("draft: %d %s", r.Status, r.Body)
	}
}

func TestFilesAndStorage(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	drop(t, ada, "Filey", map[string]string{"index.html": "x",
		"api/up.js": `export default async () => {
  const f = await bunker.files.upload("note.txt", "hello file", { type: "text/plain" });
  const bin = await bunker.files.upload("b.png", new Uint8Array([137, 80, 78, 71, 13, 10, 26, 10]), { type: "image/png" });
  await bunker.storage.set("k", { v: 1 });
  return { text: await bunker.files.read(f.id, "text"), bytes: Array.from(await bunker.files.read(bin.id)), kv: await bunker.storage.get("k"), me: (await bunker.me()).email };
}`})
	r := call(ada, "filey", "up", nil)
	if r.Status != 200 {
		t.Fatalf("up: %d %s", r.Status, r.Body)
	}
	out := r.JSON()["output"].(map[string]any)
	if out["text"] != "hello file" || len(out["bytes"].([]any)) != 8 || out["kv"].(map[string]any)["v"] != 1.0 || out["me"] != "ada@corp.com" {
		t.Fatalf("output: %v", out)
	}
}
