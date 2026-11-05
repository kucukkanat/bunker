package mcp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kucukkanat/bunker/internal/core"
)

// The agent-facing documentation lives here once and is served as
// /llms.txt, /llms-full.txt, MCP resources, the get_sdk_docs tool and the
// AGENTS.md section written by `bunker init`.

// Intro is the one-paragraph description of Bunker.
const Intro = `Bunker is a self-hosted internal app cloud. People publish small web apps ("sites": plain HTML/CSS/JS, optionally built with a bundler) that only signed-in colleagues can open. Each site is private by default and shared with people or groups as viewer or editor. Every deploy creates a version you can roll back. Pages run in a locked-down sandbox and reach data (a per-site database, file uploads, AI, Microsoft 365) only through the built-in ` + "`window.bunker`" + ` SDK.`

// SandboxRules explains what a page may and may not do.
const SandboxRules = `## Sandbox rules (read before writing a page)
Pages render inside a sandboxed iframe with an opaque origin and a strict CSP:
- NO network: fetch(), XMLHttpRequest, WebSocket and EventSource to any URL are blocked (connect-src 'none'). Use the SDK instead.
- NO external scripts, styles or fonts from CDNs. Inline <script>/<style> are fine; bundle libraries into your files (or load them from /lib/ when the admin provides it).
- NO cookies, localStorage, sessionStorage or IndexedDB. Use bunker.storage (per viewer) or bunker.db (shared).
- NO <form action=...> submissions (form-action 'none'); handle submit events in JS.
- Links with target="_blank" work. Embedding other websites in iframes does not.
- Images: same-site files, data: and blob: URLs only.
- index.html is required at the root. Relative paths only (e.g. "app.js", not "/app.js").
- Single-page apps: set the site setting spa=true so unknown paths serve index.html.
- Secrets (API keys, passwords) in uploaded files are blocked by a scanner; never put credentials in a page. Code that needs a secret goes in a server function (api/*.js) that reads it with bunker.secrets.get.`

// SDKReference documents window.bunker for pages.
const SDKReference = `## SDK reference (window.bunker, injected automatically into every HTML page)
Every method returns a Promise and runs with the VIEWER's permissions. Errors throw {code, message} with code in: forbidden, not_found, quota_exceeded, invalid, consent_required, unavailable.

### Who is viewing
` + "```js" + `
const me = await bunker.me();          // {id, name, email, groups:[...], role: "viewer"|"editor"|"owner"|"admin"}
if (await bunker.me.inGroup('Finance')) { /* ... */ }
` + "```" + `

### bunker.db: shared JSON collections with live updates
` + "```js" + `
const orders = bunker.db.collection('orders');
const { id } = await orders.add({ meal: 'Pasta' });   // docs get _id, _author, _authorName, _created, _updated
await orders.update(id, { drink: 'Juice' });           // merge
await orders.set(id, { meal: 'Salad' });               // replace
await orders.delete(id);
const doc  = await orders.get(id);
const page = await orders.list({ limit: 50, after: cursor });
const mine = await orders.query({ where: { _author: 'me' }, orderBy: '-_created' });
const stop = orders.subscribe(change => { /* {type:'added'|'updated'|'deleted', doc} */ });
await orders.rule('own');   // editors only: own (default) | private | read | append | open | group:<name>
` + "```" + `
Rules decide what viewers may do (editors always can do everything): own = read all, add, change own; private = only own docs; read = read only; append = add only; open = anyone can change; group:<name> = full access for that group, read for others. Limits: 1 MB per document.

### bunker.files: uploads
` + "```js" + `
const f = await bunker.files.upload(input.files[0], { folder: 'receipts' }); // {id, name, size, type, url}
img.src = f.url;
const all = await bunker.files.list({ folder: 'receipts' });
await bunker.files.delete(f.id);
` + "```" + `

### bunker.ai: model calls through the admin-configured provider
` + "```js" + `
const reply = await bunker.ai.chat([{ role: 'user', content: 'Summarise: ' + text }]); // reply.text
for await (const chunk of bunker.ai.stream(messages)) out.textContent += chunk;
const data = await bunker.ai.json(messages, { schema: { type: 'object', properties: { sentiment: { enum: ['positive','neutral','negative'] } } } });
` + "```" + `
Throws unavailable when AI is not configured; quota_exceeded when limits are hit.

### bunker.storage: per-viewer key/value (replaces localStorage)
` + "```js" + `
await bunker.storage.set('theme', 'dark');
const theme = await bunker.storage.get('theme');  // null if missing
await bunker.storage.remove('theme');
` + "```" + `
64 KB per value. Shared data belongs in bunker.db.

### bunker.room: ephemeral real-time messages and presence between viewers of the same page (no storage)

### bunker.m365: Microsoft 365 data the viewer can already see (when enabled by the admin)
` + "```js" + `
const rows  = await bunker.m365.lists.items(siteUrl, 'Projects', { top: 200 });
const range = await bunker.m365.excel.range(fileUrl, 'Budget!A1:F40');
const docs  = await bunker.m365.files.list(folderUrl);
const mgr   = await bunker.m365.graph('/me/manager');
` + "```" + `
The first call may show a consent prompt; declining throws consent_required.

### bunker.fn: call a server function (api/<name>.js, see "Server functions")
` + "```js" + `
const { id } = await bunker.fn('contact', { email, text });   // resolves to the function's return value
` + "```" + `
Throws functions_off (turned off by an admin), not_found, busy, timeout or function_error. From a ?draft=1 preview it runs the draft code.`

