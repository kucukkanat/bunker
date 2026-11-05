// Command perf is Bunker's load and profiling harness.
//
//	go run ./test/perf                         # default run, all scenarios
//	go run ./test/perf -only shell,sites-list  # a subset
//	go run ./test/perf -profile /tmp/prof      # also save CPU + alloc profiles
//
// It starts a Bunker server in a child process (the harness binary itself,
// in -child mode, with a loopback-only pprof listener; or -bin <bunker> for
// the real binary, without allocation numbers), on free ports with a temp
// data directory in dev mode, seeds users, sites and documents, then
// measures each scenario. See research/PERF.md.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type harness struct {
	base     string // http://127.0.0.1:port
	pprof    string // http://127.0.0.1:port (child mode only)
	cmd      *exec.Cmd
	dataDir  string
	profDir  string
	results  []result
	seed     *seedData
	duration time.Duration
	conc     int
	startup  time.Duration
}

func main() {
	child := flag.Bool("child", false, "internal: run the server")
	pprofAddr := flag.String("pprof-addr", "", "internal: pprof listen address in -child mode")
	bin := flag.String("bin", "", "run this bunker binary instead of the in-harness server (no allocation numbers)")
	users := flag.Int("users", 300, "users to seed (SSE spreads connections over them, 20 per user)")
	sites := flag.Int("sites", 200, "sites to seed (all visible to the measuring viewers)")
	docs := flag.Int("docs", 10000, "documents in the data-module collection")
	sseConns := flag.Int("sse", 5000, "concurrent /events connections")
	sseEvents := flag.Int("sse-events", 1000, "document changes to publish during the SSE test")
	sseRate := flag.Int("sse-rate", 200, "document changes per second during the SSE test")
	uploads := flag.Int("uploads", 8, "5 MB zip drops to time")
	dur := flag.Duration("d", 5*time.Second, "measurement time per throughput scenario (plus 20% warm-up)")
	conc := flag.Int("c", 64, "concurrent client connections for throughput scenarios")
	only := flag.String("only", "", "comma-separated scenarios (default all): shell,raw,sdk,sites-list,db,sse,upload,rss")
	profDir := flag.String("profile", "", "directory for per-scenario CPU and allocation profiles (child mode)")
	keep := flag.Bool("keep", false, "keep the temp data directory")
	md := flag.Bool("md", true, "print a Markdown table at the end")
	settle := flag.Duration("settle", 30*time.Second, "idle time before the final RSS reading")
	flag.Parse()
	if *child {
		os.Exit(runChild(*pprofAddr))
	}

	want := map[string]bool{}
	for _, s := range strings.Split(*only, ",") {
		if s = strings.TrimSpace(s); s != "" {
			want[s] = true
		}
	}
	run := func(s string) bool { return len(want) == 0 || want[s] }

	h := &harness{duration: *dur, conc: *conc, profDir: *profDir}
	if h.profDir != "" {
		os.MkdirAll(h.profDir, 0o755)
	}
	dir, err := os.MkdirTemp("", "bunker-perf-*")
	check(err)
	h.dataDir = dir
	if !*keep {
		defer os.RemoveAll(dir)
	}
	check(h.start(*bin))
	defer h.stop()
	fmt.Printf("bunker perf: %d CPU, GOMAXPROCS %d, %s, server pid %d ready in %s, data %s\n", runtime.NumCPU(), runtime.GOMAXPROCS(0), runtime.Version(), h.cmd.Process.Pid, h.startup.Round(time.Millisecond), dir)
	rssStart := h.rss()

	t0 := time.Now()
	nUsers := max(*users, (*sseConns+19)/20+30)
	h.seed = h.seedAll(nUsers, *sites, *docs)
	fmt.Printf("seeded %d users, %d sites, %d docs in %s\n", nUsers, *sites, *docs, time.Since(t0).Round(time.Millisecond))

	if run("rss") {
		h.idleRSS(rssStart, *sites)
	}
	if run("shell") {
		h.scenShell()
	}
	if run("raw") {
		h.scenRaw()
	}
	if run("sdk") {
		h.scenSDK()
	}
	if run("sites-list") {
		h.scenSitesList()
	}
	if run("db") {
		h.scenDB()
	}
	if run("sse") {
		h.scenSSE(*sseConns, *sseEvents, *sseRate)
	}
	if run("upload") {
		h.scenUpload(*uploads)
	}
	if run("rss") {
		time.Sleep(3 * time.Second)
		r3 := h.rss()
		h.stats(true) // two GCs: sync.Pools (net/http buffers) are emptied over two cycles
		h.stats(true) // the runtime then returns freed pages to the OS gradually
		time.Sleep(*settle)
		heap := ""
		if s := h.stats(false); s != nil {
			heap = fmt.Sprintf(", heap in use %.1f MB, %d goroutines", float64(s.HeapInuse)/1e6, s.Goroutines)
		}
		note := fmt.Sprintf("%.1f MB 3 s after the last test; %.1f MB after a GC and %s idle%s", float64(r3)/1e6, float64(h.rss())/1e6, *settle, heap)
		if h.profDir != "" && h.pprof != "" {
			if res, err := http.Get(h.pprof + "/debug/pprof/heap"); err == nil {
				if f, err := os.Create(filepath.Join(h.profDir, "heap-at-end.pprof")); err == nil {
					io.Copy(f, res.Body)
					f.Close()
				}
				res.Body.Close()
			}
		}
		h.results = append(h.results, result{Name: "RSS after the whole run", Note: note})
		fmt.Println("  RSS after the whole run:", note)
	}
	if *md {
		fmt.Println()
		fmt.Print(h.markdown())
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "perf:", err)
		os.Exit(1)
	}
}

