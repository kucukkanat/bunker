package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/mcp"
)

func init() {
	commands = append(commands,
		&command{
			Name: "ai", Args: "[site] \"<prompt>\"", Group: groupAgents,
			Summary: "Build a new site, or change one, by describing it",
			Help: `  bunker ai "A lunch order form for Fridays with a summary for the organizer"
  bunker ai team-lunch "Add a vegetarian option and make the header green"

Uses the AI provider your admin configured. Changes to an existing site land
in its draft; publish with ` + "`bunker api drafts.publish --site <site>`" + ` or the dashboard.`,
			Flags: []flagDoc{{Name: "site", Short: "s", Arg: "slug", Desc: "Change this site instead of creating one"}, {Name: "open", Short: "o", Desc: "Open the result in your browser"}},
			Run:   cmdAI,
		},
		&command{
			Name: "api", Args: "<op.name> [path params] [--param value ...] [--data json]", Group: groupAgents, Loose: true,
			Summary: "Call any API operation (see `bunker ops`)",
			Help: `Every server feature is an operation, so new features work here immediately.

  bunker api sites.list --filter mine
  bunker api sites.get team-lunch                  # positional = path params
  bunker api sharing.add --site team-lunch --type user --id ada@corp.com --role viewer
  bunker api files.write --site team-lunch --path about.html --content @about.html
  bunker api sites.update team-lunch --data '{"settings":{"spa":true}}'

Values: numbers/booleans are converted by the parameter's type; @file reads a
file (@- reads stdin); for upload parameters @folder sends a whole folder.`,
			Flags: []flagDoc{{Name: "data", Short: "d", Arg: "json", Desc: "JSON object merged into the arguments (@file works)"}},
			Run:   cmdAPI,
		},
		&command{
			Name: "ops", Args: "[filter]", Group: groupAgents,
			Summary: "List every API operation the server offers",
			Flags:   []flagDoc{{Name: "names", Desc: "Only print names (for scripts)"}, {Name: "full", Desc: "Show parameters"}},
			Run:     cmdOps,
		},
		&command{
			Name: "mcp", Args: "", Group: groupAgents,
			Summary: "Run a local MCP server (stdio) for Claude, Cursor and other agents",
			Help: `Speaks the Model Context Protocol over stdin/stdout using your CLI login.

  claude mcp add bunker -- bunker mcp
  {"mcpServers": {"bunker": {"command": "bunker", "args": ["mcp"]}}}

Remote alternative (no install): add <server>/mcp as a connector; it signs in with OAuth.`,
			Run: cmdMCP,
		},
		&command{
			Name: "init", Args: "[dir]", Group: groupAgents,
			Summary: "Set up a folder for Bunker and teach coding agents (AGENTS.md)",
			Flags:   []flagDoc{{Name: "site", Short: "s", Arg: "slug", Desc: "Link the folder to an existing site"}},
			Run:     cmdInit,
		},
		&command{
			Name: "tokens", Aliases: []string{"token"}, Args: "[ls|create|rm] ...", Group: groupAcct,
			Summary: "Manage personal access tokens for scripts and agents",
			Help: `  bunker tokens                        list
  bunker tokens create ci-deploy --scopes read,write --days 30
  bunker tokens rm 12`,
			Flags: []flagDoc{{Name: "scopes", Arg: "list", Desc: "create: read,write,admin (default read,write)"}, {Name: "days", Arg: "n", Desc: "create: lifetime in days"}},
			Run:   cmdTokens,
		},
	)
}

// ---- ai ----

