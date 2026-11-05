package functions

import (
	"context"
	"testing"
	"time"

	_ "github.com/kucukkanat/bunker/internal/data"
	"github.com/kucukkanat/bunker/internal/testkit"
)

func TestScheduleRuns(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	ada.Must("POST", "/api/v1/drop", map[string]any{"title": "Cron", "files": map[string]string{"index.html": "x",
		"api/tick.js": `export const config = { schedule: "*/5 * * * *" };
export default async (input, ctx) => { await bunker.db.collection("ticks").add({ type: ctx.trigger.type, by: ctx.user.email, at: input.at }); }`}})
	var n int
	var next int64
	k.App.DB.QueryRow(`SELECT COUNT(*), MAX(next_at) FROM fn_triggers WHERE kind='schedule' AND name='tick'`).Scan(&n, &next)
	if n != 1 || next <= time.Now().Unix() {
		t.Fatalf("schedule not registered: n=%d next=%d", n, next)
	}
	// Make it due and tick.
	k.App.DB.Exec(`UPDATE fn_triggers SET next_at=? WHERE name='tick'`, time.Now().Add(-time.Minute).Unix())
	tick(time.Now())
	for i := 0; i < 200; i++ {
		items, _ := ada.Must("GET", "/api/v1/sites/cron/db/ticks", nil)["items"].([]any)
		if len(items) == 1 {
			it := items[0].(map[string]any)
			if it["type"] != "schedule" || it["by"] != "ada@corp.com" {
				t.Fatalf("tick doc: %v", it)
			}
			k.App.DB.QueryRow(`SELECT next_at FROM fn_triggers WHERE name='tick'`).Scan(&next)
			if next <= time.Now().Unix() {
				t.Fatal("next_at not advanced")
			}
			// Publishing a version without the schedule removes it.
			ada.Must("POST", "/api/v1/sites/cron/deploy", map[string]any{"files": map[string]string{"index.html": "y"}})
			syncSite(context.Background(), siteIDOf(t, k, "cron"))
			k.App.DB.QueryRow(`SELECT COUNT(*) FROM fn_triggers WHERE name='tick'`).Scan(&n)
			if n != 0 {
				t.Fatal("schedule kept after it was removed")
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("scheduled run didn't happen")
}

func siteIDOf(t *testing.T, k *testkit.Kit, slug string) int64 {
	var id int64
	if err := k.App.DB.QueryRow(`SELECT id FROM sites WHERE slug=?`, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