func freePort() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	check(err)
	defer l.Close()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}

func (h *harness) start(bin string) error {
	port := freePort()
	h.base = "http://127.0.0.1:" + port
	var args []string
	if bin == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		bin = exe
		pp := freePort()
		h.pprof = "http://127.0.0.1:" + pp
		args = []string{"-child", "-pprof-addr", "127.0.0.1:" + pp}
	} else {
		args = []string{"serve"}
	}
	h.cmd = exec.Command(bin, args...)
	h.cmd.Env = append(os.Environ(),
		"BUNKER_DEV=1", "BUNKER_DATA_DIR="+filepath.Join(h.dataDir, "data"),
		"BUNKER_LISTEN=127.0.0.1:"+port, "BUNKER_URL="+h.base, "BUNKER_LOG_FORMAT=text")
	h.cmd.Stdout = os.Stderr
	h.cmd.Stderr = os.Stderr
	t0 := time.Now()
	if err := h.cmd.Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if res, err := http.Get(h.base + "/healthz"); err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				h.startup = time.Since(t0)
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("server didn't start")
}

func (h *harness) stop() {
	if h.cmd != nil && h.cmd.Process != nil {
		h.cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { h.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			h.cmd.Process.Kill()
		}
	}
}

// rss returns the server's resident set size in bytes.
func (h *harness) rss() int64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(h.cmd.Process.Pid) + "/status")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "VmRSS:") {
			f := strings.Fields(l)
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			return kb * 1024
		}
	}
	return 0
}

func (h *harness) stats(gc bool) *serverStats {
	if h.pprof == "" {
		return nil
	}
	u := h.pprof + "/debug/stats"
	if gc {
		u += "?gc=1"
	}
	res, err := http.Get(u)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	var s serverStats
	if json.NewDecoder(res.Body).Decode(&s) != nil {
		return nil
	}
	return &s
}

// startProfile records a CPU profile for d (and an allocation profile
// delta) when -profile is set. The returned func waits for it.
func (h *harness) startProfile(name string, d time.Duration) func() {
	if h.profDir == "" || h.pprof == "" {
		return func() {}
	}
	slug := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, strings.ToLower(name))
	save := func(url, file string) {
		res, err := http.Get(url)
		if err != nil {
			return
		}
		defer res.Body.Close()
		f, err := os.Create(filepath.Join(h.profDir, file))
		if err != nil {
			return
		}
		io.Copy(f, res.Body)
		f.Close()
	}
	save(h.pprof+"/debug/pprof/allocs", slug+".allocs-before.pprof")
	done := make(chan struct{})
	secs := max(1, int(d.Seconds()))
	go func() {
		save(fmt.Sprintf("%s/debug/pprof/profile?seconds=%d", h.pprof, secs), slug+".cpu.pprof")
		close(done)
	}()
	return func() {
		<-done
		save(h.pprof+"/debug/pprof/allocs", slug+".allocs-after.pprof")
	}
}
