package cli

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Profile is one saved login.
type Profile struct {
	Server string `json:"server"`
	Token  string `json:"token,omitempty"`
	Email  string `json:"email,omitempty"`
	Name   string `json:"name,omitempty"`
}

// Config is ~/.config/bunker/config.json.
type Config struct {
	Current  string              `json:"current,omitempty"`
	Profiles map[string]*Profile `json:"profiles"`
	path     string
}

func (x *App) configPath() string {
	if p := x.Getenv("BUNKER_CONFIG"); p != "" {
		return p
	}
	dir := x.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		if runtime.GOOS == "windows" {
			dir, _ = os.UserConfigDir()
		} else if h, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(h, ".config")
		}
	}
	return filepath.Join(dir, "bunker", "config.json")
}

func (x *App) config() *Config {
	if x.cfg != nil {
		return x.cfg
	}
	c := &Config{Profiles: map[string]*Profile{}, path: x.configPath()}
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, c)
		if c.Profiles == nil {
			c.Profiles = map[string]*Profile{}
		}
	}
	x.cfg = c
	return c
}

// Save writes the config atomically with 0600 permissions.
func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// profileFor finds the profile saved for a server URL.
func (c *Config) profileFor(server string) (string, *Profile) {
	n := normalizeServer(server)
	if p := c.Profiles[c.Current]; p != nil && normalizeServer(p.Server) == n {
		return c.Current, p
	}
	for _, k := range sortedKeys(c.Profiles) {
		if normalizeServer(c.Profiles[k].Server) == n {
			return k, c.Profiles[k]
		}
	}
	return "", nil
}

// normalizeServer turns "bunker.corp.com" into "https://bunker.corp.com".
func normalizeServer(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		host := s
		if i := strings.IndexAny(host, "/"); i >= 0 {
			host = host[:i]
		}
		h := host
		if i := strings.LastIndexByte(h, ':'); i >= 0 {
			h = h[:i]
		}
		if h == "localhost" || h == "127.0.0.1" || h == "[::1]" || strings.HasSuffix(h, ".localhost") {
			s = "http://" + s
		} else {
			s = "https://" + s
		}
	}
	u, err := url.Parse(s)
	if err != nil {
		return strings.TrimRight(s, "/")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

func profileName(server string) string {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return "default"
	}
	return strings.NewReplacer(":", "-", "[", "", "]", "").Replace(u.Host)
}

var errNotSignedIn = errors.New("you're not signed in")

// conn picks the server and token: --server / BUNKER_SERVER, then
// the project's bunker.json server (preferServer), then --profile /
// BUNKER_PROFILE / the current profile. BUNKER_TOKEN overrides the token.
func (x *App) conn(preferServer string) (server, token string, err error) {
	c := x.config()
	envTok := x.Getenv("BUNKER_TOKEN")
	server = firstNonEmpty(x.Server, x.Getenv("BUNKER_SERVER"))
	prof := firstNonEmpty(x.Profile, x.Getenv("BUNKER_PROFILE"))
	if server == "" && prof != "" {
		p := c.Profiles[prof]
		if p == nil {
			return "", "", usageErr("no saved profile %q (have: %s)", prof, strings.Join(sortedKeys(c.Profiles), ", "))
		}
		server, token = p.Server, p.Token
	}
	if server == "" && preferServer != "" {
		server = preferServer
	}
	if server == "" {
		if p := c.Profiles[c.Current]; p != nil {
			server, token = p.Server, p.Token
		}
	}
	server = normalizeServer(server)
	if token == "" && server != "" {
		if _, p := c.profileFor(server); p != nil {
			token = p.Token
		}
	}
	if envTok != "" {
		token = envTok
	}
	if server == "" {
		return "", "", errNotSignedIn
	}
	if token == "" {
		return server, "", errNotSignedInTo(server)
	}
	return server, token, nil
}

type notSignedInTo struct{ server string }

func (e *notSignedInTo) Error() string   { return "you're not signed in to " + e.server }
func (e *notSignedInTo) Is(t error) bool { return t == errNotSignedIn }
func errNotSignedInTo(s string) error    { return &notSignedInTo{s} }

// client returns an API client for the active connection.
func (x *App) client(preferServer string) (*Client, error) {
	s, t, err := x.conn(preferServer)
	if err != nil {
		return nil, err
	}
	return x.newClient(s, t), nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
