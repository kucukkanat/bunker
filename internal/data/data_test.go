package data_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/server"

	_ "github.com/kucukkanat/bunker/internal/auth"
	_ "github.com/kucukkanat/bunker/internal/data"
	_ "github.com/kucukkanat/bunker/internal/sites"
)

var (
	srv    *httptest.Server
	theApp *core.App
)

func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "bunker-data-test")
	os.Setenv("BUNKER_DB_MAX_DOCS", "1000")
	// Otherwise every dev identity becomes an admin (first-user rule).
	os.Setenv("BUNKER_ADMINS", "nobody@x.test")
	cfg := &core.Config{Dev: true, DataDir: dir, Listen: "127.0.0.1:0"}
	a, err := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		panic(err)
	}
	theApp = a
	srv = httptest.NewServer(server.Handler(a))
	a.Cfg.URL = srv.URL
	code := m.Run()
	srv.Close()
	os.RemoveAll(dir)
	os.Exit(code)
}

// client is a signed-in browser-like client.
type client struct {
	t  *testing.T
	hc *http.Client
}

// admin is the dev-mode default user (no cookie → you@localhost, admin).
func admin(t *testing.T) *client { return &client{t: t, hc: &http.Client{}} }

func as(t *testing.T, email, name, groups string) *client {
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := hc.Get(srv.URL + "/auth/dev?as=" + url.QueryEscape(email) + "&name=" + url.QueryEscape(name) + "&groups=" + url.QueryEscape(groups))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return &client{t: t, hc: hc}
}

func (c *client) do(method, path string, body any, page bool) (int, map[string]any, []byte) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rd = strings.NewReader(b)
		default:
			j, _ := json.Marshal(b)
			rd = bytes.NewReader(j)
		}
	}
	req, _ := http.NewRequest(method, srv.URL+path, rd)
	req.Header.Set("X-Bunker", "1")
	if page {
		req.Header.Set("X-Bunker-Page", "1")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	json.Unmarshal(raw, &m)
	return res.StatusCode, m, raw
}

func (c *client) must(method, path string, body any, want int) map[string]any {
	c.t.Helper()
	return c.mustAs(method, path, body, want, true)
}

// mustApp calls as the dashboard (not a page).
func (c *client) mustApp(method, path string, body any, want int) map[string]any {
	c.t.Helper()
	return c.mustAs(method, path, body, want, false)
}

func (c *client) mustAs(method, path string, body any, want int, page bool) map[string]any {
	c.t.Helper()
	st, m, raw := c.do(method, path, body, page)
	if st != want {
		c.t.Fatalf("%s %s: status %d, want %d: %s", method, path, st, want, raw)
	}
	return m
}

var siteN int

// newSite creates a published site owned by admin and shares it.
func newSite(t *testing.T, viewers ...string) string {
	siteN++
	slug := fmt.Sprintf("test-site-%d", siteN)
	a := admin(t)
	st, _, raw := a.do("POST", "/api/v1/sites", map[string]any{"title": "Test " + slug, "slug": slug}, false)
	if st != 201 {
		t.Fatalf("create site: %d %s", st, raw)
	}
	for _, v := range viewers {
		role := "viewer"
		if strings.HasPrefix(v, "editor:") {
			role, v = "editor", strings.TrimPrefix(v, "editor:")
		}
		st, _, raw := a.do("POST", "/api/v1/sites/"+slug+"/members", map[string]any{"type": "user", "id": v, "role": role}, false)
		if st != 200 {
			t.Fatalf("share: %d %s", st, raw)
		}
	}
	return slug
}

func items(m map[string]any) []map[string]any {
	var out []map[string]any
	for _, x := range m["items"].([]any) {
		out = append(out, x.(map[string]any))
	}
	return out
}

