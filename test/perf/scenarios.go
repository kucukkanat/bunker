package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const browserAE = "gzip, deflate, br, zstd"

func (h *harness) get(path, cookie string) *http.Request {
	req, _ := http.NewRequest("GET", h.base+path, nil)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	req.Header.Set("Accept-Encoding", browserAE)
	return req
}

// viewer returns (cookie, viewer index) for request i of worker w.
func (h *harness) viewer(w, i int) (string, int) {
	v := (w + i) % len(h.seed.viewers)
	return h.seed.cookies[h.seed.viewers[v]], v
}

func (h *harness) scenShell() {
	sd := h.seed
	h.load("shell page (20 viewers × all sites)", h.duration, h.conc, func(w, i int) *http.Request {
		c, _ := h.viewer(w, i)
		return h.get("/s/"+sd.sites[(w*7+i)%len(sd.sites)]+"/", c)
	}, nil)
	h.load("shell page (1 viewer, 1 site)", h.duration, h.conc, func(w, i int) *http.Request {
		return h.get("/s/"+sd.sites[0]+"/", sd.cookies[sd.viewers[0]])
	}, nil)
}

// Raw content is fetched by the sandboxed iframe: no cookies, ticket in the URL.
func (h *harness) raw(base, file, dest string) *http.Request {
	req := h.get(base+file, "")
	req.Header.Set("Sec-Fetch-Dest", dest)
	return req
}

func (h *harness) scenRaw() {
	sd := h.seed
	hot := sd.tickets[0][0]
	h.load("raw HTML (hot: 1 viewer, 1 site)", h.duration, h.conc, func(w, i int) *http.Request {
		return h.raw(hot, "", "iframe")
	}, nil)
	// 20 viewers × 200 sites = 4,000 distinct (ticket, file) pairs.
	mix := func(w, i int) string {
		k := w*131 + i
		return sd.tickets[k%len(sd.tickets)][(k/len(sd.tickets))%len(sd.sites)]
	}
	h.load("raw HTML (20 viewers × all sites)", h.duration, h.conc, func(w, i int) *http.Request {
		return h.raw(mix(w, i), "", "iframe")
	}, nil)
	h.load("raw CSS (mix)", h.duration, h.conc, func(w, i int) *http.Request {
		return h.raw(mix(w, i), "app.css", "style")
	}, nil)
	h.load("raw JS (mix)", h.duration, h.conc, func(w, i int) *http.Request {
		return h.raw(mix(w, i), "app.js", "script")
	}, nil)
	h.load("raw image (mix)", h.duration, h.conc, func(w, i int) *http.Request {
		return h.raw(mix(w, i), "logo.png", "image")
	}, nil)
	h.load("raw image 304 (If-None-Match)", h.duration, h.conc, func(w, i int) *http.Request {
		req := h.raw(mix(w, i), "logo.png", "image")
		req.Header.Set("If-None-Match", h.etag(mix(0, 0)+"logo.png"))
		return req
	}, func(s int) bool { return s == 304 })
}

var etags sync.Map

func (h *harness) etag(path string) string {
	if v, ok := etags.Load(path); ok {
		return v.(string)
	}
	res, err := httpc.Do(h.raw(path, "", "image"))
	check(err)
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	e := res.Header.Get("ETag")
	etags.Store(path, e)
	return e
}

func (h *harness) scenSDK() {
	h.load("/_/bunker.js", h.duration, h.conc, func(w, i int) *http.Request {
		return h.get("/_/bunker.js", "")
	}, nil)
}

func (h *harness) scenSitesList() {
	var out struct{ Sites []struct{ Slug string } }
	_, err := h.call("GET", "/api/v1/sites", h.seed.cookies[h.seed.viewers[0]], nil, "", &out)
	check(err)
	note := fmt.Sprintf("%d sites in the list", len(out.Sites))
	r := h.load("GET /api/v1/sites (viewers)", h.duration, h.conc, func(w, i int) *http.Request {
		c, _ := h.viewer(w, i)
		return h.get("/api/v1/sites", c)
	}, nil)
	h.results[len(h.results)-1].Note = note
	_ = r
	h.load("GET /api/v1/sites (owner)", h.duration, h.conc, func(w, i int) *http.Request {
		return h.get("/api/v1/sites", h.seed.cookies[w%owners])
	}, nil)
}

