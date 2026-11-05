// Command bunker is the Bunker server and its command-line client in one binary.
package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/server"
)

// Client is the CLI entry point, installed by internal/cli via modules.go.
var Client func(args []string) int

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "", "serve", "server":
		os.Exit(serve(false))
	case "dev":
		os.Setenv("BUNKER_DEV", "1")
		os.Exit(serve(true))
	case "healthcheck":
		os.Exit(healthcheck())
	case "version", "--version", "-v":
		fmt.Println("bunker", core.Version)
	default:
		if Client == nil {
			fmt.Fprintln(os.Stderr, "unknown command:", cmd)
			os.Exit(2)
		}
		os.Exit(Client(args))
	}
}

func serve(dev bool) int {
	level := slog.LevelInfo
	if core.EnvBool("DEBUG", false) {
		level = slog.LevelDebug
	}
	var h slog.Handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	if dev || core.Env("LOG_FORMAT", "") == "text" {
		h = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}
	log := slog.New(h)
	cfg := core.LoadConfig()
	a, err := server.New(cfg, log)
	if err != nil {
		log.Error("startup failed", "err", err)
		return 1
	}
	if err := server.Run(a); err != nil {
		log.Error("server error", "err", err)
		return 1
	}
	return 0
}

// healthcheck probes the local server's /healthz and exits 0 when healthy.
// Used by Docker HEALTHCHECK in images that have no shell or curl.
func healthcheck() int {
	_, port, err := net.SplitHostPort(core.LoadConfig().Listen)
	if err != nil || port == "" {
		port = "8080"
	}
	c := &http.Client{Timeout: 3 * time.Second}
	res, err := c.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "unhealthy:", err)
		return 1
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "unhealthy: HTTP", res.StatusCode)
		return 1
	}
	return 0
}
