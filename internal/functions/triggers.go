package functions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/auth"
	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/sites"
)

// ---- cron (5 fields, UTC) ----

type cron struct {
	min, hour, dom, mon, dow uint64
	anyDom, anyDow           bool
}

// dayOK follows cron's rule: when both day fields are restricted, either
// may match.
func (c *cron) dayOK(t time.Time) bool {
	d := c.dom&(1<<uint(t.Day())) != 0
	w := c.dow&(1<<uint(t.Weekday())) != 0
	if c.anyDom || c.anyDow {
		return d && w
	}
	return d || w
}

func parseField(f string, lo, hi int) (uint64, error) {
	var set uint64
	for _, part := range strings.Split(f, ",") {
		step := 1
		if a, b, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(b)
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("bad step in %q", part)
			}
			step, part = n, a
		}
		from, to := lo, hi
		if part != "*" {
			a, b, isRange := strings.Cut(part, "-")
			x, err := strconv.Atoi(a)
			if err != nil {
				return 0, fmt.Errorf("%q isn't a number", a)
			}
			from, to = x, x
			if isRange {
				y, err := strconv.Atoi(b)
				if err != nil {
					return 0, fmt.Errorf("%q isn't a number", b)
				}
				to = y
			} else if step > 1 {
				to = hi
			}
		}
		if from < lo || to > hi || from > to {
			return 0, fmt.Errorf("%q is outside %d-%d", part, lo, hi)
		}
		for i := from; i <= to; i += step {
			set |= 1 << uint(i)
		}
	}
	return set, nil
}

var cronAliases = map[string]string{"@hourly": "0 * * * *", "@daily": "0 0 * * *", "@weekly": "0 0 * * 0", "@monthly": "0 0 1 * *"}

func parseCron(s string) (*cron, error) {
	s = strings.TrimSpace(s)
	if a, ok := cronAliases[s]; ok {
		s = a
	}
	f := strings.Fields(s)
	if len(f) != 5 {
		return nil, fmt.Errorf("use 5 fields: minute hour day-of-month month day-of-week, like \"*/15 * * * *\"")
	}
	var c cron
	var err error
	if c.min, err = parseField(f[0], 0, 59); err != nil {
		return nil, err
	}
	if c.hour, err = parseField(f[1], 0, 23); err != nil {
		return nil, err
	}
	if c.dom, err = parseField(f[2], 1, 31); err != nil {
		return nil, err
	}
	if c.mon, err = parseField(f[3], 1, 12); err != nil {
		return nil, err
	}
	if c.dow, err = parseField(f[4], 0, 7); err != nil {
		return nil, err
	}
	if c.dow&(1<<7) != 0 { // 7 is Sunday too
		c.dow = c.dow&^(1<<7) | 1
	}
	c.anyDom, c.anyDow = f[2] == "*", f[4] == "*"
	return &c, nil
}

