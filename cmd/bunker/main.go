// Command bunker is the Bunker server and its command-line client in one binary.
package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/kucukkanat/bunker/internal/auth"
	"github.com/kucukkanat/bunker/internal/cli"
	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/server"
)

// Client is the CLI entry point (internal/cli).
var Client func(args []string) int = cli.Run

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
	// People running Bunker in a terminal get readable logs; services and
	// containers keep JSON for log collectors.
	if dev || core.Env("LOG_FORMAT", "") == "text" || (core.Env("LOG_FORMAT", "") == "" && isTerminal(os.Stderr)) {
		h = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}
	log := slog.New(h)
	cfg := core.LoadConfig()
	a, err := server.New(cfg, log)
	if err != nil {
		log.Error("startup failed", "err", err)
		return 1
	}
	if dev {
		fmt.Fprintf(os.Stderr, "\n  Bunker is running in dev mode (you're signed in as a local admin).\n  Open %s\n\n", cfg.URL)
	}
	// On a laptop, take people straight to the setup page (or the app in dev).
	if u := firstRunURL(cfg, dev, auth.Configured()); u != "" {
		go func() {
			time.Sleep(600 * time.Millisecond)
			openBrowser(u)
		}()
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

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// firstRunURL returns the page to open in a browser on first run, or "".
// Only on desktops, in an interactive terminal, outside containers.
func firstRunURL(cfg *core.Config, dev, configured bool) string {
	if core.EnvBool("NO_BROWSER", false) || (!isTerminal(os.Stderr) && runtime.GOOS != "windows") {
		return ""
	}
	if _, err := os.Stat("/.dockerenv"); err == nil || os.Getenv("KUBERNETES_SERVICE_HOST") != "" || os.Getenv("SSH_CONNECTION") != "" {
		return ""
	}
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return ""
	}
	_, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil || port == "" {
		return ""
	}
	base := "http://localhost:" + port
	switch {
	case dev:
		return base + "/"
	case !configured:
		return base + "/setup" // localhost: no setup code needed
	}
	return ""
}

func openBrowser(u string) {
	if !strings.HasPrefix(u, "http://localhost:") {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	_ = cmd.Start()
}
