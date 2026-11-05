# Bunker engineering contract (read before writing code)

Bunker = one Go binary (`cmd/bunker`) + SQLite + content-addressed blob store. Module = Go package under `internal/<name>` that calls `core.Register(core.Module{Name, Order, Migrations, Init})` from `init()` and is wired with a blank import in `cmd/bunker/modules.go` (add ONE line; conflicts there are trivial).

## Golden rules
1. **Every UI action is an `core.Op`** (`a.Handle(core.Op{...})`) with `Name` (dotted), `Method`, `Path` (Go 1.22 mux pattern), `Summary`, `Params`. The CLI, local MCP, remote MCP and agents are generated from `GET /api/v1/ops`. No hidden UI-only endpoints (use `Hidden: true` only for protocol plumbing).
2. Ops reachable by **site pages** through the SDK bridge must set `PageSafe: true`. The shell adds `X-Bunker-Page: 1` to bridged requests; non-PageSafe ops reject them. Page-safe = data/files/kv/ai/m365/rooms/events/me/connections. Never deploy/share/settings.
3. Site-scoped ops live under `/api/v1/sites/{site}/...` and authorize with `sites.Full(ctx, user, r.PathValue("site"), core.RoleViewer|RoleEditor|RoleOwner)` (or `a.SiteFor(r, role)`), which returns 404 for sites the caller can't see.
4. Errors: return `core.BadRequest/Forbidden/NotFound/Quota/...` (JSON `{"error":{"code","message"}}`). Messages are plain-English, written for non-technical people, and say what to do next.
5. Audit every security-relevant action: `a.Audit(ctx, "event.name", siteID, target, meta)`.
6. Performance is the crown jewel: no per-request goroutine leaks, bounded caches, prepared/simple SQL with indexes, stream large bodies, avoid reflection-heavy libs. Minimal dependencies (pure Go, CGO_ENABLED=0). Leave a tiny footprint: everything in `BUNKER_DATA_DIR` (default `/data` or `./bunker-data`).
7. Cookie-authenticated unsafe requests need header `X-Bunker: 1` (CSRF). The dashboard's fetch wrapper always sends it. Bearer tokens (`Authorization: Bearer bk_...`) don't.
8. Settings: `a.Setting(key)` / `a.SetSetting(ctx,key,val)`; secrets: `a.SecretSetting` / `a.SetSecretSetting` (AES-GCM). Env `BUNKER_<KEY_UPPER>` (dots→underscores) overrides.
9. Tests: `go test ./internal/<pkg>/` with an in-process app. Helper pattern: create `core.App` via `server.New(cfg, log)` with `Cfg.Dev=true` and a temp `DataDir`, then call `server.Handler(a)` with httptest. Dev mode signs every request in as admin `you@localhost` unless a session cookie exists; `/auth/dev?as=email&name=..&groups=A,B` creates a session for another user (dev only).

## Sign-in (internal/auth)
Company SSO is **optional**. Built-in accounts (email + password, argon2id) work out of the box: the setup wizard creates the first admin (`POST /api/v1/setup {provider:"local", email, name, password}`; no setup code from localhost), admins invite people (`POST /api/v1/admin/invites` → one-time link), reset links (`POST /api/v1/admin/users/{id}/reset-password`). SSO (Entra/Google/OIDC) can be enabled later; then `PUT /api/v1/admin/auth/local {enabled:false}` turns built-in accounts off. `GET /api/v1/auth/methods` → {local, localReady, sso, ssoProvider}. Server-rendered sign-in page at `/auth/login`. Microsoft 365 features (`auth.AccessToken`) only work with Entra.

