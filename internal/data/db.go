package data

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/kucukkanat/bunker/internal/core"
)

var collParam = core.Param{Name: "coll", In: "path", Required: true, Desc: "Collection name"}
var idParam = core.Param{Name: "id", In: "path", Required: true, Desc: "Document id"}

// row is one stored document.
type row struct {
	ID         string
	Data       string
	AuthorID   int64
	AuthorName string
	Created    int64
	Updated    int64
}

// JSON renders the document with its meta fields first.
func (d *row) JSON() json.RawMessage {
	var b bytes.Buffer
	b.Grow(len(d.Data) + 128)
	b.WriteString(`{"_id":`)
	idj, _ := json.Marshal(d.ID)
	b.Write(idj)
	b.WriteString(`,"_author":`)
	b.WriteString(i64(d.AuthorID))
	b.WriteString(`,"_authorName":`)
	nj, _ := json.Marshal(d.AuthorName)
	b.Write(nj)
	b.WriteString(`,"_created":"`)
	b.WriteString(ISOTime(d.Created))
	b.WriteString(`","_updated":"`)
	b.WriteString(ISOTime(d.Updated))
	b.WriteByte('"')
	if len(d.Data) > 2 {
		b.WriteByte(',')
		b.WriteString(d.Data[1:])
	} else {
		b.WriteByte('}')
	}
	return b.Bytes()
}

const rowCols = `id, data, author_id, author_name, created_at, updated_at`

func scanRow(sc interface{ Scan(...any) error }, extra ...any) (*row, error) {
	d := &row{}
	dest := append([]any{&d.ID, &d.Data, &d.AuthorID, &d.AuthorName, &d.Created, &d.Updated}, extra...)
	if err := sc.Scan(dest...); err != nil {
		return nil, err
	}
	return d, nil
}

// decodeDoc reads a JSON object body, drops reserved "_" keys and
// returns its canonical encoding.
func decodeDoc(r io.Reader) (map[string]any, error) {
	max := int64(docMaxBytes())
	lr := io.LimitReader(r, max+1)
	raw, err := io.ReadAll(lr)
	if err != nil {
		return nil, core.BadRequest("couldn't read the request")
	}
	if int64(len(raw)) > max {
		return nil, core.TooLarge("this document is larger than " + strconv.Itoa(docMaxBytes()>>10) + " KB. Store big files with bunker.files instead.")
	}
	return parseDoc(raw)
}

func parseDoc(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, core.BadRequest("documents must be JSON objects, like {\"name\": \"Ada\"}")
	}
	stripMeta(m)
	return m, nil
}

func stripMeta(m map[string]any) {
	for k := range m {
		if strings.HasPrefix(k, "_") {
			delete(m, k)
		}
	}
}

func encodeDoc(m map[string]any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return "", core.BadRequest("this document can't be stored as JSON")
	}
	s := strings.TrimRight(buf.String(), "\n")
	if len(s) > docMaxBytes() {
		return "", core.TooLarge("this document is larger than " + strconv.Itoa(docMaxBytes()>>10) + " KB. Store big files with bunker.files instead.")
	}
	return s, nil
}

// mergePatch applies an RFC 7396 JSON merge patch.
func mergePatch(dst, patch map[string]any) {
	for k, v := range patch {
		if v == nil {
			delete(dst, k)
			continue
		}
		if pm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergePatch(dm, pm)
				continue
			}
			nm := map[string]any{}
			mergePatch(nm, pm)
			dst[k] = nm
			continue
		}
		dst[k] = v
	}
}

// ---- change events ----

// publishChange sends a change to subscribers of db:<coll>. The bus
// message is {"author":N,"data":<client payload>} so subscribers can
// filter by author cheaply without decoding the document.
func publishChange(siteID int64, coll, typ string, d *row) {
	topic := "site:" + i64(siteID) + ":db:" + coll
	if app.Bus.Count(topic) == 0 {
		return
	}
	var b bytes.Buffer
	b.WriteString(`{"author":`)
	b.WriteString(i64(d.AuthorID))
	b.WriteString(`,"data":{"type":"`)
	b.WriteString(typ)
	b.WriteString(`","doc":`)
	if typ == "deleted" {
		idj, _ := json.Marshal(d.ID)
		b.WriteString(`{"_id":`)
		b.Write(idj)
		b.WriteString(`,"_author":` + i64(d.AuthorID) + `}`)
	} else {
		b.Write(d.JSON())
	}
	b.WriteString(`}}`)
	app.Bus.Publish(topic, b.Bytes())
}

// ---- storage helpers ----

type usage struct{ docs, bytes int64 }