func TestCRUDAndMeta(t *testing.T) {
	slug := newSite(t, "ann@x.test")
	ann := as(t, "ann@x.test", "Ann", "")
	base := "/api/v1/sites/" + slug + "/db/orders"
	d := ann.must("POST", base, map[string]any{"meal": "Pasta", "qty": 2, "_id": "evil", "_author": 99}, 201)
	id := d["_id"].(string)
	if len(id) != 12 || id == "evil" {
		t.Fatalf("bad id %q", id)
	}
	if d["_authorName"] != "Ann" || d["meal"] != "Pasta" || d["_created"] == nil {
		t.Fatalf("bad doc %v", d)
	}
	if d["_author"].(float64) == 99 {
		t.Fatal("client set _author")
	}
	got := ann.must("GET", base+"/"+id, nil, 200)
	if got["qty"].(float64) != 2 {
		t.Fatalf("get: %v", got)
	}
	up := ann.must("PATCH", base+"/"+id, map[string]any{"drink": "Juice", "qty": nil}, 200)
	if up["drink"] != "Juice" || up["qty"] != nil || up["meal"] != "Pasta" {
		t.Fatalf("patch: %v", up)
	}
	set := ann.must("PUT", base+"/"+id, map[string]any{"meal": "Salad"}, 200)
	if set["drink"] != nil || set["meal"] != "Salad" {
		t.Fatalf("set: %v", set)
	}
	// set with a chosen id creates
	ann.must("PUT", base+"/custom-1", map[string]any{"x": 1}, 200)
	ann.must("DELETE", base+"/"+id, nil, 204)
	ann.must("GET", base+"/"+id, nil, 404)
	// not page-safe: rule changes from pages are refused even for the owner
	st, _, _ := admin(t).do("PUT", base+"/_rule", map[string]any{"rule": "private"}, true)
	if st != 403 {
		t.Fatalf("rule via page: %d", st)
	}
	// non-objects refused
	st, _, _ = ann.do("POST", base, `[1,2]`, true)
	if st != 400 {
		t.Fatalf("array body: %d", st)
	}
	// viewers can't change rules
	st, _, _ = ann.do("PUT", base+"/_rule", map[string]any{"rule": "open"}, false)
	if st != 403 {
		t.Fatalf("viewer rule: %d", st)
	}
	// strangers see nothing
	st, _, _ = as(t, "zed@x.test", "Zed", "").do("GET", base, nil, true)
	if st != 404 {
		t.Fatalf("stranger: %d", st)
	}
}

