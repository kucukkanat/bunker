# Bunker research: product, security, deploy, identity, MCP, performance

Researched 2026-11-05. Facts checked against primary sources where it mattered (MCP spec, Microsoft Learn, Chrome docs). Anything less certain is marked **(verify)**. Links to sources are given in each section.

---

## Executive summary: top 25 recommendations (in priority order)

| # | What | Why | Effort |
|---|------|-----|--------|
| 1 | **Authorize raw site content with a capability token in the path, not with cookies.** Serve user content at `/_r/<token>/<site>/<version>/<path>`. The token is an HMAC over (site, version, viewer, exp ≤ 12h) and the parent mints it when it builds the iframe `src`. | Requests from inside an opaque-origin sandboxed iframe are cross-site. They **do not carry SameSite=Lax/Strict cookies**, and SameSite=None is dropped by third-party-cookie blocking. Cookie auth on `/s/<site>/raw/*` subresources will 401 randomly depending on the browser. | S |
| 2 | **Send `Access-Control-Allow-Origin: *` (and no credentials) on raw-content responses.** | `<script type="module">`, `@font-face`, `fetch` of own JSON and `crossorigin` assets all use CORS, and from a sandboxed frame they carry `Origin: null`. Vite/React builds will fail without this. It is safe because auth sits in the path token (#1). | S |
| 3 | **Add a `Connection-Allowlist` header to raw HTML.** Use `Connection-Allowlist: (response-origin)` (`webrtc` and `redirects` stay at their default `block`). | CSP `connect-src 'none'` **does not stop WebRTC/STUN exfiltration**, and the CSP `webrtc 'block'` directive is ignored by Chromium and WebKit (measured 2026). Connection-Allowlist shipped in **Chrome/Edge 152** and blocks WebRTC, navigations, prefetch and redirects. Most Entra shops run Edge, so this covers most viewers. | S |
| 4 | **Drop `allow-popups` from the default sandbox, set `form-action 'none'` and `base-uri 'none'`, and route external links through `bunker.open(url)`.** The parent shows "Leave Bunker for example.com?". | Popups and form posts are the easiest exfiltration path that CSP `connect-src` doesn't cover. `navigate-to` was removed from CSP. | S |
| 5 | **Parent-side postMessage hardening.** Accept a message only when `e.source === iframe.contentWindow` (the origin is the literal string `"null"`, so it can't be used as a check). Bind each iframe to a per-mount `MessageChannel` port, so the site ID comes from the port and never from the payload. Inside the SDK, check `e.origin === BUNKER_ORIGIN`. | An origin check on `"null"` would accept any sandboxed frame, including one that a malicious page nested inside itself. | S |
| 6 | **Optional "usercontent" hostname** (`BUNKER_CONTENT_ORIGIN=https://bunkerusercontent.corp.com`), used when the admin has one. Single-host mode stays the default. | This is the model Google (`googleusercontent.com`), Claude (`*.claudeusercontent.com`) and Figma use. A separate site gives isolation that doesn't depend on the sandbox attribute, process isolation, and no shared cookie jar. | M |
| 7 | **Remote MCP endpoint `/mcp` that serves both protocol eras.** Implement 2026-07-28 (stateless, `MCP-Protocol-Version`, `Mcp-Method`, `Mcp-Name`, `server/discover`) and also answer legacy `initialize` (2025-11-25 / 2025-06-18 / 2025-03-26). | Claude.ai, Claude Code, ChatGPT and Cursor are spread across both eras today. Dual-era support is explicitly allowed by the spec. | M |
| 8 | **Bunker acts as its own OAuth 2.1 authorization server for MCP**, with login delegated to Entra. Expose RFC 9728 PRM, RFC 8414 AS metadata, `client_id_metadata_document_supported: true` (CIMD), DCR `/oauth/register` as a fallback, PKCE S256 only, RFC 8707 `resource`, and RFC 9207 `iss`. | This gives zero-config connection from Claude.ai, Claude Code and ChatGPT, which all identify themselves via CIMD. ChatGPT falls back to DCR when CIMD fails. Entra itself can't act as the MCP AS: no DCR/CIMD, and its audience rules don't fit. | M |
| 9 | **Expose Bunker sites as MCP Apps** (`_meta.ui.resourceUri: "ui://bunker/<site>"`, mime `text/html;profile=mcp-app`). | Hosts that render MCP Apps include Claude, VS Code Copilot and **Microsoft 365 Copilot**. "Open my expense tracker inside Copilot/Claude" is a strong differentiator, and it reuses the same sandbox + postMessage design. | M |
| 10 | **One-command Entra setup: `bunker setup entra` (device code).** It signs in with the Microsoft Graph Command Line Tools public client `14d82eec-204b-4c2f-b7e8-296a70dab67e`, requests `Application.ReadWrite.All DelegatedPermissionGrant.ReadWrite.All`, then calls `POST /applications` → `addPassword` → `POST /servicePrincipals` → `POST /oauth2PermissionGrants` (AllPrincipals). Also ship a paste-into-Azure-Cloud-Shell `az` script and a manual fallback with an admin-consent URL. | The admin needs one role (Cloud Application Administrator is enough for delegated Graph scopes) and the setup takes 60 seconds. App registration is the #1 friction point for self-hosted Entra apps. | M |
| 11 | **Don't depend on the `groups` claim.** Configure `groupMembershipClaims: "SecurityGroup"`. When there's an overage (`_claim_names.groups` or `hasgroups` present), call `GET /me/transitiveMemberOf/microsoft.graph.group?$select=id,displayName` (**`User.Read` is enough** for the signed-in user). Cache the result per session and refresh it every ≤ 1h. | Entra drops `groups` above 200 (JWT). Share-by-group must work for large companies. | S |
| 12 | **People/group picker via `User.ReadBasic.All` + `GroupMember.Read.All`** (delegated, consented once by the admin during setup #10). Use `$search` with `ConsistencyLevel: eventual`. | Google-Docs-style sharing needs typeahead over the directory. `User.ReadBasic.All` is user-consentable. Group search needs admin consent, so bundle it into setup. | S |
| 13 | **Deploy buttons with real persistent disks:** Render (`render.yaml` + `disk:`), Railway (template with volume), DigitalOcean **Droplet** 1-click / cloud-init (App Platform has no volumes), Fly.io (`fly.toml [mounts]`), **Deploy to Azure → Ubuntu VM + managed disk via ARM/Bicep + cloud-init** (recommended for Microsoft shops), plus Coolify/CapRover templates and `docker compose`. | SQLite needs a block-device volume. Heroku, Cloud Run, DO App Platform and Koyeb free tiers can't keep it safely. **Never put SQLite on Azure Files SMB** ("database is locked", no POSIX locks). | M |
| 14 | **Azure Container Apps / App Service path = local disk + Litestream → Azure Blob, single replica.** On start: `litestream restore -if-replica-exists`, then run. | This is the only safe SQLite pattern on PaaS without block volumes. Litestream 0.5.x supports Azure Blob with SAS and managed identity. | M |
| 15 | **Split the SQLite pools:** one writer `*sql.DB` (`MaxOpenConns=1`, `_txlock=immediate`) and one reader pool (`MaxOpenConns=4–8`, `mode=ro` / `query_only`). Today `internal/core/db.go` uses a single pool of 8 with `_txlock=immediate`, so every read transaction takes the write lock. | This removes `SQLITE_BUSY` storms and lets reads scale with cores. It's the standard pattern for Go + SQLite servers. | S |
| 16 | **Content-addressed blob store + publish-time precompression.** Store sha256 → blob, precompute `.br` (q=11) and `.gz` once at upload, set `ETag` = hash, and serve with `Cache-Control: public, max-age=31536000, immutable` (the version is in the URL). HTML gets `no-cache` + ETag. | Versions dedupe for free (an AI edit changes one file). Per-request serving costs nothing in CPU. | S |
| 17 | **One multiplexed SSE stream per browser tab, owned by the parent frame**, not per iframe or per subscription. Flush via `http.ResponseController`, send a heartbeat `:\n\n` every 20s, set `X-Accel-Buffering: no`, and use an in-process pub/sub keyed by (site, collection). | HTTP/1.1 allows only 6 connections per host, so several tabs with an SSE each starve the app. Behind HTTP/2 this matters less, but one stream is still cheaper. | S |
| 18 | **Self-host a curated vendor library set at `/_vendor/`** (React, Preact, Vue petite, Alpine, Chart.js, Tailwind (prebuilt + play runtime), SheetJS, pdf.js, Mermaid, Leaflet + offline tiles note, marked, Pyodide on request). The AI builder is told to use only these. | `script-src 'self'` blocks CDNs. 90% of LLM-generated pages reference cdn.tailwindcss.com / unpkg / jsdelivr. Also auto-rewrite those known CDN URLs to `/_vendor/` at publish. | M |
| 19 | **Transpile JSX/TSX at publish with esbuild's Go API** (`github.com/evanw/esbuild/pkg/api`, in-process, ~ms). Bare imports resolve to `/_vendor/` via an import map. | This supports "Claude artifact"-style single React files and v0 output with zero extra runtime, in a single binary. | S |
| 20 | **Drop-in format support:** `.html`, folder/zip, SPA build (auto-detect `index.html` + rewrite root-absolute `/assets/` → relative), `.md` (goldmark → themed page), `.csv`/`.xlsx` (instant table app backed by `bunker.db`), `.ipynb` (render to static HTML server-side), `.pdf` (pdf.js viewer: **Chrome's built-in PDF viewer refuses to render in sandboxed iframes** (verify per browser)), slides (`.md` → reveal.js). | "Drop anything, get a shareable page" is the Tiiny.host / Netlify Drop magic that non-technical users love. | M |
| 21 | **The AI builder should edit, not regenerate:** search/replace diff edits, every edit is a version, "undo" = rollback, an auto-fix loop capped at 2 attempts using console errors captured from the iframe via the SDK, and per-user daily budgets instead of opaque credits. | Lovable/Bolt users' #1 complaint is credit burn in "debugging loops of death". | M |
| 22 | **Built-in `connect` for internal APIs via a server-side proxy allowlist** (`bunker.fetch("hr-api", "/people")`). The admin defines named upstreams. Never hand the page arbitrary egress. | This is what Retool/Power Apps users actually need ("call our API"). Keep it explicit and audited. | M |
| 23 | **Quota and abuse controls from day 1:** per-site storage cap, per-user site count, rate limits, an "owner left the company" transfer flow (when an Entra user is disabled, sites move to the manager or a group), and site archival after N days without views. | Glitch shut down hosting (July 2025) because of abuse plus the cost of millions of zombie apps. Internal platforms get orphaned-app sprawl, which is the Power Apps governance pain. | S |
| 24 | **Everything is an op: one registry drives REST, CLI, MCP tools and the UI.** Publish an OpenAPI doc, plus `bunker init` writing `AGENTS.md`/`CLAUDE.md` with SDK docs, and `/llms.txt` on the server. | This is what Val Town (Townie → MCP → CLI) and eze (`eze init`, `eze mcp`) got right. Agents are the main way power users will build. | S |
| 25 | **Installer & ops:** `curl -fsSL https://get.bunker.sh \| sh` (detects OS/arch, verifies a SHA256 + cosign/minisign signature, installs a systemd unit with hardening and the `bunker` user, and wraps the whole script in a `main()` function so a partial download does nothing), a Homebrew tap, a multi-arch distroless image, and `bunker backup`/`restore` (VACUUM INTO + blobs tar). | Minimal footprint and trust. A partial-download-safe installer is table stakes. | S |

---

## 1. Competitors and inspiration

### eze (eze.page)
This is Bunker's direct reference ([eze.page](https://eze.page)). It's MIT-licensed and self-hosted: a folder of HTML/CSS/JS becomes a private site, each at its own subdomain (`lunch-vote.eze.acme.com`). It has an SDK with `eze.db` (live subscriptions), `eze.files`, `eze.ws` (realtime rooms: cursors, chat, games), `eze.ai` and `eze.id`. Other features:
- `eze deploy` CLI, drag-and-drop folder.
- `eze init` writes agent instructions; `eze mcp` lets agents deploy.
- Sign-in via Google Workspace, Okta, Entra, OIDC or an auth proxy.
- **Refuses to start if public email domains could sign in.**

Weaknesses Bunker should exploit:
- Everyone can edit everything.
- Needs wildcard DNS and a wildcard TLS cert.
- No versions, no audit, no M365.

**Copy:**
- `eze.ws`-style realtime rooms. Bunker's plan has none. Add `bunker.room(name)`, broadcast + presence over the same SSE stream plus a POST endpoint, if cheap.
- `init`, which writes agent docs.
- The refuse-unsafe-config stance.

### Val Town
- What's good: instant deploy, every val has a URL, browser editor, Townie (AI on Claude + Val Town's own MCP server).
- Their own recommendation order is **"Townie, then MCP, then CLI"**, and they actively encourage "bring Val Town to whatever coding tool you prefer".
- Townie added a model picker and **a diff view to approve edits**. Copy the diff-approve UI for the builder's "code tab".
- Sources: [Val Town reviews](https://www.producthunt.com/products/val-town/reviews), [investor update Feb 2026](https://longform.social/post/did:plc:a2rdzfdxkjwerrfrpbwcipb2/3mh7o45ms2222).

### Glitch (shutdown lessons)
- Glitch ended project hosting and profiles on **July 8, 2025**, citing cost and abuse of millions of apps. Users got an export window and subdomain redirects.
- Lessons for Bunker:
  - (a) The **export of everything** must be one click, both per site and for the whole instance (zip + JSON of the db).
  - (b) Abuse and zombie apps are the cost center: add quotas, idle archival and view counts.
  - (c) Stable URLs matter. Support **redirects/aliases** when a site is renamed.
  - (d) The "remix" button was Glitch's most loved feature. Add **"Duplicate/Remix" with permission checks**, plus a template gallery made from remixes.
- Sources: [Glitch blog](https://blog.glitch.com/post/changes-are-coming-to-glitch/), [The Register](https://www.theregister.com/2025/05/23/glitch_app_hosting_gone), [support thread](https://support.glitch.com/t/glitch-is-ending-hosting-what-you-need-to-do/75736).

### Replit deployments / Lovable / Bolt.new / v0
- What's loved: prompt-to-app speed, live preview, one-click publish.
- What's hated (consistently across reviews):
  - credit burn;
  - "debugging loops" where the AI fixes its own errors repeatedly;
  - losing context on bigger projects;
  - failed deployments;
  - backends being flaky.
- Bunker answers:
  - No credits for users: admin-set daily budgets, visible as "you have ~40 edits left today".
  - **Edits as diffs** (search/replace blocks) so the model doesn't rewrite the whole file.
  - **Auto-fix capped at 2 tries**, then show "Here's what's broken, want me to try another approach?"
  - The backend is the fixed SDK, not generated server code, so there's nothing to deploy-fail.
  - Capture runtime errors from the iframe (`window.onerror` → SDK → parent) and give them to the model.
- Sources: [r/nocode thread](https://redlib.hackliberty.org/r/nocode/comments/1j9eac5/what_are_the_main_problems_that_people_are_facing), [Lovable debugging loop](https://mcstarters.com/blog/lovables-debugging-loop-of-death-explained-2026/), [superappp comparison](https://www.superappp.com/blog/boltnew-vs-lovable-main-differences-real-non-technical-developer-insights).

### Claude Artifacts
- Single-file HTML/React rendered in a sandboxed cross-origin iframe on `*.claudeusercontent.com` with signed URLs and strict CSP. `window.claude.complete()` is the deliberate hole, the equivalent of our `bunker.ai`.
- A React artifact is one JSX file with a fixed set of available libraries. **Copy that: a fixed, documented vendor set the AI knows about** (#18, #19).
- Sources: [Simon Willison: how Anthropic built artifacts](https://simonwillison.net/2024/Aug/28/how-anthropic-built-artifacts), [artifact frame report](https://claudeissues.com/issue/87275-claude-code-cannot-read-claude-ai-artifact-links-sandboxed-frame-blocks-text-ext).

### Retool / Appsmith / Budibase / Power Apps
- Loved: connecting to company data, CRUD tables/forms, SSO, audit.
- Hated:
  - per-seat pricing (Retool ~$50–65/builder + $7–18/end-user; Appsmith gates SSO and audit logs behind business tiers);
  - lock-in;
  - "low-code ceiling".
- Power Apps adds licensing confusion (premium connectors), delegation limits on data sources, and governance sprawl (orphaned apps when makers leave).
- Bunker answers:
  - Free/self-hosted, **SSO + audit in the free core**.
  - Plain HTML, so no ceiling and full export.
  - A table/form generator from CSV/Excel/SharePoint list (#20).
  - An ownership transfer flow (#23).
- Sources: [uibakery Retool alternatives](https://uibakery.io/retool-alternatives), [Budibase reviews](https://www.zite.com/blog/budibase-reviews), [Appsmith alternatives](https://checkthat.ai/brands/appsmith/alternatives).

### Tiiny.host / Netlify Drop / Cloudflare Pages
The UX to copy is a single drop zone on the home page that accepts a file, folder or zip and returns a URL in under 3 seconds. Specifically:
- **Show the URL + QR + "Copy link" + share dialog immediately** after the drop.
- Accept PDF and turn it into a viewer page.
- A password/expiry option. For Bunker, "expires on" maps to an auto-archive date.
- Cloudflare Pages' `_redirects`/`_headers` files: support `_redirects` for SPA fallbacks (`/* /index.html 200`).

### PocketBase
- It's the closest architectural sibling: a Go single binary with SQLite and realtime SSE subscriptions, an admin UI, and collection rules as expressions (`@request.auth.id = owner`).
- **Copy its rule expression model** for `bunker.db` collection permissions. Keep a tiny fixed grammar: `owner`, `role`, `group in`, field comparisons. Don't use arbitrary code.
- PocketBase realtime uses one SSE connection per client with a subscribe/unsubscribe POST. That's the same as recommendation #17.

### Coolify / Dokku / CapRover
These are self-host PaaS tools. They matter as **distribution channels**: ship a Coolify service template and a CapRover one-click app (see §3). Users love `git push` deploy (Dokku). Bunker's `bunker deploy` CLI plays the same role.

### Sandstorm.io: security model lessons
([docs](https://docs.sandstorm.io/en/latest/using/security-practices/), [how it works](https://sandstorm.io/how-it-works))
- **Fine-grained objects ("grains") are the unit of sharing.** Access control is enforced by the platform, so apps never manage users. That matches Bunker: a site is the grain, and the SDK enforces roles.
- **Sharing by delegation:** a user with access can re-share up to their own level, without involving an admin. Bunker should allow editors to share as viewer/editor and keep owners for delete/transfer.
- **Per-session random hostnames** defeated XSRF, reflected XSS and clickjacking. But they need wildcard DNS (Sandstorm had to build sandcats.io dynamic DNS for it), which hurt adoption. Bunker's single-host sandbox avoids that. Offer the content-origin option (#6) for those who can.
- **Sandstorm admitted it could not block WebRTC via CSP.** That's still true in 2026 for CSP, and Connection-Allowlist is the fix (#3).
- "Apps never handle credentials" is the same as Bunker's "pages never hold tokens".
- Its "binary access only" limitation (it couldn't enforce read vs write inside apps) is one Bunker beats, because data goes through the SDK with role checks.
- Powerbox (a user-driven capability picker, "choose a file/contact to grant") is a great pattern for `bunker.m365`. Instead of a broad scope, the page asks `bunker.m365.pickFile()`, the **parent** shows a OneDrive/SharePoint picker, and only that item is returned. This is the least-privilege M365 pattern.

---

## 2. Sandboxing untrusted HTML/JS on one origin

### 2.1 What actually happens in `<iframe sandbox="allow-scripts ...">` without `allow-same-origin`
- **Origin:** the document gets an opaque origin. `location.origin === "null"`, and postMessage `event.origin === "null"` in receivers.
- **CSP `'self'`:** CSP3 matches `'self'` against the policy's *self-origin*, which comes from the **response URL**, not the opaque document origin. The spec explicitly handles sandboxed srcdoc docs by using the fallback base URL. So `script-src 'self'` **does** allow `/_r/...` scripts on the Bunker host. ([CSP3](https://www.w3.org/TR/CSP3/)) Firefox historically let `*` match opaque frames while Chromium/Safari didn't ([bug 1891477](https://bugzilla.mozilla.org/show_bug.cgi?id=1891477)). **Never rely on `*`; list `'self'`.**
- **`frame-ancestors`** is checked against all ancestors. An opaque ancestor never matches, so a site cannot nest another Bunker raw page (good) ([civitai issue](https://github.com/civitai/civitai-app-starters/issues/532)).
- **Cookies:**
  - Subresource requests from the opaque-origin document are treated as **cross-site**, so `SameSite=Lax/Strict` cookies are not sent.
  - Because of third-party cookie blocking, `SameSite=None` cookies are also dropped.
  - Chrome has a proposal for a sandbox token `allow-same-site-none-cookies` ([TAG review](https://github.com/w3ctag/design-reviews/issues/1004)). Don't depend on it.
  - The *iframe navigation request itself* is initiated by the Bunker parent document (same-site), so cookies are sent on that request.
  - **Conclusion:** cookie auth works for the top HTML only, and is unreliable for scripts, images, CSS and fonts. Use path tokens (#1).
- **CORS:**
  - Module scripts, fonts, `fetch()` and `crossorigin` images send `Origin: null` and need `Access-Control-Allow-Origin: *` ([example](https://github.com/TheBushidoCollective/artifacts/pull/17), [fonts](https://forum.umbraco.com/t/save-and-preview-has-been-blocked-by-cors-policy/3787)).
  - `connect-src 'none'` blocks `fetch()` anyway. If you want pages to `fetch('./data.json')`, use `connect-src 'self'`. That is safe with path tokens + ACAO, but it lets the page read any raw URL that it knows a token for, and it only ever knows its own.
  - **Recommendation:** `connect-src 'self'` scoped by Connection-Allowlist to its own prefix: `Connection-Allowlist: ("/_r/<token>/<site>/*")`. Or stay with `'none'` and offer `bunker.asset(path)`.
- **No storage:** `localStorage`, `sessionStorage`, IndexedDB, cookies and Service Workers throw `SecurityError` in opaque origins. Ship the `bunker.storage` shim **and inject a synchronous `localStorage` polyfill** backed by an in-memory map preloaded from the parent at boot. Otherwise libraries crash at import time.
- **PDF:** Chrome won't load its PDF viewer inside sandboxed frames **(verify per browser)**. Use pdf.js.

### 2.2 Recommended response headers for raw HTML documents

```
Content-Type: text/html; charset=utf-8
Content-Security-Policy: sandbox allow-scripts allow-forms allow-modals allow-downloads;
  default-src 'none'; script-src 'self' 'unsafe-inline' 'unsafe-eval' 'wasm-unsafe-eval';
  style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:;
  media-src 'self' blob: data:; connect-src 'self'; worker-src blob: 'self';
  frame-src 'self' blob: data:; form-action 'none'; base-uri 'none';
  frame-ancestors <BUNKER_ORIGIN>; object-src 'none'
Connection-Allowlist: (response-origin)
Vary: Sec-Fetch-Dest
Access-Control-Allow-Origin: *
Cross-Origin-Resource-Policy: cross-origin
Referrer-Policy: no-referrer
X-Content-Type-Options: nosniff
Permissions-Policy: camera=(), microphone=(), geolocation=(), usb=(), serial=(), hid=(), bluetooth=(), payment=(), display-capture=(), clipboard-read=()
Cache-Control: no-cache
```

Notes on these headers:
- The **CSP `sandbox` directive** enforces the sandbox even if someone opens `/_r/...` top-level directly. Without it, a direct navigation would run user JS on the Bunker origin. That would be a critical bug, because the path token is in the URL. Always send the `sandbox` directive on every raw response, including non-HTML responses (SVG!).
- `'unsafe-eval'` is needed by Alpine, Vue's runtime compiler and the Tailwind play CDN. It's acceptable because the sandbox is the boundary, not the CSP.
- `'wasm-unsafe-eval'` is needed for Pyodide, SQLite-wasm and DuckDB-wasm.
- **Serve SVG uploaded by users with the same CSP sandbox header**, or as `Content-Disposition: attachment` when it's requested outside the iframe.
- `frame-ancestors <BUNKER_ORIGIN>` prevents other intranet sites from framing Bunker apps.
- Without `allow-popups`: `bunker.open(url)` → the parent shows a confirm → `window.open(url, '_blank', 'noopener,noreferrer')`. If you do allow popups, **never** add `allow-popups-to-escape-sandbox`.
- Never `allow-top-navigation` (only `allow-top-navigation-by-user-activation` if needed for "open full screen", which the parent can do itself).

### 2.3 Exfiltration channels and status (Nov 2026)

| Channel | Blocked by CSP? | Mitigation |
|---|---|---|
| fetch/XHR/WebSocket/EventSource/beacon | Yes (`connect-src`) | — |
| `<img>`/CSS/font to external host | Yes (`img-src` etc.) | — |
| **WebRTC STUN/TURN** (data in ICE ufrag or TURN username) | **No.** `connect-src` doesn't apply, and `webrtc 'block'` is unrecognized in Chromium 153/154 and WebKit 26.6 ([measurement](https://github.com/backnotprop/plannotator/pull/1844), [Sansec skimmer](https://sansec.io/research/webrtc-skimmer), [FF bug](https://bugzilla.mozilla.org/show_bug.cgi?id=1783489)) | **`Connection-Allowlist`** (Chromium ≥152 blocks WebRTC by default) ([Chrome guide](https://developer.chrome.com/docs/modern-web-guidance/guides/security/restrict-outbound-connections), [centralcsp](https://centralcsp.com/en/docs/web-security/policies/connection-allowlist)). Plus defense in depth: an injected first script that deletes `RTCPeerConnection`/`webkitRTCPeerConnection`/`RTCDataChannel`. This can be bypassed via fresh realms (`about:blank` child frames), so treat it only as a speed bump. Plus a static scan flag on publish. Plus Edge/Chrome enterprise policy `WebRtcIPHandling=disable_non_proxied_udp` for managed fleets (verify that it blocks STUN to arbitrary hosts) |
| Self-navigation of the iframe (`location = 'https://evil/?d=…'`) | No (`navigate-to` removed) | Connection-Allowlist covers navigations. Firefox/Safari: residual risk |
| Popups `window.open` | No | Remove `allow-popups` (#4) |
| Form submit | `form-action 'none'` | — |
| DNS prefetch / preconnect `<link rel=dns-prefetch>` | Partially. Chromium doesn't prefetch in docs with CSP? (verify) | Connection-Allowlist covers resource hints |
| `<a ping>` | `connect-src` in modern browsers | Connection-Allowlist |
| `window.opener` / `parent` | Opaque origin → cross-origin access only to `postMessage`, `location` (navigate parent is blocked without `allow-top-navigation`) | The parent rejects messages not from its own iframe port |
| Timing/covert channels between tabs (e.g., BroadcastChannel) | Partitioned by opaque origin | Accept |

**Threat model statement for docs:** the sandbox stops pages from reaching the Bunker session, other sites and arbitrary APIs. A malicious *author* could still try to exfiltrate data their viewers can see. Bunker blocks the common channels on all browsers and also WebRTC/navigation on Chromium/Edge. Remaining Firefox/Safari leaks are mitigated by audit and by the fact that authors are authenticated employees. Admins can enforce Edge via Intune. Optionally, "Strict mode": show sites only to browsers that send `Sec-CH-UA` Chromium ≥ 152.

### 2.4 How others isolate
- **Google:** user content lives on `*.googleusercontent.com`, often with per-content hashed subdomains. The registrable domain is separate from google.com, so there's no shared cookie jar.
- **Claude artifacts:** `*.frame.claudeusercontent.com`, signed URLs, sandbox + strict CSP, process isolation.
- **Figma plugins:** plugin UI is in a `null`-origin iframe (sandboxed, no allow-same-origin). The plugin main logic runs in a QuickJS/Realms sandbox. All API access goes through `postMessage` to the host, the same as Bunker.
- **CodeSandbox / StackBlitz:** a separate preview domain per sandbox (`*.csb.app`, `*.webcontainer.io`). StackBlitz uses service workers on a unique origin.
- **Observable / Jupyter:** Observable runs notebooks in a sandboxed iframe on a separate `*.static.observableusercontent.com`. JupyterLab trusts notebooks per signature and otherwise sanitizes HTML output. JupyterLite runs fully client-side.
- **MCP Apps** (the 2026 standard): host-controlled sandboxed iframe, postMessage JSON-RPC (`ui/initialize`, `tools/call`), and CSP declared in `_meta.ui.csp`. Bunker's SDK is architecturally an MCP Apps host. Consider making `bunker.js` speak a superset of the MCP Apps dialect so sites can run inside Claude/Copilot unchanged (#9).

### 2.5 Parent bridge reference implementation (sketch)

```js
// parent (Bunker app origin)
const ch = new MessageChannel();
iframe.addEventListener('load', () => {
  iframe.contentWindow.postMessage({ type: 'bunker:init', site, viewer, storage }, '*', [ch.port2]);
}, { once: true });
ch.port1.onmessage = async ({ data }) => {
  const { id, op, args } = data;                 // site is implicit: bound to this port
  if (!ALLOWED_OPS.has(op)) return ch.port1.postMessage({ id, error: 'forbidden' });
  const res = await fetch(`/api/sites/${site}/sdk/${op}`, { method: 'POST', body: JSON.stringify(args),
     headers: { 'Content-Type': 'application/json', 'X-CSRF': csrf } });
  ch.port1.postMessage({ id, result: await res.json() });
};
// Note: re-create the channel on every iframe load (navigation inside the frame).
```

```js
// bunker.js (inside sandbox)
addEventListener('message', (e) => {
  if (e.source !== parent || e.data?.type !== 'bunker:init' || !e.ports[0]) return;
  // origin is BUNKER_ORIGIN (string inserted at serve time); verify:
  if (e.origin !== BUNKER_ORIGIN) return;
  port = e.ports[0]; /* ... */
}, { once: true });
```

The server side must re-check (site, viewer, role) on every SDK call. The parent is not a security boundary against a compromised Bunker UI, but it is the only thing holding the session.

---

## 3. One-click deploy options

**Persistent disk support is the deciding factor for SQLite.**

| Target | File / URL | Persistent volume? | Verdict |
|---|---|---|---|
| Render | `render.yaml` at repo root. Button: `https://render.com/deploy?repo=https://github.com/<org>/<repo>` with image `https://render.com/images/deploy-to-render-button.svg` | Yes, `disk:` (paid instances: Starter+). A service with a disk can't scale beyond 1 instance and has no zero-downtime deploys | ✅ Ship |
| Railway | Template created in Template Composer (dashboard). Button: `https://railway.com/deploy/<template-code>` (older: `railway.app/template/<code>`), image `https://railway.com/button.svg`. Volumes are added in the composer (not `railway.json`) | Yes | ✅ Ship |
| Fly.io | `fly.toml` + `fly launch --from https://github.com/<org>/<repo>` (no web button) | Yes, `[mounts]` (one volume per machine) | ✅ Ship (docs) |
| DigitalOcean App Platform | `.do/deploy.template.yaml` (top-level `spec:` required). Button: `https://cloud.digitalocean.com/apps/new?repo=https://github.com/<org>/<repo>/tree/main`, image `https://www.deploytodo.com/do-btn-blue.svg` ([docs](https://docs.digitalocean.com/products/app-platform/how-to/add-deploy-do-button/)) | **No** block volumes | ❌ Use a Droplet + cloud-init / Marketplace 1-click instead |
| Heroku | `app.json` + `https://heroku.com/deploy?template=<repo>` | **No** (ephemeral FS) | ❌ (unless Litestream→S3) |
| Koyeb | `https://app.koyeb.com/deploy?type=docker&image=ghcr.io/<org>/bunker:latest&name=bunker&ports=8080;http;/&env[BUNKER_URL]=...` ([docs](https://www.koyeb.com/docs/build-and-deploy/deploy-to-koyeb-button)) | Volumes exist (verify the button param) | ⚠️ Optional |
| Google Cloud Run | `[![Run on Google Cloud](https://deploy.cloud.run/button.svg)](https://deploy.cloud.run/?git_repo=https://github.com/<org>/<repo>)` + `app.json` (`env`, `options.max-instances`, `hooks`) ([repo](https://github.com/GoogleCloudPlatform/cloud-run-button)) | Only GCS FUSE/NFS, not safe for SQLite | ⚠️ Only with Litestream→GCS + `max-instances: 1` |
| **Azure VM (recommended Azure path)** | `azuredeploy.json` (ARM, compiled from Bicep). Button: `https://portal.azure.com/#create/Microsoft.Template/uri/<urlencoded raw URL of azuredeploy.json>`, image `https://aka.ms/deploytoazurebutton`. Optional `createUiDefinition.json` for a nice form: `.../#create/Microsoft.Template/uri/<tpl>/createUIDefinitionUri/<ui>` | Yes (managed disk) | ✅ Ship. Microsoft shops trust their own subscription |
| Azure Container Apps | Same button style, ARM template | Azure Files only (SMB → SQLite **"database is locked"**, no POSIX byte-range locks ([write-up](https://dev.to/argha_dev/sqlite-on-azure-files-smb-a-debugging-story-with-a-humbling-ending-1p93))). NFS Azure Files needs Premium + VNet, and is still a network FS | ⚠️ Only local disk + Litestream→Blob, `maxReplicas: 1` |
| Azure App Service (Linux container) | ARM | `/home` is SMB-backed, same problem | ⚠️ Litestream pattern only |
| Coolify | Docker Compose service template with magic vars `SERVICE_FQDN_BUNKER_8080`, `SERVICE_URL_BUNKER_8080`, `SERVICE_PASSWORD_64_SECRET` ([docs](https://coolify.io/docs/services/configuration/docker-compose)). Submit to coollabsio/coolify `templates/compose/bunker.yaml` | Named volumes | ✅ |
| CapRover | One-click app YAML (`captainVersion: 4`, `services:` + `caproverOneClickApp: {variables, instructions}`) PR to caprover/one-click-apps | Volumes | ✅ |
| Docker Compose / systemd / Homebrew | see below | local disk | ✅ |

### render.yaml
```yaml
services:
  - type: web
    name: bunker
    runtime: image
    image:
      url: ghcr.io/kucukkanat/bunker:latest
    plan: starter            # disks require a paid instance
    healthCheckPath: /healthz
    disk:
      name: bunker-data
      mountPath: /data
      sizeGB: 5
    envVars:
      - key: BUNKER_DATA_DIR
        value: /data
      - key: BUNKER_SECRET
        generateValue: true  # 256-bit random
      - key: BUNKER_ENTRA_TENANT_ID
        sync: false          # prompted at deploy
      - key: BUNKER_ENTRA_CLIENT_ID
        sync: false
      - key: BUNKER_ENTRA_CLIENT_SECRET
        sync: false
```
(A `runtime: docker` + `dockerfilePath: ./Dockerfile` alternative builds from source.) Render injects `RENDER_EXTERNAL_URL`. Read it as the default `BUNKER_URL`. Source: [Blueprint spec](https://render.com/docs/blueprint-spec).

### fly.toml
```toml
app = "bunker-example"
primary_region = "ams"
[build]
  image = "ghcr.io/kucukkanat/bunker:latest"
[env]
  BUNKER_DATA_DIR = "/data"
[[mounts]]
  source = "bunker_data"
  destination = "/data"
  initial_size = "5gb"
[http_service]
  internal_port = 8080
  force_https = true
  auto_stop_machines = "off"   # SSE + SQLite: keep one machine warm
  min_machines_running = 1
  [[http_service.checks]]
    path = "/healthz"
    interval = "15s"
    timeout = "2s"
```

### Azure: Bicep for "Deploy to Azure" (VM + managed disk + cloud-init)
```bicep
param adminUsername string = 'bunker'
@secure() param adminPublicKey string
param dnsLabel string = 'bunker-${uniqueString(resourceGroup().id)}'
param vmSize string = 'Standard_B2s'
var location = resourceGroup().location
var cloudInit = base64(loadTextContent('cloud-init.yaml'))  // installs bunker, systemd, caddy for TLS
resource pip 'Microsoft.Network/publicIPAddresses@2023-09-01' = {
  name: 'bunker-ip', location: location, sku: { name: 'Standard' }
  properties: { publicIPAllocationMethod: 'Static', dnsSettings: { domainNameLabel: dnsLabel } }
}
// + vnet/subnet/NSG (allow 80/443 only), NIC, then:
resource vm 'Microsoft.Compute/virtualMachines@2024-03-01' = {
  name: 'bunker', location: location
  properties: {
    hardwareProfile: { vmSize: vmSize }
    osProfile: { computerName: 'bunker', adminUsername: adminUsername, customData: cloudInit,
      linuxConfiguration: { disablePasswordAuthentication: true,
        ssh: { publicKeys: [ { path: '/home/${adminUsername}/.ssh/authorized_keys', keyData: adminPublicKey } ] } } }
    storageProfile: {
      imageReference: { publisher: 'Canonical', offer: 'ubuntu-24_04-lts', sku: 'server', version: 'latest' }
      osDisk: { createOption: 'FromImage', managedDisk: { storageAccountType: 'Premium_LRS' } }
      dataDisks: [ { lun: 0, createOption: 'Empty', diskSizeGB: 32, managedDisk: { storageAccountType: 'Premium_LRS' } } ]
    }
    networkProfile: { networkInterfaces: [ { id: nic.id } ] }
  }
  identity: { type: 'SystemAssigned' }  // for Litestream → Blob via managed identity
}
output url string = 'https://${pip.properties.dnsSettings.fqdn}'
```
Compile with `az bicep build -f main.bicep --outfile azuredeploy.json`. Point the button at the raw GitHub URL, URL-encoded. The FQDN `<label>.<region>.cloudapp.azure.com` gives a free hostname, and Caddy/autocert gets a Let's Encrypt certificate automatically. Bunker should embed `autocert` (`golang.org/x/crypto/acme/autocert`) so no Caddy is needed. A sibling template variant can add a Storage Account + container + role assignment `Storage Blob Data Contributor` for Litestream.

**The best Microsoft-shop UX:** after deploy, the output URL opens `/setup`, which offers "Connect Entra" (device code, §4). That's two steps total.

### Container Apps + Litestream (when a VM isn't allowed)
- Single container, `minReplicas: 1`, `maxReplicas: 1`, ephemeral `EmptyDir` volume at `/data`.
- Entrypoint: `litestream restore -if-db-not-exists -if-replica-exists /data/bunker.db && exec litestream replicate -exec "bunker serve"`. Or embed Litestream as a library.
- Blobs (site files) also need to go to Blob storage, or into SQLite. **Recommendation:** keep files in a content-addressed store with a pluggable backend (local dir | S3 | Azure Blob), so stateless PaaS works.
- Revision mode `single`, with a deploy strategy that stops the old replica before starting the new one. Two writers replicating to the same path corrupts the replica.

### docker-compose.yml
```yaml
services:
  bunker:
    image: ghcr.io/kucukkanat/bunker:latest
    restart: unless-stopped
    ports: ["443:8443", "80:8080"]
    environment:
      BUNKER_URL: https://bunker.corp.com
      BUNKER_DATA_DIR: /data
    volumes: ["bunker-data:/data"]
    read_only: true
    tmpfs: ["/tmp"]
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    user: "65532:65532"
volumes:
  bunker-data:
```

### Coolify template (`templates/compose/bunker.yaml`)
```yaml
# documentation: https://bunker.sh/docs
# slogan: Private internal app cloud
# tags: internal-tools,ai,sso
# port: 8080
services:
  bunker:
    image: ghcr.io/kucukkanat/bunker:latest
    environment:
      - SERVICE_URL_BUNKER_8080
      - BUNKER_URL=${SERVICE_URL_BUNKER}
      - BUNKER_SECRET=${SERVICE_PASSWORD_64_SECRET}
      - BUNKER_DATA_DIR=/data
    volumes:
      - bunker-data:/data
    healthcheck:
      test: ["CMD", "/bunker", "healthcheck"]
      interval: 10s
```

### curl | sh installer best practices
- Wrap the whole script in `main() { ... }; main "$@"` so a truncated download executes nothing.
- Use `set -eu` and POSIX sh (not bash).
- Detect `uname -s`/`uname -m` → `linux|darwin`, `amd64|arm64`.
- Download from GitHub Releases over HTTPS. Verify the `SHA256SUMS` and its signature (cosign keyless or minisign) when the tool is available.
- Install to `/usr/local/bin` (sudo only for that step) or `~/.local/bin` without root.
- On Linux with systemd and root, offer `--service`: create a `bunker` system user, `/var/lib/bunker`, and a unit:

```ini
[Unit]
Description=Bunker
After=network-online.target
Wants=network-online.target
[Service]
User=bunker
ExecStart=/usr/local/bin/bunker serve --data /var/lib/bunker
Restart=on-failure
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/bunker
PrivateTmp=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
LockPersonality=true
MemoryDenyWriteExecute=true
SystemCallFilter=@system-service
StateDirectory=bunker
[Install]
WantedBy=multi-user.target
```
- Print the next step: `Open http://<ip>:8080/setup`.
- Make it idempotent (re-run = upgrade) and support `BUNKER_VERSION=v1.2.3`.

**Homebrew:** a tap repo `kucukkanat/homebrew-tap`. GoReleaser can generate the formula (`brews:` section) and the multi-arch images, SBOM and cosign signatures in one step. Use `brew install kucukkanat/tap/bunker` for CLI/MCP users.

---

## 4. Microsoft Entra ID

### 4.1 Automated app registration (`bunker setup entra`)
**Client options:**
- **Microsoft Graph Command Line Tools**: `14d82eec-204b-4c2f-b7e8-296a70dab67e`. It's a public client, supports device code, and can request any Graph delegated scope dynamically. The first use in a tenant needs consent: the signing-in admin consents for themselves or the org. This is the right choice.
- **Azure CLI**: `04b07795-8ddb-461a-bbee-02f9e1bf7b46`. Its Graph scopes are *preauthorized* by Microsoft and you cannot request others (AADSTS65002). `az ad app create` works, so `Application.ReadWrite.All` is in its set, but whether `DelegatedPermissionGrant.ReadWrite.All` is included is unverified. Use it only via the "paste in Cloud Shell" script (`az ad app create`, `az ad sp create`, `az ad app permission admin-consent`).
- Some tenants block the Graph CLI app (an admin disabled it, or Conditional Access applies). Fall back to the Cloud Shell script or the manual guide.

**Device code flow:**
```
POST https://login.microsoftonline.com/organizations/oauth2/v2.0/devicecode
client_id=14d82eec-204b-4c2f-b7e8-296a70dab67e
scope=https://graph.microsoft.com/Application.ReadWrite.All https://graph.microsoft.com/DelegatedPermissionGrant.ReadWrite.All offline_access
→ { device_code, user_code, verification_uri: "https://microsoft.com/devicelogin", interval, expires_in }
POST https://login.microsoftonline.com/organizations/oauth2/v2.0/token
grant_type=urn:ietf:params:oauth:grant-type:device_code&client_id=...&device_code=...
(poll; handle authorization_pending / slow_down)
```
The `tid` claim in the token is the tenant ID. Store it.

**Role needed:** at least **Cloud Application Administrator** (or Application Administrator) for creating the app and granting tenant-wide consent to *delegated* Graph permissions. **Privileged Role Administrator** is needed only for Graph *application* permissions (app roles), which Bunker should avoid. ([MS Learn](https://learn.microsoft.com/en-us/entra/identity/enterprise-apps/grant-admin-consent))

**Graph calls (v1.0):**
```http
POST https://graph.microsoft.com/v1.0/applications
{
  "displayName": "Bunker",
  "signInAudience": "AzureADMyOrg",
  "web": { "redirectUris": ["https://bunker.corp.com/auth/callback"],
           "implicitGrantSettings": { "enableIdTokenIssuance": false, "enableAccessTokenIssuance": false } },
  "groupMembershipClaims": "SecurityGroup",
  "optionalClaims": { "idToken": [ {"name":"email"}, {"name":"preferred_username"}, {"name":"groups"} ] },
  "requiredResourceAccess": [{
    "resourceAppId": "00000003-0000-0000-c000-000000000000",
    "resourceAccess": [
      {"id":"37f7f235-527c-4136-accd-4a02d197296e","type":"Scope"},  // openid
      {"id":"64a6cdd6-aab1-4aaf-94b8-3cc8405e90d0","type":"Scope"},  // email
      {"id":"14dad69e-099b-42c9-810b-d002981feec1","type":"Scope"},  // profile
      {"id":"7427e0e9-2fba-42fe-b0c0-848c9e6a8182","type":"Scope"},  // offline_access
      {"id":"e1fe6dd8-ba31-4d61-89e7-88639da4683d","type":"Scope"},  // User.Read
      {"id":"b340eb25-3456-403f-be2f-af7a0d370277","type":"Scope"},  // User.ReadBasic.All
      {"id":"bc024368-1153-4739-b217-4326f2e966d0","type":"Scope"}   // GroupMember.Read.All
    ]
  }]
}
→ { "id": "<appObjectId>", "appId": "<clientId>" }

POST https://graph.microsoft.com/v1.0/applications/<appObjectId>/addPassword
{ "passwordCredential": { "displayName": "bunker-server", "endDateTime": "2028-11-05T00:00:00Z" } }
→ { "secretText": "..." }   // only shown once; store encrypted

POST https://graph.microsoft.com/v1.0/servicePrincipals
{ "appId": "<clientId>", "appRoleAssignmentRequired": false, "tags": ["WindowsAzureActiveDirectoryIntegratedApp"] }
→ { "id": "<spObjectId>" }

GET https://graph.microsoft.com/v1.0/servicePrincipals(appId='00000003-0000-0000-c000-000000000000')?$select=id
→ { "id": "<graphSpObjectId>" }

POST https://graph.microsoft.com/v1.0/oauth2PermissionGrants
{ "clientId": "<spObjectId>", "consentType": "AllPrincipals", "resourceId": "<graphSpObjectId>",
  "scope": "openid email profile offline_access User.Read User.ReadBasic.All GroupMember.Read.All" }
```
**Verify the permission GUIDs at runtime** by resolving names from `GET /servicePrincipals(appId='00000003-…')?$select=oauth2PermissionScopes` rather than hardcoding them. The IDs above are the well-known ones, but resolving makes it robust and lets the M365 scope allowlist add `Sites.Read.All`, `Files.Read.All` and others later.

Other details:
- **Delays:** a new SP can take seconds to replicate. Retry the grant on 400/404 with backoff (up to ~30s).
- **Secret vs certificate:** offer `addKey` with a self-generated cert (private key never leaves Bunker) as the "better" option. A secret is fine for v1. Show the expiry in the admin UI and warn 30 days before it. Bunker can **self-rotate**, but only if it has `Application.ReadWrite.OwnedBy` app permission. Simpler: re-run `bunker setup entra --rotate`.
- **Federated credentials (no secret at all):** when running on Azure with managed identity, use a workload identity federation credential (`POST /applications/{id}/federatedIdentityCredentials` with issuer = MI token). This is the "Microsoft-recommended" path for the Azure deploy option.
- **Restrict who can sign in:** optionally set `appRoleAssignmentRequired: true` and assign a group (`POST /servicePrincipals/{sp}/appRoleAssignedTo` with `appRoleId` `00000000-0000-0000-0000-000000000000` default access). This needs only Cloud App Admin, since it's not a Graph app role.
- **Manual fallback:** a deep link to the portal (`https://entra.microsoft.com/#view/Microsoft_AAD_RegisteredApps/CreateApplicationBlade`) with copy-paste values, and after creation the admin consent URL `https://login.microsoftonline.com/<tenant>/adminconsent?client_id=<clientId>&redirect_uri=<url>`.

### 4.2 Groups
- The `groups` claim is limited to 200 (JWT), 150 (SAML) and 6 (implicit). On overage the token has `_claim_names: {"groups":"src1"}` + `_claim_sources`, or `hasgroups: true`. **Don't call the endpoint in `_claim_sources`** (it can point to legacy AAD Graph). Call Microsoft Graph instead. ([MS Learn](https://learn.microsoft.com/en-us/security/zero-trust/develop/configure-tokens-group-claims-app-roles))
- `GET /me/transitiveMemberOf/microsoft.graph.group?$select=id,displayName&$top=999` with header `ConsistencyLevel: eventual` (required for the OData cast) and `$count=true`. **Least privilege: `User.Read`** for the signed-in user. Objects you lack permission for may come back with only `id`, which is enough for authorization. ([MS Learn](https://learn.microsoft.com/en-us/graph/api/user-list-transitivememberof?view=graph-rest-1.0))
- **Recommendation:** always fetch memberships via Graph at login, using the delegated token obtained at sign-in (scope `User.Read`). Ignore the token claim except as a fast path. Store group IDs. Re-fetch every hour or on next login. Use transitive membership so nested groups work.
- Alternative: app roles assigned to groups → a `roles` claim. That's admin-managed and not suitable for ad-hoc sharing, but use it for the **Bunker Admin** role: create app role `Bunker.Admin` during setup and assign it to the installing admin.

### 4.3 People picker scopes
- **Users:** `GET /users?$search="displayName:ali" OR "mail:ali"&$select=id,displayName,mail,userPrincipalName,jobTitle&$top=10` + `ConsistencyLevel: eventual`. Needs `User.ReadBasic.All` (users can consent, but admin-granted in setup is better).
- **Groups:** `GET /groups?$search="displayName:fin"&$select=id,displayName,mail,securityEnabled&$top=10` + `ConsistencyLevel: eventual`. Needs `GroupMember.Read.All` (admin consent) or `Group.Read.All`.
- **"Relevant people" first:** `GET /me/people?$top=10` with `People.Read` (user-consentable), which ranks frequent collaborators. A great default list before typing.
- Cache picker results server-side for 5 min. Debounce at 250 ms.

### 4.4 Sign-in hardening
- Single-tenant authority `https://login.microsoftonline.com/<tid>/v2.0`. Validate `iss` == `https://login.microsoftonline.com/<tid>/v2.0` and `tid`.
- Use `oid` (not `sub`, not email) as the user key.
- Refuse `common`/`organizations`/`consumers` authorities at startup (matches PLAN).
- Use PKCE + `nonce` + `state`.
- Honor `wids` for an optional mapping from "Global Admin → Bunker admin" at first run.

---

## 5. MCP: remote server and OAuth

### 5.1 Protocol versions in the wild (Nov 2026)
- **2026-07-28** (current): stateless. No `initialize`, no `Mcp-Session-Id`, no GET stream. Every request carries `_meta["io.modelcontextprotocol/protocolVersion"|"clientInfo"|"clientCapabilities"]`. HTTP headers `MCP-Protocol-Version`, `Mcp-Method` and `Mcp-Name` (for `tools/call`/`resources/read`/`prompts/get`) are **required**, and a mismatch with the body → `400` + JSON-RPC `-32020 HeaderMismatch`. `server/discover` MUST be implemented. Unsupported version → `400` + `-32022` with `data.supported`. Server-to-client requests use MRTR (`InputRequiredResult`). Change notifications come via `subscriptions/listen` (SSE). ([transport](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http), [versioning](https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning), [RC post](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/))
- **Legacy:** 2025-11-25, 2025-06-18 and 2025-03-26 use `initialize`. The `MCP-Protocol-Version` header exists from 2025-06-18 onward.

**Dual-era server logic for `POST /mcp`:**
1. Validate `Origin` if present (must be Bunker's own origin or absent) → else 403.
2. Authenticate the Bearer token → else 401 with `WWW-Authenticate` (see 5.2).
3. Parse the body. If `method == "initialize"` → legacy session: reply with `protocolVersion` = min(client, "2025-11-25"), mint `Mcp-Session-Id` (optional; Bunker can stay effectively stateless and accept any session ID), and capabilities `{tools:{listChanged:false}, resources:{}, prompts:{}}`.
4. Else, if `_meta["io.modelcontextprotocol/protocolVersion"]` is present → modern path. Check that the headers match the body, then dispatch.
5. Else (legacy request after initialize) → dispatch with legacy semantics. A missing `MCP-Protocol-Version` header ⇒ treat it as `2025-03-26`.
6. `GET /mcp`: 405 for modern-only. For legacy clients, return 405 too (servers MAY decline the GET stream). `DELETE /mcp` → 200/405.
7. Respond with `application/json` for everything. Tools are fast, so SSE isn't needed except for progress on long AI-builder calls.

`server/discover` result (modern): `{ "supportedVersions": ["2026-07-28","2025-11-25","2025-06-18","2025-03-26"], "capabilities": {...}, "serverInfo": {"name":"bunker","version":"x"}, "instructions": "..." }` **(verify exact field names against schema.ts for 2026-07-28)**.

**Tool design (keep it small; agents do better with fewer tools):**
- `list_sites`, `get_site`, `create_site`
- `deploy_files` (map path→content, or a base64 zip)
- `read_file`, `edit_file` (search/replace)
- `share_site`, `list_versions`, `rollback`
- `db_query`, `db_write`
- `get_sdk_docs` (returns llms.txt)

Add tool annotations `readOnlyHint` / `destructiveHint` / `idempotentHint`. Return `structuredContent` + a text fallback. Optionally add `_meta.ui.resourceUri` on `get_site`/`open_site` to render the site as an MCP App.

### 5.2 Authorization: endpoints Bunker must serve
Bunker is both the **resource server** (`https://bunker.corp.com/mcp`) and the **authorization server** (issuer `https://bunker.corp.com`). User login on the AS's `/oauth/authorize` page reuses the normal Entra session.

**401 challenge:**
```http
HTTP/1.1 401 Unauthorized
WWW-Authenticate: Bearer resource_metadata="https://bunker.corp.com/.well-known/oauth-protected-resource/mcp", scope="bunker"
```

**Protected Resource Metadata (RFC 9728).** Serve it at **both** `/.well-known/oauth-protected-resource/mcp` and `/.well-known/oauth-protected-resource`:
```json
{
  "resource": "https://bunker.corp.com/mcp",
  "authorization_servers": ["https://bunker.corp.com"],
  "scopes_supported": ["bunker"],
  "bearer_methods_supported": ["header"],
  "resource_name": "Bunker",
  "resource_documentation": "https://bunker.corp.com/docs/mcp"
}
```

**AS metadata (RFC 8414).** Serve it at `/.well-known/oauth-authorization-server`, **and the same JSON at `/.well-known/openid-configuration`**, since some clients probe OIDC first:
```json
{
  "issuer": "https://bunker.corp.com",
  "authorization_endpoint": "https://bunker.corp.com/oauth/authorize",
  "token_endpoint": "https://bunker.corp.com/oauth/token",
  "registration_endpoint": "https://bunker.corp.com/oauth/register",
  "revocation_endpoint": "https://bunker.corp.com/oauth/revoke",
  "response_types_supported": ["code"],
  "grant_types_supported": ["authorization_code", "refresh_token"],
  "code_challenge_methods_supported": ["S256"],
  "token_endpoint_auth_methods_supported": ["none", "client_secret_post", "client_secret_basic", "private_key_jwt"],
  "scopes_supported": ["bunker"],
  "client_id_metadata_document_supported": true,
  "authorization_response_iss_parameter_supported": true
}
```
Clients **refuse to proceed if `code_challenge_methods_supported` is missing**.

**Authorize** (`GET /oauth/authorize?response_type=code&client_id=…&redirect_uri=…&code_challenge=…&code_challenge_method=S256&state=…&scope=bunker&resource=https%3A%2F%2Fbunker.corp.com%2Fmcp`):
1. If `client_id` is an https URL → **CIMD**: fetch it with an SSRF guard (https only, public IPs only unless the admin allowlists internal ones, 5s timeout, ≤ 64KB, cache per HTTP headers). Require `doc.client_id == client_id` and `redirect_uri ∈ doc.redirect_uris`.
2. Otherwise look up the DCR client.
3. Require the user to be signed in (Entra session).
4. **Show a consent screen** with `client_name`, the **redirect URI hostname shown prominently**, and a localhost warning.
5. Optional admin policy: an allowlist of client hosts (`claude.ai`, `claude.com`, `chatgpt.com`, `cursor.com`, `vscode.dev`, localhost).
6. Redirect with `code`, `state` and `iss=https://bunker.corp.com` (RFC 9207, which 2026-07-28 clients validate).

**Token** (`POST /oauth/token`):
- Verify PKCE and that `resource` equals the canonical resource.
- Issue an **opaque** access token, stored hashed (`bt_…`), TTL 1h, with audience = resource. Bunker validates its own tokens, so no JWT is needed.
- Issue a refresh token rotated on every use (required for public clients). Revoke the whole family on reuse.
- Tokens map to (user, client, scope) and show up in "Connected apps" in user settings with a Revoke button, plus an audit entry.

**DCR** (`POST /oauth/register`, RFC 7591): accept `{redirect_uris, client_name, grant_types, token_endpoint_auth_method, application_type}`. Return `client_id` (+ `client_secret` if not `none`). Rate-limit it and garbage-collect unused clients after 30 days. Keep it even though 2026-07-28 deprecates DCR: Le Chat and older clients use it, and ChatGPT falls back to it when CIMD fetch fails.

**Known clients:**
- **Claude.ai / Claude Desktop**: callback `https://claude.ai/api/mcp/auth_callback` (and future `https://claude.com/api/mcp/auth_callback`). Uses CIMD. ([Anthropic help](https://support.claude.com/en/articles/11503834-building-custom-connectors-via-remote-mcp-servers))
- **ChatGPT**: CIMD `client_id` = `https://chatgpt.com/oauth/client.json` (or `https://chatgpt.com/oauth/{callback_id}/client.json` when the AS lacks issuer-identification). Falls back to DCR. ([OpenAI auth docs](https://developers.openai.com/plugins/build/auth))
- **Claude Code / Codex / Cursor / VS Code**: localhost redirect URIs, CIMD or DCR ([CIMD adoption](https://gofranz.com/blog/state-of-cimd-adoption/)).
- **CORS:** allow `Access-Control-Allow-Origin: *` on `/.well-known/*`, `/oauth/token` and `/oauth/register` (browser-based clients). Don't use it on `/oauth/authorize`.
- **PATs too:** `Authorization: Bearer bk_pat_…` on `/mcp` for users of Claude Code/Cursor who prefer a pasted header (`claude mcp add --transport http bunker https://bunker.corp.com/mcp --header "Authorization: Bearer …"`).

### 5.3 Local stdio MCP (`bunker mcp`)
Use newline-delimited JSON-RPC on stdin/stdout and logs to stderr. Read credentials from `~/.config/bunker/credentials.json` (obtained by `bunker login` via device flow against Bunker's own `/oauth/device`). Implement the dual-era probe: answer both `initialize` and `server/discover`. It's simplest if the stdio server just proxies to remote `/mcp` with the stored token.

Sources: [MCP authorization 2025-11-25](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization), [Aaron Parecki on CIMD](https://aaronparecki.com/2025/11/25/1/mcp-authorization-spec-update), [MCP Apps](https://modelcontextprotocol.io/extensions/apps/overview).

---

## 6. Performance: SQLite, static serving, SSE

### 6.1 SQLite
- **Driver:** the repo uses `modernc.org/sqlite` (pure Go, needs no CGO and keeps the static binary). That's the right call for "single binary". Per [go-sqlite-bench](https://github.com/cvilsmeier/go-sqlite-bench), there's "no clear winner"; modernc is somewhat slower on large/insert-heavy workloads, while zombiezen (which is modernc-based with a direct API) leads concurrency tests. `ncruces/go-sqlite3` (wasm) is an alternative with built-in Litestream-friendly VFS options. Stay on modernc and revisit only if profiling shows the driver is the bottleneck.
- **Pools:**
```go
w, _ := sql.Open("sqlite", "file:"+p+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(ON)&_txlock=immediate")
w.SetMaxOpenConns(1)                // single writer: no SQLITE_BUSY between our own writers
r, _ := sql.Open("sqlite", "file:"+p+"?mode=ro&_pragma=busy_timeout(10000)&_pragma=query_only(1)&_pragma=mmap_size(268435456)&_pragma=cache_size(-32000)&_pragma=temp_store(MEMORY)")
r.SetMaxOpenConns(runtime.NumCPU()) // readers never block on the writer in WAL
```
  The current single pool with `_txlock=immediate` makes every `BeginTx` (even reads) take the RESERVED lock.
- **Batch writes:** group writes in short transactions, and never hold a write tx across network calls (AI, Graph). Use `INSERT … ON CONFLICT DO UPDATE` and `RETURNING`.
- **Checkpoints:** if Litestream is used, it controls checkpoints. Otherwise `PRAGMA wal_autocheckpoint=1000` (default) is fine. Run `PRAGMA optimize` on close and every few hours.
- **Multi-tenant data for `bunker.db`:** one table `docs(site_id, coll, id, owner, data JSONB, created, updated, PRIMARY KEY(site_id, coll, id))` `WITHOUT ROWID`, with JSON queries via `json_extract` / `->>`. Add expression indexes on demand: `CREATE INDEX … ON docs(site_id, coll, data->>'status')`. This stays one DB file, keeping backups and Litestream simple. A per-site DB file gives better isolation but is costly for 5,000 sites with Litestream. Keep one file plus quotas.
- **Blobs** go outside SQLite (content-addressed files), except tiny ones.
- **Backups:**
  - Litestream 0.5.x (rewritten storage format, faster restore, Azure SDK v2, SAS auth) ([release notes](https://newreleases.io/project/github/benbjohnson/litestream/release/v0.5.7)). It can run as a sidecar (`litestream replicate -exec`) or be embedded as a Go library (the API is less documented, verify).
  - Also offer `VACUUM INTO '/backups/bunker-<ts>.db'` nightly as the zero-dependency option.
- **Never use network filesystems** (SMB/NFS/EFS/Azure Files) for the live DB.

### 6.2 Static file serving
- **Store:** `blobs/<sha256[0:2]>/<sha256>` with `.br`/`.gz` variants generated at upload for compressible types (html, css, js, json, svg, txt, md, wasm), only when smaller by ≥ 10%. Use brotli q=11 (andybalholm/brotli, pure Go) and gzip level 9.
- **Serve:**
  - Pick a variant from `Accept-Encoding`. Set `Content-Encoding` and `Vary: Accept-Encoding`, with `ETag: "<sha256>"` (strong; use a `-br` suffix per variant).
  - Use `http.ServeContent` for Range/If-None-Match with an `*os.File` (Go uses `sendfile` for that path).
  - Precompute `Content-Type` from the extension (with `mime.TypeByExtension` overrides), and never sniff (`nosniff`).
- **Caching:** the URL contains the immutable version (`/_r/<tok>/<site>/<ver>/…`), so non-HTML assets get `public, max-age=31536000, immutable`. HTML gets `no-cache` (revalidate by ETag). The token changes per session, which defeats the browser cache. Make it stable per (viewer, site, day) or move the token to a `?t=` query param… that also changes the URL. Simplest: token TTL 24h, re-minted only after expiry.
- **Versions:** a version = manifest (path → sha256, size, ctype) stored in SQLite. Publishing is atomic (insert the manifest, flip `sites.current_version`). Rollback = flip the pointer. Diffs between manifests are cheap.
- **HTTP/2:** with built-in TLS (autocert), Go serves h2 automatically. Behind a proxy, enable h2c (`http.Server{Protocols}` in Go ≥1.24) so SSE + many assets multiplex.
- Upload limits: zip-slip guard (`filepath.IsLocal`), max files, max total size, reject symlinks, normalize Unicode paths.

### 6.3 SSE at scale in Go
```go
func sse(w http.ResponseWriter, r *http.Request) {
  rc := http.NewResponseController(w)
  _ = rc.SetWriteDeadline(time.Time{})            // disable server WriteTimeout for this stream
  h := w.Header(); h.Set("Content-Type", "text/event-stream"); h.Set("Cache-Control", "no-cache")
  h.Set("X-Accel-Buffering", "no")
  sub := bus.Subscribe(viewer, topics); defer sub.Close()
  t := time.NewTicker(20 * time.Second); defer t.Stop()
  for {
    select {
    case <-r.Context().Done(): return
    case ev := <-sub.C: fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Type, ev.JSON)
    case <-t.C: io.WriteString(w, ":\n\n")
    }
    if err := rc.Flush(); err != nil { return }
  }
}
```
- One goroutine per stream is ~8–16 KB, so 10k streams ≈ 150 MB at most. That's fine.
- The bus is a sharded map `topic → set(chan)` using non-blocking sends. Drop or close slow consumers whose buffer (e.g., 64) is full, and let them reconnect with `Last-Event-ID`. Keep a ring buffer of recent events per topic for replay.
- **Re-check authorization at subscribe and filter per-event** (row-level rules: "own rows only" collections must not broadcast other users' rows). Send only IDs + changed docs the viewer can read.
- The parent owns one EventSource per tab and fans out to iframes via their ports.
- Set `ReadHeaderTimeout` and an `IdleTimeout`. Leave `WriteTimeout` on for normal routes and clear it per stream via the ResponseController.

---

## 7. App types users want to drop in (zero server-side execution)

| Input | Handling | Notes |
|---|---|---|
| Single `.html` | As-is. Inject `<script src="/_sdk/bunker.js">` + storage polyfill as the first child of `<head>` | Auto-rewrite known CDN URLs (cdn.tailwindcss.com, unpkg, jsdelivr, cdnjs, fonts.googleapis) to `/_vendor/…` if vendored, else warn in the builder |
| Folder / `.zip` | Find `index.html` (root or single top dir) | Support `_redirects` (`/* /index.html 200` for SPAs) |
| SPA build (Vite/CRA/Next export/Astro) | Detect `assets/`/`_next/`/`_astro/`. **Rewrite root-absolute URLs** (`src="/assets/…"`, `url(/…)`, JS `"/assets/` chunks) to relative, or serve with an injected `<base href="/_r/<tok>/<site>/<ver>/">` and rewrite absolute `/` refs | Opaque origin breaks SPA libraries that use `localStorage` (polyfill) and `history.pushState` (works in an iframe; deep links via the parent hash `#/path` sync) |
| `.md` / folder of `.md` | goldmark (GFM, tables, task lists, footnotes) + chroma highlighting → themed HTML. Mermaid via `/_vendor/mermaid` | Docs sites: a nav sidebar generated from the folder tree |
| `.csv` / `.xlsx` | "Instant app": import rows into `bunker.db` collection `rows` and generate a table/filter/form page (sortable, search, add/edit by permission). Parse server-side with Go (`encoding/csv`, `github.com/xuri/excelize/v2`) or client-side with SheetJS | The killer feature for non-technical users ("turn my spreadsheet into a shared tracker") |
| `.ipynb` | Render server-side: markdown cells via goldmark, code cells highlighted, outputs: `text/html` (sandboxed anyway), `image/png` (data URI), `text/plain`, `application/vnd.plotly` (vendor plotly) | Optional "Run in browser" via JupyterLite/Pyodide (heavy, opt-in) |
| `.pdf` | Wrap in a pdf.js viewer page | Chrome's built-in viewer isn't usable in sandboxed frames (verify) |
| `.jsx` / `.tsx` single file (Claude artifact / v0 style) | esbuild Go API: `api.Build({EntryPoints, Bundle:true, Format: api.FormatESModule, JSX: api.JSXAutomatic, External:[…vendor]})` + import map → `/_vendor/react@19/…`. Wrap: `createRoot(#root).render(<Default/>)` | Pure Go, in-process, milliseconds. Also handles TypeScript |
| Python "Streamlit-like" | **Stlite** (Streamlit on Pyodide) or plain Pyodide + a tiny UI helper, vendored (~10–30 MB, lazy-loaded, cached immutable). Needs `'wasm-unsafe-eval'` | Feasible, no server execution. First load is slow. Package installs need a vendored wheel mirror (no egress); pick a fixed set: numpy, pandas, matplotlib |
| Slides | `.md` with `---` separators → reveal.js (vendored). `.pptx` → convert to PDF? (needs LibreOffice; skip) | |
| Images/video folders | Auto gallery page | |
| SQLite/DuckDB data apps | sqlite-wasm/DuckDB-wasm vendored. Files from `bunker.files` | Good for "dashboard over a CSV export" |

**Vendor set governance:** pin versions, store them under `/_vendor/<name>@<ver>/…`, serve them immutable, and list them in `/_vendor/index.json`. The AI builder's system prompt and `get_sdk_docs` include this list. Admins can add packages by uploading or by `bunker vendor add <npm-name>@<ver>` (downloads from npm at admin time, never at viewer time).

---

## Appendix: notable facts to remember
- `navigate-to` CSP was removed from the spec. Sandboxed frames can always navigate themselves.
- `webrtc` CSP directive: specified (w3c/webappsec-csp#457), **not enforced by Chromium/WebKit** as of Chrome 154 / WebKit 26.6.
- Connection-Allowlist: Chromium ≥152 (origin trial 148–151). Syntax `Connection-Allowlist: (response-origin "https://x/*"); report-to=ep`, with WebRTC and redirects blocked by default. An empty `()` blocks everything after load. A malformed header fails closed. Only honored on `Sec-Fetch-Dest` document/iframe/worker responses ([WICG spec](https://wicg.github.io/connection-allowlists/)).
- Entra groups overage: 200 JWT / 150 SAML / 6 implicit. `/me/transitiveMemberOf` least privilege = `User.Read`.
- Admin consent for delegated Graph scopes: Cloud Application Administrator suffices. Graph app roles need Privileged Role Administrator.
- MCP 2026-07-28 error codes: `-32020` HeaderMismatch, `-32022` UnsupportedProtocolVersion, missing resource → `-32602` (was `-32002`).
- Claude connector callback: `https://claude.ai/api/mcp/auth_callback` (+ `https://claude.com/...`). ChatGPT CIMD: `https://chatgpt.com/oauth/client.json`.
