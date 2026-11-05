package data

// Adaptive expression indexes.
//
// Filters and sorts on JSON fields evaluate json_extract on every document
// of the collection. When one (site, collection, field) is queried often
// enough on a collection big enough to matter, a partial expression index
// is created for it in the background:
//
//	CREATE INDEX docs_x_<hash> ON docs(site_id, coll, json_extract(data, '$."f"'), id)
//	WHERE site_id=<site> AND coll='<coll>'
//
// Partial indexes can't use bound parameters, so the site id (an integer)
// and the collection name (validated by collRe, quoted anyway) are written
// into the index and into every query as literals; the query builder writes
// the JSON path as a literal too, so the query's expression is the same
// text as the index's and SQLite's planner matches them.
//
// Every index on docs costs every statement on docs a little (planning,
// and on writes the partial-index check), and on statements that aren't
// cached it costs a prepare-time code generation step per index; 2,000 of
// them made an insert ~500x slower. So the counts are capped (default 4
// per collection, 8 per site, 64 in all), indexes no query has touched
// for a week are dropped by a daily sweep, and they are dropped with their
// collection or site. They are recorded in doc_indexes so they survive
// restarts. Building one holds the write lock (~0.3 s per 100k documents
// of the collection; writers wait, up to busy_timeout); one is built at
// a time.
//
// Only conditions every match must meet count (=, IS NULL, ranges, $in,
// not under $or; $ne, $nin, $contains and $exists can't use an index),
// plus the sort field. _id, _created and _author have their own indexes.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

func autoIndexAfter() int     { return core.EnvInt("DB_INDEX_AFTER", 20) }
func autoIndexMinDocs() int64 { return int64(core.EnvInt("DB_INDEX_MIN_DOCS", 1000)) }
func autoIndexPerColl() int   { return core.EnvInt("DB_INDEX_PER_COLLECTION", 4) } // 0 turns adaptive indexes off
func autoIndexPerSite() int   { return core.EnvInt("DB_INDEX_PER_SITE", 8) }
func autoIndexMax() int       { return core.EnvInt("DB_INDEX_MAX", 64) }
func autoIndexUnused() time.Duration {
	return time.Duration(core.EnvInt("DB_INDEX_UNUSED_DAYS", 7)) * 24 * time.Hour
}

// autoIndexWindow is the window in which DB_INDEX_AFTER queries must arrive.
const autoIndexWindow = 10 * time.Minute

// maxIndexStats bounds the in-memory usage table.
const maxIndexStats = 20000

type idxKey struct {
	site  int64
	coll  string
	field string
}

type idxStat struct {
	n       int       // queries in the current window
	since   time.Time // start of the window
	used    time.Time // last query on an indexed field
	saved   time.Time // used as last written to doc_indexes
	indexed bool
	busy    bool // queued for creation
}

type idxJob struct {
	key   idxKey
	touch bool // persist used_at instead of creating
	used  time.Time
}

type indexer struct {
	mu    sync.Mutex
	stats map[idxKey]*idxStat
	jobs  chan idxJob
	idle  sync.WaitGroup // tests: wait for queued jobs
}

var ai = &indexer{stats: map[idxKey]*idxStat{}}

// docScope is the literal "site_id=… AND coll='…'" every docs query and
// partial index uses (literals, so partial indexes match).
func docScope(site int64, coll string) string {
	return "site_id=" + i64(site) + " AND coll='" + strings.ReplaceAll(coll, "'", "''") + "'"
}

// indexable reports whether field gains from an adaptive index: JSON
// fields and the meta columns that have no index of their own.
func indexableField(f string) bool {
	switch f {
	case "", "_id", "_created", "_author":
		return false
	}
	return true
}

func indexName(k idxKey) string {
	h := sha256.Sum256([]byte(i64(k.site) + "\x00" + k.coll + "\x00" + k.field))
	return "docs_x_" + hex.EncodeToString(h[:8])
}

var indexNameRe = regexp.MustCompile(`^docs_x_[0-9a-f]{16}$`)

