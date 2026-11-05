package main

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// result is one scenario's numbers.
type result struct {
	Name       string
	Requests   int64
	Errors     int64
	Elapsed    time.Duration
	P50, P99   time.Duration
	AllocsReq  float64 // server mallocs per request (child mode only)
	BytesReq   float64 // server bytes allocated per request
	Note       string
	FirstError string
}

func (r result) rps() float64 { return float64(r.Requests) / r.Elapsed.Seconds() }

func newClient(conc int) *http.Client {
	tr := &http.Transport{
		MaxIdleConns:        conc * 2,
		MaxIdleConnsPerHost: conc * 2,
		DisableCompression:  true, // we set Accept-Encoding ourselves and never decode
		IdleConnTimeout:     90 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: 60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// load runs conc workers for d, each calling mk(worker, i) for a request.
// A response counts as an error when ok(status) is false.
func (h *harness) load(name string, d time.Duration, conc int, mk func(w, i int) *http.Request, ok func(int) bool) result {
	if ok == nil {
		ok = func(s int) bool { return s == 200 }
	}
	c := newClient(conc)
	defer c.CloseIdleConnections()
	// Warm up connections and caches.
	warm := time.Now().Add(d / 5)
	var wg sync.WaitGroup
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; time.Now().Before(warm); i++ {
				do(c, mk(w, i))
			}
		}(w)
	}
	wg.Wait()

	stopProf := h.startProfile(name, d)
	before := h.stats(false)
	var total, errs atomic.Int64
	var firstErr atomic.Value
	lats := make([][]time.Duration, conc)
	start := time.Now()
	end := start.Add(d)
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			l := make([]time.Duration, 0, 4096)
			for i := 0; ; i++ {
				t0 := time.Now()
				if !t0.Before(end) {
					break
				}
				st, err := do(c, mk(w, i))
				l = append(l, time.Since(t0))
				total.Add(1)
				if err != nil || !ok(st) {
					if errs.Add(1) == 1 {
						firstErr.Store(fmt.Sprintf("status %d err %v", st, err))
					}
				}
			}
			lats[w] = l
		}(w)
	}
	wg.Wait()
	el := time.Since(start)
	after := h.stats(false)
	stopProf()
	r := result{Name: name, Requests: total.Load(), Errors: errs.Load(), Elapsed: el}
	r.P50, r.P99 = percentiles(lats)
	if before != nil && after != nil && r.Requests > 0 {
		r.AllocsReq = float64(after.Mallocs-before.Mallocs) / float64(r.Requests)
		r.BytesReq = float64(after.TotalAlloc-before.TotalAlloc) / float64(r.Requests)
	}
	if v, ok := firstErr.Load().(string); ok {
		r.FirstError = v
	}
	h.report(r)
	return r
}

func do(c *http.Client, req *http.Request) (int, error) {
	res, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode, nil
}

func percentiles(ls [][]time.Duration) (p50, p99 time.Duration) {
	var all []time.Duration
	for _, l := range ls {
		all = append(all, l...)
	}
	if len(all) == 0 {
		return 0, 0
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	return all[len(all)*50/100], all[min(len(all)-1, len(all)*99/100)]
}

func (h *harness) report(r result) {
	h.results = append(h.results, r)
	fmt.Printf("  %-28s %9.0f req/s  p50 %-9s p99 %-9s allocs/req %7.1f  KB/req %6.1f  n=%d err=%d %s\n",
		r.Name, r.rps(), round(r.P50), round(r.P99), r.AllocsReq, r.BytesReq/1024, r.Requests, r.Errors, r.Note)
	if r.FirstError != "" {
		fmt.Printf("    first error: %s\n", r.FirstError)
	}
}

func round(d time.Duration) string {
	switch {
	case d >= time.Second:
		return d.Round(time.Millisecond).String()
	case d >= time.Millisecond:
		return d.Round(10 * time.Microsecond).String()
	}
	return d.Round(time.Microsecond).String()
}

func (h *harness) markdown() string {
	var b strings.Builder
	b.WriteString("| Scenario | req/s | p50 | p99 | allocs/req | KB alloc/req | notes |\n|---|---|---|---|---|---|---|\n")
	for _, r := range h.results {
		note := r.Note
		if r.Errors > 0 {
			note = strings.TrimSpace(note + fmt.Sprintf(" %d errors", r.Errors))
		}
		rps, p50, p99, al, kb := "–", "–", "–", "–", "–"
		if r.Requests > 0 {
			rps = fmt.Sprintf("%.0f", r.rps())
		}
		if r.P50 > 0 {
			p50, p99 = round(r.P50), round(r.P99)
		}
		if r.AllocsReq > 0 {
			al, kb = fmt.Sprintf("%.0f", r.AllocsReq), fmt.Sprintf("%.1f", r.BytesReq/1024)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n", r.Name, rps, p50, p99, al, kb, note)
	}
	return b.String()
}