// ServerFunctions documents api/*.js server functions for agents.
var ServerFunctions = strings.ReplaceAll(`## Server functions (api/*.js, run on the Bunker server)
Put code that must not run in the browser in ¦api/<name>.js¦ (or .mjs/.ts; folders allowed: api/orders/refund.ts is "orders/refund"). Each file has an ¦export default¦ async function; pages call it with ¦bunker.fn(name, input)¦. Files and folders starting with _ under api/ are shared modules (importable, not callable). Imports are bundled on publish (ESM, TypeScript types stripped, no npm packages). Function files are never served to browsers.

Prefer plain page code. Write a function only when you need one of: a secret (API key), outbound fetch() to another service, rights the viewer doesn't have (config.elevated), a schedule, a trigger on database writes, validation the viewer must not be able to bypass, or a webhook target.

¦¦¦js
// api/contact.js
export const config = { elevated: true };   // optional: elevated, schedule, onWrite, timeoutMs
export default async function (input, ctx) {
  // ctx = { user:{id,email,name,groups}, site:{slug,title,version,draft}, trigger:{type:"call"|"schedule"|"write"|"webhook"}, elevated }
  const email = String(input?.email || '').trim();
  if (!email.includes('@')) throw new Error('Please enter a valid email address.');
  const msg = await bunker.db.collection('messages').add({ email, text: String(input.text || '').slice(0, 2000), from: ctx.user.email });
  const hook = await bunker.secrets.get('SLACK_WEBHOOK');   // null until someone sets it
  if (hook) await fetch(hook, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ text: 'New message from ' + email }) });
  return { id: msg._id };                    // any JSON value, up to 1 MB
}
¦¦¦
¦¦¦js
// in the page
try { const { id } = await bunker.fn('contact', { email, text }); }
catch (err) { show(err.message); }  // err.code: functions_off | not_found | busy | timeout | function_error
¦¦¦
- Runs as the **caller**, with the caller's own permissions, by default. ¦config.elevated: true¦ runs as the site owner (still only the page-safe features: data, files, storage, AI, Microsoft 365, rooms, notify, connections). Elevated functions must check ¦ctx.user¦ and validate input themselves.
- ¦config.schedule: "0 8 * * 1"¦: 5-field cron in UTC (at most once a minute); runs as the owner with trigger {type:"schedule", at}.
- ¦config.onWrite: ["orders"]¦: runs after an add/set/update/delete in those collections with trigger {type:"write", collection, op, doc, id}, as the writer (owner if elevated). Writes made inside it don't trigger again.
- Schedules and triggers run the published version; calls from a ?draft=1 preview run the draft.
- Server SDK (global ¦bunker¦, same shapes as the page SDK): db, storage, files (plus upload(name, string|Uint8Array, {type, folder})), ai.chat/json, m365, connections.fetch, rooms(name).send, notify, me(), site; server-only ¦bunker.secrets.get(NAME)¦ (string or null) and ¦fetch(url, init)¦ (public hosts only, subject to the admin's outbound policy). console.log/info/warn/error go to the run logs. setTimeout works; there is no DOM, window, live subscriptions or streaming.
- Limits (defaults, admins can change them): 30 s per call (120 s for schedules and triggers), 64 MB memory, 1 MB input and output, 64 KB of logs and 1,000 SDK calls per run, 4 concurrent runs per app (then 429 busy).
- Secrets: never put keys in files (the deploy scanner blocks them). Read them with bunker.secrets.get and ask the person to add them with ¦bunker secrets set <site> NAME¦ (reads the value from stdin or a hidden prompt) or in the dashboard. Names look like STRIPE_KEY.
- Try and debug: ¦bunker fn ls <site>¦, ¦bunker fn call <site> <name> '{"a":1}' [--draft]¦, ¦bunker fn logs <site> --follow¦. Admins can turn functions off for the whole server, one person or one app; calls then fail with functions_off.`, "¦", "`")