func TestRules(t *testing.T) {
	slug := newSite(t, "ann@x.test", "bob@x.test", "editor:eve@x.test")
	ann := as(t, "ann@x.test", "Ann", "")
	bob := as(t, "bob@x.test", "Bob", "Finance")
	eve := as(t, "eve@x.test", "Eve", "")
	adm := admin(t)
	coll := func(c string) string { return "/api/v1/sites/" + slug + "/db/" + c }
	setRule := func(c, r string) { adm.mustApp("PUT", coll(c)+"/_rule", map[string]any{"rule": r}, 200) }

	// own: read all, edit own only
	a1 := ann.must("POST", coll("own"), map[string]any{"v": 1}, 201)["_id"].(string)
	if n := len(items(bob.must("GET", coll("own"), nil, 200))); n != 1 {
		t.Fatalf("own: bob sees %d", n)
	}
	bob.must("PATCH", coll("own")+"/"+a1, map[string]any{"v": 2}, 403)
	bob.must("DELETE", coll("own")+"/"+a1, nil, 403)
	eve.must("PATCH", coll("own")+"/"+a1, map[string]any{"v": 3}, 200) // editors bypass

	// private: only own visible
	setRule("priv", "private")
	p1 := ann.must("POST", coll("priv"), map[string]any{"secret": "a"}, 201)["_id"].(string)
	bob.must("POST", coll("priv"), map[string]any{"secret": "b"}, 201)
	if l := items(bob.must("GET", coll("priv"), nil, 200)); len(l) != 1 || l[0]["secret"] != "b" {
		t.Fatalf("private list: %v", l)
	}
	bob.must("GET", coll("priv")+"/"+p1, nil, 404)
	bob.must("DELETE", coll("priv")+"/"+p1, nil, 404)
	if c := bob.must("GET", coll("priv")+"/_count", nil, 200)["count"].(float64); c != 1 {
		t.Fatalf("private count %v", c)
	}
	if c := eve.must("GET", coll("priv")+"/_count", nil, 200)["count"].(float64); c != 2 {
		t.Fatalf("editor count %v", c)
	}
	_, _, exp := bob.do("GET", coll("priv")+"/_export?format=csv", nil, true)
	if strings.Contains(string(exp), ",a,") || !strings.Contains(string(exp), "b") {
		t.Fatalf("private export leaked: %s", exp)
	}

	// read: viewers can't write
	setRule("ro", "read")
	bob.must("POST", coll("ro"), map[string]any{"x": 1}, 403)
	eve.must("POST", coll("ro"), map[string]any{"x": 1}, 201)
	if len(items(bob.must("GET", coll("ro"), nil, 200))) != 1 {
		t.Fatal("read: viewer can't read")
	}

	// append: add, can't change own, sees only own
	setRule("app", "append")
	ap := ann.must("POST", coll("app"), map[string]any{"fb": "nice"}, 201)["_id"].(string)
	ann.must("PATCH", coll("app")+"/"+ap, map[string]any{"fb": "meh"}, 403)
	if len(items(bob.must("GET", coll("app"), nil, 200))) != 0 {
		t.Fatal("append: bob sees ann's entry")
	}

	// open: anyone edits anything
	setRule("open", "open")
	o1 := ann.must("POST", coll("open"), map[string]any{"x": 1}, 201)["_id"].(string)
	bob.must("PATCH", coll("open")+"/"+o1, map[string]any{"x": 2}, 200)
	bob.must("DELETE", coll("open")+"/"+o1, nil, 204)

	// group: members full access, others read
	setRule("fin", "group:finance")
	g1 := bob.must("POST", coll("fin"), map[string]any{"amt": 5}, 201)["_id"].(string)
	ann.must("POST", coll("fin"), map[string]any{"amt": 6}, 403)
	ann.must("GET", coll("fin")+"/"+g1, nil, 200)
	// bob's group shows up in me.groups because a rule uses it
	me := bob.must("GET", "/api/v1/sites/"+slug+"/me", nil, 200)
	if gs := me["groups"].([]any); len(gs) != 1 || gs[0] != "Finance" || me["role"] != "viewer" {
		t.Fatalf("me: %v", me)
	}
	if bob.must("GET", "/api/v1/sites/"+slug+"/me/groups/Finance", nil, 200)["member"] != true {
		t.Fatal("inGroup")
	}

	// bad rule
	st, _, _ := adm.do("PUT", coll("x")+"/_rule", map[string]any{"rule": "everyone"}, false)
	if st != 400 {
		t.Fatalf("bad rule %d", st)
	}
	// collections listing: counts only for editors
	cs := bob.must("GET", "/api/v1/sites/"+slug+"/db", nil, 200)["collections"].([]any)
	if _, ok := cs[0].(map[string]any)["count"]; ok {
		t.Fatal("viewer saw counts")
	}
	cs = eve.must("GET", "/api/v1/sites/"+slug+"/db", nil, 200)["collections"].([]any)
	if _, ok := cs[0].(map[string]any)["count"]; !ok {
		t.Fatal("editor missing counts")
	}
	// drop (editor, not page-safe)
	st, _, _ = eve.do("DELETE", coll("open"), nil, true)
	if st != 403 {
		t.Fatalf("drop via page %d", st)
	}
	st, _, _ = eve.do("DELETE", coll("own"), nil, false)
	if st != 200 {
		t.Fatalf("drop %d", st)
	}
}

