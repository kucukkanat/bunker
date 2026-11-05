// Package data is the runtime site pages use: per-site JSON document
// collections (bunker.db), live updates over SSE (db changes, rooms,
// presence, deploy notices), and per-viewer key/value storage
// (bunker.storage).
package data

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

func init() {
	core.Register(core.Module{
		Name:  "data",
		Order: 40,
		Migrations: []string{`
CREATE TABLE collections (
  site_id INTEGER NOT NULL,
  name TEXT NOT NULL,
  rule TEXT NOT NULL DEFAULT 'own',
  docs INTEGER NOT NULL DEFAULT 0,
  bytes INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(site_id, name)
);
CREATE TABLE docs (
  site_id INTEGER NOT NULL,
  coll TEXT NOT NULL,
  id TEXT NOT NULL,
  data TEXT NOT NULL,
  size INTEGER NOT NULL,
  author_id INTEGER NOT NULL,
  author_name TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(site_id, coll, id)
);
CREATE INDEX docs_created ON docs(site_id, coll, created_at, id);
CREATE INDEX docs_author ON docs(site_id, coll, author_id, created_at);
CREATE TABLE kv (
  site_id INTEGER NOT NULL,
  user_id INTEGER NOT NULL,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  size INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(site_id, user_id, key)
);
`, `
CREATE TABLE doc_indexes (
  name TEXT PRIMARY KEY,
  site_id INTEGER NOT NULL,
  coll TEXT NOT NULL,
  field TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  used_at INTEGER NOT NULL
);
CREATE INDEX doc_indexes_site ON doc_indexes(site_id, coll);
`},
		Init: initData,
	})
}

var app *core.App

func initData(a *core.App) error {
	app = a
	hub = newHub(a)
	registerDB(a)
	registerEvents(a)
	registerKV(a)
	registerMe(a)
	startIndexer(nil) // runs for the life of the process
	prepareWriteStmts(a.DB)
	// A site made from a template starts with the template's data rules.
	a.On("site.from_template", func(ctx context.Context, p any) {
		m, _ := p.(map[string]any)
		from, _ := m["from"].(int64)
		to, _ := m["to"].(int64)
		if from == 0 || to == 0 {
			return
		}
		now := core.Now()
		a.DB.ExecContext(ctx, `INSERT OR IGNORE INTO collections(site_id,name,rule,created_at,updated_at) SELECT ?,name,rule,?,? FROM collections WHERE site_id=? AND rule<>'own'`, to, now, now, from)
	})
	a.On("site.purge", func(ctx context.Context, p any) {
		id, ok := p.(int64)
		if !ok {
			return
		}
		ctx = context.WithoutCancel(ctx)
		if tx, err := a.DB.BeginTx(ctx, nil); err == nil {
			keys, err := dropIndexes(ctx, tx, id, "")
			if err == nil && tx.Commit() == nil {
				forgetIndexes(keys)
			} else {
				tx.Rollback()
			}
		}
		a.DB.ExecContext(ctx, `DELETE FROM docs WHERE site_id=?`, id)
		a.DB.ExecContext(ctx, `DELETE FROM collections WHERE site_id=?`, id)
		a.DB.ExecContext(ctx, `DELETE FROM kv WHERE site_id=?`, id)
		ruleCacheDrop(id)
	})
	return nil
}

var siteParam = core.Param{Name: "site", In: "path", Required: true, Desc: "Site address (slug)"}

// ---- limits ----

func docMaxBytes() int    { return core.EnvInt("DB_DOC_MAX_KB", 1024) << 10 }
func siteMaxBytes() int64 { return int64(core.EnvInt("DB_MAX_MB", 100)) << 20 }
func siteMaxDocs() int64  { return int64(core.EnvInt("DB_MAX_DOCS", 250000)) }
func maxCollections() int { return core.EnvInt("DB_MAX_COLLECTIONS", 200) }

// nowMS is a strictly increasing millisecond clock, so documents created
// in the same millisecond still sort in insertion order.
func nowMS() int64 {
	for {
		last := lastMS.Load()
		n := time.Now().UnixMilli()
		if n <= last {
			n = last + 1
		}
		if lastMS.CompareAndSwap(last, n) {
			return n
		}
	}
}

var lastMS atomic.Int64

// ISOTime formats unix milliseconds as an ISO 8601 UTC timestamp.
func ISOTime(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z") }

// parseTimeMS accepts an ISO timestamp or date and returns unix milliseconds.
func parseTimeMS(s string) (int64, bool) {
	for _, f := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(f, s); err == nil {
			return t.UnixMilli(), true
		}
	}
	return 0, false
}
func i64(i int64) string     { return strconv.FormatInt(i, 10) }
func humanMB(n int64) string { return strconv.FormatInt(n>>20, 10) + " MB" }

// ---- IDs ----

const idAlphabet = "0123456789abcdefghjkmnpqrstvwxyz" // Crockford base32, lowercase

// NewID returns a 12-character random base32 id (60 bits).
func NewID() string {
	var b [12]byte
	rand.Read(b[:])
	for i := range b {
		b[i] = idAlphabet[b[i]&31]
	}
	return string(b[:])
}

var (
	collRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	idRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,127}$`)
	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`) // rooms
)

func checkColl(name string) error {
	if !collRe.MatchString(name) {
		return core.BadRequest("collection names use up to 64 letters, numbers, dots, dashes and underscores, and can't start with a symbol")
	}
	return nil
}

func checkID(id string) error {
	if !idRe.MatchString(id) {
		return core.BadRequest("document ids use up to 128 letters, numbers and . _ : @ -, and can't start with a symbol")
	}
	return nil
}