// AuthGuide explains how agents authenticate.
func AuthGuide(base string) string {
	return `## Authenticate
- CLI: ` + "`bunker login " + base + "`" + ` (device flow: shows a code, you approve it in the browser) or ` + "`bunker login " + base + " --token bk_...`" + `.
- Personal access tokens: create one in Settings → Tokens (or ` + "`bunker tokens create my-agent`" + `). Send it as ` + "`Authorization: Bearer bk_...`" + `. Scopes: read, write, admin. Tokens act as you, with your permissions.
- CI: set BUNKER_SERVER=` + base + ` and BUNKER_TOKEN=bk_... and run ` + "`bunker deploy --yes`" + `.
- Device flow for your own tools: POST ` + base + `/api/v1/auth/device {"client":"my-agent"} → {deviceCode,userCode,verificationUriComplete}; ask the person to open the URL; poll POST ` + base + `/api/v1/auth/device/token {"deviceCode":"..."} until it returns {token}.
- MCP: remote server at ` + base + `/mcp (OAuth 2.1 with discovery, or a Bearer token header); local stdio server via ` + "`bunker mcp`" + `.`
}

// DeployGuide explains deploys with curl and the CLI.
func DeployGuide(base string) string {
	return `## Deploy
Fastest: ` + "`bunker deploy`" + ` in a folder (creates the site on first run, writes bunker.json, uploads only changed files, prints the URL). ` + "`bunker deploy page.html`" + ` or ` + "`cat page.html | bunker deploy -`" + ` for one file.

Create a new site from files in one call:
` + "```sh" + `
curl -sS -X POST "` + base + `/api/v1/drop?title=Team%20Lunch" \
  -H "Authorization: Bearer $BUNKER_TOKEN" -H "Content-Type: application/json" \
  -d '{"files":{"index.html":"<!doctype html><h1>Hello</h1><script>bunker.me().then(m=>document.querySelector(\"h1\").textContent=\"Hi \"+m.name)</script>"}}'
# → {"slug":"team-lunch","url":"` + base + `/s/team-lunch/","version":1,...}
` + "```" + `

Publish a new version of an existing site (files map path → text, or {"base64":"..."} for binary):
` + "```sh" + `
curl -sS -X POST "` + base + `/api/v1/sites/team-lunch/deploy" \
  -H "Authorization: Bearer $BUNKER_TOKEN" -H "Content-Type: application/json" \
  -d '{"message":"Add drinks","files":{"index.html":"...","app.js":"...","logo.png":{"base64":"iVBORw0..."}}}'
` + "```" + `
Add "publish": false to save a draft instead (preview at /s/<site>/?draft=1). Multipart uploads and .zip files work too (field "files").
Other useful calls: GET /api/v1/sites (list), GET /api/v1/sites/{site}/files (list files), GET /api/v1/sites/{site}/files/{path} (read), PUT /api/v1/sites/{site}/files/{path} (write draft file), POST /api/v1/sites/{site}/draft/publish, GET /api/v1/sites/{site}/versions, POST /api/v1/sites/{site}/versions/{n}/restore, POST /api/v1/sites/{site}/members {"type":"user","id":"ada@corp.com","role":"viewer"}.
Every operation is listed with its parameters at GET ` + base + `/api/v1/ops (and as OpenAPI at /api/v1/openapi.json).`
}

