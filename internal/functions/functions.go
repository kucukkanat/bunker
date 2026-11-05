// Package functions runs server code that site owners drop into api/:
// each call gets a fresh QuickJS instance in a wazero sandbox (internal/fnrt)
// and talks to Bunker only through the same page-safe API pages use, as the
// viewer (or as the site owner for elevated functions).
package functions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kucukkanat/bunker/internal/auth"
	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/fnrt"
	"github.com/kucukkanat/bunker/internal/sites"
)

func init() {
	core.Register(core.Module{
		Name:  "functions",
		Order: 65,
		Migrations: []string{`
CREATE TABLE fn_secrets (
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  value TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  updated_by INTEGER,
  PRIMARY KEY (site_id, name)
);
CREATE TABLE fn_blocks (
  kind TEXT NOT NULL,
  target INTEGER NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  by_id INTEGER,
  at INTEGER NOT NULL,
  PRIMARY KEY (kind, target)
);
CREATE TABLE fn_runs (
  id INTEGER PRIMARY KEY,
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  trigger TEXT NOT NULL,
  user_id INTEGER,
  status TEXT NOT NULL,
  duration_ms INTEGER NOT NULL,
  error TEXT NOT NULL DEFAULT '',
  logs TEXT NOT NULL DEFAULT '',
  at INTEGER NOT NULL
);
CREATE INDEX fn_runs_site ON fn_runs(site_id, id);
CREATE TABLE fn_triggers (
  site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  spec TEXT NOT NULL,
  next_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, name, kind, spec)
);
CREATE INDEX fn_triggers_next ON fn_triggers(kind, next_at);
CREATE TABLE fn_deployed (
  site_id INTEGER PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
  sig TEXT NOT NULL
);
`},
		Init: initFunctions,
	})
}

var app *core.App

var (
	engOnce sync.Once
	eng     *fnrt.Engine
)

func engine() *fnrt.Engine {
	engOnce.Do(func() {
		dir := ""
		if app != nil && app.Cfg.DataDir != "" {
			dir = filepath.Join(app.Cfg.DataDir, "cache", "fnrt")
		}
		eng = fnrt.New(fnrt.Config{MaxConcurrent: 1024, CacheDir: dir})
	})
	return eng
}

func initFunctions(a *core.App) error {
	app = a
	sites.RegisterHiddenFiles(hidden)
	sites.RegisterDeployInfo(func(ctx context.Context, m sites.Manifest) map[string]any {
		cat := CatalogFor(ctx, m)
		if len(cat.Fns) == 0 {
			return nil
		}
		fns := []map[string]any{}
		for _, n := range cat.Names() {
			d := cat.Fns[n]
			f := map[string]any{"name": n, "path": d.Path}
			if d.Err != "" {
				f["error"] = d.Err
			}
			fns = append(fns, f)
		}
		return map[string]any{"functions": fns}
	})
	registerOps(a)
	initTriggers(a)
	go runLogWriter()
	return nil
}

// ---- settings ----

// Settings are the admin's server function settings.
type Settings struct {
	Enabled           bool     `json:"enabled"`
	MemoryMB          int      `json:"memoryMb"`
	TimeoutMs         int      `json:"timeoutMs"`
	TriggerTimeoutMs  int      `json:"triggerTimeoutMs"`
	SiteConcurrency   int      `json:"siteConcurrency"`
	GlobalConcurrency int      `json:"globalConcurrency"`
	Outbound          string   `json:"outbound"`
	Allowlist         []string `json:"allowlist"`
}

func defaultGlobal() int { return max(4, 2*runtime.NumCPU()) }

func intSetting(key string, def, lo, hi int) int {
	n, err := strconv.Atoi(strings.TrimSpace(app.Setting(key)))
	if err != nil || n <= 0 {
		return def
	}
	return min(max(n, lo), hi)
}

