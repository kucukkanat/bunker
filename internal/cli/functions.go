package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kucukkanat/bunker/internal/mcp"
)

// Friendly commands for server functions (api/<name>.js) and their
// secrets. Everything here is also reachable as `bunker api fn.*` /
// `bunker api secrets.*`; these just read better and handle stdin.

func init() {
	commands = append(commands,
		&command{
			Name: "fn", Aliases: []string{"functions", "func"}, Args: "[ls|call|runs|logs] [site] ...", Group: groupSites,
			Summary: "List, call and watch a site's server functions",
			Help: `Server functions are files in api/ (api/orders.js → "orders") that run on
the Bunker server. Pages call them with bunker.fn('orders', input).

  bunker fn                              list functions (site from bunker.json)
  bunker fn ls team-lunch                list a site's functions, triggers and limits
  bunker fn call team-lunch orders '{"meal":"Pasta"}'
  echo '{"meal":"Pasta"}' | bunker fn call team-lunch orders -
  bunker fn call orders --draft          run the draft code (site from bunker.json)
  bunker fn runs team-lunch --name orders   recent runs with their logs
  bunker fn logs team-lunch --follow     stream console output live (Ctrl-C stops)

Input is JSON (a bare word is sent as a string); @file reads a file.`,
			Flags: []flagDoc{
				{Name: "draft", Desc: "call: run the draft version instead of the live one"},
				{Name: "name", Short: "n", Arg: "fn", Desc: "runs/logs: only this function"},
				{Name: "limit", Arg: "n", Desc: "runs/logs: how many recent runs (default 20)"},
				{Name: "follow", Short: "f", Desc: "logs: keep streaming new log lines"},
			},
			Run: cmdFn,
		},
		&command{
			Name: "secrets", Aliases: []string{"secret"}, Args: "[ls|set|rm] [site] [NAME]", Group: groupSites,
			Summary: "Manage the secrets a site's server functions can read",
			Help: `Secrets (API keys, passwords, webhook URLs) are encrypted on the server.
Server functions read them with bunker.secrets.get("NAME"); pages never see
them and nobody can read a value back. Names look like STRIPE_KEY.

  bunker secrets ls team-lunch
  bunker secrets set team-lunch SLACK_WEBHOOK          asks for the value (hidden)
  pbpaste | bunker secrets set team-lunch SLACK_WEBHOOK   reads the value from stdin
  bunker secrets rm team-lunch SLACK_WEBHOOK

The site can be left out inside a folder with bunker.json.`,
			Flags: []flagDoc{{Name: "from-file", Arg: "path", Desc: "set: read the value from this file"}},
			Run:   cmdSecrets,
		},
	)
}

var secretNameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

