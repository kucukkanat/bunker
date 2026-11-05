package cli

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
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
			Flags:   []flagDoc{{Name: "check", Desc: "Only check whether an update is available"}, {Name: "force", Desc: "Reinstall even if up to date"}},
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
	repo := firstNonEmpty(x.Getenv("BUNKER_UPDATE_REPO"), "kucukkanat/bunker")
	api := strings.TrimRight(firstNonEmpty(x.Getenv("BUNKER_UPDATE_API"), "https://api.github.com"), "/")
	c := &Client{Server: api, HTTP: httpClient}
	var rel struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	sp := x.spin("Checking for updates…")
	err := c.Get(x.ctx, "/repos/"+repo+"/releases/latest", nil, &rel)
	sp.end()
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == 404 {
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
	archs := []string{runtime.GOARCH}
	switch runtime.GOARCH {
	case "amd64":
		archs = append(archs, "x86_64", "x64")
	case "arm64":
		archs = append(archs, "aarch64")
	}
	osNames := []string{runtime.GOOS}
	if runtime.GOOS == "darwin" {
		osNames = append(osNames, "macos", "mac")
	}
	var asset, sums string
	var assetName string
	for _, as := range rel.Assets {
		n := strings.ToLower(as.Name)
		if strings.Contains(n, "checksum") || strings.Contains(n, "sha256sums") {
			sums = as.URL
			continue
		}
		if strings.HasSuffix(n, ".sha256") || strings.HasSuffix(n, ".sig") || strings.HasSuffix(n, ".pem") || strings.HasSuffix(n, ".sbom") || strings.HasSuffix(n, ".json") {
			continue
		}
		if containsAny(n, osNames) && containsAny(n, archs) && asset == "" {
			asset, assetName = as.URL, as.Name
		}
	}
	if asset == "" {
		return fmt.Errorf("release %s has no build for %s/%s", rel.Tag, runtime.GOOS, runtime.GOARCH)
	}
	for _, as := range rel.Assets {
		if as.Name == assetName+".sha256" {
			sums = as.URL
		}
	}
	if sums == "" {
		return fmt.Errorf("release %s has no checksums; not installing an unverified binary", rel.Tag)
	}
	sp = x.spin("Downloading " + assetName + "…")
	data, err := download(x, asset, 300<<20)
	if err != nil {
		sp.end()
		return err
	}
	sumData, err := download(x, sums, 1<<20)
	sp.end()
	if err != nil {
		return err
	}
	want := findChecksum(string(sumData), assetName)
	got := sha256.Sum256(data)
	if want == "" || !strings.EqualFold(want, hex.EncodeToString(got[:])) {
		return fmt.Errorf("checksum mismatch for %s; not installing", assetName)
	}
	bin, err := extractBinary(assetName, data)
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
	})
}

func containsAny(s string, subs []string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

func download(x *App, u string, limit int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(x.ctx, "GET", u, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download failed: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// findChecksum reads "sha256  name" lines (or a bare hash).
func findChecksum(text, name string) string {
	sc := bufio.NewScanner(strings.NewReader(text))
	var only string
	n := 0
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		n++
		only = f[0]
		if len(f) >= 2 && strings.TrimPrefix(f[len(f)-1], "*") == name {
			return f[0]
		}
	}
	if n == 1 && len(only) == 64 {
		return only
	}
	return ""
}

func extractBinary(name string, data []byte) ([]byte, error) {
	want := "bunker"
	if runtime.GOOS == "windows" {
		want = "bunker.exe"
	}
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".tar.gz") || strings.HasSuffix(n, ".tgz"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err != nil {
				return nil, fmt.Errorf("no %s inside %s", want, name)
			}
			if filepath.Base(h.Name) == want && h.Typeflag == tar.TypeReg {
				return io.ReadAll(io.LimitReader(tr, 300<<20))
			}
		}
	case strings.HasSuffix(n, ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == want {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, 300<<20))
			}
		}
		return nil, fmt.Errorf("no %s inside %s", want, name)
	}
	return data, nil
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
