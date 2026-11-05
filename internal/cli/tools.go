package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/selfupdate"
)

func init() {
	commands = append(commands,
		&command{
			Name: "doctor", Args: "", Group: groupAcct,
			Summary: "Check your setup: config, connection, sign-in, versions, clock",
			Run:     cmdDoctor,
		},
		&command{
			Name: "update", Aliases: []string{"upgrade"}, Args: "", Group: groupAcct,
			Summary: "Update bunker to the latest release (checksum verified)",
			Flags:   []flagDoc{{Name: "check", Desc: "Only check whether an update is available"}, {Name: "force", Desc: "Reinstall even if up to date"}, {Name: "version", Arg: "TAG", Desc: "Install a specific release, like v0.2.0"}, {Name: "remote", Desc: "Update the Bunker server you are signed in to (admins), not this program"}},
			Run:     cmdUpdate,
		},
		&command{
			Name: "completion", Args: "bash|zsh|fish", Group: groupAcct,
			Summary: "Print a shell completion script",
			Help: `  bash:  echo 'source <(bunker completion bash)' >> ~/.bashrc
  zsh:   echo 'source <(bunker completion zsh)' >> ~/.zshrc
  fish:  bunker completion fish > ~/.config/fish/completions/bunker.fish`,
			Run: cmdCompletion,
		},
	)
}

// ---- doctor ----

type check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | fail
	Detail string `json:"detail"`
}

func cmdDoctor(x *App, a *Args) error {
	var checks []check
	add := func(name, status, detail string) { checks = append(checks, check{name, status, detail}) }
	cfg := x.config()
	if st, err := os.Stat(cfg.path); err == nil {
		if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
			add("config", "warn", fmt.Sprintf("%s is readable by others (%v); run chmod 600 on it", cfg.path, st.Mode().Perm()))
		} else {
			add("config", "ok", cfg.path)
		}
	} else if x.Getenv("BUNKER_TOKEN") != "" {
		add("config", "ok", "using BUNKER_SERVER / BUNKER_TOKEN from the environment")
	} else {
		add("config", "warn", "no config yet ("+cfg.path+"); run bunker login")
	}
	proj := cwdProject()
	server, token, cerr := x.conn(proj.Server)
	if server == "" {
		add("server", "fail", "no server configured; run bunker login <url>")
		return x.report(checks)
	}
	add("server", "ok", server)
	c := x.newClient(server, token)
	start := time.Now()
	resp, err := c.Raw(x.ctx, "GET", "/healthz", nil, nil, "")
	if err != nil {
		add("connection", "fail", "can't reach "+server+": "+err.Error())
		return x.report(checks)
	}
	lat := time.Since(start)
	var health struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
	jsonDecode(resp.Body, &health)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		add("connection", "fail", fmt.Sprintf("health check answered %d", resp.StatusCode))
	} else {
		st := "ok"
		if lat > time.Second {
			st = "warn"
		}
		add("connection", st, fmt.Sprintf("healthy, %dms", lat.Milliseconds()))
	}
	if d, err := http.ParseTime(resp.Header.Get("Date")); err == nil {
		skew := time.Since(d) - lat/2
		if skew < 0 {
			skew = -skew
		}
		if skew > 2*time.Minute {
			add("clock", "warn", fmt.Sprintf("your clock is %s off from the server; token expiry may look wrong", skew.Round(time.Second)))
		} else {
			add("clock", "ok", fmt.Sprintf("within %s of the server", max(skew.Round(time.Second), time.Second)))
		}
	}
	switch {
	case health.Version == "":
	case health.Version == core.Version:
		add("version", "ok", "CLI and server are both "+core.Version)
	default:
		add("version", "warn", fmt.Sprintf("CLI %s, server %s (run bunker update if the CLI is older)", core.Version, health.Version))
	}
	if cerr != nil || token == "" {
		add("sign-in", "fail", "not signed in to "+server+"; run bunker login "+server)
	} else {
		var who me
		if err := c.Get(x.ctx, "/api/v1/me", nil, &who); err != nil {
			add("sign-in", "fail", err.Error()+"; run bunker login "+server)
		} else {
			add("sign-in", "ok", "signed in as "+displayUser(who))
		}
	}
	if ops, err := c.Ops(x.ctx); err != nil {
		add("api", "fail", "couldn't list operations: "+err.Error())
	} else {
		add("api", "ok", fmt.Sprintf("%d operations available", len(ops)))
	}
	if r, err := c.Raw(x.ctx, "GET", "/.well-known/oauth-protected-resource", nil, nil, ""); err == nil {
		r.Body.Close()
		if r.StatusCode == 200 {
			add("mcp", "ok", "remote MCP at "+server+"/mcp")
		} else {
			add("mcp", "warn", "remote MCP endpoint not available on this server")
		}
	}
	if proj.exists() {
		d := projectFile + ": "
		if proj.Site != "" {
			d += "deploys to " + proj.Site
		} else {
			d += "no site yet (the first deploy creates one)"
		}
		if proj.Server != "" && normalizeServer(proj.Server) != server {
			add("project", "warn", d+" on "+proj.Server+", but you're using "+server)
		} else {
			add("project", "ok", d)
		}
	}
	return x.report(checks)
}