func cmdAI(x *App, a *Args) error {
	site := a.Str("site")
	var prompt string
	switch {
	case site != "":
		prompt = strings.Join(a.Pos, " ")
	case len(a.Pos) >= 2:
		site, prompt = a.Pos[0], strings.Join(a.Pos[1:], " ")
	default:
		prompt = strings.Join(a.Pos, " ")
	}
	if strings.TrimSpace(prompt) == "" {
		return usageErr("describe what to build, e.g. `bunker ai \"a team lunch order form\"`")
	}
	c, err := x.client(cwdProject().Server)
	if err != nil {
		return err
	}
	path, body := "/api/v1/ai/create", map[string]any{"prompt": prompt}
	if site != "" {
		path, body = "/api/v1/sites/"+pathEsc(site)+"/builder", map[string]any{"message": prompt}
	}
	b, _ := json.Marshal(body)
	sp := x.spin("Thinking…")
	resp, err := c.Raw(x.ctx, "POST", path, nil, bytes.NewReader(b), "application/json")
	if err != nil {
		sp.end()
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		sp.end()
		e := c.errorFrom(resp)
		if ae, ok := e.(*APIError); ok && (ae.Status == 503 || strings.HasPrefix(ae.Code, "http_")) {
			ae.Hint = "AI may not be set up on this server. Check with `bunker api ai.status`."
		}
		return e
	}
	result := map[string]any{"site": site}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mt != "text/event-stream" {
		sp.end()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return fmt.Errorf("unexpected reply from the AI builder: %w", err)
		}
		if s, _ := out["slug"].(string); s != "" {
			site = s
		}
		if site != "" && out["url"] == nil {
			out["url"] = siteURL(c.Server, site)
		}
		return x.result(out, func() {
			x.ok("%s is being built %s", x.errSt.bold(site), x.errSt.dim("(it keeps going on the server)"))
			fmt.Fprintln(x.Stdout, x.outSt.url(fmt.Sprint(out["url"])))
			if a.Bool("open") {
				openBrowser(fmt.Sprint(out["url"]))
			}
		})
	}
	var files []string
	var text strings.Builder
	streaming := false
	var errMsg string
	err = mcp.ParseSSE(resp.Body, func(ev, data string) error {
		switch ev {
		case "status":
			if streaming {
				fmt.Fprintln(x.Stderr)
				streaming = false
			}
			sp.update(firstNonEmpty(mcp.SSEField(data, "status", "message", "text"), "Working…"))
			if s := sp; !s.active && !x.JSON {
				x.info("%s", mcp.SSEField(data, "status", "message", "text"))
			}
		case "text", "message":
			t := mcp.SSEField(data, "text", "delta", "content")
			text.WriteString(t)
			if !x.JSON {
				sp.end()
				streaming = true
				fmt.Fprint(x.Stderr, x.errSt.dim(t))
			}
		case "file":
			p := mcp.SSEField(data, "path", "name")
			files = append(files, p)
			if streaming {
				fmt.Fprintln(x.Stderr)
				streaming = false
			}
			sp.end()
			x.ok("%s", p)
			sp = x.spin("Working…")
		case "done":
			var m map[string]any
			if json.Unmarshal([]byte(data), &m) == nil {
				for k, v := range m {
					result[k] = v
				}
				if s, _ := m["slug"].(string); s != "" {
					site = s
				}
			}
		case "error":
			errMsg = mcp.SSEField(data, "message", "error")
		}
		return nil
	})
	sp.end()
	if streaming {
		fmt.Fprintln(x.Stderr)
	}
	if err != nil {
		return err
	}
	if errMsg != "" {
		return fmt.Errorf("the AI builder stopped: %s", errMsg)
	}
	result["site"], result["files"], result["text"] = site, files, strings.TrimSpace(text.String())
	u := ""
	if site != "" {
		u = siteURL(c.Server, site)
		if a.Str("site") != "" || len(a.Pos) >= 2 {
			u += "?draft=1"
		}
		result["url"] = u
	}
	return x.result(result, func() {
		x.ok("Done%s", map[bool]string{true: fmt.Sprintf(": %d files changed", len(files)), false: ""}[len(files) > 0])
		if u != "" {
			fmt.Fprintln(x.Stdout, x.outSt.url(u))
			if a.Bool("open") {
				openBrowser(u)
			}
		}
	})
}

// ---- api / ops ----

func findOp(ops []core.Op, name string) *core.Op {
	for i := range ops {
		if ops[i].Name == name || mcp.ToolName(ops[i].Name) == name {
			return &ops[i]
		}
	}
	return nil
}