// ---- rules ----

// Rule names. A rule decides what viewers (not editors) may do.
const (
	RuleOwn     = "own"     // read all, add, change/delete their own
	RulePrivate = "private" // add, read/change only their own
	RuleRead    = "read"    // read only
	RuleAppend  = "append"  // add, and read only their own; no changes
	RuleOpen    = "open"    // read, add, change and delete anything
	// "group:<name>": full access for members, read-only for everyone else.
)

// ValidRule normalizes a rule string or returns a friendly error.
func ValidRule(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case RuleOwn, RulePrivate, RuleRead, RuleAppend, RuleOpen:
		return strings.ToLower(s), nil
	case "":
		return RuleOwn, nil
	}
	if len(s) > 6 && strings.EqualFold(s[:6], "group:") {
		g := strings.TrimSpace(s[6:])
		if g == "" || len(g) > 200 {
			return "", core.BadRequest("group rules look like group:Finance")
		}
		return "group:" + g, nil
	}
	return "", core.BadRequest("rule must be one of own, private, read, append, open, or group:<name>")
}

// Access is what a caller may do under a rule.
type Access struct {
	Bypass  bool // editor or above: everything
	ReadAll bool
	ReadOwn bool
	Create  bool
	ModOwn  bool // change/delete own
	ModAny  bool // change/delete anyone's
}

// CanRead reports whether a document by author may be read.
func (x Access) CanRead(uid, author int64) bool {
	return x.Bypass || x.ReadAll || (x.ReadOwn && uid == author)
}

// CanModify reports whether a document by author may be changed or deleted.
func (x Access) CanModify(uid, author int64) bool {
	return x.Bypass || x.ModAny || (x.ModOwn && uid == author)
}

// AccessFor computes access for u with site role role under rule.
func AccessFor(u *core.User, role core.Role, rule string) Access {
	if role >= core.RoleEditor {
		return Access{Bypass: true, ReadAll: true, ReadOwn: true, Create: true, ModOwn: true, ModAny: true}
	}
	if role < core.RoleViewer || u == nil {
		return Access{}
	}
	switch rule {
	case RulePrivate:
		return Access{ReadOwn: true, Create: true, ModOwn: true}
	case RuleRead:
		return Access{ReadAll: true, ReadOwn: true}
	case RuleAppend:
		return Access{ReadOwn: true, Create: true}
	case RuleOpen:
		return Access{ReadAll: true, ReadOwn: true, Create: true, ModOwn: true, ModAny: true}
	}
	if strings.HasPrefix(rule, "group:") {
		if u.InGroup(rule[6:]) {
			return Access{ReadAll: true, ReadOwn: true, Create: true, ModOwn: true, ModAny: true}
		}
		return Access{ReadAll: true, ReadOwn: true}
	}
	return Access{ReadAll: true, ReadOwn: true, Create: true, ModOwn: true} // own
}

// RuleHelp explains a rule in plain words (used in errors).
func RuleHelp(rule string) string {
	switch rule {
	case RulePrivate:
		return "In this collection people can only see and change what they added themselves."
	case RuleRead:
		return "This collection is read-only for viewers. Ask an editor of the site to change it."
	case RuleAppend:
		return "In this collection people can add entries but not change or remove them."
	}
	if strings.HasPrefix(rule, "group:") {
		return "Only people in the " + rule[6:] + " group can change this collection."
	}
	return "You can only change entries you added yourself."
}

// rule cache: (site, coll) → rule. Invalidated on change; short TTL as a safety net.
type ruleEntry struct {
	rule string
	exp  time.Time
}

var (
	rcMu      sync.RWMutex
	ruleCache = map[string]ruleEntry{}
)

func ruleKey(site int64, coll string) string { return i64(site) + "/" + coll }

// CollectionRule returns the rule of a collection ("own" if it doesn't exist yet).
func CollectionRule(ctx context.Context, site int64, coll string) string {
	k := ruleKey(site, coll)
	rcMu.RLock()
	e, ok := ruleCache[k]
	rcMu.RUnlock()
	if ok && time.Now().Before(e.exp) {
		return e.rule
	}
	rule := RuleOwn
	err := app.DB.QueryRowContext(ctx, `SELECT rule FROM collections WHERE site_id=? AND name=?`, site, coll).Scan(&rule)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return RulePrivate // fail closed on database trouble
	}
	rcMu.Lock()
	if len(ruleCache) > 50000 {
		ruleCache = map[string]ruleEntry{}
	}
	ruleCache[k] = ruleEntry{rule: rule, exp: time.Now().Add(60 * time.Second)}
	rcMu.Unlock()
	return rule
}

func ruleCacheSet(site int64, coll, rule string) {
	rcMu.Lock()
	ruleCache[ruleKey(site, coll)] = ruleEntry{rule: rule, exp: time.Now().Add(60 * time.Second)}
	rcMu.Unlock()
}

func ruleCacheDrop(site int64) {
	p := i64(site) + "/"
	rcMu.Lock()
	for k := range ruleCache {
		if strings.HasPrefix(k, p) {
			delete(ruleCache, k)
		}
	}
	rcMu.Unlock()
}

// siteFor resolves the {site} path value with at least viewer access.
func siteFor(r *http.Request, min core.Role) (*core.Site, core.Role, *core.User, error) {
	s, role, err := app.SiteFor(r, min)
	if err != nil {
		return nil, role, nil, err
	}
	return s, role, core.UserFrom(r.Context()), nil
}
