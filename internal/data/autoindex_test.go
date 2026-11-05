package data_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kucukkanat/bunker/internal/data"
)

// seedMixed imports n documents with ties, missing fields, NULLs, nested
// fields and mixed types, plus 10 entries added by ann (for "me").
func seedMixed(t *testing.T, slug, coll string, n int) {
	t.Helper()
	var docs []map[string]any
	for i := 0; i < n; i++ {
		var v any
		switch i % 7 {
		case 0:
			v = i
		case 1:
			v = float64(i) + 0.5
		case 2:
			v = fmt.Sprintf("s%d", i%10)
		case 3:
			v = i%2 == 0
		case 4:
			v = nil
		case 5:
			v = []any{1, 2}
		case 6:
			v = map[string]any{"a": i % 3}
		}
		d := map[string]any{"grp": i % 5, "name": fmt.Sprintf("Item %02d", i), "v": v,
			"meta": map[string]any{"w": i % 7 * 10, "tag": fmt.Sprintf("t%d", i%3)},
			"tags": []string{fmt.Sprintf("t%d", i%3), "x"}, "done": i%2 == 0}
		if i%9 != 4 {
			d["n"] = i
		}
		if i%11 == 3 {
			delete(d, "v")
		}
		docs = append(docs, d)
	}
	j, _ := json.Marshal(docs)
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/sites/"+slug+"/db/"+coll+"/_import", strings.NewReader(string(j)))
	req.Header.Set("X-Bunker", "1")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("import: %v %v", err, res)
	}
	res.Body.Close()
	ann := as(t, "ann@x.test", "Ann", "")
	for i := 0; i < 10; i++ {
		ann.must("POST", "/api/v1/sites/"+slug+"/db/"+coll, map[string]any{"grp": i % 5, "n": 1000 + i, "name": fmt.Sprintf("Ann %d", i), "v": i}, 201)
	}
}

var spWheres = []string{
	``, `{"grp":2}`, `{"grp":{"$gt":1,"$lte":3}}`, `{"grp":{"$gte":3}}`, `{"grp":{"$lt":2}}`, `{"grp":{"$in":[1,4]}}`, `{"grp":{"$nin":[1,4]}}`,
	`{"grp":{"$ne":2}}`, `{"n":null}`, `{"n":{"$ne":null}}`, `{"n":{"$exists":false}}`, `{"v":{"$exists":true}}`, `{"meta.w":30}`, `{"meta.w":{"$gte":20}}`,
	`{"v":{"$gt":10}}`, `{"v":{"$lte":"s5"}}`, `{"v":"s3"}`, `{"v":true}`, `{"v":[1,2]}`, `{"v":{"a":1}}`, `{"v":{"$ne":[1,2]}}`, `{"v":null}`,
	`{"tags":{"$contains":"t1"}}`, `{"name":{"$contains":"item 1"}}`, `{"$or":[{"grp":1},{"n":{"$lt":5}}]}`,
	`{"$and":[{"grp":{"$gte":1}},{"done":true}]}`, `{"_author":"me"}`, `{"_author":"me","grp":3}`, `{"_author":{"$in":["me"]}}`,
	`{"_updated":{"$gte":"2000-01-01"}}`, `{"name":{"$gte":"Item 50"}}`, `{"done":false,"meta.w":{"$in":[0,10,20]}}`,
}

var spOrders = []string{"", "-_created", "_id", "grp", "-grp", "n", "-n", "v", "-v", "meta.w", "-meta.w", "name", "-_updated", "_authorName", "-_author"}

// spRun pages through every where × order with a small page size and
// returns the id sequences and counts.
func spRun(t *testing.T, c *client, base string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, w := range spWheres {
		var where any
		if w != "" {
			json.Unmarshal([]byte(w), &where)
		}
		for _, o := range spOrders {
			key := w + " | " + o
			after := ""
			var ids []string
			for pages := 0; ; pages++ {
				if pages > 100 {
					t.Fatalf("%s: pagination loop", key)
				}
				res := c.must("POST", base+"/_query", map[string]any{"where": where, "order": o, "limit": 7, "after": after}, 200)
				for _, it := range items(res) {
					ids = append(ids, it["_id"].(string))
				}
				nx, _ := res["next"].(string)
				if nx == "" {
					break
				}
				after = nx
			}
			out[key] = ids
		}
		cnt := c.must("GET", base+"/_count?where="+url.QueryEscape(w), nil, 200)
		out[w+" | count"] = []string{fmt.Sprint(cnt["count"])}
	}
	return out
}

