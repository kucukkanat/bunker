package core

import (
	"context"
	"encoding/json"
)

// AuditSink receives every audit event (e.g. syslog/webhook streaming).
var AuditSinks []func(ev map[string]any)

// Audit records an event. siteID may be 0.
func (a *App) Audit(ctx context.Context, event string, siteID int64, target string, meta map[string]any) {
	u := UserFrom(ctx)
	var actorID any
	var email string
	if u != nil {
		actorID = u.ID
		email = u.Email
		if u.ViaToken {
			if meta == nil {
				meta = map[string]any{}
			}
			meta["via"] = "token"
		}
	}
	var mj []byte
	if meta != nil {
		mj, _ = json.Marshal(meta)
	}
	var sid any
	if siteID != 0 {
		sid = siteID
	}
	ts := Now()
	ip := IPFrom(ctx)
	_, err := a.DB.ExecContext(context.WithoutCancel(ctx), `INSERT INTO audit(ts,actor_id,actor_email,event,site_id,target,ip,meta) VALUES(?,?,?,?,?,?,?,?)`, ts, actorID, email, event, sid, target, ip, nullStr(string(mj)))
	if err != nil {
		a.Log.Error("audit write failed", "event", event, "err", err)
	}
	if len(AuditSinks) > 0 {
		ev := map[string]any{"ts": ts, "actor": email, "event": event, "siteId": siteID, "target": target, "ip": ip, "meta": meta}
		for _, s := range AuditSinks {
			s(ev)
		}
	}
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
