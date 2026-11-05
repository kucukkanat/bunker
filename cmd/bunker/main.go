// Command bunker is the Bunker server and its command-line client in one binary.
package main

import (
	"fmt"
	"log/slog"
	"os"

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