func siteUsage(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, siteID int64) usage {
	var u usage
	q.QueryRowContext(ctx, `SELECT COALESCE(SUM(docs),0), COALESCE(SUM(bytes),0) FROM collections WHERE site_id=?`, siteID).Scan(&u.docs, &u.bytes)
	return u
}

func checkQuota(u usage, addDocs, addBytes int64) error {
	if addDocs > 0 && u.docs+addDocs > siteMaxDocs() {
		return core.Quota("this site's database is full (" + strconv.FormatInt(siteMaxDocs(), 10) + " entries). Remove old entries or ask an admin to raise the limit.")
	}
	if addBytes > 0 && u.bytes+addBytes > siteMaxBytes() {
		return core.Quota("this site's database is full (" + humanMB(siteMaxBytes()) + "). Remove old entries or ask an admin to raise the limit.")
	}
	return nil
}

// ensureColl creates the collection row if needed and returns its rule.
func ensureColl(ctx context.Context, tx *sql.Tx, siteID int64, coll string) (string, error) {
	var rule string
	err := tx.QueryRowContext(ctx, `SELECT rule FROM collections WHERE site_id=? AND name=?`, siteID, coll).Scan(&rule)
	if err == nil {
		return rule, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var n int
	tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM collections WHERE site_id=?`, siteID).Scan(&n)
	if n >= maxCollections() {
		return "", core.Quota("this site already has " + strconv.Itoa(n) + " collections, the most allowed. Reuse an existing one.")
	}
	now := nowMS()
	if _, err := tx.ExecContext(ctx, `INSERT INTO collections(site_id,name,rule,created_at,updated_at) VALUES(?,?,?,?,?)`, siteID, coll, RuleOwn, now, now); err != nil {
		return "", err
	}
	return RuleOwn, nil
}

func getRow(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, siteID int64, coll, id string) (*row, error) {
	d, err := scanRow(q.QueryRowContext(ctx, `SELECT `+rowCols+` FROM docs WHERE site_id=? AND coll=? AND id=?`, siteID, coll, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.NotFound("no entry with id " + id + " in " + coll)
	}
	return d, err
}

// ---- reads ----

type listResult struct {
	Items []json.RawMessage `json:"items"`
	Next  *string           `json:"next"`
}

func runQuery(ctx context.Context, siteID int64, coll string, u *core.User, acc Access, q Query) (*listResult, error) {
	if !acc.ReadAll && !acc.ReadOwn {
		return nil, core.Forbidden("you can't read this collection")
	}
	if q.Limit <= 0 {
		q.Limit = 100
	}
	if q.Limit > maxLimit {
		q.Limit = maxLimit
	}
	o, err := parseOrder(q.Order)
	if err != nil {
		return nil, err
	}
	w := &whereBuilder{uid: u.ID}
	if err := w.build(q.Where, 0); err != nil {
		return nil, err
	}
	var sb strings.Builder
	args := []any{}
	sb.WriteString(`SELECT ` + rowCols + `, ` + o.expr + ` FROM docs WHERE site_id=? AND coll=?`)
	args = append(args, o.args...)
	args = append(args, siteID, coll)
	if !acc.ReadAll {
		sb.WriteString(` AND author_id=?`)
		args = append(args, u.ID)
	}
	sb.WriteString(" AND ")
	sb.WriteString(w.sql.String())
	args = append(args, w.args...)
	if q.After != "" {
		v, id, err := decodeCursor(q.After)
		if err != nil {
			return nil, err
		}
		c, cargs := o.cursorCond(v, id)
		sb.WriteString(" AND " + c)
		args = append(args, cargs...)
	}
	dir := " ASC"
	if o.desc {
		dir = " DESC"
	}
	sb.WriteString(" ORDER BY " + o.expr + dir + ", id" + dir + " LIMIT ?")
	args = append(args, o.args...)
	args = append(args, q.Limit+1)
	rows, err := app.DB.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		if strings.Contains(err.Error(), "malformed JSON") || strings.Contains(err.Error(), "JSON path") {
			return nil, core.BadRequest("that filter can't be applied")
		}
		return nil, err
	}
	defer rows.Close()
	res := &listResult{Items: []json.RawMessage{}}
	var lastV any
	var lastID string
	n := 0
	for rows.Next() {
		var sv any
		d, err := scanRow(rows, &sv)
		if err != nil {
			return nil, err
		}
		n++
		if n > q.Limit {
			c := encodeCursor(lastV, lastID)
			res.Next = &c
			break
		}
		res.Items = append(res.Items, d.JSON())
		lastV, lastID = sv, d.ID
	}
	return res, rows.Err()
}

func countDocs(ctx context.Context, siteID int64, coll string, u *core.User, acc Access, where map[string]any) (int64, error) {
	if !acc.ReadAll && !acc.ReadOwn {
		return 0, core.Forbidden("you can't read this collection")
	}
	w := &whereBuilder{uid: u.ID}
	if err := w.build(where, 0); err != nil {
		return 0, err
	}
	q := `SELECT COUNT(*) FROM docs WHERE site_id=? AND coll=?`
	args := []any{siteID, coll}
	if !acc.ReadAll {
		q += ` AND author_id=?`
		args = append(args, u.ID)
	}
	q += " AND " + w.sql.String()
	args = append(args, w.args...)
	var n int64
	err := app.DB.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

func queryFromURL(r *http.Request) (Query, error) {
	v := r.URL.Query()
	q := Query{Order: firstNonEmpty(v.Get("order"), v.Get("orderBy"), v.Get("sort")), After: v.Get("after")}
	if l := v.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 0 {
			return q, core.BadRequest("limit must be a number from 1 to 500")
		}
		q.Limit = n
	}
	if w := v.Get("where"); w != "" {
		m, err := parseWhere([]byte(w))
		if err != nil {
			return q, err
		}
		q.Where = m
	}
	return q, nil
}

func parseWhere(b []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, core.BadRequest(`where must be a JSON object, like {"status":"open"}`)
	}
	return m, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// collCtx resolves site, role, user, collection name and access.
type collCtx struct {
	site *core.Site
	role core.Role
	u    *core.User
	coll string
	rule string
	acc  Access
}

func resolveColl(r *http.Request, min core.Role) (*collCtx, error) {
	s, role, u, err := siteFor(r, min)
	if err != nil {
		return nil, err
	}
	coll := r.PathValue("coll")
	if err := checkColl(coll); err != nil {
		return nil, err
	}
	rule := CollectionRule(r.Context(), s.ID, coll)
	return &collCtx{site: s, role: role, u: u, coll: coll, rule: rule, acc: AccessFor(u, role, rule)}, nil
}

// ---- writes ----

// writeDoc creates or replaces (mode "add", "set") or merges ("update").
func writeDoc(ctx context.Context, c *collCtx, mode, id string, body map[string]any) (*row, string, error) {
	tx, err := app.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	rule, err := ensureColl(ctx, tx, c.site.ID, c.coll)
	if err != nil {
		return nil, "", err
	}
	acc := AccessFor(c.u, c.role, rule)
	var existing *row
	if mode != "add" {
		existing, err = getRow(ctx, tx, c.site.ID, c.coll, id)
		if err != nil && !(mode == "set" && isNotFound(err)) {
			return nil, "", err
		}
	}
	now := nowMS()
	typ := "added"
	var d *row
	if existing == nil {
		if !acc.Create {
			return nil, "", core.Forbidden("you can't add to " + c.coll + ". " + RuleHelp(rule))
		}
		if mode == "add" {
			id = NewID()
		}
		data, err := encodeDoc(body)
		if err != nil {
			return nil, "", err
		}
		if err := checkQuota(siteUsage(ctx, tx, c.site.ID), 1, int64(len(data))); err != nil {
			return nil, "", err
		}
		d = &row{ID: id, Data: data, AuthorID: c.u.ID, AuthorName: c.u.Name, Created: now, Updated: now}
		if _, err := tx.ExecContext(ctx, `INSERT INTO docs(site_id,coll,id,data,size,author_id,author_name,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			c.site.ID, c.coll, d.ID, d.Data, len(d.Data), d.AuthorID, d.AuthorName, d.Created, d.Updated); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return nil, "", core.Conflict("an entry with this id already exists")
			}
			return nil, "", err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE collections SET docs=docs+1, bytes=bytes+?, updated_at=? WHERE site_id=? AND name=?`, len(d.Data), now, c.site.ID, c.coll); err != nil {
			return nil, "", err
		}
	} else {
		if !acc.CanModify(c.u.ID, existing.AuthorID) {
			if !acc.CanRead(c.u.ID, existing.AuthorID) {
				return nil, "", core.NotFound("no entry with id " + id + " in " + c.coll)
			}
			return nil, "", core.Forbidden("you can't change this entry. " + RuleHelp(rule))
		}
		next := body
		if mode == "update" {
			cur, err := parseDoc([]byte(existing.Data))
			if err != nil {
				return nil, "", err
			}
			mergePatch(cur, body)
			next = cur
		}
		data, err := encodeDoc(next)
		if err != nil {
			return nil, "", err
		}
		delta := int64(len(data) - len(existing.Data))
		if err := checkQuota(siteUsage(ctx, tx, c.site.ID), 0, delta); err != nil {
			return nil, "", err
		}
		d = &row{ID: id, Data: data, AuthorID: existing.AuthorID, AuthorName: existing.AuthorName, Created: existing.Created, Updated: now}
		if _, err := tx.ExecContext(ctx, `UPDATE docs SET data=?, size=?, updated_at=? WHERE site_id=? AND coll=? AND id=?`, d.Data, len(d.Data), now, c.site.ID, c.coll, id); err != nil {
			return nil, "", err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE collections SET bytes=bytes+?, updated_at=? WHERE site_id=? AND name=?`, delta, now, c.site.ID, c.coll); err != nil {
			return nil, "", err
		}
		typ = "updated"
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}
	return d, typ, nil
}

