// Package cli is Bunker's command-line client: sign in, deploy folders,
// manage sites and sharing, talk to the AI builder, call any API
// operation and run a local MCP server. It lives in the same binary as
// the server (cmd/bunker routes unknown subcommands here).
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sort"
	"strings"

	"github.com/kucukkanat/bunker/internal/core"
)

// App holds one invocation's I/O and global options.
type App struct {
	Stdout, Stderr io.Writer
	Stdin          io.Reader
	Getenv         func(string) string

	JSON    bool
	Yes     bool
	Debug   bool
	Profile string
	Server  string // --server override

	noColor, help bool
	outSt, errSt  styler
	errTTY        bool
	ctx           context.Context
	cfg           *Config
}

// Run is the entry point used by cmd/bunker.
func Run(args []string) int {
	x := &App{Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin, Getenv: os.Getenv}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return x.Run(ctx, args)
}

type flagDoc struct {
	Name, Short, Arg, Desc string
}

type command struct {
	Name    string
	Aliases []string
	Args    string
	Summary string
	Group   string
	Help    string // extra help text
	Flags   []flagDoc
	Run     func(x *App, a *Args) error
	// Loose commands accept flags they don't declare (api).
	Loose bool
}

const (
	groupStart  = "Get started"
	groupSites  = "Sites"
	groupAgents = "AI & agents"
	groupAcct   = "Account & tools"
)

var groupOrder = []string{groupStart, groupSites, groupAgents, groupAcct}

var commands []*command

func findCommand(name string) *command {
	for _, c := range commands {
		if c.Name == name {
			return c
		}
		for _, a := range c.Aliases {
			if a == name {
				return c
			}
		}
	}
	return nil
}

var globalFlags = []flagDoc{
	{Name: "json", Desc: "Machine-readable JSON output (for scripts and agents)"},
	{Name: "yes", Short: "y", Desc: "Skip confirmation prompts"},
	{Name: "server", Arg: "url", Desc: "Use this Bunker server (or BUNKER_SERVER)"},
	{Name: "profile", Arg: "name", Desc: "Use a saved login profile (or BUNKER_PROFILE)"},
	{Name: "no-color", Desc: "Disable colors (or NO_COLOR=1)"},
	{Name: "debug", Desc: "Print HTTP requests"},
	{Name: "help", Short: "h", Desc: "Show help"},
}

// Usage errors print the command's usage line.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func usageErr(format string, a ...any) error {
	return &usageError{fmt.Sprintf(format, a...)}
}

var errCancelled = errors.New("cancelled")

// Run executes args (without the program name).
func (x *App) Run(ctx context.Context, args []string) int {
	x.ctx = ctx
	if x.Getenv == nil {
		x.Getenv = os.Getenv
	}
	rest, err := x.parseGlobals(args)
	color := x.Getenv("NO_COLOR") == "" && x.Getenv("TERM") != "dumb"
	force := x.Getenv("FORCE_COLOR") != "" && x.Getenv("FORCE_COLOR") != "0"
	x.errTTY = isTTY(x.Stderr)
	x.outSt = styler{on: force || (color && isTTY(x.Stdout))}
	x.errSt = styler{on: force || (color && x.errTTY)}
	if x.noColor {
		x.outSt.on, x.errSt.on = false, false
	}
	if err != nil {
		return x.fail(err, nil)
	}
	if len(rest) == 0 || rest[0] == "help" || x.help && len(rest) == 0 {
		if len(rest) > 1 {
			if c := findCommand(rest[1]); c != nil {
				x.commandHelp(c)
				return 0
			}
			return x.fail(usageErr("unknown command %q", rest[1]), nil)
		}
		x.mainHelp()
		return 0
	}
	c := findCommand(rest[0])
	if c == nil {
		msg := fmt.Sprintf("unknown command %q", rest[0])
		if s := suggest(rest[0], commandNames()); s != "" {
			msg += fmt.Sprintf(". Did you mean %q?", s)
		}
		return x.fail(usageErr("%s Run `bunker help` to see all commands.", msg), nil)
	}
	if x.help {
		x.commandHelp(c)
		return 0
	}
	a, err := parseArgs(rest[1:], c)
	if err != nil {
		return x.fail(err, c)
	}
	if a.Has("help") && !(c.Loose && len(a.Pos) > 0) {
		x.commandHelp(c)
		return 0
	}
	if err := c.Run(x, a); err != nil {
		return x.fail(err, c)
	}
	return 0
}

func commandNames() []string {
	var n []string
	for _, c := range commands {
		n = append(n, c.Name)
		n = append(n, c.Aliases...)
	}
	return append(n, "serve", "dev", "demo", "version", "help")
}

