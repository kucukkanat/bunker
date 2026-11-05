package data

import (
	"context"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

// WaitIndexes waits until queued adaptive-index jobs are done.
func WaitIndexes() { ai.idle.Wait() }

// SweepIndexes runs the daily unused-index sweep as of now.
func SweepIndexes(now time.Time) { sweepIndexes(context.Background(), now) }

// QueryPlan returns EXPLAIN QUERY PLAN details of a list query as a
// reader of every entry would run it.
func QueryPlan(site int64, coll, where, order, after string) ([]string, error) {
	q := Query{Order: order, After: after, Limit: 20}
	if where != "" {
		m, err := parseWhere([]byte(where))
		if err != nil {
			return nil, err
		}
		q.Where = m
	}
	sqlq, args, err := buildQuery(site, coll, &core.User{ID: 1}, Access{ReadAll: true, ReadOwn: true}, q)
	if err != nil {
		return nil, err
	}
	rows, err := app.DB.Query("EXPLAIN QUERY PLAN "+sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			return nil, err
		}
		out = append(out, detail)
	}
	return out, rows.Err()
}
