# Bunker — Plan

Internal, self-hosted app platform. Like eze.page, but built for non-technical people: describe an app in plain words, get a private, shareable, secure site.

## How Bunker beats eze
| | eze | Bunker |
|---|---|---|
| Creating a site | Write code / use a coding agent + CLI | **AI chat builder in the browser**, drag-and-drop upload, MCP for power users |
| Permissions | Every signed-in user can edit every site | **Per-site sharing** (owner / editor / viewer, users + Entra groups), private by default |
| Mistakes | — | **Version history**, one-click rollback (every AI edit = a version) |
| Oversight | — | **Admin audit log** |
| Isolation | Subdomain per site | **Sandboxed iframe + strict CSP**, no wildcard DNS needed |
| Company data | — | **Microsoft 365 (Graph), delegated**: a page only sees what the viewer can already see |
| AI | Fixed providers | **Pluggable** (Claude, OpenAI, Azure OpenAI, Ollama), admin-picked, keys server-side |

## Decisions
- **Stack:** Go single binary + SQLite (WAL, Litestream backups), embedded web UI. Docker image.
- **Scale target:** 500–5,000 users on one VM.
- **Identity:** built-in accounts (email + password, invites) out of the box for personal use and small teams; company SSO (Microsoft Entra ID single-tenant, Google Workspace, any OIDC) is optional and admin-enabled. Multi-tenant Entra and public email domains are refused.
- **External sharing:** never.
- **URLs:** `bunker.corp.com/s/<site>`, one hostname, one TLS cert.

## Security model
1. **Sandboxed sites.** User HTML is served from `/s/<site>/raw/*` and is only rendered inside `<iframe sandbox="allow-scripts allow-forms allow-popups">`, **without** `allow-same-origin`. That gives it an opaque origin: no cookies, no access to the Bunker app, no access to other sites.
2. **CSP on raw content:** `default-src 'none'; script-src 'self' 'unsafe-inline' <sdk>; connect-src 'none'; img-src 'self' data: blob:; frame-ancestors 'self'`. Pages can't call outside URLs, so there's no data exfiltration path.
3. **SDK through postMessage.** `bunker.js` in the page talks to the parent frame. The parent checks the site ID, the viewer's role, and per-site quotas, then calls the API with the viewer's session. Pages never hold tokens or keys.
4. **Authorization on every call:** (site, viewer, role). Editors write, viewers read. DB rules default to "viewers can read and create, editing only your own rows" (configurable per collection).
5. **M365 delegated access:** the viewer approves a consent prompt, scopes are on an admin allowlist (e.g. `Sites.Read.All`, `Files.Read`), tokens are kept server-side and encrypted, and every Graph call goes into the audit log.
6. **AI:** rate limits per user and per site, a prompt/response size cap, and audit entries (metadata by default, full content optional).
7. **Audit log** covering create, deploy, share, delete, view, Graph and AI calls. Exportable as CSV or JSON, or as a syslog stream.
8. **Hardening:** upload type/size limits, a zip-slip guard, CSRF on the app, secure cookies, and secret-scanning of uploads (blocks pasted API keys).
9. **Server functions** (`api/<name>.js|ts`): untrusted code on the server, so it gets its own wall. Every call runs in a fresh QuickJS instance compiled to WASI, inside wazero (pure Go, no CGO, no containers), with a memory cap (64 MB), a wall-clock limit (30 s calls, 120 s triggers), input/output/log caps and a host-call budget. Its only way out is a JSON channel to Bunker that allows page-safe ops of that one app (as the caller, or the owner when `elevated`), its own secrets, and `fetch` under the admin's outbound policy (`internet` with the SSRF guard by default, `allowlist`, or `off`). Secrets are per app, write-only, AES-GCM at rest. Admins can turn functions off globally, per person (every app they own, with a reason the person sees) or per app. Deploys of function sets, secret changes, blocks and refused outbound calls are audited.

## SDK (`window.bunker`)
- `bunker.me()`: name, email, groups
- `bunker.db.collection(name)`: get, list, query, set, update, delete, and `subscribe` for live updates over SSE/WebSocket
- `bunker.files`: upload, list, url, delete
- `bunker.ai.chat(messages)`: goes through the admin-configured provider
- `bunker.m365`: `lists.get`, `excel.range`, `files.list` (delegated)
- `bunker.storage`: a localStorage replacement, because opaque-origin iframes have no storage
- `bunker.fn(name, input)`: call one of the app's server functions

Inside server functions the global `bunker` has the same shapes (db, storage, files, ai chat/json, m365, connections, rooms send, notify, me), plus server-only `bunker.secrets.get(NAME)`, `fetch` (outbound policy), `console` → run logs and `setTimeout`/`setInterval`. Triggers: `config.schedule` (cron, UTC, as owner), `config.onWrite` (after commits, depth 1) and webhooks that target a function. Schedules and triggers run the live version; `?draft=1` calls run the draft.

## Product surfaces
- **Home:** my sites, shared with me, templates
- **Builder:** chat on the left, live preview on the right; "Publish" creates a version; edit by asking or in the code tab
- **Upload:** drop a folder, zip, or single HTML file
- **Share dialog:** like Google Docs, with people/group picker from Entra
- **Versions:** list with diffs and restore
- **Admin:** AI provider config, M365 scope allowlist, quotas, audit log, all sites
- **CLI + MCP:** `bunker deploy`, `bunker mcp` (same API, personal access tokens tied to Entra user)

## Delivery (all of it is v1, shipped in milestones)
1. **Core:** Entra login, sites, upload, sandbox + CSP serving, sharing, versions, audit log
2. **Data:** db + live updates, files, storage SDK
3. **AI builder:** pluggable providers, chat builder, templates, `bunker.ai`
4. **Power users:** CLI, MCP, personal tokens
5. **M365:** delegated Graph, consent flow, admin scope allowlist

## Open risks
- All features in v1 is a big scope. Milestone 1 must still be usable on its own.
- The opaque-origin sandbox breaks some libraries that expect cookies or localStorage. Mitigation: the storage shim, and telling the AI builder about the limits.
- Delegated Graph scopes like `Sites.Read.All` need Entra admin consent, so involve IT early.
