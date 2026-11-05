package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

// Usage kinds.
const (
	KindBuilder = "builder"
	KindPage    = "page"
)

func today() string { return time.Unix(core.Now(), 0).UTC().Format("2006-01-02") }

// recordUsage adds one call's tokens to today's counters.
func recordUsage(ctx context.Context, userID, siteID int64, kind string, u Usage, calls int) {
	if calls == 0 && u.Billable() == 0 {
		return
	}
	_, err := app.DB.ExecContext(context.WithoutCancel(ctx), `INSERT INTO ai_usage(day,user_id,site_id,kind,input_tokens,output_tokens,billable_tokens,calls) VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(day,user_id,site_id,kind) DO UPDATE SET input_tokens=input_tokens+excluded.input_tokens, output_tokens=output_tokens+excluded.output_tokens, billable_tokens=billable_tokens+excluded.billable_tokens, calls=calls+excluded.calls`,
		today(), userID, siteID, kind, u.InputTokens+u.CacheReadTokens+u.CacheWriteTokens, u.OutputTokens, u.Billable(), calls)
	if err != nil {
		app.Log.Error("ai usage write failed", "err", err)
	}
}

func usedByUser(ctx context.Context, userID int64) int64 {
	var n int64
	app.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(billable_tokens),0) FROM ai_usage WHERE day=? AND user_id=?`, today(), userID).Scan(&n)
	return n
}

func usedBySite(ctx context.Context, siteID int64) int64 {
	var n int64
	app.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(billable_tokens),0) FROM ai_usage WHERE day=? AND site_id=?`, today(), siteID).Scan(&n)
	return n
}

// checkQuota refuses a call when the person's or the site's daily
// allowance is used up.
func checkQuota(ctx context.Context, c *Config, u *core.User, siteID int64) error {
	if c.UserDailyTokens > 0 && usedByUser(ctx, u.ID) >= c.UserDailyTokens {
		return core.Quota("You've used today's AI allowance. It resets at midnight UTC" + resetIn() + ". If you need more, ask an admin.")
	}
	if siteID != 0 && c.SiteDailyTokens > 0 && usedBySite(ctx, siteID) >= c.SiteDailyTokens {
		return core.Quota("This app has used today's AI allowance. It resets at midnight UTC" + resetIn() + ". The site owner can ask an admin for more.")
	}
	return nil
}

func resetIn() string {
	now := time.Unix(core.Now(), 0).UTC()
	next := now.Truncate(24 * time.Hour).Add(24 * time.Hour)
	h := int(next.Sub(now).Hours())
	if h <= 0 {
		return " (in less than an hour)"
	}
	if h == 1 {
		return " (in about an hour)"
	}
	return fmt.Sprintf(" (in about %d hours)", h)
}

// Budget is a person's AI allowance for today, shown in the builder.
type Budget struct {
	Used       int64  `json:"used"`
	Limit      int64  `json:"limit"` // 0 = unlimited
	Remaining  int64  `json:"remaining"`
	Percent    int    `json:"percentUsed"`
	ResetsAt   int64  `json:"resetsAt"`
	BuildsLeft int64  `json:"approxBuildsLeft,omitempty"`
	Text       string `json:"text"`
}