func isNotFound(err error) bool {
	var he *core.HTTPError
	return errors.As(err, &he) && he.Status == 404
}

func deleteDoc(ctx context.Context, c *collCtx, id string) error {
	tx, err := app.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d, err := getRow(ctx, tx, c.site.ID, c.coll, id)
	if err != nil {
		return err
	}
	if !c.acc.CanModify(c.u.ID, d.AuthorID) {
		if !c.acc.CanRead(c.u.ID, d.AuthorID) {
			return core.NotFound("no entry with id " + id + " in " + c.coll)
		}
		return core.Forbidden("you can't remove this entry. " + RuleHelp(c.rule))
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM docs WHERE site_id=? AND coll=? AND id=?`, c.site.ID, c.coll, id); err != nil {
		return err
	}
	tx.ExecContext(ctx, `UPDATE collections SET docs=MAX(docs-1,0), bytes=MAX(bytes-?,0), updated_at=? WHERE site_id=? AND name=?`, len(d.Data), nowMS(), c.site.ID, c.coll)
	if err := tx.Commit(); err != nil {
		return err
	}
	publishChange(c.site.ID, c.coll, "deleted", d)
	return nil
}

// ---- ops ----

func registerDB(a *core.App) {
	a.Handle(core.Op{Name: "db.collections", Method: "GET", Path: "/api/v1/sites/{site}/db", Summary: "List a site's data collections with their rules and sizes", PageSafe: true,
		Params: []core.Param{siteParam},
		Fn: func(r *http.Request) (any, error) {
			s, role, _, err := siteFor(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			rows, err := a.DB.QueryContext(r.Context(), `SELECT name, rule, docs, bytes, created_at, updated_at FROM collections WHERE site_id=? ORDER BY name`, s.ID)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			type coll struct {
				Name    string `json:"name"`
				Rule    string `json:"rule"`
				Count   *int64 `json:"count,omitempty"`
				Bytes   *int64 `json:"bytes,omitempty"`
				Created int64  `json:"created"`
				Updated int64  `json:"updated"`
			}
			out := []coll{}
			var total usage
			for rows.Next() {
				var c coll
				var n, b int64
				if err := rows.Scan(&c.Name, &c.Rule, &n, &b, &c.Created, &c.Updated); err != nil {
					return nil, err
				}
				total.docs += n
				total.bytes += b
				if role >= core.RoleEditor { // counts can reveal activity in private collections
					c.Count, c.Bytes = &n, &b
				}
				out = append(out, c)
			}
			res := map[string]any{"collections": out}
			if role >= core.RoleEditor {
				res["usage"] = map[string]any{"docs": total.docs, "bytes": total.bytes, "maxDocs": siteMaxDocs(), "maxBytes": siteMaxBytes()}
			}
			return res, nil
		}})

	listParams := []core.Param{siteParam, collParam,
		{Name: "where", In: "query", Desc: `Filter as JSON, e.g. {"status":"open","total":{"$gt":10},"_author":"me"}`},
		{Name: "order", In: "query", Desc: `Sort field, "-" for descending (default _created)`},
		{Name: "limit", In: "query", Type: "integer", Desc: "Page size (1–500, default 100)"},
		{Name: "after", In: "query", Desc: "Cursor from the previous page's next"},
	}
	a.Handle(core.Op{Name: "db.list", Method: "GET", Path: "/api/v1/sites/{site}/db/{coll}", Summary: "List or filter entries in a collection", PageSafe: true,
		Params: listParams,
		Fn: func(r *http.Request) (any, error) {
			c, err := resolveColl(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			q, err := queryFromURL(r)
			if err != nil {
				return nil, err
			}
			return runQuery(r.Context(), c.site.ID, c.coll, c.u, c.acc, q)
		}})

	a.Handle(core.Op{Name: "db.query", Method: "POST", Path: "/api/v1/sites/{site}/db/{coll}/_query", Summary: "Query entries with a filter in the body", PageSafe: true,
		Params: []core.Param{siteParam, collParam,
			{Name: "where", In: "body", Type: "object", Desc: "Filter"},
			{Name: "order", In: "body", Desc: "Sort field"},
			{Name: "limit", In: "body", Type: "integer"},
			{Name: "after", In: "body", Desc: "Cursor"},
		},
		Fn: func(r *http.Request) (any, error) {
			c, err := resolveColl(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			var raw struct {
				Where   json.RawMessage `json:"where"`
				Order   string          `json:"order"`
				OrderBy string          `json:"orderBy"`
				Limit   int             `json:"limit"`
				After   string          `json:"after"`
			}
			if err := core.Decode(r, &raw); err != nil {
				return nil, err
			}
			q := Query{Order: firstNonEmpty(raw.Order, raw.OrderBy), Limit: raw.Limit, After: raw.After}
			if len(raw.Where) > 0 && string(raw.Where) != "null" {
				if q.Where, err = parseWhere(raw.Where); err != nil {
					return nil, err
				}
			}
			return runQuery(r.Context(), c.site.ID, c.coll, c.u, c.acc, q)
		}})

	a.Handle(core.Op{Name: "db.count", Method: "GET", Path: "/api/v1/sites/{site}/db/{coll}/_count", Summary: "Count entries (optionally matching a filter)", PageSafe: true,
		Params: []core.Param{siteParam, collParam, {Name: "where", In: "query", Desc: "Filter as JSON"}},
		Fn: func(r *http.Request) (any, error) {
			c, err := resolveColl(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			q, err := queryFromURL(r)
			if err != nil {
				return nil, err
			}
			n, err := countDocs(r.Context(), c.site.ID, c.coll, c.u, c.acc, q.Where)
			if err != nil {
				return nil, err
			}
			return map[string]any{"count": n}, nil
		}})

	a.Handle(core.Op{Name: "db.add", Method: "POST", Path: "/api/v1/sites/{site}/db/{coll}", Summary: "Add an entry (the collection is created on first use)", PageSafe: true,
		Params: []core.Param{siteParam, collParam, {Name: "doc", In: "body", Type: "object", Required: true, Desc: "The entry as a JSON object"}},
		Fn: func(r *http.Request) (any, error) {
			c, err := resolveColl(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			body, err := decodeDoc(r.Body)
			if err != nil {
				return nil, err
			}
			d, typ, err := writeDoc(r.Context(), c, "add", "", body)
			if err != nil {
				return nil, err
			}
			publishChange(c.site.ID, c.coll, typ, d)
			return core.Created{V: d.JSON()}, nil
		}})

	a.Handle(core.Op{Name: "db.get", Method: "GET", Path: "/api/v1/sites/{site}/db/{coll}/{id}", Summary: "Get one entry", PageSafe: true,
		Params: []core.Param{siteParam, collParam, idParam},
		Fn: func(r *http.Request) (any, error) {
			c, err := resolveColl(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			id := r.PathValue("id")
			if err := checkID(id); err != nil {
				return nil, err
			}
			d, err := getRow(r.Context(), a.DB, c.site.ID, c.coll, id)
			if err != nil {
				return nil, err
			}
			if !c.acc.CanRead(c.u.ID, d.AuthorID) {
				return nil, core.NotFound("no entry with id " + id + " in " + c.coll)
			}
			return d.JSON(), nil
		}})

	writeOp := func(mode string) func(r *http.Request) (any, error) {
		return func(r *http.Request) (any, error) {
			c, err := resolveColl(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			id := r.PathValue("id")
			if err := checkID(id); err != nil {
				return nil, err
			}
			body, err := decodeDoc(r.Body)
			if err != nil {
				return nil, err
			}
			d, typ, err := writeDoc(r.Context(), c, mode, id, body)
			if err != nil {
				return nil, err
			}
			publishChange(c.site.ID, c.coll, typ, d)
			return d.JSON(), nil
		}
	}
	a.Handle(core.Op{Name: "db.set", Method: "PUT", Path: "/api/v1/sites/{site}/db/{coll}/{id}", Summary: "Create or replace an entry with a chosen id", PageSafe: true,
		Params: []core.Param{siteParam, collParam, idParam, {Name: "doc", In: "body", Type: "object", Required: true}},
		Fn:     writeOp("set")})
	a.Handle(core.Op{Name: "db.update", Method: "PATCH", Path: "/api/v1/sites/{site}/db/{coll}/{id}", Summary: "Change some fields of an entry (null removes a field)", PageSafe: true,
		Params: []core.Param{siteParam, collParam, idParam, {Name: "doc", In: "body", Type: "object", Required: true}},
		Fn:     writeOp("update")})

	a.Handle(core.Op{Name: "db.delete", Method: "DELETE", Path: "/api/v1/sites/{site}/db/{coll}/{id}", Summary: "Remove an entry", PageSafe: true, Destructive: true,
		Params: []core.Param{siteParam, collParam, idParam},
		Fn: func(r *http.Request) (any, error) {
			c, err := resolveColl(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			id := r.PathValue("id")
			if err := checkID(id); err != nil {
				return nil, err
			}
			if err := deleteDoc(r.Context(), c, id); err != nil {
				return nil, err
			}
			return nil, nil
		}})

	a.Handle(core.Op{Name: "db.rule", Method: "PUT", Path: "/api/v1/sites/{site}/db/{coll}/_rule", Summary: "Set who can read and change a collection (editors only)",
		Params: []core.Param{siteParam, collParam, {Name: "rule", In: "body", Required: true, Desc: "own | private | read | append | open | group:<name>"}},
		Fn: func(r *http.Request) (any, error) {
			c, err := resolveColl(r, core.RoleEditor)
			if err != nil {
				return nil, err
			}
			var in struct{ Rule string }
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			rule, err := ValidRule(in.Rule)
			if err != nil {
				return nil, err
			}
			now := nowMS()
			var cnt int
			a.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM collections WHERE site_id=?`, c.site.ID).Scan(&cnt)
			res, err := a.DB.ExecContext(r.Context(), `UPDATE collections SET rule=?, updated_at=? WHERE site_id=? AND name=?`, rule, now, c.site.ID, c.coll)
			if err != nil {
				return nil, err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				if cnt >= maxCollections() {
					return nil, core.Quota("this site already has the most collections allowed")
				}
				if _, err := a.DB.ExecContext(r.Context(), `INSERT INTO collections(site_id,name,rule,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(site_id,name) DO UPDATE SET rule=excluded.rule`, c.site.ID, c.coll, rule, now, now); err != nil {
					return nil, err
				}
			}
			ruleCacheSet(c.site.ID, c.coll, rule)
			a.Audit(r.Context(), "db.rule", c.site.ID, c.coll, map[string]any{"rule": rule, "was": c.rule})
			return map[string]any{"name": c.coll, "rule": rule}, nil
		}})

	a.Handle(core.Op{Name: "db.drop", Method: "DELETE", Path: "/api/v1/sites/{site}/db/{coll}", Summary: "Delete a collection and all its entries (editors only)", Destructive: true,
		Params: []core.Param{siteParam, collParam},
		Fn: func(r *http.Request) (any, error) {
			c, err := resolveColl(r, core.RoleEditor)
			if err != nil {
				return nil, err
			}
			tx, err := a.DB.BeginTx(r.Context(), nil)
			if err != nil {
				return nil, err
			}
			defer tx.Rollback()
			res, err := tx.ExecContext(r.Context(), `DELETE FROM docs WHERE site_id=? AND coll=?`, c.site.ID, c.coll)
			if err != nil {
				return nil, err
			}
			n, _ := res.RowsAffected()
			tx.ExecContext(r.Context(), `DELETE FROM collections WHERE site_id=? AND name=?`, c.site.ID, c.coll)
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			ruleCacheSet(c.site.ID, c.coll, RuleOwn)
			a.Audit(r.Context(), "db.drop", c.site.ID, c.coll, map[string]any{"docs": n})
			a.Bus.Publish("site:"+i64(c.site.ID)+":db:"+c.coll, []byte(`{"author":0,"data":{"type":"dropped"}}`))
			return map[string]any{"deleted": n}, nil
		}})

	a.Handle(core.Op{Name: "db.export", Method: "GET", Path: "/api/v1/sites/{site}/db/{coll}/_export", Summary: "Download a collection as CSV or JSON", PageSafe: true,
		Params: []core.Param{siteParam, collParam, {Name: "format", In: "query", Enum: []string{"json", "csv"}, Desc: "Default json"}},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			c, err := resolveColl(r, core.RoleViewer)
			if err != nil {
				core.Err(w, err)
				return
			}
			if err := exportColl(w, r, c); err != nil {
				core.Err(w, err)
			}
		}})

	a.Handle(core.Op{Name: "db.import", Method: "POST", Path: "/api/v1/sites/{site}/db/{coll}/_import", Summary: "Load entries from CSV or a JSON array (editors only)", Body: "raw",
		Params: []core.Param{siteParam, collParam,
			{Name: "file", In: "body", Type: "file", Required: true, Desc: "CSV (first row is the header) or a JSON array of objects"},
			{Name: "mode", In: "query", Enum: []string{"append", "replace"}, Desc: "replace removes existing entries first (default append)"},
		},
		Fn: func(r *http.Request) (any, error) {
			c, err := resolveColl(r, core.RoleEditor)
			if err != nil {
				return nil, err
			}
			return importColl(r, c)
		}})
}