func (x *App) report(checks []check) error {
	failed := false
	for _, c := range checks {
		if c.Status == "fail" {
			failed = true
		}
	}
	if x.JSON {
		x.printJSON(map[string]any{"ok": !failed, "checks": checks})
	} else {
		for _, c := range checks {
			sym := x.outSt.green(symOK)
			switch c.Status {
			case "warn":
				sym = x.outSt.yellow(symWarn)
			case "fail":
				sym = x.outSt.red(symErr)
			}
			fmt.Fprintf(x.Stdout, "%s %-11s %s\n", sym, c.Name, x.outSt.dim(c.Detail))
		}
	}
	if failed {
		return &quietErr{}
	}
	return nil
}

// quietErr exits 1 without printing (the report already explained).
type quietErr struct{}

func (*quietErr) Error() string { return "" }

// ---- update ----

func cmdUpdate(x *App, a *Args) error {
	if a.Bool("remote") {
		return cmdUpdateServer(x, a)
	}
	src := selfupdate.DefaultSource()
	src.Repo = firstNonEmpty(x.Getenv("BUNKER_UPDATE_REPO"), src.Repo)
	if v := x.Getenv("BUNKER_UPDATE_API"); v != "" {
		src.API = strings.TrimRight(v, "/")
	}
	src.Client, src.Agent = httpClient, userAgent
	sp := x.spin("Checking for updates…")
	rel, err := src.Get(x.ctx, a.Str("version"))
	sp.end()
	if errors.Is(err, selfupdate.ErrNoRelease) {
		return fmt.Errorf("no Bunker release has been published yet. Get the latest code with: go install github.com/kucukkanat/bunker/cmd/bunker@latest (or run the install.sh one-liner again)")
	}
	if err != nil {
		return fmt.Errorf("couldn't check for updates: %w", err)
	}
	latest := strings.TrimPrefix(rel.Tag, "v")
	current := strings.TrimPrefix(core.Version, "v")
	if latest == current && !a.Bool("force") {
		return x.result(map[string]any{"current": core.Version, "latest": rel.Tag, "updated": false}, func() { x.ok("bunker %s is the latest version", core.Version) })
	}
	if a.Bool("check") {
		return x.result(map[string]any{"current": core.Version, "latest": rel.Tag, "available": true}, func() {
			x.info("bunker %s is available (you have %s). Run bunker update", rel.Tag, core.Version)
		})
	}
	sp = x.spin("Downloading " + rel.Tag + "…")
	bin, err := src.Download(x.ctx, rel, nil)
	sp.end()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return fmt.Errorf("can't write next to %s (%v); try with sudo or reinstall", exe, err)
	}
	if runtime.GOOS == "windows" {
		os.Remove(exe + ".old")
		if err := os.Rename(exe, exe+".old"); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, exe); err != nil {
		return err
	}
	return x.result(map[string]any{"current": core.Version, "latest": rel.Tag, "updated": true, "path": exe}, func() {
		x.ok("Updated bunker %s → %s", core.Version, rel.Tag)
		x.info("A running server switches to it on its next restart (or update it from Admin → Updates)")
	})
}

// cmdUpdateServer updates the signed-in server (Admin → Updates).
func cmdUpdateServer(x *App, a *Args) error {
	c, err := x.client("")
	if err != nil {
		return err
	}
	type job struct {
		State, Version, Error string
		Done, Total           int64
	}
	type status struct {
		Current, Latest, Reason, CheckError string
		UpdateAvailable, CanUpdate          bool
		Job                                 job
	}
	var st status
	sp := x.spin("Checking for updates…")
	err = c.Call(x.ctx, "POST", "/api/v1/admin/update/check", nil, nil, &st)
	sp.end()
	if err != nil {
		return err
	}
	if st.CheckError != "" {
		return errors.New(st.CheckError)
	}
	want := a.Str("version")
	if want == "" && !st.UpdateAvailable {
		return x.result(map[string]any{"current": st.Current, "latest": st.Latest, "updated": false}, func() {
			x.ok("The server runs %s, the latest version", st.Current)
		})
	}
	target := firstNonEmpty(want, st.Latest)
	if a.Bool("check") {
		return x.result(map[string]any{"current": st.Current, "latest": st.Latest, "available": st.UpdateAvailable, "canUpdate": st.CanUpdate, "reason": st.Reason}, func() {
			x.info("Bunker %s is available for the server (it runs %s). Run bunker update --remote", target, st.Current)
			if !st.CanUpdate {
				x.info("%s", st.Reason)
			}
		})
	}
	if !st.CanUpdate {
		return errors.New(st.Reason)
	}
	from := st.Current
	if err := c.Call(x.ctx, "POST", "/api/v1/admin/update", nil, map[string]any{"version": want}, nil); err != nil {
		return err
	}
	sp = x.spin("Updating the server to " + target + "…")
	defer sp.end()
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		var h struct {
			Version string `json:"version"`
		}
		if c.Get(x.ctx, "/healthz", nil, &h) == nil && h.Version != "" && h.Version != from {
			sp.end()
			return x.result(map[string]any{"from": from, "current": h.Version, "updated": true}, func() {
				x.ok("The server runs %s now (was %s)", h.Version, from)
			})
		}
		var s status
		if c.Get(x.ctx, "/api/v1/admin/update", nil, &s) == nil && s.Job.State == "failed" {
			return fmt.Errorf("the update didn't finish: %s", s.Job.Error)
		}
	}
	return errors.New("the server hasn't come back yet; check its logs (it goes back to the previous version by itself if the new one can't start)")
}

