package main

// Child mode: the harness re-executes itself with -child to run a Bunker
// server in a separate process (so load-generator work doesn't pollute
// the server's CPU and allocation numbers). The child adds a loopback-only
// net/http/pprof listener; the real bunker binary never includes it.

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/server"

	// Same module set as cmd/bunker/modules.go.
	_ "github.com/kucukkanat/bunker/internal/admin"
	_ "github.com/kucukkanat/bunker/internal/ai"
	_ "github.com/kucukkanat/bunker/internal/apptypes"
	_ "github.com/kucukkanat/bunker/internal/auth"
	_ "github.com/kucukkanat/bunker/internal/connections"
	_ "github.com/kucukkanat/bunker/internal/data"
	_ "github.com/kucukkanat/bunker/internal/entra"
	_ "github.com/kucukkanat/bunker/internal/files"
	_ "github.com/kucukkanat/bunker/internal/hooks"
	_ "github.com/kucukkanat/bunker/internal/m365"
	_ "github.com/kucukkanat/bunker/internal/mcp"
	_ "github.com/kucukkanat/bunker/internal/notify"
	_ "github.com/kucukkanat/bunker/internal/sites"
	_ "github.com/kucukkanat/bunker/internal/templates"
	_ "github.com/kucukkanat/bunker/internal/tlsauto"
	_ "github.com/kucukkanat/bunker/internal/vendorlib"
	_ "github.com/kucukkanat/bunker/internal/web"
)

// serverStats is what the child reports at /debug/stats.
type serverStats struct {
	Mallocs    uint64 `json:"mallocs"`
	TotalAlloc uint64 `json:"totalAlloc"`
	HeapInuse  uint64 `json:"heapInuse"`
	HeapAlloc  uint64 `json:"heapAlloc"`
	StackInuse uint64 `json:"stackInuse"`
	Sys        uint64 `json:"sys"`
	NumGC      uint32 `json:"numGC"`
	Goroutines int    `json:"goroutines"`
}

func runChild(pprofAddr string) int {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	cfg := core.LoadConfig()
	a, err := server.New(cfg, log)
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup failed:", err)
		return 1
	}
	if pprofAddr != "" {
		mux := http.NewServeMux()
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		mux.HandleFunc("/debug/stats", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("gc") == "1" {
				runtime.GC()
			}
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			json.NewEncoder(w).Encode(serverStats{ms.Mallocs, ms.TotalAlloc, ms.HeapInuse, ms.HeapAlloc, ms.StackInuse, ms.Sys, ms.NumGC, runtime.NumGoroutine()})
		})
		go func() {
			if err := http.ListenAndServe(pprofAddr, mux); err != nil {
				fmt.Fprintln(os.Stderr, "pprof:", err)
			}
		}()
	}
	if err := server.Run(a); err != nil {
		fmt.Fprintln(os.Stderr, "server:", err)
		return 1
	}
	return 0
}