// noteQuery counts a query's index-friendly fields and queues an index
// when one gets hot. It never blocks the query.
func noteQuery(site int64, coll string, fields []string, orderField string) {
	if autoIndexPerColl() <= 0 || ai.jobs == nil {
		return
	}
	now := time.Now()
	after := autoIndexAfter()
	var seen [9]string
	ns := 0
	ai.mu.Lock()
	defer ai.mu.Unlock()
	note := func(f string) {
		if !indexableField(f) {
			return
		}
		for _, s := range seen[:ns] {
			if s == f {
				return
			}
		}
		if ns < len(seen) {
			seen[ns] = f
			ns++
		}
		k := idxKey{site, coll, f}
		st := ai.stats[k]
		if st == nil {
			if len(ai.stats) >= maxIndexStats && !pruneStats(now) {
				return
			}
			st = &idxStat{since: now}
			ai.stats[k] = st
		}
		if st.indexed {
			st.used = now
			if now.Sub(st.saved) > time.Hour {
				st.saved = now
				ai.queue(idxJob{key: k, touch: true, used: now})
			}
			return
		}
		if now.Sub(st.since) > autoIndexWindow {
			st.n, st.since = 0, now
		}
		st.n++
		if st.n >= after && !st.busy {
			st.busy = ai.queue(idxJob{key: k})
		}
	}
	for _, f := range fields {
		note(f)
	}
	note(orderField)
}

// queue hands a job to the worker without blocking (ai.mu held).
func (a *indexer) queue(j idxJob) bool {
	a.idle.Add(1)
	select {
	case a.jobs <- j:
		return true
	default:
		a.idle.Done()
		return false
	}
}

// pruneStats drops idle counters (ai.mu held); reports whether there is room.
func pruneStats(now time.Time) bool {
	for k, st := range ai.stats {
		if !st.indexed && !st.busy && now.Sub(st.since) > autoIndexWindow {
			delete(ai.stats, k)
		}
	}
	return len(ai.stats) < maxIndexStats
}

// startIndexer loads the recorded indexes and starts the worker that
// builds them (one at a time) and the daily sweep. A nil stop runs them
// for the life of the process.
func startIndexer(stop <-chan struct{}) {
	ai.mu.Lock()
	ai.stats = map[idxKey]*idxStat{}
	ai.mu.Unlock()
	ai.jobs = make(chan idxJob, 64)
	loadIndexes()
	go func() {
		for {
			select {
			case <-stop:
				return
			case j := <-ai.jobs:
				runIndexJob(j)
				ai.idle.Done()
			}
		}
	}()
	go func() {
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				sweepIndexes(context.Background(), time.Now())
			}
		}
	}()
}

func loadIndexes() {
	rows, err := app.DB.Query(`SELECT site_id, coll, field, used_at FROM doc_indexes`)
	if err != nil {
		return
	}
	defer rows.Close()
	ai.mu.Lock()
	defer ai.mu.Unlock()
	for rows.Next() {
		var k idxKey
		var used int64
		if rows.Scan(&k.site, &k.coll, &k.field, &used) == nil {
			t := time.UnixMilli(used)
			ai.stats[k] = &idxStat{indexed: true, used: t, saved: t, since: t}
		}
	}
}

func runIndexJob(j idxJob) {
	ctx := context.Background()
	if j.touch {
		app.DB.ExecContext(ctx, `UPDATE doc_indexes SET used_at=? WHERE name=?`, j.used.UnixMilli(), indexName(j.key))
		return
	}
	ok, err := createIndex(ctx, j.key)
	if err != nil {
		app.Log.Warn("db index", "site", j.key.site, "coll", j.key.coll, "field", j.key.field, "err", err)
	}
	now := time.Now()
	ai.mu.Lock()
	if st := ai.stats[j.key]; st != nil {
		st.busy = false
		if ok {
			st.indexed, st.used, st.saved = true, now, now
		} else { // too small or at a cap: count again from zero
			st.n, st.since = 0, now
		}
	}
	ai.mu.Unlock()
}

