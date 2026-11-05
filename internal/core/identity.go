package core

import (
	"context"
	"net/http"
	"slices"
	"strings"
)

// Group is an identity-provider group (Entra object ID + display name).
type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// User is the authenticated caller.
type User struct {
	ID      int64   `json:"id"`
	Subject string  `json:"-"` // IdP subject / Entra oid
	Email   string  `json:"email"`
	Name    string  `json:"name"`
	IsAdmin bool    `json:"isAdmin"`
	Groups  []Group `json:"groups,omitempty"`

	// Set when authenticated with a personal access token.
	ViaToken bool     `json:"-"`
	Scopes   []string `json:"-"`
	TokenID  int64    `json:"-"`
	// SessionID is set for cookie sessions.
	SessionID string `json:"-"`
}

// HasScope reports whether the caller may perform actions of scope s.
// Cookie sessions have every scope (admin still requires IsAdmin).
func (u *User) HasScope(s string) bool {
	if !u.ViaToken {
		return true
	}
	if slices.Contains(u.Scopes, ScopeAdmin) {
		return true
	}
	if s == ScopeRead {
		return slices.Contains(u.Scopes, ScopeRead) || slices.Contains(u.Scopes, ScopeWrite)
	}
	return slices.Contains(u.Scopes, s)
}

// InGroup matches by group ID or (case-insensitive) display name. When g
// is an object ID (a GUID, as Entra uses), only the ID counts: display
// names are chosen by whoever creates a group, so a group named after
// another group's ID must not match it.
func (u *User) InGroup(g string) bool {
	byID := IsObjectID(g)
	for _, x := range u.Groups {
		if x.ID == g || (byID && strings.EqualFold(x.ID, g)) || (!byID && strings.EqualFold(x.Name, g)) {
			return true
		}
	}
	return false
}

// InGroupID matches by group ID only (for privileges like admin rights,
// where a self-chosen display name must never be enough).
func (u *User) InGroupID(g string) bool {
	for _, x := range u.Groups {
		if x.ID == g || (IsObjectID(g) && strings.EqualFold(x.ID, g)) {
			return true
		}
	}
	return false
}

// IsObjectID reports whether s looks like a GUID (8-4-4-4-12 hex).
func IsObjectID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

type ctxKey int

const (
	userKey ctxKey = iota
	ipKey
)

// WithUser attaches u to ctx.
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// UserFrom returns the signed-in user or nil.
func UserFrom(ctx context.Context) *User {
	u, _ := ctx.Value(userKey).(*User)
	return u
}

// MustUser returns the user or panics (use only after auth middleware).
func MustUser(r *http.Request) *User { return UserFrom(r.Context()) }

// WithIP attaches the client IP to ctx.
func WithIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ipKey, ip)
}

// IPFrom returns the client IP.
func IPFrom(ctx context.Context) string {
	s, _ := ctx.Value(ipKey).(string)
	return s
}

// Role on a site.
type Role int

const (
	RoleNone Role = iota
	RoleViewer
	RoleEditor
	RoleOwner
	RoleAdmin
)

func (r Role) String() string {
	switch r {
	case RoleViewer:
		return "viewer"
	case RoleEditor:
		return "editor"
	case RoleOwner:
		return "owner"
	case RoleAdmin:
		return "admin"
	}
	return "none"
}

// ParseRole parses viewer/editor/owner.
func ParseRole(s string) Role {
	switch strings.ToLower(s) {
	case "viewer", "view", "read":
		return RoleViewer
	case "editor", "edit", "write":
		return RoleEditor
	case "owner":
		return RoleOwner
	}
	return RoleNone
}

// Site is the minimal site record shared across modules.
type Site struct {
	ID      int64  `json:"id"`
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	OwnerID int64  `json:"ownerId"`
	Version int64  `json:"version"` // live version number, 0 = none
	Kind    string `json:"kind"`
	OrgView bool   `json:"orgView"`
}

// SiteResolver looks up a site and the caller's role on it.
type SiteResolver interface {
	Resolve(ctx context.Context, u *User, slug string) (*Site, Role, error)
}

// SiteFor resolves the {site} path parameter and enforces a minimum role.
// Unknown sites and sites the caller can't see both return 404.
func (a *App) SiteFor(r *http.Request, min Role) (*Site, Role, error) {
	u := UserFrom(r.Context())
	if u == nil {
		return nil, RoleNone, Unauthorized("sign in required")
	}
	slug := r.PathValue("site")
	s, role, err := a.Sites.Resolve(r.Context(), u, slug)
	if err != nil {
		return nil, RoleNone, err
	}
	if role == RoleNone {
		return nil, RoleNone, NotFound("site not found")
	}
	if role < min {
		return nil, role, Forbidden("you need " + min.String() + " access to this site")
	}
	return s, role, nil
}
