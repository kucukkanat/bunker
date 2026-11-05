# Changelog

## Unreleased

### Server functions
- **A backend next to your pages.** Drop `api/<name>.js`, `.mjs` or `.ts` with an `export default` and call it from a page with `bunker.fn('<name>', input)`. Nested folders work (`api/orders/refund.ts` → `orders/refund`); files starting with `_` are shared modules. Relative imports and TypeScript are bundled at publish with the embedded esbuild; no npm, no build step.
- **Sandboxed per call.** Each run gets a fresh QuickJS (quickjs-ng 0.17.0) instance compiled to WebAssembly, run by wazero in Bunker's own process. Pure Go, no containers. Defaults: 64 MB memory, 30 s per call, 120 s per trigger, 1 MB input and output, 1,000 host calls, 4 runs at once per app.
- **The viewer's rights by default.** Functions use the same page-safe operations and collection rules as the page. `config.elevated: true` runs as the app's owner.
- **Triggers:** `config.schedule` (cron, UTC), `config.onWrite` (after adds, sets, updates and deletes in listed collections), and inbound webhooks that target a function (its return value is the response).
- **Secrets:** per-app, write-only, encrypted at rest, read with `bunker.secrets.get(NAME)`.
- **Outbound `fetch`** under an admin policy: `internet` (public hosts only, private addresses refused; the default), `allowlist` or `off`.
- **Dashboard:** a Functions tab with test runs against the draft or live code, the last 200 runs with logs, live logs and secrets. CLI: `bunker fn`, `bunker fn call`, `bunker fn logs`, `bunker secrets`. MCP tools for all of it.
- **Admin:** Admin → Functions to turn them off, change limits and the outbound policy, and block a person (every app they own) or one app, with a reason the person sees. `BUNKER_FUNCTIONS_*` variables pin these settings. New audit events `functions.*`.

### Changed
- **`api/*.js`, `api/*.mjs` and `api/*.ts` files with `export default` are now server functions and are no longer served to browsers** (they return 404), and neither is anything under `api/_*`. Other files in `api/`, such as `api/data.json`, are served as before. If an app loaded such a file as a browser script, move it out of `api/`.

## v0.1.0 (2026-11-05): first release

Your own private app cloud: drop a file or describe an app, get a private, shareable, sandboxed web app. One Go binary, one data folder.

### Make apps
- **Drop anything:** HTML, folders, zips, SPA build output (root-absolute paths rewritten), Markdown docs sites and slide decks, CSV/TSV/Excel table apps, JSON viewer, image galleries, PDFs (pdf.js), audio/video, Jupyter notebooks, text, React/JSX/TSX/TypeScript (esbuild at publish, preact/compat), `<script type="text/babel">` pages, unbuilt Vite projects.
- **CDN links just work:** popular libraries (React, Vue, Chart.js, Tailwind, Alpine, d3, Bootstrap, Leaflet, …) are built in at `/_/lib` and CDN tags are rewritten at publish; unknown external URLs are reported as warnings.
- **AI builder:** Claude, OpenAI, Azure OpenAI or any OpenAI-compatible/local model; edits drafts with diffs, streams progress, sets data rules, offers to fix preview errors (max 2 rounds), daily budgets. Zero setup with `ANTHROPIC_API_KEY`/`OPENAI_API_KEY`, one-click local Ollama/LM Studio.
- **16 templates:** requests & approvals, kanban tracker, sign-up sheet, feedback with AI summary, poll, dashboard, team directory & who’s out, meeting notes, equipment check-out, room & desk booking, inventory, incident log, onboarding checklist, goals & OKRs, kudos wall, lunch orders.
- **Import from GitHub:** paste a public repo or folder link on Home.
- **`bunker demo`:** dev mode pre-filled with example apps, teammates and data.
- **Versions and drafts:** every publish is a version; diff, preview, one-click restore; duplicate; zip export.

### Runtime for pages (`window.bunker`)
- Database collections with access rules (own, private, read, append, open, group) and live updates; files; per-person storage (localStorage shim); rooms and presence; AI; notifications; Microsoft 365 data with the viewer's own rights; admin-defined API connections with SSRF protection.
- **Inbound webhooks:** secret URLs that add JSON/form posts to a collection (Power Automate, Zapier, scripts).

### Share
- Private by default; people, groups, everyone in the org, or (admin-allowed) public links with no sign-in.
- "Ask for access" with one-click approval, in-app notifications (and email with SMTP), view stats.

### Sign-in
- Built-in accounts out of the box (argon2id, invite and reset links, re-send invites).
- Optional company sign-in: Microsoft Entra ID (with automatic app registration), Google Workspace, any OIDC. Multi-tenant Entra and public email domains refused.

### Agents and automation
- Everything in the UI is an API operation: OpenAPI, CLI (`bunker deploy`, `share`, `rollback`, `ai`, `api <any op>`, `backup`, `restore`, …), local MCP (`bunker mcp`) and remote MCP at `/mcp` with OAuth 2.1 (Claude, Cursor, ChatGPT connect with just the URL), MCP Apps (open Bunker sites inside Claude/Copilot), `/llms.txt`.

### Security
- Opaque-origin sandboxed iframes, signed content tickets, strict CSP, `Connection-Allowlist`, no popups, no keys in pages, secret scanning on upload, executable blocking, zip-bomb and parser limits, CSRF protection, rate limits, audit log with export/webhook/syslog streaming, optional separate content origin (`BUNKER_CONTENT_URL`). Adversarial audit: 19 findings fixed (see `research/SECURITY-AUDIT.md`).

### Operate
- One-click deploys: Render, Railway (template with a volume at `/data`), Fly.io, Azure VM (with HTTPS), Koyeb (builds from the repo), Coolify, CapRover, Docker/Compose (+ Caddy; builds from GitHub when no image is published), Linux installer with hardened systemd (builds from source when no release exists), Windows PowerShell installer, `go install github.com/kucukkanat/bunker/cmd/bunker@latest`.
- Automatic HTTPS (Let's Encrypt) on bare machines; learns its URL; desktop first run opens the setup page.
- Admin: people, sites, sign-in, AI, Microsoft 365, connections, sharing policy, email, audit log, stats; offboarding (move someone's sites), idle archival, backups (download, scheduled snapshots, `bunker backup`/`restore`), Prometheus `/metrics`.
- Footprint: ~27 MB idle RSS, content-addressed precompressed (brotli/gzip) files; on 4 vCPU the shell page serves ~38k req/s and site files 22–41k req/s; 5,000 live update streams fan out 1,000 changes with p99 ~0.3 s; filtered/sorted `bunker.db` queries get adaptive indexes (10k docs: ~70 → ~4,000 req/s) (see `research/PERF.md`, reproducible with `go run ./test/perf`).
- Optional email (SMTP) for invites, resets and notifications; installable dashboard (web app manifest).
- Accessibility: WCAG 2.2 AA checks (axe, 202 audits) with zero violations.