// fail prints an error in a short, helpful way and returns the exit code.
func (x *App) fail(err error, c *command) int {
	var qe *quietErr
	if errors.As(err, &qe) {
		return 1
	}
	if errors.Is(err, errCancelled) {
		if !x.JSON {
			fmt.Fprintln(x.Stderr, x.errSt.dim("Cancelled."))
		}
		return 1
	}
	code, msg, hint, exit := "error", err.Error(), "", 1
	var ue *usageError
	var ae *APIError
	var ne net.Error
	var oe *net.OpError
	switch {
	case errors.As(err, &ue):
		code, exit = "usage", 2
		if c != nil {
			hint = "Usage: bunker " + c.Name + " " + c.Args + "   (see `bunker " + c.Name + " --help`)"
		}
	case errors.As(err, &ae):
		code, msg = ae.Code, ae.Message
		hint = ae.hint()
	case errors.As(err, &oe), errors.As(err, &ne):
		code = "network"
		msg = "Can't reach the Bunker server: " + err.Error()
		hint = "Check the address and your connection, then try `bunker doctor`."
	case errors.Is(err, errNotSignedIn):
		code = "unauthorized"
		hint = "Run `bunker login https://your-bunker` (or set BUNKER_SERVER and BUNKER_TOKEN)."
	}
	if x.JSON {
		e := map[string]any{"code": code, "message": msg}
		if hint != "" {
			e["hint"] = hint
		}
		if ae != nil {
			e["status"] = ae.Status
		}
		enc := jsonEncoder(x.Stderr)
		enc.Encode(map[string]any{"error": e})
		return exit
	}
	fmt.Fprintln(x.Stderr, x.errSt.red(symErr)+" "+capitalize(msg))
	if hint != "" {
		fmt.Fprintln(x.Stderr, "  "+x.errSt.dim(hint))
	}
	return exit
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	// Leave a leading file name alone ("index.html (line 1) ...").
	if first, _, _ := strings.Cut(s, " "); strings.ContainsAny(first, "./") {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (x *App) mainHelp() {
	s := x.outSt
	w := x.Stdout
	fmt.Fprintf(w, "%s %s\n\n", s.bold(s.moss("Bunker")), s.dim("— your private app cloud ("+core.Version+")"))
	fmt.Fprintf(w, "%s bunker <command> [flags]\n\n", s.dim("Usage:"))
	byGroup := map[string][]*command{}
	for _, c := range commands {
		byGroup[c.Group] = append(byGroup[c.Group], c)
	}
	row := func(name, desc string) {
		fmt.Fprintf(w, "  %s%s %s\n", s.cyan(name), strings.Repeat(" ", max(1, 34-len(name))), desc)
	}
	for _, g := range groupOrder {
		fmt.Fprintln(w, s.bold(g))
		for _, c := range byGroup[g] {
			row(strings.TrimSpace(c.Name+" "+shortArgs(c.Args)), c.Summary)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, s.bold("Server"))
	row("serve", "Run the Bunker server (also the default with no command)")
	row("dev", "Run a local single-user server for development")
	row("demo", "Like dev, pre-filled with example apps, people and data")
	row("version", "Print the version")
	fmt.Fprintln(w)
	fmt.Fprintln(w, s.bold("Global flags"))
	for _, f := range globalFlags {
		row(flagLabel(f), f.Desc)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s bunker login https://bunker.example.com && bunker deploy\n", s.dim("Start:"))
	fmt.Fprintf(w, "%s bunker help <command>   %s https://<server>/llms.txt\n", s.dim("More:"), s.dim("Agents:"))
}

func shortArgs(a string) string {
	if len(a) <= 26 {
		return a
	}
	if f := strings.Fields(a); len(f) > 0 {
		return f[0]
	}
	return ""
}

func flagLabel(f flagDoc) string {
	l := "--" + f.Name
	if f.Short != "" {
		l = "-" + f.Short + ", " + l
	}
	if f.Arg != "" {
		l += " <" + f.Arg + ">"
	}
	return l
}

func (x *App) commandHelp(c *command) {
	s := x.outSt
	w := x.Stdout
	fmt.Fprintf(w, "%s\n\n%s bunker %s %s\n", c.Summary, s.dim("Usage:"), c.Name, c.Args)
	if len(c.Aliases) > 0 {
		fmt.Fprintf(w, "%s %s\n", s.dim("Aliases:"), strings.Join(c.Aliases, ", "))
	}
	if c.Help != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, strings.TrimSpace(c.Help))
	}
	if len(c.Flags) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, s.bold("Flags"))
		for _, f := range c.Flags {
			l := flagLabel(f)
			fmt.Fprintf(w, "  %s%s %s\n", s.cyan(l), strings.Repeat(" ", max(1, 26-len(l))), f.Desc)
		}
	}
	fmt.Fprintf(w, "\n%s --json, --yes, --server, --profile, --no-color\n", s.dim("Global flags:"))
}

// suggest returns the closest candidate within edit distance 2.
func suggest(s string, cands []string) string {
	best, bd := "", 3
	for _, c := range cands {
		if d := editDistance(s, c); d < bd {
			best, bd = c, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func sortedKeys[V any](m map[string]V) []string {
	k := make([]string, 0, len(m))
	for s := range m {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}
