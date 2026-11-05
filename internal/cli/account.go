package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

func init() {
	commands = append(commands,
		&command{
			Name: "login", Args: "[url]", Group: groupStart,
			Summary: "Sign in to a Bunker server",
			Help: `Opens your browser to approve this computer (a one-time code is shown
here too), then saves a personal token in ~/.config/bunker/config.json.

  bunker login https://bunker.corp.com
  bunker login https://bunker.corp.com --token bk_...   # paste a token
  echo $TOKEN | bunker login bunker.corp.com --token -  # token from stdin

CI: skip login and set BUNKER_SERVER and BUNKER_TOKEN instead.`,
			Flags: []flagDoc{
				{Name: "token", Arg: "?token", Desc: "Use an existing personal token (\"-\" reads stdin)"},
				{Name: "no-browser", Desc: "Don't open the browser automatically"},
				{Name: "scopes", Arg: "list", Desc: "Token scopes to request (default read,write)"},
			},
			Run: cmdLogin,
		},
		&command{
			Name: "logout", Args: "", Group: groupAcct,
			Summary: "Sign out and revoke this computer's token",
			Flags:   []flagDoc{{Name: "keep-token", Desc: "Forget the token locally but don't revoke it"}},
			Run:     cmdLogout,
		},
		&command{
			Name: "whoami", Args: "", Group: groupAcct,
			Summary: "Show who you're signed in as",
			Run:     cmdWhoami,
		},
	)
}

type me struct {
	ID      int64  `json:"id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"isAdmin"`
	Dev     bool   `json:"dev"`
	Groups  []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"groups"`
}

func cmdLogin(x *App, a *Args) error {
	server := firstNonEmpty(a.Arg(0), x.Server, x.Getenv("BUNKER_SERVER"))
	if server == "" {
		if p := x.config().Profiles[x.config().Current]; p != nil {
			server = p.Server
		}
	}
	if server == "" {
		if !isTTY(x.Stdin) {
			return usageErr("which server? e.g. `bunker login https://bunker.corp.com`")
		}
		server = x.prompt("Bunker address (e.g. bunker.corp.com):")
		if server == "" {
			return errCancelled
		}
	}
	server = normalizeServer(server)
	c := x.newClient(server, "")
	sp := x.spin("Connecting to " + server + "…")
	var health struct {
		Version string `json:"version"`
	}
	if err := c.Get(x.ctx, "/healthz", nil, &health); err != nil {
		sp.end()
		return err
	}
	sp.end()
	var token string
	if a.Has("token") {
		token = strings.TrimSpace(a.Str("token"))
		if token == "" || token == "true" || token == "-" {
			if isTTY(x.Stdin) && token != "-" {
				token = x.prompt("Paste your token:")
			} else {
				line, _ := bufio.NewReader(x.Stdin).ReadString('\n')
				token = strings.TrimSpace(line)
			}
		}
		if !strings.HasPrefix(token, "bk_") {
			return usageErr("that doesn't look like a Bunker token (they start with bk_)")
		}
	} else {
		t, err := x.deviceLogin(c, a)
		if err != nil {
			return err
		}
		token = t
	}
	c.Token = token
	var who me
	if err := c.Get(x.ctx, "/api/v1/me", nil, &who); err != nil {
		return err
	}
	cfg := x.config()
	name, p := cfg.profileFor(server)
	if x.Profile != "" {
		name, p = x.Profile, cfg.Profiles[x.Profile]
	}
	if name == "" {
		name = profileName(server)
	}
	if p == nil {
		p = &Profile{}
		cfg.Profiles[name] = p
	}
	p.Server, p.Token, p.Email, p.Name = server, token, who.Email, who.Name
	cfg.Current = name
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("couldn't save your login: %w", err)
	}
	return x.result(map[string]any{"server": server, "profile": name, "email": who.Email, "name": who.Name, "isAdmin": who.IsAdmin, "config": cfg.path}, func() {
		x.ok("Signed in to %s as %s", x.errSt.bold(server), x.errSt.bold(displayUser(who)))
		x.info("Next: run %s in a folder with an index.html", x.errSt.bold("bunker deploy"))
	})
}

func displayUser(m me) string {
	if m.Name != "" && m.Email != "" && m.Name != m.Email {
		return m.Name + " <" + m.Email + ">"
	}
	return firstNonEmpty(m.Email, m.Name)
}