// AgentGuide renders the complete agent guide. full adds parameter
// details for every operation.
func AgentGuide(base string, ops []core.Op, full bool) string {
	var b strings.Builder
	b.WriteString("# Bunker\n\n> " + Intro + "\n\n")
	b.WriteString("Server: " + base + "\n\n")
	if !full {
		b.WriteString("Full guide with every operation's parameters: " + base + "/llms-full.txt\n\n")
	}
	b.WriteString(AuthGuide(base) + "\n\n")
	b.WriteString(DeployGuide(base) + "\n\n")
	b.WriteString(SandboxRules + "\n\n")
	b.WriteString(SDKReference + "\n\n")
	b.WriteString(ServerFunctions + "\n\n")
	b.WriteString(OpsList(ops, full))
	return b.String()
}

// OpsList renders the operation catalogue as markdown.
func OpsList(ops []core.Op, full bool) string {
	var b strings.Builder
	b.WriteString("## API operations\nAll paths are relative to the server. Call them with `bunker api <name> --param value`, as MCP tools (name with dots → underscores), or over HTTP.\n\n")
	groups := map[string][]core.Op{}
	var names []string
	for _, o := range ops {
		if o.Hidden {
			continue
		}
		g := o.Name
		if i := strings.IndexByte(g, '.'); i > 0 {
			g = g[:i]
		}
		if _, ok := groups[g]; !ok {
			names = append(names, g)
		}
		groups[g] = append(groups[g], o)
	}
	sort.Strings(names)
	for _, g := range names {
		b.WriteString("### " + g + "\n")
		for _, o := range groups[g] {
			fmt.Fprintf(&b, "- `%s` %s %s: %s", o.Name, o.Method, o.Path, o.Summary)
			if o.Access == core.AccessAdmin {
				b.WriteString(" (admins)")
			}
			if o.Destructive {
				b.WriteString(" (destructive)")
			}
			b.WriteString("\n")
			if full {
				for _, p := range o.Params {
					t := p.Type
					if t == "" {
						t = "string"
					}
					req := ""
					if p.Required {
						req = ", required"
					}
					fmt.Fprintf(&b, "  - `%s` (%s in %s%s)", p.Name, t, p.In, req)
					if p.Desc != "" {
						b.WriteString(": " + p.Desc)
					}
					if len(p.Enum) > 0 {
						b.WriteString(" [" + strings.Join(p.Enum, " | ") + "]")
					}
					b.WriteString("\n")
				}
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// AgentsMD is the section `bunker init` writes into AGENTS.md.
func AgentsMD(base, site string) string {
	if base == "" {
		base = "https://<your-bunker>"
	}
	target := "a new site (created on first deploy)"
	if site != "" {
		target = "the site `" + site + "` (" + base + "/s/" + site + "/)"
	}
	return `## Bunker

This folder deploys to Bunker (` + base + `), an internal app cloud, as ` + target + `.

- Deploy: ` + "`bunker deploy`" + ` (add ` + "`--draft`" + ` to preview at /s/<site>/?draft=1, ` + "`-m \"message\"`" + ` to describe the change). It builds first when package.json has a build script, then uploads dist/, build/ or out/.
- Roll back: ` + "`bunker versions`" + ` then ` + "`bunker rollback <site> <n>`" + `.
- Discover anything else: ` + "`bunker ops`" + ` and ` + "`bunker api <op.name> --param value`" + `. Use ` + "`--json`" + ` for machine-readable output.
- Full docs for agents: ` + base + `/llms-full.txt

` + SandboxRules + "\n\n" + SDKReference + "\n\n" + ServerFunctions + "\n"
}