func readAt(x *App, v string) ([]byte, error) {
	if v == "@-" {
		return io.ReadAll(x.Stdin)
	}
	return os.ReadFile(strings.TrimPrefix(v, "@"))
}

// coerce converts a CLI string into the param's JSON type.
func coerce(x *App, op *core.Op, p *core.Param, v string) (any, error) {
	if strings.HasPrefix(v, "@") && len(v) > 1 {
		if p.Type == "file" && op.Body == "multipart" {
			return filesArg(strings.TrimPrefix(v, "@"))
		}
		b, err := readAt(x, v)
		if err != nil {
			return nil, err
		}
		v = string(b)
		if p.Type == "object" || p.Type == "array" {
			var out any
			if err := json.Unmarshal(b, &out); err != nil {
				return nil, usageErr("--%s: %s isn't valid JSON", p.Name, v)
			}
			return out, nil
		}
		return v, nil
	}
	switch p.Type {
	case "integer", "number":
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, usageErr("--%s needs a number", p.Name)
		}
		return n, nil
	case "boolean":
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, usageErr("--%s needs true or false", p.Name)
		}
		return b, nil
	case "array":
		if strings.HasPrefix(strings.TrimSpace(v), "[") {
			var out []any
			if err := json.Unmarshal([]byte(v), &out); err != nil {
				return nil, usageErr("--%s: invalid JSON array", p.Name)
			}
			return out, nil
		}
		var out []any
		for _, s := range strings.Split(v, ",") {
			out = append(out, strings.TrimSpace(s))
		}
		return out, nil
	case "object":
		var out any
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			return nil, usageErr("--%s needs a JSON object", p.Name)
		}
		return out, nil
	}
	return v, nil
}

// filesArg turns a file or folder into {path: {base64}} for upload params.
func filesArg(p string) (any, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, usageErr("%s doesn't exist", p)
	}
	out := map[string]any{}
	add := func(rel, abs string) error {
		b, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		out[rel] = map[string]any{"base64": base64.StdEncoding.EncodeToString(b)}
		return nil
	}
	if !st.IsDir() {
		return out, add(filepath.Base(p), p)
	}
	files, err := collectFiles(p, NewIgnorer(p))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if err := add(f.Rel, f.Abs); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func cmdAPI(x *App, a *Args) error {
	name := a.Arg(0)
	if name == "" {
		return usageErr("which operation? List them with `bunker ops`")
	}
	c, err := x.client(cwdProject().Server)
	if err != nil {
		return err
	}
	ops, err := c.Ops(x.ctx)
	if err != nil {
		return err
	}
	op := findOp(ops, name)
	if op == nil {
		var names []string
		for _, o := range ops {
			names = append(names, o.Name)
		}
		msg := fmt.Sprintf("unknown operation %q", name)
		if s := suggest(name, names); s != "" {
			msg += fmt.Sprintf(" (did you mean %s?)", s)
		}
		return usageErr("%s; see `bunker ops`", msg)
	}
	if a.Has("help") {
		x.opHelp(op)
		return nil
	}
	args := map[string]any{}
	if d := a.Str("data"); d != "" {
		raw := []byte(d)
		if strings.HasPrefix(d, "@") {
			if raw, err = readAt(x, d); err != nil {
				return err
			}
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return usageErr("--data must be a JSON object: %v", err)
		}
	}
	// Positional values fill path params in order.
	pos := a.Pos[1:]
	for i := range op.Params {
		p := &op.Params[i]
		if p.In == "path" && len(pos) > 0 {
			if _, set := args[p.Name]; !set {
				args[p.Name] = pos[0]
				pos = pos[1:]
			}
		}
	}
	if len(pos) > 0 {
		return usageErr("too many arguments for %s: %s", op.Name, strings.Join(pos, " "))
	}
	for _, f := range a.Names() {
		if f == "data" || f == "help" {
			continue
		}
		var p *core.Param
		for i := range op.Params {
			if op.Params[i].Name == f {
				p = &op.Params[i]
			}
		}
		if p == nil {
			var ns []string
			for _, pp := range op.Params {
				ns = append(ns, "--"+pp.Name)
			}
			hint := "it takes no parameters"
			if len(ns) > 0 {
				hint = "it takes " + strings.Join(ns, ", ")
			}
			return usageErr("%s has no parameter --%s (%s)", op.Name, f, hint)
		}
		v, err := coerce(x, op, p, a.Str(f))
		if err != nil {
			return err
		}
		args[f] = v
	}
	if op.Destructive {
		if err := x.confirm(fmt.Sprintf("%s: %s. Continue?", op.Name, op.Summary)); err != nil {
			return err
		}
	}
	req, err := mcp.BuildRequest(*op, args)
	if err != nil {
		return usageErr("%s", err.Error())
	}
	var body io.Reader
	if req.Body != nil {
		body = bytes.NewReader(req.Body)
	}
	resp, err := c.Raw(x.ctx, req.Method, req.Path, req.Query, body, req.ContentType)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return c.errorFrom(resp)
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch {
	case mt == "text/event-stream":
		return mcp.ParseSSE(resp.Body, func(ev, data string) error {
			if x.JSON {
				var d any = data
				var j any
				if json.Unmarshal([]byte(data), &j) == nil {
					d = j
				}
				return jsonEncoder(x.Stdout).Encode(map[string]any{"event": ev, "data": d})
			}
			fmt.Fprintf(x.Stdout, "%s %s\n", x.outSt.cyan(ev), data)
			return nil
		})
	case mt == "application/json":
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if isTTY(x.Stdout) && !x.JSON {
			var buf bytes.Buffer
			if json.Indent(&buf, b, "", "  ") == nil {
				b = append(buf.Bytes(), '\n')
			}
		}
		_, err = x.Stdout.Write(b)
		return err
	case resp.StatusCode == 204:
		if x.JSON {
			return x.printJSON(map[string]any{"ok": true})
		}
		x.ok("Done")
		return nil
	}
	_, err = io.Copy(x.Stdout, resp.Body)
	return err
}

