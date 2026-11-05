package data

// Benchmarks behind the adaptive-index design (research/PERF.md):
//
//	go test ./internal/data -run '^$' -bench 'JSONFilter|IndexesOnDocs' -benchtime 2s

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/core"
)

// benchDocs makes a docs table (the module's schema) holding perColl
// documents in collection "items" of sites 1–3, written interleaved so a
// collection's rows are spread over the table as in a shared database.
func benchDocs(b *testing.B, perColl int) *sql.DB {
	b.Helper()
	db, err := core.OpenDB(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE docs (site_id INTEGER NOT NULL, coll TEXT NOT NULL, id TEXT NOT NULL, data TEXT NOT NULL, size INTEGER NOT NULL,
  author_id INTEGER NOT NULL, author_name TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY(site_id, coll, id));
CREATE INDEX docs_created ON docs(site_id, coll, created_at, id);
CREATE INDEX docs_author ON docs(site_id, coll, author_id, created_at);`); err != nil {
		b.Fatal(err)
	}
	tx, _ := db.Begin()
	st, _ := tx.Prepare(sqlInsertDoc)
	statuses := []string{"open", "closed", "waiting", "done"}
	for i := 0; i < perColl; i++ {
		for s := int64(1); s <= 3; s++ {
			d := fmt.Sprintf(`{"amount":%v,"n":%d,"note":"%s","status":"%s","tags":["a","b"],"title":"Item number %d"}`, float64(i%997)*1.25, i, strings.Repeat("x", 80), statuses[i%4], i)
			if _, err := st.Exec(s, "items", NewID(), d, len(d), 1+i%20, "Ann", 1700000000000+int64(i), 1700000000000+int64(i)); err != nil {
				b.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	return db
}

func benchIndex(site int64, field string) string {
	expr, _, _ := fieldExpr(field)
	return `CREATE INDEX ` + indexName(idxKey{site, "items", field}) + ` ON docs(site_id, coll, ` + expr + `, id) WHERE ` + docScope(site, "items")
}

func parallelQuery(b *testing.B, db *sql.DB, q string, args ...any) {
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rows, err := db.Query(q, args...)
			if err != nil {
				b.Error(err)
				return
			}
			for rows.Next() {
			}
			rows.Close()
		}
	})
}

// The harness's "where + order" query: status = 'open', newest amount
// first, 20 per page, over 10,000 documents.
func BenchmarkJSONFilter(b *testing.B) {
	w := &whereBuilder{}
	w.build(map[string]any{"status": "open"}, 0)
	o, _ := parseOrder("-amount")
	q := `SELECT ` + rowCols + `, ` + o.expr + ` FROM docs WHERE ` + docScope(2, "items") + ` AND ` + w.sql.String() + ` ORDER BY ` + o.expr + ` DESC, id DESC LIMIT 21`
	b.Run("no-index", func(b *testing.B) {
		parallelQuery(b, benchDocs(b, 10000), q, w.args...)
	})
	b.Run("adaptive-index", func(b *testing.B) {
		db := benchDocs(b, 10000)
		db.Exec(benchIndex(2, "amount"))
		db.Exec(benchIndex(2, "status"))
		db.Exec(`PRAGMA analysis_limit=1000`)
		db.Exec(`ANALYZE docs`)
		parallelQuery(b, db, q, w.args...)
	})
}

// Every index on docs costs every statement on docs: prepare-time code
// generation per index (unprepared insert) and the partial-index check
// per write. This is why the index counts are capped and why the write
// path uses statements prepared outside the write lock.
func BenchmarkIndexesOnDocs(b *testing.B) {
	for _, n := range []int{0, 8, 64, 200} {
		db := benchDocs(b, 2000)
		for i := 0; i < n; i++ {
			db.Exec(benchIndex(100+int64(i), fmt.Sprintf("f%d", i%7)))
		}
		doc := `{"amount":1,"status":"open"}`
		b.Run(fmt.Sprintf("indexes=%d/insert", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := db.Exec(sqlInsertDoc, 2, "items", NewID(), doc, len(doc), 1, "a", 1, 1); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("indexes=%d/insert-prepared", n), func(b *testing.B) {
			st, _ := db.Prepare(sqlInsertDoc)
			defer st.Close()
			for i := 0; i < b.N; i++ {
				if _, err := st.Exec(2, "items", NewID(), doc, len(doc), 1, "a", 1, 1); err != nil {
					b.Fatal(err)
				}
			}
		})
		var id string
		db.QueryRow(`SELECT id FROM docs WHERE site_id=2 LIMIT 1`).Scan(&id)
		b.Run(fmt.Sprintf("indexes=%d/get", n), func(b *testing.B) {
			parallelQuery(b, db, sqlGetRow, 2, "items", id)
		})
	}
}