func (h *harness) page(req *http.Request) *http.Request {
	req.Header.Set("X-Bunker", "1")
	req.Header.Set("X-Bunker-Page", "1")
	return req
}

func (h *harness) scenDB() {
	sd := h.seed
	p := "/api/v1/sites/" + sd.dataSite + "/db/items"
	h.load("db list (limit 50)", h.duration, h.conc, func(w, i int) *http.Request {
		c, _ := h.viewer(w, i)
		return h.page(h.get(p+"?limit=50", c))
	}, nil)
	where := url.QueryEscape(`{"status":"open"}`)
	h.load("db query (where+order, 20)", h.duration, h.conc, func(w, i int) *http.Request {
		c, _ := h.viewer(w, i)
		return h.page(h.get(p+"?where="+where+"&order=-amount&limit=20", c))
	}, nil)
	h.load("db get by id", h.duration, h.conc, func(w, i int) *http.Request {
		c, _ := h.viewer(w, i)
		return h.page(h.get(p+"/"+sd.docIDs[(w*31+i)%len(sd.docIDs)], c))
	}, nil)
	h.load("db add", h.duration, h.conc, func(w, i int) *http.Request {
		c, _ := h.viewer(w, i)
		req, _ := http.NewRequest("POST", h.base+p, bytes.NewReader([]byte(`{"status":"open","title":"added","n":`+strconv.Itoa(i)+`}`)))
		req.Header.Set("Cookie", c)
		req.Header.Set("Content-Type", "application/json")
		return h.page(req)
	}, func(s int) bool { return s == 200 || s == 201 })
}

// ---- SSE ----