func cmdOps(x *App, a *Args) error {
	var c *Client
	server, token, err := x.conn(cwdProject().Server)
	if server == "" {
		return err
	}
	c = x.newClient(server, token)
	ops, err := c.Ops(x.ctx)
	if err != nil {
		return err
	}
	filter := a.Arg(0)
	var sel []core.Op
	for _, o := range ops {
		if filter == "" || strings.Contains(o.Name, filter) || strings.Contains(strings.ToLower(o.Summary), strings.ToLower(filter)) {
			sel = append(sel, o)
		}
	}
	if a.Bool("names") {
		for _, o := range sel {
			fmt.Fprintln(x.Stdout, o.Name)
		}
		return nil
	}
	if x.JSON {
		return x.printJSON(map[string]any{"ops": sel})
	}
	if a.Bool("full") {
		fmt.Fprint(x.Stdout, mcp.OpsList(sel, true))
		return nil
	}
	var rows [][]string
	for _, o := range sel {
		flags := ""
		if o.Destructive {
			flags = x.outSt.red("!")
		}
		if o.Access == core.AccessAdmin {
			flags += x.outSt.yellow("A")
		}
		rows = append(rows, []string{x.outSt.cyan(o.Name), o.Method, o.Path, flags, clipStr(o.Summary, 60)})
	}
	x.table([]string{"OPERATION", "METHOD", "PATH", "", "SUMMARY"}, rows)
	x.info("%d operations. Call one with `bunker api <operation> --param value`; see parameters with `bunker api <operation> --help`", len(sel))
	return nil
}

// ---- mcp ----

func cmdMCP(x *App, a *Args) error {
	server, token, err := x.conn(cwdProject().Server)
	if server == "" {
		return err
	}
	c := x.newClient(server, token)
	c.debug = nil // stdout belongs to the protocol
	if token == "" {
		fmt.Fprintln(x.Stderr, "bunker mcp: not signed in to "+server+"; tools will fail until you run `bunker login`")
	}
	srv := &mcp.Server{Backend: c, Version: core.Version}
	var who me
	if token != "" && c.Get(x.ctx, "/api/v1/me", nil, &who) == nil {
		srv.IsAdmin = &who.IsAdmin
	}
	fmt.Fprintln(x.Stderr, "bunker mcp: serving "+server+" over stdio")
	return mcp.ServeStdio(x.ctx, srv, x.Stdin, x.Stdout)
}