// next returns the first matching minute strictly after t.
func (c *cron) next(t time.Time) time.Time {
	t = t.UTC().Truncate(time.Minute).Add(time.Minute)
	end := t.AddDate(5, 0, 0)
	for t.Before(end) {
		if c.mon&(1<<uint(t.Month())) == 0 {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if !c.dayOK(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if c.hour&(1<<uint(t.Hour())) == 0 {
			t = t.Truncate(time.Hour).Add(time.Hour)
			continue
		}
		if c.min&(1<<uint(t.Minute())) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return end
}

// ---- background work queue ----

type job struct {
	siteID  int64
	name    string
	input   json.RawMessage
	as      *core.User
	trigger map[string]any
}

var (
	jobs     = make(chan job, 2048)
	stopOnce sync.Once
	stop     = make(chan struct{})
	// writeTrig: site → collection → function names (live versions).
	trigMu    sync.RWMutex
	writeTrig = map[int64]map[string][]string{}
)

const workers = 4

func initTriggers(a *core.App) {
	a.On("site.published", func(ctx context.Context, p any) {
		m, _ := p.(map[string]any)
		id, _ := m["siteId"].(int64)
		if id > 0 {
			syncSite(context.WithoutCancel(ctx), id) // triggers are live when the deploy returns
		}
	})
	a.On("site.deleted", func(ctx context.Context, p any) {
		if id, ok := p.(int64); ok {
			trigMu.Lock()
			delete(writeTrig, id)
			trigMu.Unlock()
		}
	})
	a.On("db.written", onWrite)
	a.On("shutdown", func(context.Context, any) { stopOnce.Do(func() { close(stop) }) })
	loadWriteTriggers(context.Background())
	for i := 0; i < workers; i++ {
		go worker()
	}
	go scheduler()
}

func loadWriteTriggers(ctx context.Context) {
	rows, err := app.DB.QueryContext(ctx, `SELECT site_id, name, spec FROM fn_triggers WHERE kind='write'`)
	if err != nil {
		return
	}
	defer rows.Close()
	trigMu.Lock()
	defer trigMu.Unlock()
	for rows.Next() {
		var id int64
		var name, coll string
		rows.Scan(&id, &name, &coll)
		if writeTrig[id] == nil {
			writeTrig[id] = map[string][]string{}
		}
		writeTrig[id][coll] = append(writeTrig[id][coll], name)
	}
}

func worker() {
	for {
		select {
		case <-stop:
			return
		case j := <-jobs:
			runJob(j)
		}
	}
}

func runJob(j job) {
	ctx := context.Background()
	s, err := sites.GetByID(ctx, j.siteID, false)
	if err != nil || s.ArchivedAt != nil || s.Version == 0 {
		return
	}
	as := j.as
	if as == nil {
		if as, err = auth.LoadUser(ctx, s.OwnerID); err != nil || as == nil {
			return
		}
	}
	if _, err := run(core.WithUser(ctx, as), Call{Site: s, Name: j.name, Input: j.input, As: as, Trigger: j.trigger}); err != nil {
		app.Log.Debug("function trigger failed", "site", s.Slug, "fn", j.name, "err", err)
	}
}

func enqueue(j job) bool {
	select {
	case jobs <- j:
		return true
	default:
		app.Log.Warn("server function queue full; dropping a trigger", "site", j.siteID, "fn", j.name)
		return false
	}
}

// onWrite fires write triggers after a committed change.
func onWrite(ctx context.Context, p any) {
	if ctx.Value(triggerDepthKey) != nil {
		return // writes made by a write trigger don't trigger again
	}
	m, _ := p.(map[string]any)
	siteID, _ := m["siteId"].(int64)
	coll, _ := m["collection"].(string)
	trigMu.RLock()
	names := writeTrig[siteID][coll]
	trigMu.RUnlock()
	if len(names) == 0 {
		return
	}
	u, _ := m["user"].(*core.User)
	op := map[string]string{"added": "add", "updated": "update", "deleted": "delete"}[fmt.Sprint(m["type"])]
	if op == "" {
		op = fmt.Sprint(m["type"])
	}
	trig := map[string]any{"type": "write", "collection": coll, "op": op, "id": m["id"]}
	if d, ok := m["doc"].(json.RawMessage); ok && len(d) > 0 {
		trig["doc"] = d
	}
	input, _ := json.Marshal(trig)
	for _, n := range names {
		enqueue(job{siteID: siteID, name: n, input: input, as: u, trigger: trig})
	}
}

func scheduler() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			tick(time.Now())
		}
	}
}

func tick(now time.Time) {
	ctx := context.Background()
	rows, err := app.DB.QueryContext(ctx, `SELECT site_id, name, spec, next_at FROM fn_triggers WHERE kind='schedule' AND next_at>0 AND next_at<=? LIMIT 500`, now.Unix())
	if err != nil {
		return
	}
	type due struct {
		site       int64
		name, spec string
		at         int64
	}
	var ds []due
	for rows.Next() {
		var d due
		rows.Scan(&d.site, &d.name, &d.spec, &d.at)
		ds = append(ds, d)
	}
	rows.Close()
	for _, d := range ds {
		c, err := parseCron(d.spec)
		if err != nil {
			continue
		}
		next := c.next(now).Unix()
		res, err := app.DB.ExecContext(ctx, `UPDATE fn_triggers SET next_at=? WHERE site_id=? AND name=? AND kind='schedule' AND spec=? AND next_at=?`, next, d.site, d.name, d.spec, d.at)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue // another tick got it
		}
		at := time.Unix(d.at, 0).UTC().Format(time.RFC3339)
		trig := map[string]any{"type": "schedule", "at": at, "schedule": d.spec}
		input, _ := json.Marshal(trig)
		enqueue(job{siteID: d.site, name: d.name, input: input, trigger: trig})
	}
}

