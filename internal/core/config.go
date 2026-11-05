package core

import (
	"os"
	"strconv"
	"strings"
)

// Config is static process configuration from environment variables.
// Runtime settings that admins change in the UI live in the settings
// table (see Settings).
type Config struct {
	URL        string // public base URL, e.g. https://bunker.corp.com (no trailing slash)
	Listen     string // listen address
	DataDir    string
	TLS        string // auto | off | file
	TLSCert    string
	TLSKey     string
	Dev        bool // local single-user development mode
	// ContentURL optionally serves user content from a separate origin
	// (e.g. https://usercontent.bunker.corp.com) for defense in depth.
	ContentURL string
	TrustProxy bool
}

// Env returns BUNKER_<name>, or def.
func Env(name, def string) string {
	if v, ok := os.LookupEnv("BUNKER_" + name); ok && v != "" {
		return v
	}
	return def
}

// EnvBool parses BUNKER_<name> as a bool.
func EnvBool(name string, def bool) bool {
	v := Env(name, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// EnvInt parses BUNKER_<name> as an int.
func EnvInt(name string, def int) int {
	v := Env(name, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// LoadConfig reads configuration from the environment, applying
// platform-friendly defaults (PORT from PaaS providers, etc).
func LoadConfig() *Config {
	c := &Config{
		URL:     strings.TrimRight(Env("URL", ""), "/"),
		DataDir: Env("DATA_DIR", ""),
		TLS:     Env("TLS", ""),
		TLSCert: Env("TLS_CERT", ""),
		TLSKey:  Env("TLS_KEY", ""),
		Dev:     EnvBool("DEV", false),
		ContentURL: strings.TrimRight(Env("CONTENT_URL", ""), "/"),
	}
	if c.DataDir == "" {
		if _, err := os.Stat("/data"); err == nil {
			c.DataDir = "/data"
		} else {
			c.DataDir = "./bunker-data"
		}
	}
	// PaaS platforms (Render, Railway, Fly, Heroku, Cloud Run, Azure) inject PORT
	// and terminate TLS for us.
	port := os.Getenv("PORT")
	c.Listen = Env("LISTEN", "")
	if c.Listen == "" {
		if port != "" {
			c.Listen = ":" + port
		} else if c.Dev {
			c.Listen = "127.0.0.1:8080"
		} else {
			c.Listen = ":8080"
		}
	}
	if c.URL == "" {
		c.URL = detectPlatformURL()
	}
	listenSet := Env("LISTEN", "") != ""
	if c.TLS == "" {
		switch {
		case c.TLSCert != "":
			c.TLS = "file"
		case port != "" || c.Dev || listenSet || !strings.HasPrefix(c.URL, "https://") || isLocalURL(c.URL) || inContainer():
			c.TLS = "off"
		default:
			// A bare machine with an https URL: get certificates automatically.
			c.TLS = "auto"
			c.Listen = ":443"
		}
	}
	if c.TLS == "auto" && !listenSet {
		c.Listen = ":443"
	}
	c.TrustProxy = EnvBool("TRUST_PROXY", port != "" || c.TLS == "off")
	if c.URL == "" && c.Dev {
		c.URL = "http://" + strings.Replace(c.Listen, "0.0.0.0", "localhost", 1)
		if strings.HasPrefix(c.Listen, ":") {
			c.URL = "http://localhost" + c.Listen
		}
	}
	return c
}

// inContainer: inside Docker/Kubernetes TLS is normally terminated by a
// proxy or the platform, so we never grab ports 80/443 automatically there.
func inContainer() bool {
	for _, f := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(f); err == nil {
			return true
		}
	}
	return os.Getenv("KUBERNETES_SERVICE_HOST") != "" || os.Getenv("container") != ""
}

func isLocalURL(u string) bool {
	return strings.Contains(u, "://localhost") || strings.Contains(u, "://127.") || strings.Contains(u, "://[::1]")
}

// detectPlatformURL figures out our public URL on common one-click hosts.
func detectPlatformURL() string {
	if v := os.Getenv("RENDER_EXTERNAL_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	if v := os.Getenv("RAILWAY_PUBLIC_DOMAIN"); v != "" {
		return "https://" + v
	}
	if v := os.Getenv("FLY_APP_NAME"); v != "" {
		return "https://" + v + ".fly.dev"
	}
	if v := os.Getenv("WEBSITE_HOSTNAME"); v != "" { // Azure App Service
		return "https://" + v
	}
	if v := os.Getenv("KOYEB_PUBLIC_DOMAIN"); v != "" {
		return "https://" + v
	}
	if v := os.Getenv("HEROKU_APP_DEFAULT_DOMAIN_NAME"); v != "" {
		return "https://" + v
	}
	if v := os.Getenv("APP_URL"); v != "" { // DigitalOcean App Platform
		return strings.TrimRight(v, "/")
	}
	return ""
}