// ---- export / import ----

const exportMaxRows = 200000

func exportColl(w http.ResponseWriter, r *http.Request, c *collCtx) error {
	if !c.acc.ReadAll && !c.acc.ReadOwn {
		return core.Forbidden("you can't read this collection")
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" {
		return core.BadRequest("format must be csv or json")
	}
	q := `SELECT ` + rowCols + ` FROM docs WHERE site_id=? AND coll=?`
	args := []any{c.site.ID, c.coll}
	if !c.acc.ReadAll {
		q += ` AND author_id=?`
		args = append(args, c.u.ID)
	}
	q += ` ORDER BY created_at, id LIMIT ?`
	args = append(args, exportMaxRows)
	ctx := r.Context()
	rows, err := app.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": c.coll + "." + format}))
	if format == "json" {
		h.Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(200)
		w.Write([]byte("["))
		first := true
		for rows.Next() {
			d, err := scanRow(rows)
			if err != nil {
				break
			}
			if !first {
				w.Write([]byte(",\n"))
			}
			first = false
			w.Write(d.JSON())
		}
		w.Write([]byte("]\n"))
		app.Audit(ctx, "db.export", c.site.ID, c.coll, map[string]any{"format": format})
		return nil
	}
	// CSV needs the column set up front: collect rows (bounded by the site quota).
	var docs []*row
	var maps []map[string]any
	cols := []string{}
	seen := map[string]bool{}
	for rows.Next() {
		d, err := scanRow(rows)
		if err != nil {
			return err
		}
		m, _ := parseDoc([]byte(d.Data))
		for _, k := range sortedKeys(m) {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
		docs = append(docs, d)
		maps = append(maps, m)
	}
	h.Set("Content-Type", "text/csv; charset=utf-8")
	w.WriteHeader(200)
	cw := csv.NewWriter(w)
	header := []string{"_id"}
	for _, c := range cols {
		header = append(header, csvText(c))
	}
	header = append(header, "_author", "_authorName", "_created", "_updated")
	cw.Write(header)
	rec := make([]string, len(header))
	for i, d := range docs {
		rec[0] = d.ID
		for j, k := range cols {
			rec[j+1] = csvCell(maps[i][k])
		}
		n := len(cols) + 1
		rec[n], rec[n+1], rec[n+2], rec[n+3] = i64(d.AuthorID), csvText(d.AuthorName), ISOTime(d.Created), ISOTime(d.Updated)
		cw.Write(rec)
	}
	cw.Flush()
	app.Audit(ctx, "db.export", c.site.ID, c.coll, map[string]any{"format": format, "rows": len(docs)})
	return nil
}

func csvCell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return csvText(x)
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// csvText defuses spreadsheet formula injection: text written by viewers
// that starts with = + - @ (or a tab/CR) gets a leading apostrophe.
func csvText(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// csvValue turns a CSV cell into a JSON value: numbers and true/false
// become typed, everything else stays text.
func csvValue(s string) any {
	t := strings.TrimSpace(s)
	switch t {
	case "":
		return nil
	case "true", "TRUE", "True":
		return true
	case "false", "FALSE", "False":
		return false
	}
	if len(t) < 20 && (t == "0" || !strings.HasPrefix(strings.TrimPrefix(t, "-"), "0") || strings.HasPrefix(strings.TrimPrefix(t, "-"), "0.")) {
		if _, err := strconv.ParseFloat(t, 64); err == nil && !strings.ContainsAny(t, "eEInfinityNaN+") {
			return json.Number(t)
		}
	}
	return s
}

const importMaxRows = 50000

func importColl(r *http.Request, c *collCtx) (any, error) {
	ctx := r.Context()
	max := int64(core.EnvInt("UPLOAD_MAX_MB", 100)) << 20
	body := http.MaxBytesReader(nil, r.Body, max)
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	var src io.Reader = body
	name := ""
	if ct == "multipart/form-data" {
		mr, err := r.MultipartReader()
		if err != nil {
			return nil, core.BadRequest("bad upload")
		}
		for {
			p, err := mr.NextPart()
			if err != nil {
				return nil, core.BadRequest("attach a CSV or JSON file")
			}
			if p.FileName() != "" || p.FormName() == "file" {
				src, name = p, p.FileName()
				break
			}
		}
	}
	raw, err := io.ReadAll(src)
	if err != nil {
		return nil, core.TooLarge("that file is too large to import")
	}
	trim := bytes.TrimLeft(raw, " \t\r\n\xef\xbb\xbf")
	var items []map[string]any
	if len(trim) > 0 && (trim[0] == '[' || trim[0] == '{') && !strings.HasSuffix(strings.ToLower(name), ".csv") {
		dec := json.NewDecoder(bytes.NewReader(trim))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, core.BadRequest("that JSON couldn't be read: " + err.Error())
		}
		switch x := v.(type) {
		case []any:
			for _, it := range x {
				m, ok := it.(map[string]any)
				if !ok {
					return nil, core.BadRequest("a JSON import must be a list of objects")
				}
				items = append(items, m)
			}
		case map[string]any:
			if list, ok := x["items"].([]any); ok { // our own list format
				for _, it := range list {
					if m, ok := it.(map[string]any); ok {
						items = append(items, m)
					}
				}
			} else {
				items = append(items, x)
			}
		}
	} else {
		cr := csv.NewReader(bytes.NewReader(trim))
		cr.FieldsPerRecord = -1
		cr.LazyQuotes = true
		recs, err := cr.ReadAll()
		if err != nil {
			return nil, core.BadRequest("that CSV couldn't be read: " + err.Error())
		}
		if len(recs) < 1 {
			return nil, core.BadRequest("the CSV is empty")
		}
		header := recs[0]
		for _, rec := range recs[1:] {
			m := map[string]any{}
			for i, h := range header {
				h = strings.TrimSpace(h)
				if h == "" || i >= len(rec) {
					continue
				}
				if v := csvValue(rec[i]); v != nil {
					m[h] = v
				}
			}
			if len(m) > 0 {
				items = append(items, m)
			}
		}
	}
	if len(items) > importMaxRows {
		return nil, core.TooLarge("imports are limited to " + strconv.Itoa(importMaxRows) + " rows at a time")
	}
	replace := r.URL.Query().Get("mode") == "replace"
	tx, err := app.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := ensureColl(ctx, tx, c.site.ID, c.coll); err != nil {
		return nil, err
	}
	if replace {
		tx.ExecContext(ctx, `DELETE FROM docs WHERE site_id=? AND coll=?`, c.site.ID, c.coll)
		tx.ExecContext(ctx, `UPDATE collections SET docs=0, bytes=0 WHERE site_id=? AND name=?`, c.site.ID, c.coll)
	}
	use := siteUsage(ctx, tx, c.site.ID)
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO docs(site_id,coll,id,data,size,author_id,author_name,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(site_id,coll,id) DO UPDATE SET data=excluded.data, size=excluded.size, updated_at=excluded.updated_at`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	now := nowMS()
	var added, bytesAdded int64
	for i, m := range items {
		id, _ := m["_id"].(string)
		if id == "" || checkID(id) != nil {
			id = NewID()
		}
		created := now + int64(i) // keep file order
		stripMeta(m)
		data, err := encodeDoc(m)
		if err != nil {
			return nil, core.BadRequest("row " + strconv.Itoa(i+1) + ": " + err.Error())
		}
		if err := checkQuota(use, added+1, bytesAdded+int64(len(data))); err != nil {
			return nil, err
		}
		if _, err := stmt.ExecContext(ctx, c.site.ID, c.coll, id, data, len(data), c.u.ID, c.u.Name, created, created); err != nil {
			return nil, err
		}
		added++
		bytesAdded += int64(len(data))
	}
	// Recount (upserts may have replaced rows).
	tx.ExecContext(ctx, `UPDATE collections SET docs=(SELECT COUNT(*) FROM docs WHERE site_id=? AND coll=?), bytes=(SELECT COALESCE(SUM(size),0) FROM docs WHERE site_id=? AND coll=?), updated_at=? WHERE site_id=? AND name=?`,
		c.site.ID, c.coll, c.site.ID, c.coll, now, c.site.ID, c.coll)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	app.Audit(ctx, "db.import", c.site.ID, c.coll, map[string]any{"rows": added, "replace": replace})
	app.Bus.Publish("site:"+i64(c.site.ID)+":db:"+c.coll, []byte(`{"author":0,"data":{"type":"reset"}}`))
	return map[string]any{"imported": added, "collection": c.coll}, nil
}
