# Bunker performance measurements

## Load and profiling harness (`test/perf`)

A Go program with no extra dependencies. It starts Bunker in a child
process on free loopback ports with a temp data directory in dev mode,
seeds data through the public API, then measures each scenario.

```
go run ./test/perf                          # everything (~3 min)
go run ./test/perf -only raw,sites-list     # shell,raw,sdk,sites-list,db,sse,upload,rss
go run ./test/perf -profile /tmp/prof       # + CPU and allocation profiles per scenario
go tool pprof -top /tmp/prof/raw-css--mix-.cpu.pprof
go tool pprof -sample_index=alloc_space -base /tmp/prof/X.allocs-before.pprof /tmp/prof/X.allocs-after.pprof
go build -o /tmp/bunker ./cmd/bunker && go run ./test/perf -bin /tmp/bunker -only rss,shell   # the real binary
```

- **Server process.** By default the child is the harness binary itself
  (`-child`), which links the same modules as `cmd/bunker` plus a
  `net/http/pprof` listener on a second loopback port and `/debug/stats`
  (MemStats, goroutines). pprof is only ever in this harness build, never
  in `bunker`. `-bin <path>` runs a real binary instead (throughput,
  latency and RSS, but no allocation numbers or profiles).
- **Seed.** `-users` (300) dev sessions via `/auth/dev?as=…` (users 0–9
  own sites, 10–29 are the measured viewers, the rest hold SSE streams,
  20 per user = the per-user cap); `-sites` (200) dropped sites with
  HTML (root-relative links), CSS, JS and a 12 KB image, a third
  org-visible, a third shared with a group, a third shared with each
  viewer by email; a data site with `-docs` (10,000) documents.
- **Scenarios.** Each throughput scenario runs `-c` (64) keep-alive
  connections for `-d` (5 s) after a 20% warm-up and reports req/s,
  p50/p99 latency and server mallocs and bytes per request. Shell pages
  and raw content are fetched like a browser does (raw content without
  cookies, ticket in the URL, `Accept-Encoding: gzip, deflate, br, zstd`).
  "Mix" rotates 20 viewers × 200 sites (4,000 distinct tickets).
  SSE opens `-sse` (5,000) raw `GET /events?ch=db:items` streams, then
  publishes `-sse-events` (1,000) document changes at `-sse-rate`
  (200/s) and measures publish-to-receive latency of every delivery.
  Uploads drop `-uploads` 4.7 MB zips (200 HTML pages + 16 incompressible
  images) at concurrency 1 and 4. RSS is read from `/proc/<pid>/status`
  (empty, idle after seeding, and after the run plus two GCs and
  `-settle`).
- The load generator shares the CPUs with the server (as the earlier
  numbers did). Other jobs on the machine move numbers by ±20%: compare
  runs made back to back, and repeat.

## Optimization pass (November 2026)

4 vCPU Intel Xeon @ 2.10 GHz, Go 1.26, default harness settings. Two
full runs of the old code (`ffda420`) and two of the new, alternating;
both values shown. Allocations are server-side per request.