// createIndex builds the index for k if the collection is big enough and
// the caps allow. It runs in one write transaction with the doc_indexes
// row, so a concurrent collection drop can't leave an orphan behind.
func createIndex(ctx context.Context, k idxKey) (bool, error) {
	expr, _, err := fieldExpr(k.field)
	if err != nil || checkColl(k.coll) != nil {
		return false, nil
	}
	tx, err := app.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var docs int64
	if tx.QueryRowContext(ctx, `SELECT docs FROM collections WHERE site_id=? AND name=?`, k.site, k.coll).Scan(&docs) != nil || docs < autoIndexMinDocs() {
		return false, nil
	}
	name := indexName(k)
	var total, inSite, inColl, exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(site_id=?),0), COALESCE(SUM(site_id=? AND coll=?),0), COALESCE(SUM(name=?),0) FROM doc_indexes`,
		k.site, k.site, k.coll, name).Scan(&total, &inSite, &inColl, &exists); err != nil {
		return false, err
	}
	if exists == 0 && (inColl >= autoIndexPerColl() || inSite >= autoIndexPerSite() || total >= autoIndexMax()) {
		return false, nil
	}
	start := time.Now()
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS `+name+` ON docs(site_id, coll, `+expr+`, id) WHERE `+docScope(k.site, k.coll)); err != nil {
		return false, err
	}
	if err := analyzeDocs(ctx, tx); err != nil {
		return false, err
	}
	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO doc_indexes(name, site_id, coll, field, created_at, used_at) VALUES(?,?,?,?,?,?)`, name, k.site, k.coll, k.field, now, now); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	app.Log.Info("db index created", "site", k.site, "coll", k.coll, "field", k.field, "docs", docs, "ms", time.Since(start).Milliseconds())
	return true, nil
}

// analyzeDocs refreshes the planner statistics of every index on docs,
// sampling at most 1,000 entries per index (the full statistics PRAGMA
// optimize keeps would read the whole table). Without statistics for the
// new index the planner prefers walking docs_created in order and reads a
// whole collection for a selective filter; with statistics for only some
// indexes it can pick a full scan plus sort over the index that serves a
// sort. Both measured; with every index sampled, the plans are right.
//
// It runs in the transaction that creates the index: other connections
// reload schema and statistics together when they see the schema change,
// so none plans with the new index but without its statistics (a separate
// ANALYZE after the commit left that window open). ~40 ms for 300k
// documents and 11 indexes.
func analyzeDocs(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `PRAGMA analysis_limit=1000`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `ANALYZE docs`)
	tx.ExecContext(ctx, `PRAGMA analysis_limit=0`) // the connection goes back to the pool
	return err
}

// dropIndexes drops the adaptive indexes of a collection (or of every
// collection of the site when coll is "") inside tx. Call forgetIndexes
// with the result after the commit.
func dropIndexes(ctx context.Context, tx *sql.Tx, site int64, coll string) ([]idxKey, error) {
	q := `SELECT name, coll, field FROM doc_indexes WHERE site_id=?`
	args := []any{site}
	if coll != "" {
		q += ` AND coll=?`
		args = append(args, coll)
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var names []string
	var keys []idxKey
	for rows.Next() {
		var name string
		k := idxKey{site: site}
		if err := rows.Scan(&name, &k.coll, &k.field); err != nil {
			rows.Close()
			return nil, err
		}
		names, keys = append(names, name), append(keys, k)
	}
	rows.Close()
	for _, n := range names {
		if !indexNameRe.MatchString(n) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS `+n); err != nil {
			return nil, err
		}
	}
	if len(names) > 0 {
		q = strings.Replace(q, "SELECT name, coll, field FROM", "DELETE FROM", 1)
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func forgetIndexes(keys []idxKey) {
	ai.mu.Lock()
	for _, k := range keys {
		delete(ai.stats, k)
	}
	ai.mu.Unlock()
}

// sweepIndexes saves last-use times and drops indexes unused for
// DB_INDEX_UNUSED_DAYS.
func sweepIndexes(ctx context.Context, now time.Time) {
	type touch struct {
		name string
		used int64
	}
	var touches []touch
	ai.mu.Lock()
	for k, st := range ai.stats {
		if st.indexed && st.used.After(st.saved) {
			st.saved = st.used
			touches = append(touches, touch{indexName(k), st.used.UnixMilli()})
		}
	}
	ai.mu.Unlock()
	for _, t := range touches {
		app.DB.ExecContext(ctx, `UPDATE doc_indexes SET used_at=? WHERE name=? AND used_at<?`, t.used, t.name, t.used)
	}
	rows, err := app.DB.QueryContext(ctx, `SELECT name, site_id, coll, field FROM doc_indexes WHERE used_at<?`, now.Add(-autoIndexUnused()).UnixMilli())
	if err != nil {
		return
	}
	type stale struct {
		name string
		k    idxKey
	}
	var list []stale
	for rows.Next() {
		var s stale
		if rows.Scan(&s.name, &s.k.site, &s.k.coll, &s.k.field) == nil && indexNameRe.MatchString(s.name) {
			list = append(list, s)
		}
	}
	rows.Close()
	for _, s := range list {
		tx, err := app.DB.BeginTx(ctx, nil)
		if err != nil {
			return
		}
		// Still unused (a query may have touched it since the select)?
		res, err := tx.ExecContext(ctx, `DELETE FROM doc_indexes WHERE name=? AND used_at<?`, s.name, now.Add(-autoIndexUnused()).UnixMilli())
		if n, _ := res.RowsAffected(); err != nil || n == 0 {
			tx.Rollback()
			continue
		}
		if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS `+s.name); err != nil || tx.Commit() != nil {
			tx.Rollback()
			continue
		}
		forgetIndexes([]idxKey{s.k})
		app.Log.Info("db index dropped (unused)", "site", s.k.site, "coll", s.k.coll, "field", s.k.field)
	}
}
