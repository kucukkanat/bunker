# Changelog

## v0.1.0 (unreleased): first release

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
- One-click deploys: Render, Railway (4-click guide until the template is published), Fly.io, Azure VM (with HTTPS), Koyeb (builds from the repo), Coolify, CapRover, Docker/Compose (+ Caddy; builds from GitHub when no image is published), Linux installer with hardened systemd (builds from source when no release exists), Windows PowerShell installer, `go install github.com/kucukkanat/bunker/cmd/bunker@latest`.
- Automatic HTTPS (Let's Encrypt) on bare machines; learns its URL; desktop first run opens the setup page.
- Admin: people, sites, sign-in, AI, Microsoft 365, connections, sharing policy, email, audit log, stats; offboarding (move someone's sites), idle archival, backups (download, scheduled snapshots, `bunker backup`/`restore`), Prometheus `/metrics`.
- Footprint: ~27 MB idle RSS, content-addressed precompressed (brotli/gzip) files; on 4 vCPU the shell page serves ~38k req/s and site files 22–41k req/s; 5,000 live update streams fan out 1,000 changes with p99 ~0.3 s; filtered/sorted `bunker.db` queries get adaptive indexes (10k docs: ~70 → ~4,000 req/s) (see `research/PERF.md`, reproducible with `go run ./test/perf`).
- Optional email (SMTP) for invites, resets and notifications; installable dashboard (web app manifest).
- Accessibility: WCAG 2.2 AA checks (axe, 202 audits) with zero violations.
