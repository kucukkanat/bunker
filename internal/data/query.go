package data

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kucukkanat/bunker/internal/core"
)

// Query is a list/query request.
type Query struct {
	Where map[string]any `json:"where"`
	Order string         `json:"order"`
	Limit int            `json:"limit"`
	After string         `json:"after"`
}

const maxLimit = 500

var fieldRe = regexp.MustCompile(`^[A-Za-z0-9_$@-]{1,64}(\.[A-Za-z0-9_$@-]{1,64}){0,7}$`)

// fieldExpr maps a field name to an SQL expression. Meta fields map to
// columns; everything else to json_extract with the path inlined as a
// literal (fieldRe allows no quotes), so the expression is the same text
// as an adaptive index's (see autoindex.go) and the planner can use it.
// path is the quoted JSON path literal ("" for meta columns).
func fieldExpr(f string) (expr, path string, err error) {
	switch f {
	case "_id":
		return "id", "", nil
	case "_author":
		return "author_id", "", nil
	case "_authorName":
		return "author_name", "", nil
	case "_created":
		return "created_at", "", nil
	case "_updated":
		return "updated_at", "", nil
	}
	if strings.HasPrefix(f, "_") || !fieldRe.MatchString(f) {
		return "", "", core.BadRequest("can't filter or sort by \"" + f + "\". Use field names made of letters, numbers and _ (dots for nested fields).")
	}
	path = "'" + jsonPath(f) + "'"
	return "json_extract(data, " + path + ")", path, nil
}

func jsonPath(f string) string {
	var b strings.Builder
	b.WriteString("$")
	for _, seg := range strings.Split(f, ".") {
		b.WriteString(`."`)
		b.WriteString(seg) // validated: no quotes or backslashes
		b.WriteString(`"`)
	}
	return b.String()
}

// sqlValue converts a decoded JSON scalar into a bind value.
func sqlValue(v any) (any, bool) {
	switch x := v.(type) {
	case nil:
		return nil, true
	case bool:
		if x {
			return int64(1), true
		}
		return int64(0), true
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n, true
		}
		f, err := x.Float64()
		return f, err == nil
	case float64:
		return x, true
	case int:
		return int64(x), true
	case int64:
		return x, true
	case string:
		return x, true
	}
	return nil, false
}

type whereBuilder struct {
	uid   int64
	sql   strings.Builder
	args  []any
	conds int
	// fields used in index-friendly conditions (=, IS NULL, ranges, IN)
	// that every match must satisfy (not under $or): adaptive index candidates.
	fields []string
	inOr   int
}