## Core API (internal/core)
- `core.UserFrom(ctx) *core.User` {ID, Email, Name, IsAdmin, Groups[]{ID,Name}, ViaToken, Scopes}; `u.InGroup(nameOrID)`.
- Roles: `core.RoleNone < RoleViewer < RoleEditor < RoleOwner < RoleAdmin`.
- `a.Bus.Publish(topic, []byte)` / `a.Bus.Subscribe(topic) (<-chan []byte, cancel)`. Topic convention: `site:<id>:<channel>` e.g. `site:12:db`, `site:12:room:<name>`, `site:12:deploy`, `site:12:draft`.
- `a.Blobs.PutBytes/Put/Open/Read/Exists/Gzip` (sha256 content addressing, dedupe).
- `a.On(event, fn)` / `a.Emit(ctx, event, payload)` hooks: `site.deleted` (siteID int64), `site.purge` (siteID), `site.published` (map siteId/slug/version), `site.shared`, `site.viewed`, `blobs.gc` (map[string]bool of used hashes — add yours), `shutdown`.
- `core.Encrypt/Decrypt`, `core.MAC`, `core.RandomToken`, `core.HashToken`.

## Sites (internal/sites) — exported helpers
- `sites.Full(ctx,u,slug,minRole) (*SiteRecord, core.Role, error)`, `GetBySlug`, `GetByID`, `RoleOf`, `CreateSite(ctx,u,title,slug,desc,icon)`.
- Files: `sites.WorkingManifest(ctx,s)` (draft or live copy), `sites.SaveDraft(ctx,u,s,manifest)`, `sites.Commit(ctx,u,s,manifest,msg,source,publish)`, `sites.StoreFiles(map[path][]byte) (Manifest,error)`, `sites.Normalize`, `sites.ScanSecrets`, `sites.CleanPath`, `sites.ContentType`, `sites.LoadManifest(ctx,siteID,n)` (n=0 → draft).
- Deploy-time transformers: `sites.RegisterTransformer(sites.Transformer{Name, Priority, Apply(ctx, files map[string][]byte) (kind string, err error)})` — mutate `files` in place (e.g. add index.html for a .md upload). Return a kind like "markdown", "csv", "react".
- Ticketed raw content: `sites.MakeTicket(siteID, ver, userID)`, `sites.ParseTicket`, `sites.RegisterRawPrefix("~f/", handler)` → serves `/s/<slug>/~f/<ticket>/<rest>` with the content CSP already set.
- Existing ops: sites.list/create/get/update/delete/restore/transfer/star, sharing.list/add/remove, sites.deploy (multipart|zip|json|raw), sites.drop (create+deploy), blobs.missing/blobs.put/sites.commit (incremental), versions.list/restore/diff, files.list/read/write/delete (draft), drafts.publish/discard, tokens.*, auth.device.*, me.get, users.list/update, directory.search, setup.status/configure.
- Page URL: `/s/<slug>/` → shell page (`internal/sites/serve.go` shellTmpl) that embeds `<iframe sandbox="allow-scripts allow-forms allow-popups allow-popups-to-escape-sandbox allow-downloads allow-modals" src="/s/<slug>/~r/<ticket>/<path>">` and loads `/_/shell.js`. `?draft=1` previews the draft (editors), `?v=N` a version. The shell div `#bk-shell` has data-site, data-site-id, data-role, data-version, data-live, data-draft, data-badge, data-title, data-icon, data-user.
- Every served HTML file gets `<script src="/_/bunker.js"></script>` injected first in `<head>`.

## SDK bridge protocol (internal/web/static/bunker.js ↔ shell.js)
Page → parent: `{bk:1, id, type:'api', method, path, body, headers?}` where `path` is relative to `/api/v1/sites/<slug>` (e.g. `/db/orders`) or exactly `/me`. Body may contain Blob/File (shell builds multipart when `body.__multipart`).
Parent → page: `{bk:1, id, ok:true, status, data}` or `{bk:1, id, ok:false, status, error:{code,message}}`.
Events: page sends `{bk:1, type:'subscribe', channel:'db:<coll>'|'room:<name>'|'deploy'|'presence:<room>'}`; shell keeps ONE EventSource to `GET /api/v1/sites/<slug>/events?ch=a,b` and forwards `{bk:1, type:'event', channel, data}`.
Other page→shell messages: `{type:'navigate', path}` (sync URL bar), `{type:'title', title}`, `{type:'toast', text}`, `{type:'consent', scopes}` (M365 incremental consent).
The shell validates `event.source === iframe.contentWindow`, only forwards to the site's own API prefix, always sends `X-Bunker: 1` and `X-Bunker-Page: 1`.

