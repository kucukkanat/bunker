package server

import (
	"fmt"
	"net"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

var (
	mRequests [6]atomic.Int64 // by status class 1xx..5xx (index 0 unused)
	mInflight atomic.Int64
	mDurNanos atomic.Int64
	started   = time.Now()
)

func observe(status int, d time.Duration) {
	c := status / 100
	if c < 1 || c > 5 {
		c = 5
	}
	mRequests[c].Add(1)
	mDurNanos.Add(int64(d))
}

// metricsHandler serves Prometheus text metrics. Access: loopback, or
// Authorization: Bearer $BUNKER_METRICS_TOKEN.
func metricsHandler(a *core.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := core.Env("METRICS_TOKEN", "")
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		ok := (ip != nil && ip.IsLoopback() && r.Header.Get("X-Forwarded-For") == "") || (tok != "" && r.Header.Get("Authorization") == "Bearer "+tok)
		if !ok {
			http.NotFound(w, r)
			return
		}
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		var users, sites, versions int64
		a.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users)
		a.DB.QueryRow(`SELECT COUNT(*) FROM sites WHERE deleted_at IS NULL`).Scan(&sites)
		a.DB.QueryRow(`SELECT COUNT(*) FROM versions`).Scan(&versions)
		st := a.DB.Stats()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# TYPE bunker_http_requests_total counter\n")
		for c := 1; c <= 5; c++ {
			fmt.Fprintf(w, "bunker_http_requests_total{code=\"%dxx\"} %d\n", c, mRequests[c].Load())
		}
		fmt.Fprintf(w, "# TYPE bunker_http_request_seconds_total counter\nbunker_http_request_seconds_total %.3f\n", float64(mDurNanos.Load())/1e9)
		fmt.Fprintf(w, "# TYPE bunker_http_inflight gauge\nbunker_http_inflight %d\n", mInflight.Load())
		fmt.Fprintf(w, "# TYPE bunker_users gauge\nbunker_users %d\n# TYPE bunker_sites gauge\nbunker_sites %d\n# TYPE bunker_versions gauge\nbunker_versions %d\n", users, sites, versions)
		fmt.Fprintf(w, "# TYPE bunker_db_open_connections gauge\nbunker_db_open_connections %d\nbunker_db_wait_count %d\n", st.OpenConnections, st.WaitCount)
		fmt.Fprintf(w, "# TYPE go_goroutines gauge\ngo_goroutines %d\n# TYPE go_memstats_heap_alloc_bytes gauge\ngo_memstats_heap_alloc_bytes %d\n", runtime.NumGoroutine(), ms.HeapAlloc)
		fmt.Fprintf(w, "# TYPE process_uptime_seconds gauge\nprocess_uptime_seconds %.0f\nbunker_build_info{version=%q} 1\n", time.Since(started).Seconds(), core.Version)
	}
}