// userBudget computes today's allowance and a rough "builds left"
// estimate from this person's average builder request size.
func userBudget(ctx context.Context, userID int64) Budget {
	c := currentConfig()
	now := time.Unix(core.Now(), 0).UTC()
	b := Budget{Used: usedByUser(ctx, userID), Limit: c.UserDailyTokens, ResetsAt: now.Truncate(24 * time.Hour).Add(24 * time.Hour).Unix()}
	if b.Limit == 0 {
		b.Remaining = -1
		b.Text = "No daily AI limit."
		return b
	}
	b.Remaining = max(b.Limit-b.Used, 0)
	b.Percent = int(min(b.Used*100/max(b.Limit, 1), 100))
	var tokens, calls int64
	app.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(billable_tokens),0), COALESCE(SUM(calls),0) FROM ai_usage WHERE user_id=? AND kind=? AND day>=?`,
		userID, KindBuilder, now.AddDate(0, 0, -14).Format("2006-01-02")).Scan(&tokens, &calls)
	avg := int64(60_000) // a typical build with a capable model
	if calls >= 3 && tokens/calls > 1000 {
		avg = tokens / calls
	}
	b.BuildsLeft = b.Remaining / avg
	switch {
	case b.Remaining == 0:
		b.Text = "You've used today's AI allowance. It resets at midnight UTC."
	case b.BuildsLeft >= 100:
		b.Text = fmt.Sprintf("%d%% of today's AI allowance used.", b.Percent)
	case b.BuildsLeft >= 1:
		b.Text = fmt.Sprintf("About %d builder requests left today.", b.BuildsLeft)
	default:
		b.Text = "Less than one builder request left today."
	}
	return b
}

// usageReport aggregates usage for the admin dashboard.
func usageReport(ctx context.Context, days int) (any, error) {
	since := time.Unix(core.Now(), 0).UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	type row struct {
		ID       int64  `json:"id"`
		Name     string `json:"name,omitempty"`
		Email    string `json:"email,omitempty"`
		Slug     string `json:"slug,omitempty"`
		Input    int64  `json:"inputTokens"`
		Output   int64  `json:"outputTokens"`
		Billable int64  `json:"tokens"`
		Calls    int64  `json:"calls"`
		Builder  int64  `json:"builderTokens"`
		Page     int64  `json:"pageTokens"`
	}
	scanRows := func(q string, withEmail bool) ([]row, error) {
		rows, err := app.DB.QueryContext(ctx, q, since)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []row{}
		for rows.Next() {
			var r row
			var a, b string
			if err := rows.Scan(&r.ID, &a, &b, &r.Input, &r.Output, &r.Billable, &r.Calls, &r.Builder, &r.Page); err != nil {
				return nil, err
			}
			if withEmail {
				r.Name, r.Email = a, b
			} else {
				r.Name, r.Slug = a, b
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
	const sums = `SUM(u.input_tokens), SUM(u.output_tokens), SUM(u.billable_tokens), SUM(u.calls),
 SUM(CASE WHEN u.kind='builder' THEN u.billable_tokens ELSE 0 END), SUM(CASE WHEN u.kind='page' THEN u.billable_tokens ELSE 0 END)`
	byUser, err := scanRows(`SELECT u.user_id, COALESCE(us.name,''), COALESCE(us.email,''), `+sums+` FROM ai_usage u LEFT JOIN users us ON us.id=u.user_id WHERE u.day>=? GROUP BY u.user_id ORDER BY 6 DESC LIMIT 500`, true)
	if err != nil {
		return nil, err
	}
	bySite, err := scanRows(`SELECT u.site_id, COALESCE(s.title,''), COALESCE(s.slug,''), `+sums+` FROM ai_usage u LEFT JOIN sites s ON s.id=u.site_id WHERE u.day>=? AND u.site_id<>0 GROUP BY u.site_id ORDER BY 6 DESC LIMIT 500`, false)
	if err != nil {
		return nil, err
	}
	type dayRow struct {
		Day      string `json:"day"`
		Input    int64  `json:"inputTokens"`
		Output   int64  `json:"outputTokens"`
		Billable int64  `json:"tokens"`
		Calls    int64  `json:"calls"`
	}
	byDay := []dayRow{}
	var total dayRow
	rows, err := app.DB.QueryContext(ctx, `SELECT day, SUM(input_tokens), SUM(output_tokens), SUM(billable_tokens), SUM(calls) FROM ai_usage WHERE day>=? GROUP BY day ORDER BY day`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d dayRow
		if err := rows.Scan(&d.Day, &d.Input, &d.Output, &d.Billable, &d.Calls); err != nil {
			return nil, err
		}
		total.Input += d.Input
		total.Output += d.Output
		total.Billable += d.Billable
		total.Calls += d.Calls
		byDay = append(byDay, d)
	}
	c := currentConfig()
	return map[string]any{
		"days":   days,
		"since":  since,
		"totals": map[string]any{"inputTokens": total.Input, "outputTokens": total.Output, "tokens": total.Billable, "calls": total.Calls},
		"byUser": byUser,
		"bySite": bySite,
		"byDay":  byDay,
		"quotas": map[string]any{"userDailyTokens": c.UserDailyTokens, "siteDailyTokens": c.SiteDailyTokens, "maxRequestTokens": c.MaxRequestTokens},
		"note":   "tokens = input + output, with cached prompt tokens counted at one tenth (like providers bill them).",
	}, rows.Err()
}

// auditCall records an ai.call event (content only when logContent is on).
func auditCall(ctx context.Context, c *Config, siteID int64, target, kind, model string, u Usage, ms int64, extra map[string]any, prompt, answer string) {
	meta := map[string]any{"kind": kind, "provider": c.Provider, "model": model, "inputTokens": u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens, "outputTokens": u.OutputTokens, "ms": ms}
	for k, v := range extra {
		meta[k] = v
	}
	if c.LogContent {
		meta["prompt"] = clip(prompt, 8000)
		meta["response"] = clip(answer, 8000)
	}
	app.Audit(ctx, "ai.call", siteID, target, meta)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Don't split a UTF-8 sequence.
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n] + "…"
}