func (w *whereBuilder) add(s string, args ...any) {
	w.sql.WriteString(s)
	w.args = append(w.args, args...)
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// build writes "(c1 AND c2 ...)" for a where object.
func (w *whereBuilder) build(m map[string]any, depth int) error {
	if depth > 3 {
		return core.BadRequest("filters can be nested at most 3 levels deep")
	}
	if len(m) == 0 {
		w.add("1")
		return nil
	}
	w.add("(")
	for i, k := range sortedKeys(m) {
		if i > 0 {
			w.add(" AND ")
		}
		if w.conds++; w.conds > 50 {
			return core.BadRequest("too many filter conditions (50 at most)")
		}
		v := m[k]
		if k == "$or" || k == "$and" {
			list, ok := v.([]any)
			if !ok || len(list) == 0 {
				return core.BadRequest(k + " needs a list of filters")
			}
			if k == "$or" {
				w.inOr++
			}
			w.add("(")
			for j, item := range list {
				sub, ok := item.(map[string]any)
				if !ok {
					return core.BadRequest(k + " needs a list of filters")
				}
				if j > 0 {
					if k == "$or" {
						w.add(" OR ")
					} else {
						w.add(" AND ")
					}
				}
				if err := w.build(sub, depth+1); err != nil {
					return err
				}
			}
			w.add(")")
			if k == "$or" {
				w.inOr--
			}
			continue
		}
		expr, path, err := fieldExpr(k)
		if err != nil {
			return err
		}
		if ops, ok := v.(map[string]any); ok && hasOps(ops) {
			w.add("(")
			for j, op := range sortedKeys(ops) {
				if j > 0 {
					w.add(" AND ")
				}
				if err := w.cond(k, expr, path, op, ops[op]); err != nil {
					return err
				}
			}
			w.add(")")
			continue
		}
		if err := w.cond(k, expr, path, "$eq", v); err != nil {
			return err
		}
	}
	w.add(")")
	return nil
}

func hasOps(m map[string]any) bool {
	for k := range m {
		if strings.HasPrefix(k, "$") {
			return true
		}
	}
	return false
}

func (w *whereBuilder) indexable(field string) {
	if w.inOr == 0 {
		w.fields = append(w.fields, field)
	}
}

func (w *whereBuilder) cond(field, expr, path string, op string, v any) error {
	if field == "_author" {
		if s, ok := v.(string); ok && s == "me" {
			v = json.Number(strconv.FormatInt(w.uid, 10))
		}
		if list, ok := v.([]any); ok {
			for i, x := range list {
				if s, ok := x.(string); ok && s == "me" {
					list[i] = json.Number(strconv.FormatInt(w.uid, 10))
				}
			}
		}
	}
	if field == "_created" || field == "_updated" {
		conv := func(x any) any {
			if s, ok := x.(string); ok {
				if ms, ok := parseTimeMS(s); ok {
					return json.Number(strconv.FormatInt(ms, 10))
				}
			}
			return x
		}
		if list, ok := v.([]any); ok {
			for i := range list {
				list[i] = conv(list[i])
			}
		} else {
			v = conv(v)
		}
	}
	switch op {
	case "$eq", "$ne":
		if v == nil {
			if op == "$eq" {
				w.indexable(field)
				w.add(expr + " IS NULL")
			} else {
				w.add(expr + " IS NOT NULL")
			}
			return nil
		}
		val, ok := sqlValue(v)
		if !ok { // object or array: compare canonical JSON text
			b, _ := json.Marshal(v)
			if op == "$eq" {
				w.add(expr+" = json(?)", string(b))
			} else {
				w.add("("+expr+" IS NULL OR "+expr+" != json(?))", string(b))
			}
			return nil
		}
		if op == "$eq" {
			w.indexable(field)
			w.add(expr+" = ?", val)
		} else {
			w.add("("+expr+" IS NULL OR "+expr+" != ?)", val)
		}
	case "$gt", "$gte", "$lt", "$lte":
		val, ok := sqlValue(v)
		if !ok || val == nil {
			return core.BadRequest(op + " needs a number, text or true/false")
		}
		sym := map[string]string{"$gt": ">", "$gte": ">=", "$lt": "<", "$lte": "<="}[op]
		w.indexable(field)
		w.add(expr+" "+sym+" ?", val)
	case "$in", "$nin":
		list, ok := v.([]any)
		if !ok {
			return core.BadRequest(op + " needs a list of values")
		}
		if len(list) > 100 {
			return core.BadRequest(op + " accepts at most 100 values")
		}
		if len(list) == 0 {
			if op == "$in" {
				w.add("0")
			} else {
				w.add("1")
			}
			return nil
		}
		args := make([]any, 0, len(list))
		ph := make([]string, len(list))
		for i, x := range list {
			val, ok := sqlValue(x)
			if !ok || val == nil {
				return core.BadRequest(op + " values must be numbers, text or true/false")
			}
			ph[i] = "?"
			args = append(args, val)
		}
		if op == "$in" {
			w.indexable(field)
			w.add(expr+" IN ("+strings.Join(ph, ",")+")", args...)
		} else {
			w.add("("+expr+" IS NULL OR "+expr+" NOT IN ("+strings.Join(ph, ",")+"))", args...)
		}
	case "$contains":
		val, ok := sqlValue(v)
		if !ok || val == nil {
			return core.BadRequest("$contains needs a number, text or true/false")
		}
		if path == "" { // meta column: substring match on text columns
			s, _ := val.(string)
			w.add("instr(lower("+expr+"), lower(?)) > 0", s)
			return nil
		}
		// Arrays: element match. Text: case-insensitive substring.
		w.add(`(CASE json_type(data, `+path+`) WHEN 'array' THEN EXISTS(SELECT 1 FROM json_each(docs.data, `+path+`) WHERE value = ?) WHEN 'text' THEN instr(lower(`+expr+`), lower(?)) > 0 ELSE 0 END)`,
			val, toString(val))
	case "$exists":
		b, _ := v.(bool)
		if path == "" {
			if b {
				w.add("1")
			} else {
				w.add("0")
			}
			return nil
		}
		if b {
			w.add("json_type(data, " + path + ") IS NOT NULL")
		} else {
			w.add("json_type(data, " + path + ") IS NULL")
		}
	default:
		return core.BadRequest("unknown filter " + op + ". Use $eq, $ne, $gt, $gte, $lt, $lte, $in, $nin, $contains or $exists.")
	}
	return nil
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	}
	return ""
}

// order describes a sort.
type order struct {
	field string
	expr  string
	desc  bool
}

func parseOrder(s string) (order, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "_created"
	}
	o := order{}
	if strings.HasPrefix(s, "-") {
		o.desc = true
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	expr, _, err := fieldExpr(s)
	if err != nil {
		return o, err
	}
	o.field, o.expr = s, expr
	return o, nil
}

// cursor is [lastSortValue, lastID].
func encodeCursor(v any, id string) string {
	if b, ok := v.([]byte); ok {
		v = string(b)
	}
	b, _ := json.Marshal([]any{v, id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (any, string, error) {
	bad := core.BadRequest("that page cursor isn't valid; start again from the first page")
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) > 4096 {
		return nil, "", bad
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	var arr []any
	if dec.Decode(&arr) != nil || len(arr) != 2 {
		return nil, "", bad
	}
	id, ok := arr[1].(string)
	if !ok {
		return nil, "", bad
	}
	v, ok := sqlValue(arr[0])
	if !ok {
		return nil, "", bad
	}
	return v, id, nil
}

// cursorCond returns the keyset condition that continues after (v, id).
// SQLite sorts NULLs first ascending and last descending.
func (o order) cursorCond(v any, id string) (string, []any) {
	e := o.expr
	if !o.desc {
		if v == nil {
			return "(" + e + " IS NOT NULL OR (" + e + " IS NULL AND id > ?))", []any{id}
		}
		return "(" + e + " > ? OR (" + e + " = ? AND id > ?))", []any{v, v, id}
	}
	if v == nil {
		return "(" + e + " IS NULL AND id < ?)", []any{id}
	}
	return "(" + e + " < ? OR " + e + " IS NULL OR (" + e + " = ? AND id < ?))", []any{v, v, id}
}
