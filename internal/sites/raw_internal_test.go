package sites

import (
	"bytes"
	"testing"
	"time"
)

// The raw template cache rewrites with a placeholder and substitutes the
// real prefix later; that must give exactly what rewriting directly gives.
func TestRawTemplateMatchesDirect(t *testing.T) {
	prefix := "/s/my-site/~r/12-3-45-1700000000-AbC_d-ef/"
	cases := []struct {
		body string
		html bool
	}{
		{`<!doctype html><html><head><link rel="stylesheet" href="/app.css"></head><body><img src=/a.png srcset="/a.png 1x, /b.png 2x"><a href='/x'>x</a><script src="/_/bunker.js"></script><a href="//cdn/x">c</a></body></html>`, true},
		{`<html lang="en"><body style="background:url(/bg.png)"><form action="/send"></form><video poster="/p.jpg"></video></body></html>`, true},
		{`no head or html tag, plain text`, true},
		{`.a{background:url(/img/a.png)} @import "/base.css"; .b{background:url("/b.png")} .c{background:url(//cdn/c.png)}`, false},
		{`$1 ${2} /path`, false},
	}
	for _, c := range cases {
		want := transformDirect([]byte(c.body), "index.html", prefix, c.html)
		got := bytes.ReplaceAll(transformDirect([]byte(c.body), "index.html", string(prefixMark), c.html), prefixMark, []byte(prefix))
		if !bytes.Equal(got, want) {
			t.Errorf("mismatch for %q:\n got %s\nwant %s", c.body, got, want)
		}
	}
}

func TestBoundedCache(t *testing.T) {
	c := &boundedCache{maxN: 3, maxBytes: 4096}
	for _, k := range []string{"a", "b", "c"} {
		c.put(k, rawEntry{b: []byte(k), exp: farFuture})
	}
	if _, ok := c.get("a"); !ok {
		t.Fatal("a missing")
	}
	c.put("d", rawEntry{b: []byte("d"), exp: farFuture}) // over maxN: a, b, c become the old generation
	if len(c.cur) != 1 || len(c.old) != 3 {
		t.Fatalf("no rollover: cur %d old %d", len(c.cur), len(c.old))
	}
	if _, ok := c.get("b"); !ok || len(c.cur) != 2 {
		t.Fatal("old-generation hit not promoted")
	}
	c.put("e", rawEntry{b: []byte("e"), exp: farFuture})
	c.put("f", rawEntry{b: []byte("f"), exp: farFuture}) // rollover: a and c are gone
	if _, ok := c.get("a"); ok {
		t.Fatal("entry survived two rollovers")
	}
	for _, k := range []string{"b", "d", "e", "f"} {
		if _, ok := c.get(k); !ok {
			t.Fatalf("%s missing", k)
		}
	}
	c.put("big", rawEntry{b: make([]byte, 2000), exp: farFuture}) // > maxBytes/4: not cached
	if _, ok := c.get("big"); ok {
		t.Fatal("oversized entry cached")
	}
	for i := 0; i < 8; i++ {
		c.put(string(rune('m'+i)), rawEntry{b: make([]byte, 900), exp: farFuture})
		if c.bytes > c.maxBytes {
			t.Fatalf("bytes %d over budget", c.bytes)
		}
	}
	c.put("x", rawEntry{b: []byte("x"), exp: time.Now().Add(-time.Second)})
	if _, ok := c.get("x"); ok {
		t.Fatal("expired entry returned")
	}
}

var farFuture = time.Now().Add(time.Hour)
