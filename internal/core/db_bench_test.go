package core

// Pool-configuration benchmark for research recommendation #15 (split the
// SQLite pools). 32 goroutines run hot-path style point reads (a
// GetBySlug-like SELECT plus a RoleOf-like membership lookup) while 4
// goroutines write (autocommit INSERTs and short INSERT+UPDATE
// transactions). Results are recorded in research/PERF.md.
//
//	go test ./internal/core -run '^$' -bench Pools -benchtime 100000x

import (
	"database/sql"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const benchPragmas = "_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-32000)"

type poolCfg struct {
	name string
	open func(path string) (w, r *sql.DB, err error)
}

func openPool(dsn string, max int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(max)
	db.SetMaxIdleConns(max)
	return db, db.Ping()
}

var poolCfgs = []poolCfg{
	{"current_single8_immediate", func(p string) (*sql.DB, *sql.DB, error) {
		db, err := OpenDB(filepath.Dir(p))
		return db, db, err
	}},
	{"single16_immediate", func(p string) (*sql.DB, *sql.DB, error) {
		db, err := openPool("file:"+p+"?"+benchPragmas+"&_txlock=immediate", 16)
		return db, db, err
	}},
	{"single8_deferred", func(p string) (*sql.DB, *sql.DB, error) {
		db, err := openPool("file:"+p+"?"+benchPragmas+"&_txlock=deferred", 8)
		return db, db, err
	}},
	{"writer1_readerNCPU", func(p string) (*sql.DB, *sql.DB, error) {
		w, err := openPool("file:"+p+"?"+benchPragmas+"&_txlock=immediate", 1)
		if err != nil {
			return nil, nil, err
		}
		r, err := openPool("file:"+p+"?"+benchPragmas+"&_pragma=query_only(1)&_txlock=deferred", runtime.NumCPU())
		return w, r, err
	}},
	{"writer1_reader8", func(p string) (*sql.DB, *sql.DB, error) {
		w, err := openPool("file:"+p+"?"+benchPragmas+"&_txlock=immediate", 1)
		if err != nil {
			return nil, nil, err
		}
		r, err := openPool("file:"+p+"?"+benchPragmas+"&_pragma=query_only(1)&_txlock=deferred", 8)
		return w, r, err
	}},
	{"writer2_reader8", func(p string) (*sql.DB, *sql.DB, error) {
		w, err := openPool("file:"+p+"?"+benchPragmas+"&_txlock=immediate", 2)
		if err != nil {
			return nil, nil, err
		}
		r, err := openPool("file:"+p+"?"+benchPragmas+"&_pragma=query_only(1)&_txlock=deferred", 8)
		return w, r, err
	}},
}

const benchSites = 2000

func seedBench(b *testing.B, db *sql.DB) {
	b.Helper()
	stmts := []string{
		`CREATE TABLE sites (id INTEGER PRIMARY KEY, slug TEXT UNIQUE NOT NULL, title TEXT NOT NULL, owner_id INTEGER NOT NULL, live_version INTEGER NOT NULL DEFAULT 0, settings TEXT NOT NULL DEFAULT '{}', updated_at INTEGER NOT NULL, deleted_at INTEGER)`,
		`CREATE TABLE site_members (site_id INTEGER NOT NULL, ptype TEXT NOT NULL, pid TEXT NOT NULL, role INTEGER NOT NULL, PRIMARY KEY(site_id, ptype, pid))`,
		`CREATE TABLE audit (id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, actor_id INTEGER, event TEXT NOT NULL, site_id INTEGER, target TEXT, meta TEXT)`,
		`CREATE INDEX audit_ts ON audit(ts)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			b.Fatal(err)
		}
	}
	tx, _ := db.Begin()
	for i := 1; i <= benchSites; i++ {
		tx.Exec(`INSERT INTO sites(id,slug,title,owner_id,updated_at) VALUES(?,?,?,?,?)`, i, fmt.Sprintf("site-%d", i), "Site", i%50+1, i)
		for j := 0; j < 5; j++ {
			tx.Exec(`INSERT INTO site_members VALUES(?,?,?,?)`, i, "user", fmt.Sprintf("u%d@corp.com", (i+j)%300), 1)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkPools(b *testing.B) {
	const readers, writers = 32, 4
	for _, cfg := range poolCfgs {
		b.Run(cfg.name, func(b *testing.B) {
			dir := b.TempDir()
			w, r, err := cfg.open(filepath.Join(dir, "bunker.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer w.Close()
			if r != w {
				defer r.Close()
			}
			seedBench(b, w)
			var left atomic.Int64
			left.Store(int64(b.N))
			var writes, busy atomic.Int64
			stop := make(chan struct{})
			lat := make([][]time.Duration, readers)
			var wg, wwg sync.WaitGroup
			b.ResetTimer()
			start := time.Now()
			for k := 0; k < writers; k++ {
				wwg.Add(1)
				go func(k int) {
					defer wwg.Done()
					for n := 0; ; n++ {
						select {
						case <-stop:
							return
						default:
						}
						var err error
						if n%2 == 0 {
							_, err = w.Exec(`INSERT INTO audit(ts,actor_id,event,site_id,target,meta) VALUES(?,?,?,?,?,?)`, time.Now().Unix(), k, "site.view", n%benchSites+1, "x", `{"a":1}`)
						} else {
							var tx *sql.Tx
							if tx, err = w.Begin(); err == nil {
								tx.Exec(`INSERT INTO audit(ts,actor_id,event,site_id,target) VALUES(?,?,?,?,?)`, time.Now().Unix(), k, "site.deploy", n%benchSites+1, "y")
								tx.Exec(`UPDATE sites SET updated_at=? WHERE id=?`, time.Now().UnixNano(), n%benchSites+1)
								err = tx.Commit()
							}
						}
						if err != nil {
							busy.Add(1)
						} else {
							writes.Add(1)
						}
					}
				}(k)
			}
			for g := 0; g < readers; g++ {
				wg.Add(1)
				go func(g int) {
					defer wg.Done()
					rng := rand.New(rand.NewPCG(uint64(g), 7))
					for left.Add(-1) >= 0 {
						t0 := time.Now()
						id := rng.IntN(benchSites) + 1
						var slug, title, settings string
						var owner, ver int64
						if err := r.QueryRow(`SELECT slug,title,owner_id,live_version,settings FROM sites WHERE slug=? AND deleted_at IS NULL`, fmt.Sprintf("site-%d", id)).Scan(&slug, &title, &owner, &ver, &settings); err != nil {
							b.Error(err)
							return
						}
						var role int
						r.QueryRow(`SELECT role FROM site_members WHERE site_id=? AND ptype='user' AND pid=?`, id, fmt.Sprintf("u%d@corp.com", g)).Scan(&role)
						lat[g] = append(lat[g], time.Since(t0))
					}
				}(g)
			}
			wg.Wait()
			el := time.Since(start)
			close(stop)
			wwg.Wait()
			b.StopTimer()
			all := slices.Concat(lat...)
			slices.Sort(all)
			if len(all) > 0 {
				b.ReportMetric(float64(all[len(all)*99/100].Microseconds()), "p99_read_µs")
			}
			b.ReportMetric(float64(b.N)/el.Seconds(), "reads/s")
			b.ReportMetric(float64(writes.Load())/el.Seconds(), "writes/s")
			b.ReportMetric(float64(busy.Load()), "write_errors")
		})
	}
}