// ---- init ----

const agentsStart = "<!-- bunker:start -->"
const agentsEnd = "<!-- bunker:end -->"

func upsertSection(existing, section string) string {
	block := agentsStart + "\n" + strings.TrimSpace(section) + "\n" + agentsEnd + "\n"
	if i := strings.Index(existing, agentsStart); i >= 0 {
		if j := strings.Index(existing[i:], agentsEnd); j >= 0 {
			rest := existing[i+j+len(agentsEnd):]
			rest = strings.TrimPrefix(rest, "\n")
			return existing[:i] + block + rest
		}
	}
	if strings.TrimSpace(existing) == "" {
		return "# AGENTS.md\n\n" + block
	}
	return strings.TrimRight(existing, "\n") + "\n\n" + block
}

func cmdInit(x *App, a *Args) error {
	dir := firstNonEmpty(a.Arg(0), ".")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	proj, err := readProject(dir)
	if err != nil {
		return err
	}
	server, _, _ := x.conn(proj.Server)
	if server != "" {
		proj.Server = server
	}
	if s := a.Str("site"); s != "" {
		proj.Site = s
	}
	var wrote []string
	if err := proj.save(); err != nil {
		return err
	}
	wrote = append(wrote, projectFile)
	ap := filepath.Join(dir, "AGENTS.md")
	old, _ := os.ReadFile(ap)
	if err := os.WriteFile(ap, []byte(upsertSection(string(old), mcp.AgentsMD(proj.Server, proj.Site))), 0o644); err != nil {
		return err
	}
	wrote = append(wrote, "AGENTS.md")
	cp := filepath.Join(dir, "CLAUDE.md")
	cl, _ := os.ReadFile(cp)
	if !bytes.Contains(cl, []byte("AGENTS.md")) {
		add := "This is a Bunker site. Read AGENTS.md for the sandbox rules, the window.bunker SDK and how to deploy.\n\n@AGENTS.md\n"
		if len(cl) > 0 {
			cl = append(bytes.TrimRight(cl, "\n"), []byte("\n\n"+add)...)
		} else {
			cl = []byte("# CLAUDE.md\n\n" + add)
		}
		if err := os.WriteFile(cp, cl, 0o644); err != nil {
			return err
		}
		wrote = append(wrote, "CLAUDE.md")
	}
	return x.result(map[string]any{"dir": dir, "server": proj.Server, "site": proj.Site, "files": wrote}, func() {
		x.ok("Set up %s for Bunker: %s", dir, strings.Join(wrote, ", "))
		if proj.Server == "" {
			x.warn("Not signed in yet: run bunker login <url>, then bunker deploy")
		} else {
			x.info("Coding agents now know the SDK and sandbox rules. Deploy with: bunker deploy")
		}
	})
}

// ---- tokens ----