type fnInfo struct {
	Name      string   `json:"name"`
	Path      string   `json:"path,omitempty"`
	Elevated  bool     `json:"elevated,omitempty"`
	Schedule  string   `json:"schedule,omitempty"`
	OnWrite   []string `json:"onWrite,omitempty"`
	TimeoutMs int64    `json:"timeoutMs,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// triggers describes how a function runs, for tables and deploy output.
func (f fnInfo) triggers() string {
	var t []string
	if f.Schedule != "" {
		t = append(t, "schedule "+f.Schedule)
	}
	if len(f.OnWrite) > 0 {
		t = append(t, "on write to "+strings.Join(f.OnWrite, ", "))
	}
	if len(t) == 0 {
		return "called by pages"
	}
	return strings.Join(t, "; ")
}

func fnPath(site, name string) string {
	name = strings.Trim(strings.TrimPrefix(strings.Trim(name, "/"), "api/"), "/")
	for _, ext := range []string{".js", ".mjs", ".ts"} {
		name = strings.TrimSuffix(name, ext)
	}
	return "/api/v1/sites/" + pathEsc(site) + "/functions/" + escSegments(name)
}

func cmdFn(x *App, a *Args) error {
	sub, rest := a.Arg(0), a.Pos
	switch sub {
	case "ls", "list", "call", "run", "runs", "logs", "log":
		rest = rest[1:]
	case "":
	default:
		// `bunker fn team-lunch` lists that site.
		sub = "ls"
	}
	at := func(i int) string {
		if i < len(rest) {
			return rest[i]
		}
		return ""
	}
	site := func(example string) (string, error) {
		if s := at(0); s != "" {
			return s, nil
		}
		if p := cwdProject(); p.Site != "" {
			return p.Site, nil
		}
		return "", usageErr("which site? pass its address, e.g. `%s` (or run it in a folder with bunker.json)", example)
	}
	c, err := x.client(cwdProject().Server)
	if err != nil {
		return err
	}
	switch sub {
	case "", "ls", "list":
		s, err := site("bunker fn ls team-lunch")
		if err != nil {
			return err
		}
		var out struct {
			Enabled bool `json:"enabled"`
			Blocked *struct {
				Scope  string `json:"scope"`
				Reason string `json:"reason"`
			} `json:"blocked"`
			Functions []fnInfo       `json:"functions"`
			Limits    map[string]any `json:"limits"`
		}
		if err := c.Get(x.ctx, "/api/v1/sites/"+pathEsc(s)+"/functions", nil, &out); err != nil {
			return err
		}
		return x.result(out, func() {
			if !out.Enabled || out.Blocked != nil {
				why := "Server functions are turned off"
				if out.Blocked != nil {
					switch out.Blocked.Scope {
					case "user":
						why += " for this app's owner"
					case "site":
						why += " for this app"
					default:
						why += " on this server"
					}
					if out.Blocked.Reason != "" {
						why += ": " + out.Blocked.Reason
					}
				}
				x.warn("%s. Ask an admin if you need them.", why)
			}
			if len(out.Functions) == 0 {
				x.info("No server functions yet. Add api/<name>.js with an `export default async function (input, ctx) {…}` and deploy.")
				return
			}
			var rows [][]string
			for _, f := range out.Functions {
				runsAs := "viewer"
				if f.Elevated {
					runsAs = x.outSt.yellow("owner")
				}
				note := f.triggers()
				if f.Error != "" {
					note = x.outSt.red("error: " + clipStr(f.Error, 60))
				}
				rows = append(rows, []string{f.Name, firstNonEmpty(f.Path, "api/"+f.Name+".js"), runsAs, note})
			}
			x.table([]string{"NAME", "FILE", "RUNS AS", "TRIGGERS"}, rows)
		})

	case "call", "run":
		var s, name, input string
		switch {
		case len(rest) >= 3:
			s, name, input = rest[0], rest[1], rest[2]
		case len(rest) == 2 && cwdProject().Site != "" && looksLikeInput(rest[1]):
			s, name, input = cwdProject().Site, rest[0], rest[1]
		case len(rest) == 2:
			s, name = rest[0], rest[1]
		case len(rest) == 1 && cwdProject().Site != "":
			s, name = cwdProject().Site, rest[0]
		default:
			return usageErr("which function? e.g. `bunker fn call team-lunch orders '{\"meal\":\"Pasta\"}'`")
		}
		in, err := fnInput(x, input)
		if err != nil {
			return err
		}
		body := map[string]any{"input": in}
		if a.Bool("draft") {
			body["draft"] = true
		}
		b, _ := json.Marshal(body)
		resp, err := c.Raw(x.ctx, "POST", fnPath(s, name), nil, bytes.NewReader(b), "application/json")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if resp.StatusCode >= 400 {
			return fnCallError(c, resp.StatusCode, raw)
		}
		var out struct {
			Output     json.RawMessage `json:"output"`
			DurationMs int64           `json:"durationMs"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return fmt.Errorf("unexpected reply from the server: %w", err)
		}
		if x.JSON {
			return x.printJSON(out)
		}
		var pretty bytes.Buffer
		if len(out.Output) == 0 || json.Indent(&pretty, out.Output, "", "  ") != nil {
			pretty.Reset()
			pretty.WriteString("null")
		}
		fmt.Fprintln(x.Stdout, pretty.String())
		x.info("%s ran in %dms", name, out.DurationMs)
		return nil

	case "runs":
		s, err := site("bunker fn runs team-lunch")
		if err != nil {
			return err
		}
		runs, err := fnRuns(x, c, s, a)
		if err != nil {
			return err
		}
		return x.result(map[string]any{"items": runs}, func() {
			if len(runs) == 0 {
				x.info("No runs yet.")
				return
			}
			var rows [][]string
			for _, r := range runs {
				st := x.outSt.green(r.Status)
				if r.Status != "ok" {
					st = x.outSt.red(firstNonEmpty(r.Status, "error"))
				}
				rows = append(rows, []string{r.at(), r.Name, r.trigger(), r.User.label(), st, fmt.Sprintf("%dms", r.DurationMs), clipStr(r.Error, 50)})
			}
			x.table([]string{"WHEN", "NAME", "TRIGGER", "USER", "STATUS", "TIME", "ERROR"}, rows)
		})

	case "logs", "log":
		s, err := site("bunker fn logs team-lunch --follow")
		if err != nil {
			return err
		}
		only := a.Str("name")
		if !a.Bool("follow") {
			runs, err := fnRuns(x, c, s, a)
			if err != nil {
				return err
			}
			if x.JSON {
				return x.printJSON(map[string]any{"items": runs})
			}
			// Oldest first, like a log file.
			for i := len(runs) - 1; i >= 0; i-- {
				r := runs[i]
				fmt.Fprintf(x.Stdout, "%s %s %s %s\n", x.outSt.dim(r.at()), x.outSt.bold(r.Name), x.outSt.dim(r.trigger()), statusWord(x, r.Status))
				for _, l := range strings.Split(strings.TrimRight(r.Logs, "\n"), "\n") {
					if l != "" {
						fmt.Fprintln(x.Stdout, "  "+l)
					}
				}
				if r.Error != "" {
					fmt.Fprintln(x.Stdout, "  "+x.outSt.red(r.Error))
				}
			}
			if len(runs) == 0 {
				x.info("No runs yet. Use --follow to wait for new ones.")
			}
			return nil
		}
		return fnFollow(x, c, s, only)
	}
	return usageErr("unknown fn command %q (use ls, call, runs or logs)", sub)
}