func current() Settings {
	s := Settings{
		Enabled:           app.Setting("functions.enabled") != "false",
		MemoryMB:          intSetting("functions.memory_mb", 64, 16, 1024),
		TimeoutMs:         intSetting("functions.timeout_ms", 30000, 1000, 300000),
		TriggerTimeoutMs:  intSetting("functions.trigger_timeout_ms", 120000, 1000, 900000),
		SiteConcurrency:   intSetting("functions.site_concurrency", 4, 1, 256),
		GlobalConcurrency: intSetting("functions.global_concurrency", defaultGlobal(), 1, 1024),
		Outbound:          app.Setting("functions.outbound"),
		Allowlist:         []string{},
	}
	switch s.Outbound {
	case "internet", "allowlist", "off":
	default:
		s.Outbound = "internet"
	}
	for _, h := range strings.Split(app.Setting("functions.allowlist"), ",") {
		if h = strings.TrimSpace(strings.ToLower(h)); h != "" {
			s.Allowlist = append(s.Allowlist, h)
		}
	}
	return s
}

// ---- blocks ----

// Block explains why server functions are off for an app.
type Block struct {
	Scope  string `json:"scope"` // global | user | site
	Reason string `json:"reason"`
	By     string `json:"by,omitempty"`
	At     int64  `json:"at,omitempty"`
}

func blockOf(ctx context.Context, kind string, id int64) *Block {
	var reason string
	var by, at int64
	if err := app.DB.QueryRowContext(ctx, `SELECT reason, COALESCE(by_id,0), at FROM fn_blocks WHERE kind=? AND target=?`, kind, id).Scan(&reason, &by, &at); err != nil {
		return nil
	}
	b := &Block{Scope: kind, Reason: reason, At: at}
	if by > 0 {
		app.DB.QueryRowContext(ctx, `SELECT name FROM users WHERE id=?`, by).Scan(&b.By)
	}
	return b
}

// BlockFor returns why functions can't run for a site, or nil.
func BlockFor(ctx context.Context, s *core.Site) *Block {
	if !current().Enabled {
		return &Block{Scope: "global", Reason: "An admin turned off server functions for this Bunker."}
	}
	if b := blockOf(ctx, "user", s.OwnerID); b != nil {
		return b
	}
	return blockOf(ctx, "site", s.ID)
}

func offError(b *Block) error {
	msg := "Server functions are turned off"
	switch b.Scope {
	case "user":
		msg += " for apps owned by this person"
	case "site":
		msg += " for this app"
	}
	msg += "."
	if r := strings.TrimSpace(b.Reason); r != "" && b.Scope != "global" {
		msg += " Reason: " + r
		if !strings.ContainsAny(r[len(r)-1:], ".!?") {
			msg += "."
		}
	}
	msg += " Ask a Bunker admin to turn them back on."
	return core.NewErr(http.StatusForbidden, "functions_off", msg)
}

// ---- concurrency ----

var (
	inflight  atomic.Int64
	siteMu    sync.Mutex
	siteSlots = map[int64]int{}
)

