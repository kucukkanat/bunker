# Changelog

## v0.1.0 (unreleased): first release

Your own private app cloud: drop a file or describe an app, get a private, shareable, sandboxed web app. One Go binary, one data folder.

### Make apps
- **Drop anything:** HTML, folders, zips, SPA build output (root-absolute paths rewritten), Markdown docs sites and slide decks, CSV/TSV/Excel table apps, JSON viewer, image galleries, PDFs (pdf.js), audio/video, Jupyter notebooks, text, React/JSX/TSX/TypeScript (esbuild at publish, preact/compat), `<script type="text/babel">` pages, unbuilt Vite projects.
- **CDN links just work:** popular libraries (React, Vue, Chart.js, Tailwind, Alpine, d3, Bootstrap, Leaflet, …) are built in at `/_/lib` and CDN tags are rewritten at publish; unknown external URLs are reported as warnings.
- **AI builder:** Claude, OpenAI, Azure OpenAI or any OpenAI-compatible/local model; edits drafts with diffs, streams progress, sets data rules, offers to fix preview errors (max 2 rounds), daily budgets. Zero setup with `ANTHROPIC_API_KEY`/`OPENAI_API_KEY`, one-click local Ollama/LM Studio.
- **8 templates:** requests & approvals, kanban tracker, sign-up sheet, feedback with AI summary, poll, dashboard, team directory & who's out, meeting notes.
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
- One-click deploys: Render, Railway, Fly.io, Azure VM (with HTTPS), Koyeb, Coolify, CapRover, Docker/Compose (+ Caddy), Linux installer with hardened systemd (builds from source when no release exists), Windows PowerShell installer.
- Automatic HTTPS (Let's Encrypt) on bare machines; learns its URL; desktop first run opens the setup page.
- Admin: people, sites, sign-in, AI, Microsoft 365, connections, sharing policy, email, audit log, stats; offboarding (move someone's sites), idle archival, backups (download, scheduled snapshots, `bunker backup`/`restore`), Prometheus `/metrics`.
- Footprint: ~26 MB idle RSS, content-addressed precompressed (brotli/gzip) files, ~30k req/s on 4 cores.
- Accessibility: WCAG 2.2 AA checks (axe) with no serious/critical violations.