func (h *harness) scenSSE(nConns, nEvents, rate int) {
	sd := h.seed
	fmt.Printf("  SSE: opening %d connections...\n", nConns)
	before := h.stats(true)
	rssBefore := h.rss()
	// Users after the viewers each get up to 20 streams (the per-user cap).
	firstUser := viewerFrom + numViewers
	usable := len(sd.cookies) - firstUser
	if usable*20 < nConns {
		check(fmt.Errorf("need %d users for %d SSE connections", (nConns+19)/20+firstUser, nConns))
	}
	type conn struct {
		c    net.Conn
		lats []int64
		got  int
	}
	conns := make([]*conn, nConns)
	path := "/api/v1/sites/" + sd.dataSite + "/events?ch=db:items"
	host := h.base[len("http://"):]
	var openErr atomic.Value
	var ready sync.WaitGroup
	var done sync.WaitGroup
	var received atomic.Int64
	t0 := time.Now()
	sem := make(chan struct{}, 128)
	for i := 0; i < nConns; i++ {
		ready.Add(1)
		done.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer done.Done()
			cookie := sd.cookies[firstUser+i/20]
			nc, err := net.Dial("tcp", host)
			if err != nil {
				openErr.Store(err)
				<-sem
				ready.Done()
				return
			}
			fmt.Fprintf(nc, "GET %s HTTP/1.1\r\nHost: %s\r\nCookie: %s\r\nAccept: text/event-stream\r\nX-Bunker-Page: 1\r\n\r\n", path, host, cookie)
			br := bufio.NewReaderSize(nc, 4096)
			c := &conn{c: nc, lats: make([]int64, 0, nEvents)}
			conns[i] = c
			// Wait for the ": connected" comment.
			for {
				line, err := br.ReadSlice('\n')
				if err != nil {
					openErr.Store(err)
					<-sem
					ready.Done()
					return
				}
				if bytes.HasPrefix(line, []byte(": connected")) {
					break
				}
				if bytes.HasPrefix(line, []byte("HTTP/1.1")) && !bytes.Contains(line, []byte(" 200 ")) {
					openErr.Store(fmt.Errorf("SSE open: %s", bytes.TrimSpace(line)))
				}
			}
			<-sem
			ready.Done()
			marker := []byte(`"t":`)
			for {
				line, err := br.ReadSlice('\n')
				if err != nil {
					return
				}
				if !bytes.HasPrefix(line, []byte("data: ")) {
					continue
				}
				k := bytes.Index(line, marker)
				if k < 0 {
					continue
				}
				rest := line[k+len(marker):]
				e := bytes.IndexAny(rest, ",}")
				if e < 0 {
					continue
				}
				ts, err := strconv.ParseInt(string(rest[:e]), 10, 64)
				if err != nil {
					continue
				}
				c.lats = append(c.lats, time.Now().UnixNano()-ts)
				c.got++
				received.Add(1)
			}
		}(i)
	}
	ready.Wait()
	openTime := time.Since(t0)
	if v := openErr.Load(); v != nil {
		fmt.Printf("    open error: %v\n", v)
	}
	time.Sleep(2 * time.Second)
	after := h.stats(true)
	rssAfter := h.rss()
	perConnNote := ""
	if before != nil && after != nil {
		heap := float64(int64(after.HeapInuse+after.StackInuse) - int64(before.HeapInuse+before.StackInuse))
		perConnNote = fmt.Sprintf("heap+stack %.1f KB/conn, goroutines +%d", heap/float64(nConns)/1024, after.Goroutines-before.Goroutines)
	}
	rssPer := float64(rssAfter-rssBefore) / float64(nConns) / 1024
	fmt.Printf("  SSE: %d open in %s; RSS +%.1f KB/conn; %s\n", nConns, openTime.Round(time.Millisecond), rssPer, perConnNote)
	h.results = append(h.results, result{Name: fmt.Sprintf("SSE: %d connections open", nConns),
		Note: fmt.Sprintf("open in %s, RSS %.0f→%.0f MB (+%.1f KB/conn), %s", openTime.Round(time.Millisecond), float64(rssBefore)/1e6, float64(rssAfter)/1e6, rssPer, perConnNote)})

	// Publish nEvents document changes at rate/s from a few writers.
	stopProf := h.startProfile("sse-fanout", time.Duration(nEvents)*time.Second/time.Duration(rate))
	pubBefore := h.stats(false)
	interval := time.Second / time.Duration(rate)
	pc := newClient(8)
	p := "/api/v1/sites/" + sd.dataSite + "/db/items"
	pubStart := time.Now()
	var pubErrs atomic.Int64
	var pw sync.WaitGroup
	tick := time.NewTicker(interval)
	for i := 0; i < nEvents; i++ {
		<-tick.C
		pw.Add(1)
		go func(i int) {
			defer pw.Done()
			body := fmt.Sprintf(`{"kind":"sse","i":%d,"t":%d}`, i, time.Now().UnixNano())
			req, _ := http.NewRequest("POST", h.base+p, bytes.NewReader([]byte(body)))
			req.Header.Set("Cookie", sd.cookies[viewerFrom+i%numViewers])
			req.Header.Set("Content-Type", "application/json")
			if st, err := do(pc, h.page(req)); err != nil || st != 201 && st != 200 {
				pubErrs.Add(1)
			}
		}(i)
	}
	tick.Stop()
	pw.Wait()
	want := int64(nConns) * int64(nEvents-int(pubErrs.Load()))
	deadline := time.Now().Add(30 * time.Second)
	for received.Load() < want && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	fanTime := time.Since(pubStart)
	pubAfter := h.stats(false)
	rssEnd := h.rss()
	stopProf()
	for _, c := range conns {
		if c != nil {
			c.c.Close()
		}
	}
	done.Wait()
	var all []int64
	for _, c := range conns {
		if c != nil {
			all = append(all, c.lats...)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	r := result{Name: fmt.Sprintf("SSE fan-out: %d changes × %d conns", nEvents, nConns), Requests: 0}
	if len(all) > 0 {
		r.P50 = time.Duration(all[len(all)/2])
		r.P99 = time.Duration(all[min(len(all)-1, len(all)*99/100)])
	}
	if pubBefore != nil && pubAfter != nil && received.Load() > 0 {
		r.AllocsReq = float64(pubAfter.Mallocs-pubBefore.Mallocs) / float64(received.Load())
		r.BytesReq = float64(pubAfter.TotalAlloc-pubBefore.TotalAlloc) / float64(received.Load())
	}
	r.Note = fmt.Sprintf("delivered %d/%d (%.1f%%) in %s at %d changes/s; %.0f deliveries/s; allocs are per delivery; publish errors %d",
		received.Load(), want, 100*float64(received.Load())/float64(max(want, 1)), fanTime.Round(time.Millisecond), rate,
		float64(received.Load())/fanTime.Seconds(), pubErrs.Load())
	if pubAfter != nil {
		r.Note += fmt.Sprintf("; at the end heap %.0f MB, stacks %.0f MB, RSS %.0f MB", float64(pubAfter.HeapInuse)/1e6, float64(pubAfter.StackInuse)/1e6, float64(rssEnd)/1e6)
	}
	h.results = append(h.results, r)
	fmt.Printf("  %s: p50 %s p99 %s; %s\n", r.Name, round(r.P50), round(r.P99), r.Note)
	time.Sleep(time.Second)
}

// ---- uploads ----

func (h *harness) scenUpload(n int) {
	zips := make([][]byte, n)
	for i := range zips {
		zips[i] = zip5MB(uint64(i + 1))
	}
	size := float64(len(zips[0])) / 1e6
	for _, conc := range []int{1, 4} {
		stopProf := h.startProfile(fmt.Sprintf("upload-c%d", conc), 3*time.Second)
		before := h.stats(false)
		var lats []time.Duration
		var mu sync.Mutex
		var errs atomic.Int64
		start := time.Now()
		parallel(n, conc, func(i int) {
			body, ct := multipartFiles(map[string]string{"title": fmt.Sprintf("Upload %d-%d", conc, i)}, map[string][]byte{"site.zip": zips[i]})
			t := time.Now()
			if _, err := h.call("POST", "/api/v1/drop", h.seed.cookies[i%owners], body, ct, nil); err != nil {
				errs.Add(1)
				fmt.Println("    upload error:", err)
			}
			mu.Lock()
			lats = append(lats, time.Since(t))
			mu.Unlock()
		})
		el := time.Since(start)
		after := h.stats(false)
		stopProf()
		r := result{Name: fmt.Sprintf("drop %.1f MB zip (concurrency %d)", size, conc), Requests: int64(n), Errors: errs.Load(), Elapsed: el}
		r.P50, r.P99 = percentiles([][]time.Duration{lats})
		if before != nil && after != nil {
			r.AllocsReq = float64(after.Mallocs-before.Mallocs) / float64(n)
			r.BytesReq = float64(after.TotalAlloc-before.TotalAlloc) / float64(n)
		}
		r.Note = fmt.Sprintf("%.1f MB/s", size*float64(n)/el.Seconds())
		h.report(r)
	}
}

// ---- idle RSS ----

func (h *harness) idleRSS(start int64, nSites int) {
	time.Sleep(5 * time.Second)
	idle := h.rss()
	s := h.stats(false)
	heap := ""
	if s != nil {
		heap = fmt.Sprintf(", heap in use %.1f MB, %d goroutines", float64(s.HeapInuse)/1e6, s.Goroutines)
	}
	fmt.Printf("  RSS: %.1f MB at start, %.1f MB idle after seeding %d sites%s\n", float64(start)/1e6, float64(idle)/1e6, nSites, heap)
	h.results = append(h.results, result{Name: "idle RSS", Note: fmt.Sprintf("%.1f MB empty → %.1f MB idle after seeding %d sites%s", float64(start)/1e6, float64(idle)/1e6, nSites, heap)})
}
