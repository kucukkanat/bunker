// Package demo fills a fresh dev server with friendly example content
// (`bunker demo`), so someone trying Bunker sees real apps, people and
// data within seconds instead of an empty screen.
package demo

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

func init() {
	core.Register(core.Module{Name: "demo", Order: 950, Init: func(a *core.App) error {
		if !a.Cfg.Dev || !core.EnvBool("DEMO", false) {
			return nil
		}
		var n int
		a.DB.QueryRow(`SELECT COUNT(*) FROM sites WHERE is_template=0`).Scan(&n)
		if n > 0 {
			return nil // already has content
		}
		go seed(a)
		return nil
	}})
}

type client struct {
	base string
	hc   *http.Client
}

func newClient(base, as, name, groups string) *client {
	jar, _ := cookiejar.New(nil)
	c := &client{base: base, hc: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
	if as != "" {
		c.hc.Get(base + "/auth/dev?as=" + as + "&name=" + strings.ReplaceAll(name, " ", "%20") + "&groups=" + groups + "&next=/healthz")
	}
	return c
}

func (c *client) do(method, path string, body any) map[string]any {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("X-Bunker", "1")
	req.Header.Set("Content-Type", "application/json")
	res, err := c.hc.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return out
}

func seed(a *core.App) {
	base := strings.TrimRight(a.Cfg.PublicURL(), "/")
	// Wait until the server answers.
	for i := 0; i < 100; i++ {
		if res, err := http.Get(base + "/healthz"); err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	me := newClient(base, "", "", "")
	team := []struct{ email, name string }{{"maya@example.com", "Maya Okafor"}, {"tom@example.com", "Tom Becker"}, {"lea@example.com", "Léa Martin"}, {"sam@example.com", "Sam Rivera"}}
	people := map[string]*client{}
	for _, p := range team {
		people[p.email] = newClient(base, p.email, p.name, "Team")
	}
	share := func(slug string, role string) {
		me.do("POST", "/api/v1/sites/"+slug+"/members", map[string]any{"type": "group", "id": "Team", "name": "Team", "role": role})
	}

	// 1. A live kanban board.
	if s := me.do("POST", "/api/v1/sites", map[string]any{"title": "Website launch", "template": "template-tracker", "icon": "🚀"}); s != nil {
		share("website-launch", "editor")
		cards := [][3]string{{"Write launch blog post", "To do", "maya@example.com"}, {"Final logo files", "Done", "lea@example.com"}, {"Pricing page copy", "In progress", "tom@example.com"}, {"Book the launch call", "To do", "sam@example.com"}, {"Fix mobile menu", "In progress", "lea@example.com"}}
		for i, c := range cards {
			who := people[c[2]]
			who.do("POST", "/api/v1/sites/website-launch/db/cards", map[string]any{"title": c[0], "col": c[1], "owner": nameOf(team, c[2]), "order": i})
		}
	}
	// 2. Requests & approvals with history.
	if s := me.do("POST", "/api/v1/sites", map[string]any{"title": "Equipment requests", "template": "template-requests", "icon": "💻"}); s != nil {
		share("equipment-requests", "viewer")
		reqs := []struct {
			who, what, cat string
			amount         float64
		}{{"tom@example.com", "Second monitor", "Hardware", 220}, {"sam@example.com", "Standing desk", "Furniture", 480}, {"maya@example.com", "Design course", "Training", 150}}
		for _, r := range reqs {
			people[r.who].do("POST", "/api/v1/sites/equipment-requests/db/requests", map[string]any{"what": r.what, "amount": r.amount, "category": r.cat, "neededBy": time.Now().AddDate(0, 0, 14).Format("2006-01-02"), "why": "So I can work faster."})
		}
	}
	// 3. A poll with votes.
	if s := me.do("POST", "/api/v1/sites", map[string]any{"title": "Team offsite vote", "template": "template-poll", "icon": "🗳️"}); s != nil {
		share("team-offsite-vote", "viewer")
		for i, p := range team {
			people[p.email].do("POST", "/api/v1/sites/team-offsite-vote/db/votes", map[string]any{"option": []string{"Lisbon", "Mountains", "Lisbon", "Berlin"}[i]})
		}
	}
	// 4. A spreadsheet that becomes a table app.
	csv := "Month,Team,Budget,Spent\nJul,Design,12000,9800\nJul,Engineering,40000,41200\nAug,Design,12000,11100\nAug,Engineering,40000,38700\nSep,Design,14000,12900\nSep,Engineering,42000,40100\n"
	me.do("POST", "/api/v1/drop", map[string]any{"title": "Q3 budget", "files": map[string]string{"q3-budget.csv": csv}})
	// 5. A Markdown handbook that becomes a docs site.
	me.do("POST", "/api/v1/drop", map[string]any{"title": "Team handbook", "files": map[string]string{
		"index.md":       "# Team handbook\n\nWelcome! This handbook was a folder of Markdown files, dropped on Bunker.\n\n- [How we work](how-we-work.md)\n- [Time off](time-off.md)\n",
		"how-we-work.md": "# How we work\n\n## Meetings\n\nShort, with notes. Decisions go in the meeting notes app.\n\n## Tools\n\n| Need | Use |\n|---|---|\n| Tasks | Website launch board |\n| Requests | Equipment requests |\n",
		"time-off.md":    "# Time off\n\nAsk two weeks ahead. Add it to the team directory so everyone knows.\n",
	}})
	share("team-handbook", "viewer")
	// 6. A hand-written page.
	me.do("POST", "/api/v1/drop", map[string]any{"title": "Lunch on Friday", "files": map[string]string{"index.html": lunchHTML}})
	a.Log.Info("demo content ready", "url", base)
}

func nameOf(team []struct{ email, name string }, e string) string {
	for _, p := range team {
		if p.email == e {
			return p.name
		}
	}
	return e
}

const lunchHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Lunch on Friday</title>
<link rel="stylesheet" href="/_/kit.css"></head><body><main class="container">
<h1>🥗 Lunch on Friday</h1><p>Pick what you'd like. Orders close Thursday at 4pm.</p>
<form id="f" class="card stack"><label>Your choice <select id="meal"><option>Pasta</option><option>Salad bowl</option><option>Curry</option></select></label>
<button>Order</button></form><h2>Orders</h2><ul id="list"></ul></main>
<script>
const orders = bunker.db.collection('orders');
async function load(){ const r = await orders.list({limit:100}); document.getElementById('list').replaceChildren(...r.items.map(o=>{const li=document.createElement('li');li.textContent=o._authorName+': '+o.meal;return li})) }
document.getElementById('f').onsubmit = async e => { e.preventDefault(); await orders.add({meal: document.getElementById('meal').value}); bunker.toast('Ordered!'); };
orders.subscribe(load); load();
</script></body></html>`
