// Package core holds the shared contracts every Bunker module builds on:
// the App container, module + migration registry, operation registry
// (which powers the HTTP API, CLI and MCP from one definition), request
// identity, and HTTP helpers.
package core

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"sort"
	"sync"
)

// Version is set at build time with -ldflags "-X github.com/kucukkanat/bunker/internal/core.Version=v1.2.3".
var Version = "dev"

// Module is a self-contained feature (sites, db, files, ai, ...).
// Modules register themselves from an init() function and are wired by
// a blank import in cmd/bunker/modules.go.
type Module struct {
	Name string
	// Order controls Init order (lower first). Default 100.
	Order int
	// Migrations are applied in order, once each, tracked per module.
	Migrations []string
	// Init registers routes/ops and starts background work.
	Init func(a *App) error
}

var (
	modMu   sync.Mutex
	modules []Module
)

// Register adds a module. Call from init().
func Register(m Module) {
	modMu.Lock()
	defer modMu.Unlock()
	if m.Order == 0 {
		m.Order = 100
	}
	modules = append(modules, m)
}

// Modules returns registered modules sorted by Order then Name.
func Modules() []Module {
	modMu.Lock()
	defer modMu.Unlock()
	out := append([]Module(nil), modules...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// App is the dependency container passed to every module.
type App struct {
	Cfg   *Config
	DB    *sql.DB
	Blobs *Blobs
	Bus   *Bus
	Log   *slog.Logger
	Mux   *http.ServeMux

	// Sites resolves a site + the caller's role. Provided by the sites module.
	Sites SiteResolver
	// Authn identifies the caller from a request (session cookie or bearer
	// token). Provided by the auth module. Returns nil when anonymous.
	Authn func(r *http.Request) *User
	// Directory searches people and groups for share dialogs. Optional.
	Directory Directory

	// Hooks other modules can fill in.
	hooksMu sync.RWMutex
	hooks   map[string][]func(ctx context.Context, payload any)

	opsMu sync.RWMutex
	ops   []Op

	pubMu  sync.RWMutex
	public []string // path prefixes that skip authentication
}

// On registers a hook listener (e.g. "site.deleted").
func (a *App) On(event string, fn func(ctx context.Context, payload any)) {
	a.hooksMu.Lock()
	defer a.hooksMu.Unlock()
	if a.hooks == nil {
		a.hooks = map[string][]func(context.Context, any){}
	}
	a.hooks[event] = append(a.hooks[event], fn)
}

// Emit calls hook listeners synchronously.
func (a *App) Emit(ctx context.Context, event string, payload any) {
	a.hooksMu.RLock()
	fns := a.hooks[event]
	a.hooksMu.RUnlock()
	for _, fn := range fns {
		fn(ctx, payload)
	}
}

// Public marks a path prefix as not requiring a signed-in user.
func (a *App) Public(prefix string) {
	a.pubMu.Lock()
	defer a.pubMu.Unlock()
	a.public = append(a.public, prefix)
}

// IsPublic reports whether path skips authentication.
func (a *App) IsPublic(path string) bool {
	a.pubMu.RLock()
	defer a.pubMu.RUnlock()
	for _, p := range a.public {
		if p == path || (len(p) > 0 && p[len(p)-1] == '/' && len(path) >= len(p) && path[:len(p)] == p) {
			return true
		}
	}
	return false
}

// DirEntry is a person or group from the identity directory.
type DirEntry struct {
	Type  string `json:"type"` // user | group
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

// Directory searches the organization's people and groups.
type Directory interface {
	Search(ctx context.Context, u *User, q string) ([]DirEntry, error)
}