func acquire(ctx context.Context, siteID int64, st Settings) (func(), error) {
	deadline := time.Now().Add(150 * time.Millisecond)
	for {
		siteMu.Lock()
		if siteSlots[siteID] < st.SiteConcurrency && inflight.Load() < int64(st.GlobalConcurrency) {
			siteSlots[siteID]++
			inflight.Add(1)
			siteMu.Unlock()
			return func() {
				siteMu.Lock()
				if siteSlots[siteID]--; siteSlots[siteID] <= 0 {
					delete(siteSlots, siteID)
				}
				inflight.Add(-1)
				siteMu.Unlock()
			}, nil
		}
		siteMu.Unlock()
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, core.NewErr(http.StatusTooManyRequests, "busy", "This app's server functions are busy. Try again in a moment.")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ---- invoking ----

type ctxKey int

const triggerDepthKey ctxKey = 1

// Call describes one function run.
type Call struct {
	Site    *sites.SiteRecord
	Name    string
	Input   json.RawMessage
	As      *core.User // the caller; replaced by the owner for elevated functions
	Trigger map[string]any
	Draft   bool
}

// Result is a finished run.
type Result struct {
	Output   json.RawMessage `json:"output"`
	Duration time.Duration   `json:"-"`
	Stack    string          `json:"-"`
}

// Invoke runs a function as a user, for other modules (webhooks).
func Invoke(ctx context.Context, site *sites.SiteRecord, name string, input json.RawMessage, as *core.User, trigger map[string]any) (json.RawMessage, error) {
	res, err := run(ctx, Call{Site: site, Name: name, Input: input, As: as, Trigger: trigger})
	if err != nil {
		return nil, err
	}
	return res.Output, nil
}

func run(ctx context.Context, c Call) (*Result, error) {
	st := current()
	if b := BlockFor(ctx, &c.Site.Site); b != nil {
		return nil, offError(b)
	}
	var m sites.Manifest
	var err error
	if c.Draft {
		m, _, err = sites.WorkingManifest(ctx, c.Site)
	} else if c.Site.Version > 0 {
		m, err = sites.LoadManifest(ctx, c.Site.ID, c.Site.Version)
	}
	if err != nil || m == nil {
		return nil, core.NotFound("this app has no published version yet")
	}
	def := CatalogFor(ctx, m).Fns[c.Name]
	if def == nil {
		return nil, core.NotFound("there's no server function called " + c.Name + " (add api/" + c.Name + ".js)")
	}
	if def.Err != "" {
		return nil, core.NewErr(http.StatusInternalServerError, "function_error", "api/"+strings.TrimPrefix(def.Path, Dir)+" can't run: "+def.Err)
	}
	as := c.As
	if def.Config.Elevated {
		owner, err := auth.LoadUser(ctx, c.Site.OwnerID)
		if err != nil || owner == nil {
			return nil, core.Forbidden("this app's owner left, so its elevated functions can't run")
		}
		as = owner
	}
	if as == nil {
		return nil, core.Unauthorized("sign in to use this app")
	}
	trig := c.Trigger
	if trig == nil {
		trig = map[string]any{"type": "call"}
	}
	background := trig["type"] != "call" && trig["type"] != "webhook"
	timeout := time.Duration(st.TimeoutMs) * time.Millisecond
	if background {
		timeout = time.Duration(st.TriggerTimeoutMs) * time.Millisecond
	}
	if def.Config.TimeoutMs > 0 {
		timeout = min(timeout, time.Duration(def.Config.TimeoutMs)*time.Millisecond)
	}
	release, err := acquire(ctx, c.Site.ID, st)
	if err != nil {
		return nil, err
	}
	defer release()

	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if trig["type"] == "write" {
		rctx = context.WithValue(rctx, triggerDepthKey, 1)
	}
	// ctx.user is who asked, even when an elevated function acts as the
	// owner (schedules have no caller, so they report the owner).
	caller := c.As
	if caller == nil {
		caller = as
	}
	groups := make([]string, 0, len(caller.Groups))
	for _, g := range caller.Groups {
		groups = append(groups, g.Name)
	}
	cctx, _ := json.Marshal(map[string]any{
		"user":     map[string]any{"id": caller.ID, "email": caller.Email, "name": caller.Name, "groups": groups},
		"site":     map[string]any{"slug": c.Site.Slug, "title": c.Site.Title, "version": c.Site.Version, "draft": c.Draft},
		"trigger":  trig,
		"elevated": def.Config.Elevated,
	})
	input := c.Input
	if len(input) == 0 {
		input = json.RawMessage("null")
	}
	runID := core.RandomToken(8)
	logs := &logBuf{}
	h := &host{site: c.Site, as: as, st: st, ctx: rctx}
	topic := "site:" + strconv.FormatInt(c.Site.ID, 10) + ":logs"
	res, err := engine().Run(rctx, fnrt.Job{
		Code:    string(def.Code),
		Input:   input,
		Context: cctx,
		Limits: fnrt.Limits{
			MemoryBytes:  int64(st.MemoryMB) << 20,
			Timeout:      timeout,
			MaxOutput:    1 << 20,
			MaxLogBytes:  64 << 10,
			MaxHostCalls: 1000,
		},
		Host: h.handle,
		Log: func(level, line string) {
			logs.add(level, line)
			if app.Bus.Count(topic) > 0 {
				b, _ := json.Marshal(map[string]any{"run": runID, "name": c.Name, "level": level, "line": line, "at": time.Now().UnixMilli()})
				app.Bus.Publish(topic, b)
			}
		},
	})
	if res == nil {
		res = &fnrt.Result{}
	}
	rec := runRec{site: c.Site.ID, name: c.Name, trigger: trigName(trig), user: as.ID, dur: res.Duration, logs: logs.String(), at: core.Now()}
	if err != nil {
		herr, stack := mapErr(err, timeout)
		rec.status, rec.err = "error", herr.Message
		if stack != "" {
			rec.err += "\n" + stack
		}
		logRun(rec)
		return &Result{Duration: res.Duration, Stack: stack}, herr
	}
	rec.status = "ok"
	logRun(rec)
	return &Result{Output: res.Output, Duration: res.Duration}, nil
}

func trigName(t map[string]any) string {
	s, _ := t["type"].(string)
	if s == "" {
		return "call"
	}
	return s
}

func errText(err error) string {
	var fe *fnrt.Error
	if errors.As(err, &fe) {
		return fe.Message
	}
	return err.Error()
}

// mapErr turns an engine error into an API error (and a stack for editors).
func mapErr(err error, timeout time.Duration) (*core.HTTPError, string) {
	var fe *fnrt.Error
	if !errors.As(err, &fe) {
		var he *core.HTTPError
		if errors.As(err, &he) {
			return he, ""
		}
		return core.NewErr(500, "function_error", err.Error()), ""
	}
	switch fe.Code {
	case "busy":
		return core.NewErr(429, "busy", "Server functions are busy right now. Try again in a moment."), ""
	case "timeout":
		return core.NewErr(504, "timeout", "The function took longer than "+timeout.String()+" and was stopped."), ""
	case "memory":
		return core.NewErr(500, "function_error", "The function ran out of memory and was stopped."), fe.Stack
	case "output_too_large":
		return core.NewErr(500, "function_error", "The function returned more than 1 MB."), ""
	case "too_many_calls":
		return core.NewErr(500, "function_error", "The function made more than 1,000 Bunker calls in one run."), ""
	default:
		return core.NewErr(500, "function_error", fe.Message), fe.Stack
	}
}

// ---- run log ----

type logBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuf) add(level, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.b.Len() > 8<<10 {
		return
	}
	if level != "" && level != "log" && level != "info" {
		l.b.WriteString("[" + level + "] ")
	}
	l.b.WriteString(line)
	l.b.WriteByte('\n')
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.b.String()
	if len(s) > 8<<10 {
		s = s[:8<<10] + "\n… (logs cut off)"
	}
	return s
}

type runRec struct {
	site              int64
	name, trigger     string
	user              int64
	status, err, logs string
	dur               time.Duration
	at                int64
}

var runQ = make(chan runRec, 1024)

func logRun(r runRec) {
	select {
	case runQ <- r:
	default: // the writer is behind; dropping a log line beats slowing calls
	}
}

const keepRuns = 200

func runLogWriter() {
	pruned := map[int64]int{}
	for r := range runQ {
		ctx := context.Background()
		app.DB.ExecContext(ctx, `INSERT INTO fn_runs(site_id,name,trigger,user_id,status,duration_ms,error,logs,at) VALUES(?,?,?,?,?,?,?,?,?)`,
			r.site, r.name, r.trigger, r.user, r.status, r.dur.Milliseconds(), r.err, r.logs, r.at)
		if pruned[r.site]++; pruned[r.site] >= 20 {
			pruned[r.site] = 0
			app.DB.ExecContext(ctx, `DELETE FROM fn_runs WHERE site_id=? AND id < (SELECT id FROM fn_runs WHERE site_id=? ORDER BY id DESC LIMIT 1 OFFSET ?)`, r.site, r.site, keepRuns-1)
		}
		if len(pruned) > 10000 {
			pruned = map[int64]int{}
		}
	}
}