// looksLikeInput tells `fn call <name> <input>` (site from bunker.json)
// apart from `fn call <site> <name>`.
func looksLikeInput(s string) bool {
	if s == "-" || strings.HasPrefix(s, "@") {
		return true
	}
	return json.Valid([]byte(s)) && !isBareName(s)
}

func isBareName(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '/') {
			return false
		}
	}
	return s != "" && !isNumber(s)
}

// fnInput parses the call input: JSON, "-" (stdin), @file, or a bare
// string. Empty means null.
func fnInput(x *App, v string) (any, error) {
	var b []byte
	switch {
	case v == "":
		return nil, nil
	case v == "-" || strings.HasPrefix(v, "@"):
		var err error
		if v == "-" || v == "@-" {
			b, err = io.ReadAll(io.LimitReader(x.Stdin, 2<<20))
		} else {
			b, err = os.ReadFile(strings.TrimPrefix(v, "@"))
		}
		if err != nil {
			return nil, err
		}
	default:
		b = []byte(v)
	}
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil, nil
	}
	if len(b) > 1<<20 {
		return nil, usageErr("the input is larger than 1 MB, the most a function accepts")
	}
	if json.Valid(b) {
		return json.RawMessage(b), nil
	}
	if v != "-" && !strings.HasPrefix(v, "@") {
		return v, nil // a bare word: send it as a string
	}
	return nil, usageErr("the input isn't valid JSON")
}

func fnCallError(c *Client, status int, raw []byte) error {
	code, msg := mcp.APIError(&mcp.Response{Status: status, Body: raw})
	e := &APIError{Status: status, Code: code, Message: msg, Server: c.Server}
	var full struct {
		Error struct {
			Stack string `json:"stack"`
		} `json:"error"`
	}
	json.Unmarshal(raw, &full)
	switch code {
	case "functions_off":
		e.Hint = "An admin turned server functions off here. Ask them, or check `bunker api functions.status`."
	case "busy":
		e.Hint = "Too many runs at once. Try again in a moment."
	case "timeout":
		e.Hint = "The function ran out of time. Make it do less per call, or raise config.timeoutMs."
	case "function_error":
		e.Hint = "The function's code threw an error. See its logs with `bunker fn logs`."
	case "not_found":
		e.Hint = "Check the name with `bunker fn ls` (api/orders.js is called \"orders\"). Use --draft for functions you haven't published."
	}
	if full.Error.Stack != "" {
		e.Message += "\n" + strings.TrimRight(full.Error.Stack, "\n")
	}
	return e
}

type fnRun struct {
	ID      any             `json:"id"`
	Name    string          `json:"name"`
	Trigger json.RawMessage `json:"trigger"`
	User    fnUser          `json:"user"`
	Status  string          `json:"status"`
	// DurationMs, Error and Logs are as stored for the run.
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error"`
	Logs       string `json:"logs"`
	At         any    `json:"at"`
}