// ---- completion ----

func cmdCompletion(x *App, a *Args) error {
	var names []string
	for _, c := range commands {
		names = append(names, c.Name)
	}
	names = append(names, "serve", "dev", "demo", "version", "help")
	list := strings.Join(names, " ")
	switch a.Arg(0) {
	case "bash":
		fmt.Fprintf(x.Stdout, `# bunker bash completion
_bunker() {
  local cur prev
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[1]}"
  if [ "$COMP_CWORD" -eq 1 ]; then
    COMPREPLY=( $(compgen -W "%s" -- "$cur") ); return
  fi
  case "$prev" in
    api) [ "$COMP_CWORD" -eq 2 ] && COMPREPLY=( $(compgen -W "$(bunker ops --names 2>/dev/null)" -- "$cur") ); return;;
    sites) [ "$COMP_CWORD" -eq 2 ] && COMPREPLY=( $(compgen -W "ls create info rename rm" -- "$cur") ); return;;
    tokens) [ "$COMP_CWORD" -eq 2 ] && COMPREPLY=( $(compgen -W "ls create rm" -- "$cur") ); return;;
    completion) COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") ); return;;
    deploy|pull|init) COMPREPLY=( $(compgen -d -- "$cur") ); return;;
  esac
  COMPREPLY=( $(compgen -W "--json --yes --help" -- "$cur") )
}
complete -F _bunker bunker
`, list)
	case "zsh":
		var b strings.Builder
		for _, c := range commands {
			fmt.Fprintf(&b, "    '%s:%s'\n", c.Name, strings.ReplaceAll(c.Summary, "'", ""))
		}
		fmt.Fprintf(x.Stdout, `#compdef bunker
_bunker() {
  local -a cmds
  cmds=(
%s    'serve:Run the Bunker server'
    'dev:Run a local dev server'
    'version:Print the version'
  )
  if (( CURRENT == 2 )); then
    _describe 'command' cmds
  elif [[ $words[2] == api && CURRENT == 3 ]]; then
    compadd -- ${(f)"$(bunker ops --names 2>/dev/null)"}
  elif [[ $words[2] == sites && CURRENT == 3 ]]; then
    compadd ls create info rename rm
  elif [[ $words[2] == tokens && CURRENT == 3 ]]; then
    compadd ls create rm
  elif [[ $words[2] == completion ]]; then
    compadd bash zsh fish
  else
    _files
  fi
}
compdef _bunker bunker
`, b.String())
	case "fish":
		fmt.Fprintln(x.Stdout, "# bunker fish completion")
		fmt.Fprintln(x.Stdout, "complete -c bunker -f")
		for _, c := range commands {
			fmt.Fprintf(x.Stdout, "complete -c bunker -n '__fish_use_subcommand' -a %s -d '%s'\n", c.Name, strings.ReplaceAll(c.Summary, "'", ""))
		}
		fmt.Fprintln(x.Stdout, "complete -c bunker -n '__fish_seen_subcommand_from api' -a '(bunker ops --names 2>/dev/null)'")
		fmt.Fprintln(x.Stdout, "complete -c bunker -n '__fish_seen_subcommand_from sites' -a 'ls create info rename rm'")
		fmt.Fprintln(x.Stdout, "complete -c bunker -n '__fish_seen_subcommand_from tokens' -a 'ls create rm'")
		fmt.Fprintln(x.Stdout, "complete -c bunker -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'")
		fmt.Fprintln(x.Stdout, "complete -c bunker -l json -d 'JSON output'")
		fmt.Fprintln(x.Stdout, "complete -c bunker -s y -l yes -d 'Skip confirmations'")
	default:
		return usageErr("choose a shell: bash, zsh or fish")
	}
	return nil
}