func cmdTokens(x *App, a *Args) error {
	c, err := x.client(cwdProject().Server)
	if err != nil {
		return err
	}
	sub := a.Arg(0)
	switch sub {
	case "", "ls", "list":
		var l struct {
			Tokens []struct {
				ID        int64    `json:"id"`
				Name      string   `json:"name"`
				Prefix    string   `json:"prefix"`
				Scopes    []string `json:"scopes"`
				Client    string   `json:"client"`
				CreatedAt int64    `json:"createdAt"`
				LastUsed  *int64   `json:"lastUsedAt"`
				ExpiresAt *int64   `json:"expiresAt"`
			} `json:"tokens"`
		}
		if err := c.Get(x.ctx, "/api/v1/tokens", nil, &l); err != nil {
			return err
		}
		return x.result(l, func() {
			var rows [][]string
			for _, t := range l.Tokens {
				lu, ex := "never", "never"
				if t.LastUsed != nil {
					lu = relTime(*t.LastUsed)
				}
				if t.ExpiresAt != nil {
					ex = relTime(*t.ExpiresAt)
				}
				mine := ""
				if strings.HasPrefix(c.Token, t.Prefix) {
					mine = x.outSt.green(" (this CLI)")
				}
				rows = append(rows, []string{strconv.FormatInt(t.ID, 10), clipStr(t.Name, 30) + mine, t.Prefix + "…", strings.Join(t.Scopes, ","), t.Client, lu, ex})
			}
			x.table([]string{"ID", "NAME", "TOKEN", "SCOPES", "CLIENT", "LAST USED", "EXPIRES"}, rows)
		})
	case "create", "new":
		name := strings.Join(a.Pos[1:], " ")
		if name == "" {
			return usageErr("give it a name, e.g. `bunker tokens create ci-deploy`")
		}
		body := map[string]any{"name": name}
		if s := a.Str("scopes"); s != "" {
			body["scopes"] = strings.Split(s, ",")
		}
		if d, err := a.Int("days", 0); err != nil {
			return err
		} else if d > 0 {
			body["days"] = d
		}
		var out struct {
			ID    int64  `json:"id"`
			Token string `json:"token"`
		}
		if err := c.Call(x.ctx, "POST", "/api/v1/tokens", nil, body, &out); err != nil {
			return err
		}
		return x.result(out, func() {
			x.ok("Created token %s %s", x.errSt.bold(name), x.errSt.dim(fmt.Sprintf("(id %d)", out.ID)))
			fmt.Fprintln(x.Stdout, out.Token)
			x.info("Copy it now; it won't be shown again. Use it as BUNKER_TOKEN or Authorization: Bearer <token>")
		})
	case "rm", "delete", "revoke":
		if len(a.Pos) < 2 {
			return usageErr("which token? e.g. `bunker tokens rm 12` (ids from `bunker tokens`)")
		}
		for _, id := range a.Pos[1:] {
			if _, err := strconv.Atoi(id); err != nil {
				return usageErr("token ids are numbers (see `bunker tokens`)")
			}
			if err := x.confirm("Revoke token " + id + "? Anything using it stops working."); err != nil {
				return err
			}
			if err := c.Call(x.ctx, "DELETE", "/api/v1/tokens/"+url.PathEscape(id), nil, nil, nil); err != nil {
				return err
			}
			x.ok("Revoked token %s", id)
		}
		if x.JSON {
			return x.printJSON(map[string]any{"ok": true})
		}
		return nil
	}
	return usageErr("unknown tokens command %q (use ls, create or rm)", sub)
}

func (x *App) opHelp(op *core.Op) {
	st := x.outSt
	fmt.Fprintf(x.Stdout, "%s  %s\n\n%s bunker api %s", st.bold(op.Name), op.Summary, st.dim("Usage:"), op.Name)
	for _, p := range op.Params {
		if p.In == "path" {
			fmt.Fprintf(x.Stdout, " <%s>", p.Name)
		}
	}
	fmt.Fprintf(x.Stdout, " [--param value]\n%s %s %s\n", st.dim("HTTP:"), op.Method, op.Path)
	if len(op.Params) > 0 {
		fmt.Fprintln(x.Stdout, "\n"+st.bold("Parameters"))
		for _, p := range op.Params {
			t := firstNonEmpty(p.Type, "string")
			req := ""
			if p.Required || p.In == "path" {
				req = st.yellow(" required")
			}
			desc := p.Desc
			if len(p.Enum) > 0 {
				desc = strings.TrimSpace(desc + " (" + strings.Join(p.Enum, "|") + ")")
			}
			l := "--" + p.Name
			fmt.Fprintf(x.Stdout, "  %s%s %s%s %s\n", st.cyan(l), strings.Repeat(" ", max(1, 18-len(l))), st.dim(t+" in "+p.In), req, desc)
		}
	}
	if op.Destructive {
		fmt.Fprintln(x.Stdout, "\n"+st.red("Destructive:")+" asks for confirmation (skip with --yes).")
	}
}