// syncSite refreshes a site's triggers from its live version and audits
// changes to its set of functions.
func syncSite(ctx context.Context, siteID int64) {
	s, err := sites.GetByID(ctx, siteID, false)
	if err != nil {
		return
	}
	cat := emptyCatalog
	if s.Version > 0 {
		if m, err := sites.LoadManifest(ctx, s.ID, s.Version); err == nil {
			cat = CatalogFor(ctx, m)
		}
	}
	type fnSum struct {
		Name     string   `json:"name"`
		Elevated bool     `json:"elevated,omitempty"`
		Schedule string   `json:"schedule,omitempty"`
		OnWrite  []string `json:"onWrite,omitempty"`
	}
	var sum []fnSum
	for _, n := range cat.Names() {
		d := cat.Fns[n]
		sum = append(sum, fnSum{n, d.Config.Elevated, d.Config.Schedule, d.Config.OnWrite})
	}
	b, _ := json.Marshal(sum)
	h := sha256.Sum256(b)
	sig := hex.EncodeToString(h[:])
	var prev string
	app.DB.QueryRowContext(ctx, `SELECT sig FROM fn_deployed WHERE site_id=?`, s.ID).Scan(&prev)
	if prev != sig && (prev != "" || len(sum) > 0) {
		app.DB.ExecContext(ctx, `INSERT INTO fn_deployed(site_id,sig) VALUES(?,?) ON CONFLICT(site_id) DO UPDATE SET sig=excluded.sig`, s.ID, sig)
		app.Audit(ctx, "functions.deployed", s.ID, s.Slug, map[string]any{"functions": sum, "version": s.Version})
	}

	tx, err := app.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	// Keep next_at for schedules that didn't change.
	old := map[string]int64{}
	if rows, err := tx.QueryContext(ctx, `SELECT name, spec, next_at FROM fn_triggers WHERE site_id=? AND kind='schedule'`, s.ID); err == nil {
		for rows.Next() {
			var n, sp string
			var at int64
			rows.Scan(&n, &sp, &at)
			old[n+"\x00"+sp] = at
		}
		rows.Close()
	}
	tx.ExecContext(ctx, `DELETE FROM fn_triggers WHERE site_id=?`, s.ID)
	writes := map[string][]string{}
	now := time.Now()
	for _, n := range cat.Names() {
		d := cat.Fns[n]
		if d.Err != "" {
			continue
		}
		if d.Config.Schedule != "" {
			c, err := parseCron(d.Config.Schedule)
			if err != nil {
				continue
			}
			at, ok := old[n+"\x00"+d.Config.Schedule]
			if !ok || at <= 0 {
				at = c.next(now).Unix()
			}
			tx.ExecContext(ctx, `INSERT OR IGNORE INTO fn_triggers(site_id,name,kind,spec,next_at) VALUES(?,?,?,?,?)`, s.ID, n, "schedule", d.Config.Schedule, at)
		}
		for _, coll := range d.Config.OnWrite {
			tx.ExecContext(ctx, `INSERT OR IGNORE INTO fn_triggers(site_id,name,kind,spec) VALUES(?,?,?,?)`, s.ID, n, "write", coll)
			writes[coll] = append(writes[coll], n)
		}
	}
	if tx.Commit() != nil {
		return
	}
	trigMu.Lock()
	if len(writes) == 0 {
		delete(writeTrig, s.ID)
	} else {
		writeTrig[s.ID] = writes
	}
	trigMu.Unlock()
}