// fnUser accepts a user object, an email/name string or an id.
type fnUser struct {
	ID    any    `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

func (u *fnUser) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		u.Name = s
		return nil
	}
	type plain fnUser
	var p plain
	if json.Unmarshal(b, &p) == nil {
		*u = fnUser(p)
		return nil
	}
	var id any
	json.Unmarshal(b, &id)
	u.ID = id
	return nil
}

func (u fnUser) label() string {
	if u.Email != "" {
		return u.Email
	}
	if u.Name != "" {
		return u.Name
	}
	if u.ID != nil {
		return fmt.Sprint(u.ID)
	}
	return "-"
}

func (r fnRun) trigger() string {
	var s string
	if json.Unmarshal(r.Trigger, &s) == nil {
		return firstNonEmpty(s, "call")
	}
	var t struct {
		Type string `json:"type"`
	}
	json.Unmarshal(r.Trigger, &t)
	return firstNonEmpty(t.Type, "call")
}

func (r fnRun) at() string { return fmtWhen(r.At) }

// fmtWhen renders a unix time (seconds or ms) or an RFC 3339 string.
func fmtWhen(v any) string {
	switch t := v.(type) {
	case float64:
		n := int64(t)
		if n > 1e12 {
			n /= 1000
		}
		return time.Unix(n, 0).Format("Jan 2 15:04:05")
	case string:
		if p, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return p.Local().Format("Jan 2 15:04:05")
		}
		return t
	}
	return ""
}

func statusWord(x *App, s string) string {
	if s == "ok" {
		return x.outSt.green("ok")
	}
	return x.outSt.red(firstNonEmpty(s, "error"))
}

func fnRuns(x *App, c *Client, site string, a *Args) ([]fnRun, error) {
	q := url.Values{}
	if n := a.Str("name"); n != "" {
		q.Set("name", n)
	}
	limit, err := a.Int("limit", 20)
	if err != nil {
		return nil, err
	}
	q.Set("limit", strconv.Itoa(limit))
	var out struct {
		Items []fnRun `json:"items"`
	}
	if err := c.Get(x.ctx, "/api/v1/sites/"+pathEsc(site)+"/functions/_runs", q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// fnFollow streams the site's `logs` events channel until Ctrl-C.
func fnFollow(x *App, c *Client, site, only string) error {
	q := url.Values{"ch": {"logs"}}
	resp, err := c.Raw(x.ctx, "GET", "/api/v1/sites/"+pathEsc(site)+"/events", q, nil, "")
	if err != nil {
		if x.ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return c.errorFrom(resp)
	}
	if !x.JSON {
		x.info("Streaming logs for %s (Ctrl-C to stop)…", x.errSt.bold(site))
	}
	enc := jsonEncoder(x.Stdout)
	err = mcp.ParseSSE(resp.Body, func(ev, data string) error {
		if ev == "bye" {
			return io.EOF
		}
		var f struct {
			Ch   string `json:"ch"`
			Data struct {
				Run   any    `json:"run"`
				Name  string `json:"name"`
				Level string `json:"level"`
				Line  string `json:"line"`
				At    any    `json:"at"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(data), &f) != nil || (f.Ch != "" && f.Ch != "logs") {
			return nil
		}
		l := f.Data
		if only != "" && l.Name != only {
			return nil
		}
		if x.JSON {
			return enc.Encode(l)
		}
		line := l.Line
		switch l.Level {
		case "error":
			line = x.outSt.red(line)
		case "warn":
			line = x.outSt.yellow(line)
		}
		fmt.Fprintf(x.Stdout, "%s %s %s\n", x.outSt.dim(firstNonEmpty(fmtWhen(l.At), time.Now().Format("Jan 2 15:04:05"))), x.outSt.cyan(l.Name), line)
		return nil
	})
	if err == io.EOF || x.ctx.Err() != nil {
		return nil
	}
	return err
}

// ---- secrets ----

