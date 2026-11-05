package data

import (
	"context"
	"database/sql"
)

// Write transactions hold SQLite's write lock from BEGIN IMMEDIATE to
// COMMIT, so every writer waits for whatever happens inside one, including
// parsing and planning each statement (~20% of a db add's CPU, and more
// with every index on docs). wtx runs the statements of the document write
// path through statements prepared once at startup, outside any lock;
// database/sql keeps them prepared per connection.

// txQ is what the write helpers need from a transaction.
type txQ interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type wtx struct{ *sql.Tx }

// writeStmts are prepared by prepareWriteStmts. The map is only written
// before the server starts, so reads need no lock.
var writeStmts = map[string]*sql.Stmt{}

var writeSQL = []string{
	`SELECT rule FROM collections WHERE site_id=? AND name=?`,
	sqlSiteUsage,
	sqlGetRow,
	sqlInsertDoc,
	sqlUpdateDoc,
	`UPDATE collections SET docs=docs+1, bytes=bytes+?, updated_at=? WHERE site_id=? AND name=?`,
	`UPDATE collections SET bytes=bytes+?, updated_at=? WHERE site_id=? AND name=?`,
	`DELETE FROM docs WHERE site_id=? AND coll=? AND id=?`,
	`UPDATE collections SET docs=MAX(docs-1,0), bytes=MAX(bytes-?,0), updated_at=? WHERE site_id=? AND name=?`,
}

const (
	sqlSiteUsage = `SELECT COALESCE(SUM(docs),0), COALESCE(SUM(bytes),0) FROM collections WHERE site_id=?`
	sqlGetRow    = `SELECT ` + rowCols + ` FROM docs WHERE site_id=? AND coll=? AND id=?`
	sqlInsertDoc = `INSERT INTO docs(site_id,coll,id,data,size,author_id,author_name,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`
	sqlUpdateDoc = `UPDATE docs SET data=?, size=?, updated_at=? WHERE site_id=? AND coll=? AND id=?`
)

func prepareWriteStmts(db *sql.DB) {
	for _, q := range writeSQL {
		if st, err := db.Prepare(q); err == nil {
			writeStmts[q] = st
		}
	}
}

func (t wtx) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if st := writeStmts[q]; st != nil {
		return t.Tx.StmtContext(ctx, st).QueryRowContext(ctx, args...)
	}
	return t.Tx.QueryRowContext(ctx, q, args...)
}

func (t wtx) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if st := writeStmts[q]; st != nil {
		return t.Tx.StmtContext(ctx, st).ExecContext(ctx, args...)
	}
	return t.Tx.ExecContext(ctx, q, args...)
}