func (x *App) deviceLogin(c *Client, a *Args) (string, error) {
	host, _ := os.Hostname()
	client := "Bunker CLI"
	if host != "" {
		client += " on " + host
	}
	if len(client) > 60 {
		client = client[:60]
	}
	in := map[string]any{"client": client}
	if s := a.Str("scopes"); s != "" {
		in["scopes"] = strings.Split(s, ",")
	}
	var start struct {
		DeviceCode string `json:"deviceCode"`
		UserCode   string `json:"userCode"`
		ExpiresIn  int    `json:"expiresIn"`
		Interval   int    `json:"interval"`
		URI        string `json:"verificationUri"`
		URIFull    string `json:"verificationUriComplete"`
	}
	if err := c.Call(x.ctx, "POST", "/api/v1/auth/device", nil, in, &start); err != nil {
		return "", err
	}
	s := x.errSt
	if x.JSON {
		jsonEncoder(x.Stderr).Encode(map[string]any{"userCode": start.UserCode, "verificationUri": start.URIFull})
	} else {
		fmt.Fprintf(x.Stderr, "\n  Your one-time code: %s\n\n", s.bold(s.moss(start.UserCode)))
	}
	opened := !a.Bool("no-browser") && isTTY(x.Stdin) && openBrowser(start.URIFull)
	if opened {
		x.info("Opened %s in your browser. Approve the code there.", s.url(start.URIFull))
	} else {
		x.info("Open %s and approve the code.", s.url(start.URIFull))
	}
	interval := time.Duration(max(1, start.Interval)) * time.Second
	deadline := time.Now().Add(time.Duration(max(60, start.ExpiresIn)) * time.Second)
	sp := x.spin("Waiting for approval…")
	defer sp.end()
	for time.Now().Before(deadline) {
		select {
		case <-x.ctx.Done():
			return "", errCancelled
		case <-time.After(interval):
		}
		var out struct {
			Token string `json:"token"`
		}
		err := c.Call(x.ctx, "POST", "/api/v1/auth/device/token", nil, map[string]any{"deviceCode": start.DeviceCode}, &out)
		if err == nil && out.Token != "" {
			return out.Token, nil
		}
		if ae, ok := err.(*APIError); ok {
			switch ae.Code {
			case "authorization_pending":
				continue
			case "access_denied":
				return "", fmt.Errorf("the sign-in was denied in the browser")
			case "expired_token":
				return "", fmt.Errorf("the code expired; run `bunker login` again")
			}
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("the code expired; run `bunker login` again")
}

func cmdLogout(x *App, a *Args) error {
	cfg := x.config()
	server, token, err := x.conn("")
	if err != nil {
		return err
	}
	name, p := cfg.profileFor(server)
	revoked := false
	if !a.Bool("keep-token") && token != "" {
		c := x.newClient(server, token)
		var l struct {
			Tokens []struct {
				ID     int64  `json:"id"`
				Prefix string `json:"prefix"`
			} `json:"tokens"`
		}
		if c.Get(x.ctx, "/api/v1/tokens", nil, &l) == nil {
			for _, t := range l.Tokens {
				if len(token) >= len(t.Prefix) && t.Prefix != "" && token[:len(t.Prefix)] == t.Prefix {
					if c.Call(x.ctx, "DELETE", fmt.Sprintf("/api/v1/tokens/%d", t.ID), nil, nil, nil) == nil {
						revoked = true
					}
					break
				}
			}
		}
	}
	if p != nil {
		p.Token = ""
		if err := cfg.Save(); err != nil {
			return err
		}
	}
	return x.result(map[string]any{"server": server, "profile": name, "revoked": revoked}, func() {
		if revoked {
			x.ok("Signed out of %s and revoked the token", server)
		} else {
			x.ok("Signed out of %s", server)
		}
		if x.Getenv("BUNKER_TOKEN") != "" {
			x.warn("BUNKER_TOKEN is still set in your environment")
		}
	})
}

func cmdWhoami(x *App, a *Args) error {
	c, err := x.client(cwdProject().Server)
	if err != nil {
		return err
	}
	var who me
	if err := c.Get(x.ctx, "/api/v1/me", nil, &who); err != nil {
		return err
	}
	return x.result(map[string]any{"server": c.Server, "id": who.ID, "email": who.Email, "name": who.Name, "isAdmin": who.IsAdmin, "groups": who.Groups}, func() {
		role := ""
		if who.IsAdmin {
			role = " " + x.outSt.yellow("(admin)")
		}
		fmt.Fprintf(x.Stdout, "%s%s\n", x.outSt.bold(displayUser(who)), role)
		fmt.Fprintf(x.Stdout, "%s %s\n", x.outSt.dim("on"), c.Server)
		if len(who.Groups) > 0 {
			var g []string
			for _, gr := range who.Groups {
				g = append(g, gr.Name)
			}
			fmt.Fprintf(x.Stdout, "%s %s\n", x.outSt.dim("groups"), strings.Join(g, ", "))
		}
	})
}