| Scenario | req/s before | req/s after | p99 before → after | allocs/req | KB alloc/req |
|---|---|---|---|---|---|
| shell page (20 viewers × 200 sites)¹ | 23.4k / 28.7k | 39.1k / 38.3k | 10.3–13.2 → 8.4 ms | 202 → 67 | 8.4 → 7.3 |
| shell page (1 viewer, 1 site)¹ | 29.9k / 28.8k | 37.4k / 37.7k | 9.6–10.5 → 8.5–8.7 ms | 201 → 65 | 8.4 → 7.3 |
| raw HTML, hot (1 viewer, 1 site) | 24.5k / 24.3k | 31.9k / 37.6k | 11.3 → 8.9–11.2 ms | 125 → 71 | 8.3 → 6.1 |
| raw HTML, mix (4,000 tickets) | 424 / 487 | 27.4k / 29.4k | 740–860 → 16–20 ms | 305 → 82 | 27,400 → 24–29 |
| raw CSS, mix | 530 / 772 | 30.4k / 21.2k | 350–530 → 13–24 ms | 249 → 83 | 27,400 → 20–32 |
| raw JS, mix | 14.7k / 15.7k | 22.3k / 24.0k | 16–19 → 11–13 ms | 139 → 86 | 41 → 39 |
| raw image, mix | 17.0k / 13.9k | 25.7k / 25.4k | 16–20 → 11–12 ms | 133 → 79 | 20.6 → 18.4 |
| raw image, 304 | 24.2k / 25.9k | 36.4k / 41.1k | 11–12 → 8–10 ms | 126 → 71 | 8.2 → 6.0 |
| `/_/bunker.js` (unchanged code) | 25.0k / 28.9k | 29.3k / 32.0k | 12–13 → 10–11 ms | 55 → 55 | 14 → 14 |
| `GET /api/v1/sites`, viewer (201 sites) | 1.03k / 1.09k | 1.27k / 1.32k | 99–120 → 112–120 ms | 5,869 → 5,115 | 254 → 212 |
| `GET /api/v1/sites`, owner | 1.31k / 1.52k | 2.05k / 2.05k | 78–104 → 74–76 ms | 3,872 → 3,256 | 173 → 141 |
| db list (limit 50, 10k docs) | 5.8k / 4.7k | 6.1k / 6.3k | 37–48 → 30–32 ms | 1,127 → 1,127 | 64 → 64 |
| db query (where + order, 10k docs) | 65 / 65 | 65 / 61 | 3.5–3.6 → 3.2–4.3 s | 563 → 563 | 33 → 33 |
| db get by id | 22.9k / 23.7k | 23.2k / 25.7k | 11–12 → 10–11 ms | 109 → 110 | 6.7 → 6.7 |
| db add | 2.98k / 2.85k | 2.88k / 3.13k | 107–108 → 105–108 ms | 214 → 214 | 12 → 12 |
| drop 4.7 MB zip, concurrency 1 | 26.5 / 21.8 MB/s | 23.8 / 27.7 MB/s | 216–244 → 180–229 ms | 14.2k → 11.1k | 35,500 → 10,800 |
| drop 4.7 MB zip, concurrency 4 | 88 / 46 MB/s | 57 / 59 MB/s | 223–436 → 338–375 ms | 11.8k → 8.7k | 35,300 → 10,500 |

¹ Shell rows: a separate back-to-back run (`-only shell`, `-d 4s`) of
the build just before and just after the shell renderer change (item 6
below); in the full runs the shell page was 15.4k/29.8k before and
25.6k/30.1k without that change.

(db list/query/get/add and the SDK had no code changes that touch them:
their differences are run-to-run noise.)

**SSE, 5,000 connections on one collection, 1,000 changes at 200/s**
(5 million deliveries):

| | before | after |
|---|---|---|
| delivered | 46% / 64% (queues overflowed) | 100% / 100% |
| deliveries/s | 65k / 90k | 929k / 932k |
| publish → receive p50 / p99 | 0.89 s / 1.30 s; 0.50 s / 1.08 s | 99 ms / 297 ms; 82 ms / 254 ms |
| open 5,000 streams | 370–490 ms | 350–370 ms |
| memory per connection | RSS +30–32 KB, heap+stack 45 KB | RSS +30–33 KB, heap+stack 31.5 KB |
| goroutines per connection | 2 (handler + net/http reader) | 2 |