func TestQueries(t *testing.T) {
	slug := newSite(t, "ann@x.test")
	ann := as(t, "ann@x.test", "Ann", "")
	adm := admin(t)
	base := "/api/v1/sites/" + slug + "/db/items"
	for i := 0; i < 25; i++ {
		c := adm
		if i%5 == 0 {
			c = ann
		}
		doc := map[string]any{"n": i, "name": fmt.Sprintf("Item %02d", i), "tags": []string{"t" + fmt.Sprint(i%3)}, "done": i%2 == 0, "meta": map[string]any{"w": i * 10}}
		if i == 7 {
			delete(doc, "n")
		}
		c.must("POST", base, doc, 201)
	}
	q := func(where any, order string, limit int, after string) map[string]any {
		body := map[string]any{"where": where, "order": order, "limit": limit, "after": after}
		return ann.must("POST", base+"/_query", body, 200)
	}
	if n := len(items(q(map[string]any{"n": map[string]any{"$gte": 20}}, "", 0, ""))); n != 5 {
		t.Fatalf("$gte: %d", n)
	}
	if n := len(items(q(map[string]any{"n": map[string]any{"$gt": 3, "$lt": 6}}, "", 0, ""))); n != 2 {
		t.Fatalf("range: %d", n)
	}
	if n := len(items(q(map[string]any{"n": map[string]any{"$in": []int{1, 2, 3}}}, "", 0, ""))); n != 3 {
		t.Fatalf("$in: %d", n)
	}
	if n := len(items(q(map[string]any{"n": map[string]any{"$ne": 1}}, "", 0, ""))); n != 24 {
		t.Fatalf("$ne: %d", n)
	}
	if n := len(items(q(map[string]any{"tags": map[string]any{"$contains": "t1"}}, "", 0, ""))); n != 8 {
		t.Fatalf("$contains array: %d", n)
	}
	if n := len(items(q(map[string]any{"name": map[string]any{"$contains": "item 1"}}, "", 0, ""))); n != 10 {
		t.Fatalf("$contains text: %d", n)
	}
	if n := len(items(q(map[string]any{"done": true}, "", 0, ""))); n != 13 {
		t.Fatalf("bool eq: %d", n)
	}
	if n := len(items(q(map[string]any{"meta.w": 50}, "", 0, ""))); n != 1 {
		t.Fatalf("nested: %d", n)
	}
	if n := len(items(q(map[string]any{"_author": "me"}, "", 0, ""))); n != 5 {
		t.Fatalf("_author me: %d", n)
	}
	if n := len(items(q(map[string]any{"n": nil}, "", 0, ""))); n != 1 {
		t.Fatalf("null: %d", n)
	}
	if n := len(items(q(map[string]any{"$or": []any{map[string]any{"n": 1}, map[string]any{"n": 2}}}, "", 0, ""))); n != 2 {
		t.Fatalf("$or: %d", n)
	}
	// ISO dates on meta timestamps
	if n := len(items(q(map[string]any{"_created": map[string]any{"$gte": "2000-01-01"}}, "", 0, ""))); n != 25 {
		t.Fatalf("_created ISO $gte: %d", n)
	}
	if n := len(items(q(map[string]any{"_created": map[string]any{"$lt": "2000-01-01T00:00:00Z"}}, "", 0, ""))); n != 0 {
		t.Fatalf("_created ISO $lt: %d", n)
	}
	// GET with where in the query string
	w, _ := json.Marshal(map[string]any{"n": map[string]any{"$lte": 2}})
	if n := len(items(ann.must("GET", base+"?where="+url.QueryEscape(string(w)), nil, 200))); n != 3 {
		t.Fatalf("GET where: %d", n)
	}
	// ordering
	l := items(q(nil, "-n", 3, ""))
	if l[0]["n"].(float64) != 24 || l[2]["n"].(float64) != 22 {
		t.Fatalf("order -n: %v", l)
	}
	l = items(q(nil, "-_created", 1, ""))
	if l[0]["n"].(float64) != 24 {
		t.Fatalf("order -_created: %v", l)
	}
	// pagination over every order, including NULLs (item 7 has no n)
	for _, ord := range []string{"", "-_created", "n", "-n", "name", "-meta.w", "done"} {
		seen := map[string]bool{}
		after := ""
		pages := 0
		for {
			res := q(nil, ord, 4, after)
			for _, it := range items(res) {
				id := it["_id"].(string)
				if seen[id] {
					t.Fatalf("order %q: duplicate %s", ord, id)
				}
				seen[id] = true
			}
			pages++
			nx, _ := res["next"].(string)
			if nx == "" {
				break
			}
			after = nx
			if pages > 20 {
				t.Fatal("pagination loop")
			}
		}
		if len(seen) != 25 {
			t.Fatalf("order %q: paged %d docs", ord, len(seen))
		}
	}
	// errors
	st, _, _ := ann.do("POST", base+"/_query", map[string]any{"where": map[string]any{"n": map[string]any{"$regex": "x"}}}, true)
	if st != 400 {
		t.Fatalf("unknown op %d", st)
	}
	st, _, _ = ann.do("POST", base+"/_query", map[string]any{"order": "a'b"}, true)
	if st != 400 {
		t.Fatalf("bad field %d", st)
	}
	// limit cap
	if n := len(items(ann.must("GET", base+"?limit=10000", nil, 200))); n != 25 {
		t.Fatalf("limit: %d", n)
	}
}

