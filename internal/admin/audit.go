package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

// ---- query ----

type auditRow struct {
	ID      int64          `json:"id"`
	TS      int64          `json:"ts"`
	ActorID *int64         `json:"actorId,omitempty"`
	Actor   string         `json:"actor"`
	Event   string         `json:"event"`
	SiteID  *int64         `json:"siteId,omitempty"`
	Site    string         `json:"site,omitempty"`
	Target  string         `json:"target,omitempty"`
	IP      string         `json:"ip,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// parseTime accepts unix seconds, YYYY-MM-DD or RFC 3339.
func parseTime(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n > 1e12 { // milliseconds
			n /= 1000
		}
		return n, nil
	}
	for _, l := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(l, s); err == nil {
			return t.Unix(), nil
		}
	}
	return 0, core.BadRequest("dates look like 2026-05-01 or 2026-05-01T09:00:00Z")
}

// auditQuery builds the WHERE clause shared by list and export.
func auditQuery(ctx context.Context, q url.Values) (string, []any, error) {
	var where []string
	var args []any
	if ev := strings.TrimSpace(q.Get("event")); ev != "" {
		var ors []string
		for _, e := range strings.Split(ev, ",") {
			e = strings.TrimSpace(e)
			if strings.HasSuffix(e, "*") || strings.HasSuffix(e, ".") {
				ors = append(ors, "a.event LIKE ? ESCAPE '\\'")
				args = append(args, likeEscape(strings.TrimSuffix(e, "*"))+"%")
			} else if e != "" {
				ors = append(ors, "a.event=?")
				args = append(args, e)
			}
		}
		if len(ors) > 0 {
			where = append(where, "("+strings.Join(ors, " OR ")+")")
		}
	}
	if u := strings.TrimSpace(q.Get("user")); u != "" {
		if id, err := strconv.ParseInt(u, 10, 64); err == nil {
			where = append(where, "a.actor_id=?")
			args = append(args, id)
		} else {
			where = append(where, "lower(a.actor_email)=?")
			args = append(args, strings.ToLower(u))
		}
	}
	if s := strings.TrimSpace(q.Get("site")); s != "" {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			where = append(where, "a.site_id=?")
			args = append(args, id)
		} else {
			where = append(where, "a.site_id=(SELECT id FROM sites WHERE slug=?)")
			args = append(args, strings.ToLower(s))
		}
	}
	if t := strings.TrimSpace(q.Get("q")); t != "" {
		where = append(where, "(a.target LIKE ? ESCAPE '\\' OR a.meta LIKE ? ESCAPE '\\')")
		like := "%" + likeEscape(t) + "%"
		args = append(args, like, like)
	}
	since, err := parseTime(q.Get("since"))
	if err != nil {
		return "", nil, err
	}
	if since > 0 {
		where = append(where, "a.ts>=?")
		args = append(args, since)
	}
	before, err := parseTime(q.Get("before"))
	if err != nil {
		return "", nil, err
	}
	if before > 0 {
		where = append(where, "a.ts<?")
		args = append(args, before)
	}
	if id, err := strconv.ParseInt(q.Get("beforeId"), 10, 64); err == nil && id > 0 {
		where = append(where, "a.id<?")
		args = append(args, id)
	}
	w := ""
	if len(where) > 0 {
		w = " WHERE " + strings.Join(where, " AND ")
	}
	return w, args, nil
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

const auditSelect = `SELECT a.id,a.ts,a.actor_id,COALESCE(a.actor_email,''),a.event,a.site_id,COALESCE(s.slug,''),COALESCE(a.target,''),COALESCE(a.ip,''),COALESCE(a.meta,'') FROM audit a LEFT JOIN sites s ON s.id=a.site_id`

func scanAudit(rows *sql.Rows) (auditRow, error) {
	var r auditRow
	var actor, site sql.NullInt64
	var meta string
	if err := rows.Scan(&r.ID, &r.TS, &actor, &r.Actor, &r.Event, &site, &r.Site, &r.Target, &r.IP, &meta); err != nil {
		return r, err
	}
	if actor.Valid {
		r.ActorID = &actor.Int64
	}
	if site.Valid {
		r.SiteID = &site.Int64
	}
	if meta != "" {
		json.Unmarshal([]byte(meta), &r.Meta)
	}
	return r, nil
}

func registerAudit(a *core.App) {
	filters := []core.Param{
		{Name: "event", In: "query", Desc: "Event name(s), comma-separated; end with * for a prefix (site.*)"},
		{Name: "user", In: "query", Desc: "Actor email or user ID"},
		{Name: "site", In: "query", Desc: "Site address or ID"},
		{Name: "q", In: "query", Desc: "Text in target or details"},
		{Name: "since", In: "query", Desc: "From (date, RFC 3339 or unix seconds)"},
		{Name: "before", In: "query", Desc: "Until (exclusive)"},
	}
	a.Handle(core.Op{Name: "admin.audit", Method: "GET", Path: "/api/v1/admin/audit", Access: core.AccessAdmin, Summary: "Search the audit log",
		Params: append(append([]core.Param{}, filters...), core.Param{Name: "limit", In: "query", Type: "integer", Desc: "Default 100, max 1000"}, core.Param{Name: "beforeId", In: "query", Type: "integer", Desc: "Paging: the `next` value from the previous page"}),
		Fn: func(r *http.Request) (any, error) {
			w, args, err := auditQuery(r.Context(), r.URL.Query())
			if err != nil {
				return nil, err
			}
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			if limit <= 0 {
				limit = 100
			}
			limit = min(limit, 1000)
			rows, err := a.DB.QueryContext(r.Context(), auditSelect+w+` ORDER BY a.id DESC LIMIT ?`, append(args, limit+1)...)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			out := []auditRow{}
			for rows.Next() {
				row, err := scanAudit(rows)
				if err != nil {
					return nil, err
				}
				out = append(out, row)
			}
			res := map[string]any{"events": out}
			if len(out) > limit {
				out = out[:limit]
				res["events"] = out
				res["next"] = out[len(out)-1].ID
			}
			return res, nil
		}})
	a.Handle(core.Op{Name: "admin.audit.export", Method: "GET", Path: "/api/v1/admin/audit/export", Access: core.AccessAdmin, Summary: "Download the audit log as CSV or JSON",
		Params: append(append([]core.Param{}, filters...), core.Param{Name: "format", In: "query", Enum: []string{"csv", "json", "ndjson"}}),
		Handler: func(w http.ResponseWriter, r *http.Request) {
			where, args, err := auditQuery(r.Context(), r.URL.Query())
			if err != nil {
				core.Err(w, err)
				return
			}
			format := r.URL.Query().Get("format")
			if format == "" {
				format = "csv"
			}
			if format != "csv" && format != "json" && format != "ndjson" {
				core.Err(w, core.BadRequest("format is csv, json or ndjson"))
				return
			}
			rows, err := a.DB.QueryContext(r.Context(), auditSelect+where+` ORDER BY a.id`, args...)
			if err != nil {
				core.Err(w, core.Internal("couldn't read the audit log"))
				return
			}
			defer rows.Close()
			a.Audit(r.Context(), "admin.audit.export", 0, format, map[string]any{"filters": r.URL.Query()})
			name := "bunker-audit-" + time.Now().UTC().Format("2006-01-02") + "." + map[string]string{"csv": "csv", "json": "json", "ndjson": "ndjson"}[format]
			ct := map[string]string{"csv": "text/csv; charset=utf-8", "json": "application/json", "ndjson": "application/x-ndjson"}[format]
			w.Header().Set("Content-Type", ct)
			w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
			w.Header().Set("Cache-Control", "no-store")
			fl, _ := w.(http.Flusher)
			var cw *csv.Writer
			enc := json.NewEncoder(w)
			enc.SetEscapeHTML(false)
			switch format {
			case "csv":
				w.Write([]byte("\xef\xbb\xbf"))
				cw = csv.NewWriter(w)
				cw.Write([]string{"id", "time", "actor", "actor_id", "event", "site", "site_id", "target", "ip", "details"})
			case "json":
				w.Write([]byte("[\n"))
			}
			n := 0
			for rows.Next() {
				row, err := scanAudit(rows)
				if err != nil {
					break
				}
				switch format {
				case "csv":
					meta := ""
					if row.Meta != nil {
						b, _ := json.Marshal(row.Meta)
						meta = string(b)
					}
					cw.Write([]string{strconv.FormatInt(row.ID, 10), time.Unix(row.TS, 0).UTC().Format(time.RFC3339), row.Actor, ptr(row.ActorID), row.Event, row.Site, ptr(row.SiteID), csvSafe(row.Target), row.IP, csvSafe(meta)})
				case "json":
					if n > 0 {
						w.Write([]byte(",\n"))
					}
					b, _ := json.Marshal(row)
					w.Write(b)
				default:
					enc.Encode(row)
				}
				n++
				if n%2000 == 0 {
					if cw != nil {
						cw.Flush()
					}
					if fl != nil {
						fl.Flush()
					}
				}
			}
			if cw != nil {
				cw.Flush()
			}
			if format == "json" {
				w.Write([]byte("\n]\n"))
			}
		}})
	a.Handle(core.Op{Name: "admin.audit.streaming.get", Method: "GET", Path: "/api/v1/admin/audit/streaming", Access: core.AccessAdmin, Summary: "Where audit events are streamed (SIEM)",
		Fn: func(r *http.Request) (any, error) {
			wh := a.SecretSetting("audit.webhook")
			if u, err := url.Parse(wh); err == nil && wh != "" {
				u.RawQuery, u.User = "", nil
				wh = u.String() + " (credentials hidden)"
			}
			return map[string]any{"webhook": wh, "syslog": a.Setting("audit.syslog"), "retentionDays": retentionDays(), "dropped": sinkDropped.Load(), "sent": sinkSent.Load(), "lastError": lastSinkErr()}, nil
		}})
	a.Handle(core.Op{Name: "admin.audit.streaming.update", Method: "PUT", Path: "/api/v1/admin/audit/streaming", Access: core.AccessAdmin, Summary: "Stream audit events to a webhook or syslog",
		Params: []core.Param{{Name: "webhook", In: "body", Desc: "https:// URL receiving JSON batches (empty = off)"}, {Name: "syslog", In: "body", Desc: "udp://host:514 or tcp://host:6514 (empty = off)"}, {Name: "retentionDays", In: "body", Type: "integer", Desc: "Delete events older than this (0 = keep forever)"}},
		Fn: func(r *http.Request) (any, error) {
			var in struct {
				Webhook       *string `json:"webhook"`
				Syslog        *string `json:"syslog"`
				RetentionDays *int    `json:"retentionDays"`
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			ctx := r.Context()
			if in.Webhook != nil {
				if err := validWebhook(*in.Webhook); err != nil {
					return nil, err
				}
				a.SetSecretSetting("audit.webhook", strings.TrimSpace(*in.Webhook))
			}
			if in.Syslog != nil {
				if _, _, err := parseSyslog(*in.Syslog); err != nil && *in.Syslog != "" {
					return nil, err
				}
				a.SetSetting(ctx, "audit.syslog", strings.TrimSpace(*in.Syslog))
			}
			if in.RetentionDays != nil {
				if *in.RetentionDays < 0 {
					return nil, core.BadRequest("retentionDays can't be negative")
				}
				a.SetSetting(ctx, "audit.retention_days", strconv.Itoa(*in.RetentionDays))
			}
			refreshSinks()
			a.Audit(ctx, "admin.config", 0, "audit.streaming", map[string]any{"webhook": in.Webhook != nil && *in.Webhook != "", "syslog": a.Setting("audit.syslog"), "retentionDays": retentionDays()})
			return map[string]any{"ok": true}, nil
		}})
}

func ptr(p *int64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatInt(*p, 10)
}

// csvSafe defuses spreadsheet formula injection in exported text.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// ---- streaming sinks ----

var (
	sinkOnce    sync.Once
	sinkQ       = make(chan map[string]any, 10000)
	sinkDropped atomic.Int64
	sinkSent    atomic.Int64
	sinkErrMu   sync.Mutex
	sinkErr     string
	flushReq    = make(chan chan struct{})
	sinkOn      atomic.Bool // any sink configured (refreshed every few seconds)
)

func refreshSinks() {
	sinkOn.Store(app != nil && (app.SecretSetting("audit.webhook") != "" || app.Setting("audit.syslog") != ""))
}

func lastSinkErr() string {
	sinkErrMu.Lock()
	defer sinkErrMu.Unlock()
	return sinkErr
}

func setSinkErr(s string) {
	sinkErrMu.Lock()
	sinkErr = s
	sinkErrMu.Unlock()
}

// startSinks installs one non-blocking audit sink for the process. Audit
// writers never wait on the network: when the queue is full, events are
// dropped (and counted) rather than slowing requests down.
func startSinks(a *core.App) {
	sinkOnce.Do(func() {
		core.AuditSinks = append(core.AuditSinks, func(ev map[string]any) {
			if !sinkOn.Load() {
				return
			}
			select {
			case sinkQ <- ev:
			default:
				sinkDropped.Add(1)
			}
		})
		go sinkLoop()
	})
	refreshSinks()
}

func flushSinks() {
	done := make(chan struct{})
	select {
	case flushReq <- done:
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	case <-time.After(time.Second):
	}
}

var sinkHTTP = &http.Client{Timeout: 10 * time.Second}

func sinkLoop() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	var batch []map[string]any
	send := func() {
		if len(batch) == 0 {
			return
		}
		b := batch
		batch = nil
		if wh := app.SecretSetting("audit.webhook"); wh != "" {
			if err := postWebhook(wh, b); err != nil {
				setSinkErr("webhook: " + err.Error())
			} else {
				sinkSent.Add(int64(len(b)))
			}
		}
		if sl := app.Setting("audit.syslog"); sl != "" {
			if err := sendSyslog(sl, b); err != nil {
				setSinkErr("syslog: " + err.Error())
			}
		}
	}
	for {
		select {
		case ev := <-sinkQ:
			batch = append(batch, ev)
			if len(batch) >= 200 {
				send()
			}
		case <-t.C:
			refreshSinks()
			send()
		case done := <-flushReq:
			for len(sinkQ) > 0 {
				batch = append(batch, <-sinkQ)
			}
			send()
			close(done)
		}
	}
}

func validWebhook(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(app.Cfg.Dev && u.Scheme == "http")) {
		return core.BadRequest("the webhook must be an https:// address")
	}
	return nil
}

func postWebhook(wh string, batch []map[string]any) error {
	if validWebhook(wh) != nil {
		return fmt.Errorf("not an https URL")
	}
	body, _ := json.Marshal(map[string]any{"source": "bunker", "events": batch})
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		req, _ := http.NewRequest("POST", wh, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Bunker/"+core.Version)
		res, err := sinkHTTP.Do(req)
		if err != nil {
			last = err
			continue
		}
		res.Body.Close()
		if res.StatusCode < 300 {
			return nil
		}
		last = fmt.Errorf("HTTP %d", res.StatusCode)
		if res.StatusCode < 500 && res.StatusCode != 429 {
			break
		}
	}
	return last
}

func parseSyslog(s string) (string, string, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "udp" && u.Scheme != "tcp") || u.Host == "" {
		return "", "", core.BadRequest("syslog looks like udp://siem.corp.com:514 or tcp://siem.corp.com:6514")
	}
	host := u.Host
	if u.Port() == "" {
		host += ":514"
	}
	return u.Scheme, host, nil
}

var (
	slMu   sync.Mutex
	slConn net.Conn
	slAddr string
)

// sendSyslog writes RFC 5424 messages (facility local0/auth, JSON body).
// TCP uses octet-counting framing (RFC 6587).
func sendSyslog(target string, batch []map[string]any) error {
	network, addr, err := parseSyslog(target)
	if err != nil {
		return err
	}
	slMu.Lock()
	defer slMu.Unlock()
	if slConn == nil || slAddr != network+addr {
		if slConn != nil {
			slConn.Close()
		}
		c, err := net.DialTimeout(network, addr, 5*time.Second)
		if err != nil {
			slConn = nil
			return err
		}
		slConn, slAddr = c, network+addr
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "-"
	}
	for _, ev := range batch {
		msg := syslogLine(host, ev)
		if network == "tcp" {
			msg = strconv.Itoa(len(msg)) + " " + msg
		}
		slConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := slConn.Write([]byte(msg)); err != nil {
			slConn.Close()
			slConn = nil
			return err
		}
		sinkSent.Add(1)
	}
	return nil
}

func syslogLine(host string, ev map[string]any) string {
	ts := time.Now().UTC()
	if v, ok := ev["ts"].(int64); ok {
		ts = time.Unix(v, 0).UTC()
	}
	b, _ := json.Marshal(ev)
	name, _ := ev["event"].(string)
	msgid := strings.Map(func(r rune) rune {
		if r < 33 || r > 126 {
			return -1
		}
		return r
	}, name)
	if msgid == "" || len(msgid) > 32 {
		msgid = "-"
	}
	// <PRI>1 TIMESTAMP HOST APP PROCID MSGID SD MSG ; PRI 110 = facility 13 (log audit) * 8 + severity 6 (info)
	return fmt.Sprintf("<110>1 %s %s bunker %d %s - %s\n", ts.Format(time.RFC3339), host, os.Getpid(), msgid, b)
}

// ---- retention ----

func retentionDays() int {
	if n, err := strconv.Atoi(app.Setting("audit.retention_days")); err == nil {
		return n
	}
	return 365
}

// PruneAudit deletes events older than the retention window in chunks.
func PruneAudit(ctx context.Context) int64 {
	days := retentionDays()
	if days <= 0 {
		return 0
	}
	cut := core.Now() - int64(days)*86400
	var total int64
	for {
		res, err := app.DB.ExecContext(ctx, `DELETE FROM audit WHERE id IN (SELECT id FROM audit WHERE ts<? ORDER BY id LIMIT 5000)`, cut)
		if err != nil {
			return total
		}
		n, _ := res.RowsAffected()
		total += n
		if n < 5000 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return total
}

func retentionLoop(ctx context.Context, a *core.App) {
	select {
	case <-time.After(2 * time.Minute):
	case <-ctx.Done():
		return
	}
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		if n := PruneAudit(ctx); n > 0 {
			a.Log.Info("audit retention", "deleted", n, "days", retentionDays())
		}
		select {
		case <-t.C:
		case <-ctx.Done():
			return
		}
	}
}