func siteID(t *testing.T, slug string) int64 {
	var id int64
	if err := theApp.DB.QueryRow(`SELECT id FROM sites WHERE slug=?`, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func indexCount(t *testing.T, site int64, coll string) (recorded, inSchema int) {
	t.Helper()
	theApp.DB.QueryRow(`SELECT COUNT(*) FROM doc_indexes WHERE site_id=? AND coll=?`, site, coll).Scan(&recorded)
	theApp.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='docs' AND sql LIKE ?`, fmt.Sprintf("%%WHERE site_id=%d AND coll='%s'", site, coll)).Scan(&inSchema)
	return
}

func plan(t *testing.T, site int64, coll, where, order, after string) string {
	t.Helper()
	p, err := data.QueryPlan(site, coll, where, order, after)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(p, "; ")
}

func TestAdaptiveIndexesKeepResults(t *testing.T) {
	t.Setenv("BUNKER_DB_INDEX_AFTER", "3")
	t.Setenv("BUNKER_DB_INDEX_MIN_DOCS", "50")
	t.Setenv("BUNKER_DB_INDEX_PER_COLLECTION", "0") // off for the reference run
	t.Setenv("BUNKER_DB_INDEX_PER_SITE", "20")
	slug := newSite(t, "ann@x.test")
	site := siteID(t, slug)
	base := "/api/v1/sites/" + slug + "/db/items"
	seedMixed(t, slug, "items", 90)
	ann := as(t, "ann@x.test", "Ann", "")

	before := spRun(t, ann, base)
	if r, s := indexCount(t, site, "items"); r != 0 || s != 0 {
		t.Fatalf("indexes while off: %d %d", r, s)
	}
	if !strings.Contains(plan(t, site, "items", `{"grp":2}`, "-v", ""), "TEMP B-TREE") {
		t.Fatal("expected a sort without indexes")
	}

	t.Setenv("BUNKER_DB_INDEX_PER_COLLECTION", "20")
	spRun(t, ann, base) // hot: creates the indexes
	data.WaitIndexes()
	r, s := indexCount(t, site, "items")
	// grp, n, v, meta.w, name, done, _updated, _authorName
	if r != 8 || s != 8 {
		t.Fatalf("indexes: recorded %d, in schema %d", r, s)
	}
	after := spRun(t, ann, base)
	if !reflect.DeepEqual(before, after) {
		for k := range before {
			if !reflect.DeepEqual(before[k], after[k]) {
				t.Errorf("%s:\n before %v\n after  %v", k, before[k], after[k])
			}
		}
		t.FailNow()
	}

	// The planner uses them: filters, sorts (no sort step), cursors, nested fields, meta columns.
	for _, c := range []struct{ where, order string }{
		{`{"n":5}`, ""}, {`{"n":{"$in":[1,4]}}`, ""}, {`{"n":{"$gt":10,"$lte":12}}`, ""}, {`{"name":"Item 07"}`, "-_created"},
		{`{"grp":{"$gt":1,"$lte":3}}`, "grp"}, {`{"n":null}`, "n"}, {`{"_author":"me","grp":3}`, "-grp"},
		{``, "-v"}, {``, "v"}, {``, "meta.w"}, {`{"meta.w":30}`, "-meta.w"}, {``, "-_updated"}, {``, "_authorName"},
	} {
		p := plan(t, site, "items", c.where, c.order, "")
		if !strings.Contains(p, "docs_x_") {
			t.Errorf("%s %s: index not used: %s", c.where, c.order, p)
		}
		if c.where == "" && strings.Contains(p, "TEMP B-TREE") {
			t.Errorf("%s: sorted instead of reading the index in order: %s", c.order, p)
		}
	}
	res := ann.must("POST", base+"/_query", map[string]any{"order": "-n", "limit": 5}, 200)
	if p := plan(t, site, "items", "", "-n", res["next"].(string)); !strings.Contains(p, "docs_x_") || strings.Contains(p, "TEMP B-TREE") {
		t.Errorf("cursor page: %s", p)
	}
	// $or / $contains alone aren't index candidates; meta columns with their own indexes aren't either.
	var fields []string
	rows, _ := theApp.DB.Query(`SELECT field FROM doc_indexes WHERE site_id=? ORDER BY field`, site)
	for rows.Next() {
		var f string
		rows.Scan(&f)
		fields = append(fields, f)
	}
	rows.Close()
	if want := []string{"_authorName", "_updated", "done", "grp", "meta.w", "n", "name", "v"}; !reflect.DeepEqual(fields, want) {
		t.Fatalf("indexed fields %v, want %v", fields, want)
	}

	// Writes keep working and stay visible through the indexes.
	adm := admin(t)
	d := adm.must("POST", base, map[string]any{"grp": 2, "n": 999999}, 201)
	l := items(ann.must("POST", base+"/_query", map[string]any{"order": "-n", "limit": 1}, 200))
	if l[0]["_id"] != d["_id"] {
		t.Fatalf("new doc not first by -n: %v", l)
	}
	adm.must("PATCH", base+"/"+d["_id"].(string), map[string]any{"n": nil}, 200)
	if n := len(items(ann.must("POST", base+"/_query", map[string]any{"where": map[string]any{"n": 999999}}, 200))); n != 0 {
		t.Fatalf("updated doc still matches: %d", n)
	}

	// Dropping the collection drops its indexes.
	adm.mustApp("DELETE", base, nil, 200)
	if r, s := indexCount(t, site, "items"); r != 0 || s != 0 {
		t.Fatalf("after drop: recorded %d, in schema %d", r, s)
	}
}

func TestAdaptiveIndexLimitsAndCleanup(t *testing.T) {
	t.Setenv("BUNKER_DB_INDEX_AFTER", "3")
	t.Setenv("BUNKER_DB_INDEX_MIN_DOCS", "50")
	t.Setenv("BUNKER_DB_INDEX_PER_COLLECTION", "2")
	t.Setenv("BUNKER_DB_INDEX_PER_SITE", "3")
	slug := newSite(t, "ann@x.test")
	site := siteID(t, slug)
	ann := as(t, "ann@x.test", "Ann", "")
	seedMixed(t, slug, "big", 60)
	seedMixed(t, slug, "big2", 60)
	seedMixed(t, slug, "small", 20)
	hit := func(coll, order string, n int) {
		for i := 0; i < n; i++ {
			ann.must("GET", "/api/v1/sites/"+slug+"/db/"+coll+"?order="+order+"&limit=1", nil, 200)
		}
		data.WaitIndexes()
	}
	hit("big", "grp", 2)
	if r, _ := indexCount(t, site, "big"); r != 0 {
		t.Fatalf("index before %d queries", 3)
	}
	hit("big", "grp", 1)
	hit("small", "grp", 5) // under DB_INDEX_MIN_DOCS
	if r, _ := indexCount(t, site, "big"); r != 1 {
		t.Fatalf("big: %d indexes", r)
	}
	if r, _ := indexCount(t, site, "small"); r != 0 {
		t.Fatalf("small collection indexed")
	}
	hit("big", "n", 3)
	hit("big", "v", 3) // per-collection cap (2)
	if r, s := indexCount(t, site, "big"); r != 2 || s != 2 {
		t.Fatalf("big after cap: %d %d", r, s)
	}
	hit("big2", "n", 3)
	hit("big2", "v", 3) // per-site cap (3)
	if r, _ := indexCount(t, site, "big2"); r != 1 {
		t.Fatalf("big2 after site cap: %d", r)
	}

	// Unused for a week: the daily sweep drops them.
	data.SweepIndexes(time.Now())
	if r, _ := indexCount(t, site, "big"); r != 2 {
		t.Fatalf("sweep dropped used indexes: %d", r)
	}
	data.SweepIndexes(time.Now().Add(8 * 24 * time.Hour))
	if r, s := indexCount(t, site, "big"); r != 0 || s != 0 {
		t.Fatalf("after sweep: %d %d", r, s)
	}
	// They come back when queried again, and a purged site loses them.
	hit("big", "grp", 3)
	hit("big2", "v", 3)
	if r, _ := indexCount(t, site, "big"); r != 1 {
		t.Fatalf("re-created: %d", r)
	}
	theApp.Emit(context.Background(), "site.purge", site)
	var left int
	theApp.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND sql LIKE ?`, fmt.Sprintf("%%WHERE site_id=%d AND %%", site)).Scan(&left)
	var rec int
	theApp.DB.QueryRow(`SELECT COUNT(*) FROM doc_indexes WHERE site_id=?`, site).Scan(&rec)
	if left != 0 || rec != 0 {
		t.Fatalf("after purge: %d in schema, %d recorded", left, rec)
	}
}
