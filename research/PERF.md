# Bunker performance measurements

## SQLite pool split (research recommendation #15) — not adopted

**Question.** Would a dedicated single-connection writer pool plus a read-only
reader pool beat the current single pool (`MaxOpenConns=8`, `_txlock=immediate`,
WAL) in `internal/core/db.go`?

**Benchmark.** `internal/core/db_bench_test.go` (`BenchmarkPools`): 32
goroutines run hot-path point reads (a `GetBySlug`-like `SELECT … FROM sites
WHERE slug=?` plus a `RoleOf`-like `site_members` lookup) over 2,000 sites while
4 goroutines write without pause: half autocommit `INSERT INTO audit`, half
short `BEGIN; INSERT; UPDATE sites; COMMIT` transactions. Each run is 100,000
reads. Reader pools use `query_only(1)` and `_txlock=deferred`.

```
go test ./internal/core -run '^$' -bench Pools -benchtime 100000x -count 3
```

4 vCPU Intel Xeon @ 2.10GHz, Go 1.26, modernc.org/sqlite, 3 runs each (range):

| Configuration | reads/s | p99 read | writes/s |
|---|---|---|---|
| **current**: one pool of 8, `_txlock=immediate` | 31.3k – 34.0k | 3.6 – 4.1 ms | 2.06k – 2.14k |
| one pool of 16, immediate | 31.8k – 36.7k | 5.0 – 6.3 ms | 1.21k – 1.43k |
| one pool of 8, `_txlock=deferred` | 31.5k – 33.1k | 3.6 – 3.9 ms | 2.26k – 2.33k |
| writer pool of 1 + reader pool of NumCPU (4) | 29.1k – 32.0k | 12.8 – 14.1 ms | 3.49k – 3.50k |
| writer pool of 1 + reader pool of 8 | 35.3k – 37.3k | 5.5 – 8.7 ms | 2.01k – 2.16k |
| writer pool of 2 + reader pool of 8 | 35.2k – 37.2k | 4.9 – 5.9 ms | 2.14k – 2.37k |

No write errors (`SQLITE_BUSY`) in any configuration.

**Findings.**
- No configuration is a clear win. The best split (1 writer + 8 readers) reads
  about 8% faster, which is close to run-to-run noise, and its p99 read latency
  is 1.5–2× worse. A reader pool sized to NumCPU starves reads (p99 ~13 ms).
- The premise of #15 doesn't hold for this code base: hot read paths use plain
  autocommit queries, not transactions, so `_txlock=immediate` never makes a
  read take the write lock. Only the 7 `BeginTx` call sites (all writes) use it,
  and for them `immediate` is the right choice: it avoids deferred-to-write lock
  upgrades failing with `SQLITE_BUSY` mid-transaction. `deferred` showed no
  measurable read benefit.
- WAL already lets readers run alongside the single writer. Waiting writers
  hold at most 4 of the 8 pool connections here, so readers are not starved.
- The hottest lookups (`sites.GetBySlug`, `RoleOf`, manifests, sessions) are
  also served from in-process caches before SQLite is touched.

**Decision.** Keep the single pool. A separate `a.RDB` would add a second
handle that every module has to use correctly (read-after-write on a different
connection is a subtle source of bugs), for no measured gain. Revisit if
profiling a real deployment shows pool wait time (`db.Stats().WaitDuration`)
growing.