At the end of the fan-out the heap is ~160 MB and stacks ~63 MB (both
versions; mostly net/http's per-connection buffers). After the streams
close and two GCs, RSS returns to ~60 MB (heap 17 MB), same as before:
nothing is retained.

**Memory and startup** (real stripped binary, `-bin`, dev mode):

| | before | after |
|---|---|---|
| RSS, empty data dir | 26.7 MB | 27.8–28.9 MB |
| RSS idle after seeding 300 users, 200 sites, 10k docs | 41.2–41.5 MB | 41.8–42.0 MB |
| RSS after shell-page load + GC + 20 s | 45.1–45.2 MB | 45.8–46.2 MB |
| start to healthy (`/healthz`) | 255–307 ms | 154–205 ms |

The empty-start RSS is ~1–2 MB higher, most likely the pooled gzip
compressor kept by `sync.Pool` across the startup GC; it is not growth.

### What changed (by impact)

1. **Raw HTML/CSS cache** (`internal/sites/serve.go`). The transformed
   HTML/CSS (SDK injection + root-path rewrite) was cached per ticket
   prefix — per viewer, site and 6-hour window — in a 2,000-entry map
   cleared when full, and each miss read the blob, ran the rewrite
   regexps and compressed with brotli *and* gzip. With 4,000 distinct
   tickets nearly every request missed. Now: a per-blob template
   rewritten once with a placeholder for the prefix (substituted per
   ticket; a unit test checks it equals rewriting directly); per prefix
   only the compressed variant the client asked for; both caches are
   two-generation caches bounded by entries and bytes (`BUNKER_RAW_CACHE_MB`,
   16 MB per generation; templates 8 MB), so hot entries survive a rollover.
   Worst-case memory is now bounded (~48 MB); before, 2,000 entries × 3
   copies of pages of any size.
2. **Brotli window** (`core.CompressBrotli`). Quality 9 with the default
   4 MB window sets up a 32 MB hash table even for a 5 KB page (5 ms,
   33 MB allocated). The window is now sized to the input when it is at
   most 64 KB: 0.75 ms, 1.4 MB, same output size (±4 bytes on the
   platform's own JS/CSS). Gzip writers (~800 KB of state) are pooled.
   Blobs brotli doesn't shrink are remembered instead of recompressed on
   every request. This also speeds up startup (static asset compression).
3. **SSE fan-out** (`internal/data/events.go`). Each event made every
   connection re-resolve site and role (two lookups under the global
   sites mutex), re-parse the change and look up the collection rule.
   Now the topic forwarder parses each change and reads the rule once;
   a stream re-resolves the viewer's role only when the sites cache
   generation changed (any share/update/deploy invalidation — revocations
   still apply to the next event) or a second has passed; queued events
   are two pointers (4 KB queue per connection instead of 16 KB); frames
   are handed to the writer every 4 KB.
4. **Ticket viewers** (`loadTicketUser`). Sandboxed pages load assets
   without cookies, so every asset did a `users` query. Cached for 10 s
   and dropped immediately when the auth module invalidates a user (new
   `user.changed` hook emitted by `auth.InvalidateUser`).
5. **Site list** (`listSites`). One `site_members` query for all listed
   sites (`primeRoles`) instead of one `RoleOf` query per site; one
   `users` query for all owners; a subquery on `site_members(ptype, pid)`
   instead of `LEFT JOIN` + `DISTINCT` over every column (1.6 → 1.1 ms in
   SQLite); `settings = '{}'` not JSON-decoded. Site/role cache fills that
   race with an invalidation no longer re-insert stale entries.
6. **Shell page renderer** (`internal/sites/shell.go`). html/template's
   per-field escaper pipeline was ~40% of a shell request's CPU. The
   template is executed once with markers to get its static segments;
   requests write those with each value escaped exactly as html/template
   escapes it in that position (attribute/RCDATA escaping; URL filter and
   normalizer for `src` and `data-icon`). A differential test compares the
   bytes with the template for thousands of random and hostile inputs, and
   an unknown position falls back to the template. 202 → 66 allocs.
7. **Settings** (`App.Setting`) are cached in memory (SetSetting is the
   only writer; a generation counter keeps a racing read from caching a
   stale value). Every scanned site row called `PublicLinksAllowed`,
   i.e. a settings query.
8. **Uploads.** Zip entries and `.zip` parts are read into buffers sized
   from the declared size (capped at 16 MB per entry and 32 MB per part,
   so a lying header costs little); the babel check no longer lowercases
   two string copies of every HTML file; `Blobs.PutBytes` no longer
   copies. 35 → 10.8 MB allocated per 4.7 MB upload.
9. **`BUNKER_MEMORY_LIMIT`** (e.g. `400MiB`, `1GB`): sets
   `debug.SetMemoryLimit` at startup unless `GOMEMLIMIT` is set. Off by
   default.

### Behavior notes (what to watch)

- Settings are cached per process: anything that writes the `settings`
  table without `SetSetting` (nothing does today; a second process on the
  same data directory isn't supported) would not be seen.
- An open SSE stream picks up role changes that bypass `sites.Invalidate`
  within a second instead of on the next event; shares, updates and
  deploys still apply to the next event.
- Ticket viewers are cached 10 s; `auth.InvalidateUser` (disable, role or
  group changes, sign-out) drops the entry at once.
- Sites with equal `updated_at` may list in a different order than before
  (the order among ties was already unspecified).
- Brotli output for pages ≤ 64 KB uses a smaller window; it is ordinary
  brotli and the same size.

### Tried and not adopted

- **Prepared-statement cache** (`db.Prepare` per query string, bounded):
  the JSON-filter query got 15–20% slower (62–68 → 51–55 req/s at
  `-c 8`, three alternating runs; with the cache disabled it went back
  to 64–69), and nothing else measurably improved because the hot point
  reads are already served from in-process caches. Removed.
- **Writing db list responses as raw bytes** (skipping encoding/json's
  re-validation of `json.RawMessage`, ~20% of CPU in the profile): no
  measurable throughput change at `-c 8` (4.0–4.4k either way). Removed.
- **`mmap_size(268435456)`**: JSON-filter query +13% (65 → 73–74 req/s),
  everything else unchanged, idle RSS +2 MB (mapped pages count as
  resident). Not adopted given the footprint goal; a one-line change in
  `core.OpenDB` if wanted.

### Where the time goes now (profiles)

- **db query with `where` + `order` on a JSON field** (65 req/s over
  10k docs, ~60 ms CPU each): every row is parsed by `json_extract`
  (`jsonTranslateTextToBlob` ~22%), fetched from the table through the
  `docs_created` index (`BtreeTableMoveto` ~25%; `docs` is a rowid table
  whose primary key is a separate index), and modernc's libc `malloc`
  takes a global mutex (`Mutex.lockSlow` ~20% under concurrency). Next
  steps, in order of payoff: expression indexes created on demand for
  hot `(site, coll, field)` filters/sorts (the research doc's
  recommendation; needs the path inlined as a literal for the planner to
  use it), storing documents as JSONB, or a `WITHOUT ROWID` docs table
  (a table rewrite migration).
- **`GET /api/v1/sites`** with 200 visible sites (~1.3–2k req/s): ~70% is
  SQLite stepping and scanning ~210 rows × 17 columns (~5 µs/row in
  modernc), ~12% JSON encoding. A bigger win needs an in-memory snapshot
  of site rows, which means every writer of `sites` (about 20 statements
  in 5 packages) must invalidate it.
- **Raw content** (22–41k req/s) is now mostly net/http, header writes,
  file I/O and the (cached) site/role lookups; the HMAC ticket check is
  ~2% of CPU, not worth caching.

### Website claims (`website/src/pages/index.astro`), checked

- **"~30k req/s on 4 cores"**: holds for page/asset serving: the shell
  page ~38k (26–30k before this pass), raw content 22–41k, `/_/bunker.js` 29–32k req/s (load
  generator on the same 4 cores). Before this pass, it did **not** hold
  for raw HTML/CSS once more than a few hundred viewer×site pairs were
  active (~0.4–0.8k req/s). API calls are far lower: site list with 200
  sites 1.3–2k, db list 6k, db get 23–26k, db add ~3k, a JSON-field
  filter over 10k docs ~65 req/s.
- **"~26 MB idle"**: the stripped binary uses 26.7–28.9 MB with an empty
  data directory and ~42 MB idle after 300 users, 200 sites and 10k
  documents; ~46 MB after load.
- **"5,000 people per VM"**: 5,000 live SSE streams cost ~150 MB RSS
  (~30 KB each) and 10,000 goroutines, and one collection watched by all
  5,000 now keeps up with 200 changes/s (930k deliveries/s, p99 ~0.3 s);
  before, the same load dropped 36–54% of deliveries. That supports
  5,000 concurrent viewers on 4 vCPU with ~1 GB RAM for this workload;
  it doesn't cover heavy JSON-filter queries (above).

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