func TestExportImportAndLimits(t *testing.T) {
	slug := newSite(t)
	adm := admin(t)
	base := "/api/v1/sites/" + slug + "/db/people"
	req, _ := http.NewRequest("POST", srv.URL+base+"/_import", strings.NewReader("name,age,active,zip\nAda,36,true,0123\nAlan,41,false,\n"))
	req.Header.Set("X-Bunker", "1")
	req.Header.Set("Content-Type", "text/csv")
	res, _ := http.DefaultClient.Do(req)
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"imported":2`) {
		t.Fatalf("import: %d %s", res.StatusCode, b)
	}
	l := items(adm.must("GET", base+"?order=name", nil, 200))
	if l[0]["age"].(float64) != 36 || l[0]["active"] != true || l[0]["zip"] != "0123" || l[1]["zip"] != nil {
		t.Fatalf("import types: %v", l)
	}
	_, _, csvb := adm.do("GET", base+"/_export?format=csv", nil, true)
	r := bufio.NewReader(bytes.NewReader(csvb))
	head, _ := r.ReadString('\n')
	if !strings.HasPrefix(head, "_id,active,age,name,zip,_author") {
		t.Fatalf("csv header %q", head)
	}
	_, _, js := adm.do("GET", base+"/_export", nil, true)
	var arr []map[string]any
	if json.Unmarshal(js, &arr) != nil || len(arr) != 2 {
		t.Fatalf("json export %s", js)
	}
	// JSON array import, replace mode
	req, _ = http.NewRequest("POST", srv.URL+base+"/_import?mode=replace", strings.NewReader(`[{"name":"Grace"}]`))
	req.Header.Set("X-Bunker", "1")
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if n := len(items(adm.must("GET", base, nil, 200))); n != 1 {
		t.Fatalf("replace: %d", n)
	}
	// 1 MB per doc
	big := strings.Repeat("x", 1100<<10)
	st, m, _ := adm.do("POST", base, map[string]any{"b": big}, true)
	if st != 413 {
		t.Fatalf("big doc: %d %v", st, m)
	}
	// doc-count quota (BUNKER_DB_MAX_DOCS=1000 in TestMain)
	var docs []map[string]any
	for i := 0; i < 1000; i++ {
		docs = append(docs, map[string]any{"i": i})
	}
	j, _ := json.Marshal(docs)
	req, _ = http.NewRequest("POST", srv.URL+base+"/_import", bytes.NewReader(j))
	req.Header.Set("X-Bunker", "1")
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 429 {
		t.Fatalf("quota import: %d", res.StatusCode)
	}
}

// sse reads events from a stream into a channel.
func sse(t *testing.T, c *client, slug, chans string) (<-chan map[string]any, func()) {
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/sites/"+slug+"/events?ch="+url.QueryEscape(chans), nil)
	req.Header.Set("X-Bunker-Page", "1")
	res, err := c.hc.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("sse: %v %v", err, res.Status)
	}
	out := make(chan map[string]any, 100)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "data: ") {
				var m map[string]any
				json.Unmarshal([]byte(line[6:]), &m)
				out <- m
			}
		}
		close(out)
	}()
	return out, func() { res.Body.Close() }
}

func next(t *testing.T, ch <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for an event")
	}
	return nil
}

func none(t *testing.T, ch <-chan map[string]any) {
	t.Helper()
	select {
	case m := <-ch:
		t.Fatalf("unexpected event %v", m)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestLiveUpdates(t *testing.T) {
	slug := newSite(t, "ann@x.test", "bob@x.test")
	ann := as(t, "ann@x.test", "Ann", "")
	bob := as(t, "bob@x.test", "Bob", "")
	adm := admin(t)
	base := "/api/v1/sites/" + slug + "/db/"
	adm.mustApp("PUT", base+"secret/_rule", map[string]any{"rule": "private"}, 200)

	bobEv, stopBob := sse(t, bob, slug, "db:orders,db:secret,room:lobby,presence:lobby,deploy")
	defer stopBob()
	if m := next(t, bobEv); m["ch"] != "presence:lobby" || m["data"].(map[string]any)["type"] != "sync" {
		t.Fatalf("sync: %v", m)
	}
	time.Sleep(50 * time.Millisecond)

	d := ann.must("POST", base+"orders", map[string]any{"meal": "Soup"}, 201)
	m := next(t, bobEv)
	ev := m["data"].(map[string]any)
	if m["ch"] != "db:orders" || ev["type"] != "added" || ev["doc"].(map[string]any)["meal"] != "Soup" {
		t.Fatalf("added: %v", m)
	}
	ann.must("PATCH", base+"orders/"+d["_id"].(string), map[string]any{"meal": "Stew"}, 200)
	if ev := next(t, bobEv)["data"].(map[string]any); ev["type"] != "updated" {
		t.Fatalf("updated: %v", ev)
	}
	ann.must("DELETE", base+"orders/"+d["_id"].(string), nil, 204)
	if ev := next(t, bobEv)["data"].(map[string]any); ev["type"] != "deleted" || ev["doc"].(map[string]any)["_id"] != d["_id"] {
		t.Fatalf("deleted: %v", ev)
	}

	// private collection: bob must not see ann's doc, but sees his own
	ann.must("POST", base+"secret", map[string]any{"pin": 1234}, 201)
	none(t, bobEv)
	bob.must("POST", base+"secret", map[string]any{"pin": 1}, 201)
	if m := next(t, bobEv); m["ch"] != "db:secret" {
		t.Fatalf("own private: %v", m)
	}

	// rooms
	ann.must("POST", "/api/v1/sites/"+slug+"/rooms/lobby", map[string]any{"data": map[string]any{"hi": true}}, 200)
	m = next(t, bobEv)
	if m["ch"] != "room:lobby" || m["data"].(map[string]any)["from"].(map[string]any)["name"] != "Ann" {
		t.Fatalf("room: %v", m)
	}
	// presence
	pr := ann.must("POST", "/api/v1/sites/"+slug+"/rooms/lobby/presence", map[string]any{"state": map[string]any{"cursor": 1}}, 200)
	if len(pr["members"].([]any)) != 1 {
		t.Fatalf("presence: %v", pr)
	}
	m = next(t, bobEv)
	if m["ch"] != "presence:lobby" || m["data"].(map[string]any)["type"] != "join" {
		t.Fatalf("join: %v", m)
	}
	ann.must("DELETE", "/api/v1/sites/"+slug+"/rooms/lobby/presence", nil, 204)
	if m := next(t, bobEv); m["data"].(map[string]any)["type"] != "leave" {
		t.Fatalf("leave: %v", m)
	}

	// deploy notice
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/sites/"+slug+"/deploy", strings.NewReader(`{"files":{"index.html":{"content":"<h1>v2</h1>"}}}`))
	req.Header.Set("X-Bunker", "1")
	req.Header.Set("Content-Type", "application/json")
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("deploy %d", res.StatusCode)
	}
	if m := next(t, bobEv); m["ch"] != "deploy" || m["data"].(map[string]any)["type"] != "deploy" {
		t.Fatalf("deploy: %v", m)
	}

	// bad channel, unknown site
	req, _ = http.NewRequest("GET", srv.URL+"/api/v1/sites/"+slug+"/events?ch=bogus:x", nil)
	res, _ = bob.hc.Do(req)
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("bad channel %d", res.StatusCode)
	}
}

func TestConnectionCap(t *testing.T) {
	slug := newSite(t, "cap@x.test")
	c := as(t, "cap@x.test", "Cap", "")
	var stops []func()
	for i := 0; i < 20; i++ {
		_, stop := sse(t, c, slug, "deploy")
		stops = append(stops, stop)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/sites/"+slug+"/events?ch=deploy", nil)
	res, _ := c.hc.Do(req)
	res.Body.Close()
	if res.StatusCode != 429 {
		t.Fatalf("21st connection: %d", res.StatusCode)
	}
	for _, s := range stops {
		s()
	}
	// slots are released on disconnect
	deadline := time.Now().Add(3 * time.Second)
	for {
		res, _ := c.hc.Get(srv.URL + "/api/v1/sites/" + slug + "/events?ch=deploy")
		ok := res.StatusCode == 200
		res.Body.Close()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("connection slots not released")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestKV(t *testing.T) {
	slug := newSite(t, "ann@x.test", "bob@x.test")
	ann := as(t, "ann@x.test", "Ann", "")
	bob := as(t, "bob@x.test", "Bob", "")
	kv := "/api/v1/sites/" + slug + "/kv"
	ann.must("PUT", kv+"/theme", map[string]any{"value": "dark"}, 200)
	ann.must("PUT", kv+"/"+url.PathEscape("ls:a/b"), map[string]any{"value": map[string]any{"x": 1}}, 200)
	if v := ann.must("GET", kv+"/theme", nil, 200); v["value"] != "dark" || v["exists"] != true {
		t.Fatalf("get: %v", v)
	}
	if v := bob.must("GET", kv+"/theme", nil, 200); v["value"] != nil || v["exists"] != false {
		t.Fatalf("isolation: %v", v)
	}
	l := ann.must("GET", kv+"?values=1&prefix=ls:", nil, 200)
	if ks := l["keys"].([]any); len(ks) != 1 || ks[0] != "ls:a/b" {
		t.Fatalf("list: %v", l)
	}
	ann.must("DELETE", kv+"/theme", nil, 204)
	if v := ann.must("GET", kv+"/theme", nil, 200); v["exists"] != false {
		t.Fatal("delete")
	}
	st, _, _ := ann.do("PUT", kv+"/big", map[string]any{"value": strings.Repeat("x", 70<<10)}, true)
	if st != 413 {
		t.Fatalf("big value %d", st)
	}
	ann.must("DELETE", kv, nil, 200)
	if ks := ann.must("GET", kv, nil, 200)["keys"].([]any); len(ks) != 0 {
		t.Fatalf("clear: %v", ks)
	}
}

// TestShutdown must run last: it closes the live-update hub.
func TestShutdown(t *testing.T) {
	slug := newSite(t, "sd@x.test")
	c := as(t, "sd@x.test", "Sd", "")
	ev, stop := sse(t, c, slug, "deploy")
	defer stop()
	theApp.Emit(context.Background(), "shutdown", nil)
	select {
	case _, ok := <-ev:
		for ok {
			_, ok = <-ev
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream not closed on shutdown")
	}
	res, _ := c.hc.Get(srv.URL + "/api/v1/sites/" + slug + "/events?ch=deploy")
	res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatalf("after shutdown: %d", res.StatusCode)
	}
}