## API surface owned by each workstream (paths are the contract)
**data** (`internal/data`, SDK `bunker.db`, `bunker.storage`, `bunker.room`):
- `GET /api/v1/sites/{site}/db` collections+counts+rules · `GET|POST /db/{coll}` list(query: where JSON, order "-field", limit, after)/add · `GET|PUT|PATCH|DELETE /db/{coll}/{id}` · `PUT /db/{coll}/_rule` {rule} (editor) · `DELETE /db/{coll}` drop (editor) · `GET /db/{coll}/_export?format=csv|json` · `POST /db/{coll}/_import`.
- Rules: own (default), private, read, append, open, `group:<name>`; editors bypass. Docs get `_id,_author,_authorName,_created,_updated`.
- `GET /api/v1/sites/{site}/events?ch=` SSE (db changes, rooms, presence, deploy/draft notices; heartbeat 25s).
- `POST /api/v1/sites/{site}/rooms/{room}` {data} ephemeral broadcast; presence via `POST /rooms/{room}/presence` {state}.
- `GET|PUT|DELETE /api/v1/sites/{site}/kv/{key}` per-viewer storage (64 KB/value).
**files** (`internal/files`, SDK `bunker.files`): `GET|POST /api/v1/sites/{site}/uploads` (multipart, folder param), `DELETE /uploads/{id}`, content via `/s/<slug>/~f/<ticket>/<id>/<name>`; rules like db.
**ai** (`internal/ai`, SDK `bunker.ai`): admin `GET|PUT /api/v1/admin/ai` (provider anthropic|openai|azure-openai|openai-compatible|none, key, endpoint, models, quotas); `POST /api/v1/sites/{site}/ai/chat` {messages, system, json, schema, stream} (PageSafe, SSE when stream); builder `POST /api/v1/sites/{site}/builder` {message} SSE events (status|text|file|done|error) editing the DRAFT via tools; `GET /api/v1/sites/{site}/builder/history`; `POST /api/v1/ai/create` {prompt} → creates a site and starts building (returns slug). `GET /api/v1/ai/status` (is AI configured).
**m365 / connections** (`internal/m365`, `internal/connections`): `GET /api/v1/sites/{site}/m365/graph?path=` (allowlisted, delegated), helpers `/m365/lists/items`, `/m365/excel/range`, `/m365/files`; admin `GET|PUT /api/v1/admin/m365`. Connections: admin CRUD `/api/v1/admin/connections`, page call `POST /api/v1/sites/{site}/connections/{name}/fetch` {method, path, query, body}.
**cli/mcp** (`internal/cli`, `internal/mcp`): CLI client in the same binary; remote MCP endpoint `POST /mcp` (+ OAuth metadata/DCR/authorize/token), `/llms.txt`.

## Brand / UI
Colors: forest #1F3A2E, forest-700 #2F5543, moss #4F7A63, paper #F7F4EC, sand #E9E3D3, ink #1C211E, slate #5A635D, clay #C2683F (accent, sparingly). Dark: bg #121714, surface #1A211D, text #E8EBE6, accent #8FBF9F. Headings serif (Fraunces → fallback Georgia), body system-ui/Inter. Voice: plain words, calm, short. App pages CSP: `script-src 'self'` (no inline scripts!), `style-src 'self' 'unsafe-inline'`, `connect-src 'self'`.

## Commit messages
End with:
```
Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01LZES8o2Lem8J5XywqE5Rcv
```