func cmdSecrets(x *App, a *Args) error {
	sub, rest := a.Arg(0), a.Pos
	switch sub {
	case "ls", "list", "set", "add", "put", "rm", "delete", "remove", "unset":
		rest = rest[1:]
	case "":
		sub = "ls"
	default:
		sub = "ls"
	}
	// [site] NAME: a NAME is upper case, a site address never is.
	var site, name string
	switch {
	case len(rest) >= 2:
		site, name = rest[0], rest[1]
	case len(rest) == 1 && secretNameRe.MatchString(rest[0]) && sub != "ls":
		name = rest[0]
	case len(rest) == 1:
		site = rest[0]
	}
	if site == "" {
		site = cwdProject().Site
	}
	if site == "" {
		return usageErr("which site? e.g. `bunker secrets %s team-lunch%s` (or run it in a folder with bunker.json)", sub, map[bool]string{true: "", false: " API_KEY"}[sub == "ls"])
	}
	c, err := x.client(cwdProject().Server)
	if err != nil {
		return err
	}
	base := "/api/v1/sites/" + pathEsc(site) + "/secrets"
	needName := func() error {
		if name == "" {
			return usageErr("which secret? e.g. `bunker secrets %s %s SLACK_WEBHOOK`", sub, site)
		}
		if !secretNameRe.MatchString(name) {
			return usageErr("secret names use capital letters, digits and _ and start with a letter, like SLACK_WEBHOOK")
		}
		return nil
	}
	switch sub {
	case "ls", "list":
		var out struct {
			Items []struct {
				Name      string `json:"name"`
				UpdatedAt any    `json:"updatedAt"`
				UpdatedBy any    `json:"updatedBy"`
			} `json:"items"`
		}
		if err := c.Get(x.ctx, base, nil, &out); err != nil {
			return err
		}
		return x.result(out, func() {
			if len(out.Items) == 0 {
				x.info("No secrets yet. Add one with: bunker secrets set %s NAME", site)
				return
			}
			var rows [][]string
			for _, s := range out.Items {
				by := ""
				if s.UpdatedBy != nil {
					var u fnUser
					b, _ := json.Marshal(s.UpdatedBy)
					json.Unmarshal(b, &u)
					by = u.label()
				}
				rows = append(rows, []string{s.Name, fmtWhen(s.UpdatedAt), by})
			}
			x.table([]string{"NAME", "UPDATED", "BY"}, rows)
		})
	case "set", "add", "put":
		if err := needName(); err != nil {
			return err
		}
		val, err := secretValue(x, a, name)
		if err != nil {
			return err
		}
		if err := c.Call(x.ctx, "PUT", base+"/"+pathEsc(name), nil, map[string]any{"value": val}, nil); err != nil {
			return err
		}
		return x.result(map[string]any{"ok": true, "name": name}, func() {
			x.ok("Saved %s for %s %s", x.errSt.bold(name), site, x.errSt.dim("(read it in a function with bunker.secrets.get(\""+name+"\"))"))
		})
	case "rm", "delete", "remove", "unset":
		if err := needName(); err != nil {
			return err
		}
		if err := x.confirm("Delete the secret " + name + " from " + site + "? Functions that use it will stop working."); err != nil {
			return err
		}
		if err := c.Call(x.ctx, "DELETE", base+"/"+pathEsc(name), nil, nil, nil); err != nil {
			return err
		}
		return x.result(map[string]any{"ok": true, "name": name}, func() { x.ok("Deleted %s", name) })
	}
	return usageErr("unknown secrets command %q (use ls, set or rm)", sub)
}

// secretValue reads a secret from --from-file, piped stdin or a hidden
// prompt. Values never come from argv, so they stay out of shell history.
func secretValue(x *App, a *Args, name string) (string, error) {
	var b []byte
	var err error
	switch {
	case a.Str("from-file") != "":
		b, err = os.ReadFile(a.Str("from-file"))
	case !isTTY(x.Stdin):
		b, err = io.ReadAll(io.LimitReader(x.Stdin, 17<<10))
	default:
		fmt.Fprintf(x.Stderr, "%s Value for %s %s ", x.errSt.cyan("?"), name, x.errSt.dim("(hidden)"))
		restore := noEcho(x.Stdin)
		line, _ := bufio.NewReader(x.Stdin).ReadString('\n')
		restore()
		fmt.Fprintln(x.Stderr)
		b = []byte(line)
	}
	if err != nil {
		return "", err
	}
	v := strings.TrimRight(string(b), "\r\n")
	if v == "" {
		return "", usageErr("the value is empty; nothing was saved")
	}
	if len(v) > 16<<10 {
		return "", usageErr("secret values can be 16 KB at most")
	}
	return v, nil
}

// noEcho turns off terminal echo while a secret is typed (best effort,
// via stty, so no extra dependency). It returns a function that restores it.
func noEcho(in io.Reader) func() {
	f, ok := in.(*os.File)
	if !ok || runtime.GOOS == "windows" {
		return func() {}
	}
	off := exec.Command("stty", "-echo")
	off.Stdin = f
	if off.Run() != nil {
		return func() {}
	}
	return func() {
		on := exec.Command("stty", "echo")
		on.Stdin = f
		on.Run()
	}
}
