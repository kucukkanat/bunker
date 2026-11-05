# Bunker security audit (2026-11-05)

Adversarial review of everything under `internal/`. Five threat actors were in scope:

1. An unauthenticated internet attacker.
2. A signed-in viewer trying to escalate.
3. A malicious site author whose page runs in other people's browsers.
4. A malicious MCP/OAuth client.
5. A malicious uploaded file.

Every finding below was reproduced with a failing test or a browser script before it was fixed. Each fix keeps that test as a regression test (named in the table). Severity is rated for a typical deployment: internet-reachable, built-in accounts or Entra.

## Findings

| ID | Severity | Component | Finding | Status | Regression test |
|----|----------|-----------|---------|--------|-----------------|
| F1 | **High** | `server` learnURL | **Host-header poisoning of the public URL.** Without `BUNKER_URL`, the first request from *anyone* set `server.url` permanently. One anonymous `GET /` with `Host: evil.example` on a fresh install was enough. After that, invite and reset links, the OIDC `redirect_uri`, device-flow and OAuth metadata, and the Entra auto-setup app registration's redirect URI all pointed at the attacker. Example: an admin sends a poisoned invite link (possibly an admin invite), the invitee's click hands the one-time token to the attacker, and the attacker creates the account. | Fixed. Only a signed-in admin's browser session, or the setup request (which the setup code authorizes), records the URL. | `server/security_test.go` `TestAnonymousRequestCantPoisonPublicURL`, `TestSetupRemembersURL` |
| F2 | **High** | `auth` setup, `entra` auto-setup | **First-run takeover behind a same-host reverse proxy.** The setup code was skipped for any loopback peer that sent no `X-Forwarded-For`. A proxy on the same machine that sets only `X-Real-IP` or `Forwarded` (or nothing, e.g. a minimal nginx `proxy_pass`) made every internet request look local. Anyone could then create the first admin. | Fixed. `auth.IsLocalRequest` requires a loopback peer, no proxy headers (XFF, X-Real-IP, Forwarded, X-Forwarded-Host/Proto, Via, CF-/True-Client-IP…) and a `localhost`/loopback Host. | `auth/security_test.go` `TestSetupLoopbackNeedsLocalHost` |
| F3 | **High** | `core.User.InGroup`, `auth` admins | **Group display names spoofed group IDs and admin groups.** Group matching accepted the group ID *or* the display name. Entra members can usually create groups and pick any display name. A group named after the object ID of "Finance" (which the share picker stores) granted Finance's site shares, `group:` data rules and upload rules. A group named like an entry in the `admins` setting made its creator a Bunker admin. | Fixed. GUID-shaped entries match IDs only, and `admins` matches groups by ID only (`InGroupID`). | `core/identity_test.go` |
| F4 | Medium | `auth.SafeNext` | **Open redirect after sign-in.** Only a leading `/\` was refused. Browsers strip tabs and newlines inside URLs and read `\` as `/`, so `next=/%09/evil.example` produced `Location: /\t/evil.example`, which Chromium follows to `//evil.example` (verified with Playwright). | Fixed. Any control character or backslash is refused. | `auth/security_test.go` `TestSafeNextRejectsBrowserNormalizedRedirects`, `TestLoginRedirectNotOpen` |
| F5 | Medium | `server.clientIP`, `auth` login limiter | **X-Forwarded-For spoofing.** The left-most (client-supplied) XFF entry was trusted, and `TrustProxy` is on by default whenever TLS is off. Rotating it bypassed every per-IP limit (setup code, password guessing, OAuth client registration) and forged audit IPs. Separately, the login-attempt table was wiped once it held 100k keys, so flooding it with throwaway IP keys reset per-email lockouts. | Fixed. The right-most entry (the one our proxy appended) is used. On overflow only IP keys are evicted; email lockouts are kept. | `server/security_test.go` `TestForwardedForCantDodgeRateLimit`, `auth/ratelimit_internal_test.go` |
| F6 | Medium | `auth` callback, `entra` auto-setup | **Entra guest accounts were not refused** despite the "guests can't use Bunker" policy. Detection relied on the `acct` claim, which Entra sends only as an optional claim, and neither the auto-setup nor the docs configured it. | Fixed. The auto-setup requests `acct`, and a token whose `idp` names another tenant or provider also counts as a guest. | `auth/guest_internal_test.go`, `entra/entra_test.go` |
| F7 | Medium | `sites` deploy | **Zip expansion DoS.** Each zip in a multipart upload was checked against the site size limit on its own. One 100 MB request made of small, highly compressed zips could expand to many GB in memory. Any signed-in user can call `/api/v1/drop`. | Fixed. One expansion budget covers the whole upload. | `sites/security_test.go` `TestManyZipsShareOneLimit` |
| F8 | Medium | `apptypes` markdown/notebooks | **CPU DoS: goldmark v1.8.7 (the latest version) is quadratic.** Measured per file: 200 KB of `[a](` took about 17 s, 50,000 nested list markers about 12 s, and deep `>` quotes about 7 s. The same held for notebook markdown cells. Deploys share a 4-slot semaphore, so a few uploads stalled all deploys. | Mitigated. Each deploy has a markdown budget: 4 MB, at most 3000 `[` per document, at most 32 container markers opening a line. Anything over it is shown as escaped text. | `apptypes/markdown_dos_test.go` |
| F9 | Medium | `apptypes` xlsx | **Spreadsheet expansion DoS.** A 600 KB `.xlsx` unpacking to 250 MB of tiny shared strings took 20 s and a 1.6 GB heap per deploy. Every part could expand to 256 MB, and every sheet entry could re-read it. | Fixed. Limits are 64 MB per part and 128 MB actually read per workbook, and shared strings are capped at the cell limit. | `apptypes/xlsx_dos_test.go` |
| F10 | Medium | `cli` deploy | **`bunker deploy` followed file symlinks out of the folder.** An untrusted project (a downloaded template or a cloned repo) containing `dist/logo.png -> ~/.ssh/id_ed25519` published the key to every viewer. Binary-looking names are not secret-scanned. | Fixed. Symlinks whose target is outside the project are skipped. | `cli/security_test.go` |
| F11 | Low | `server` CSRF | The Origin check compared against the URL known at startup, so it stayed off for the whole run after the URL was learned. The `X-Bunker` header still protected the API. | Fixed. Compares against the current URL. | (covered by `server` tests) |
| F12 | Low | `auth` form posts | The login and invite Origin check used a prefix match, so `https://bunker.example.co` passed for `https://bunker.example.com` (login CSRF from a look-alike domain). | Fixed. Exact match. | `auth/security_test.go` `TestLoginOriginPrefixIsCrossSite` |
| F13 | Low | `sites.update` | Editors could mark a private site as a template, which lets anyone copy its files. Org-wide viewing is owner-only. Separately, a rejected owner-only change still applied earlier fields such as the title. | Fixed. Owner-only fields (orgView, isTemplate, slug) are checked before anything is written. | `sites/security_test.go` `TestOnlyOwnerOffersTemplate` |
| F14 | Low | `data` SSE | A live stream kept the role it was opened with. An editor who was demoted kept receiving other people's entries in private collections until reconnecting. | Fixed. Each batch uses the current role (cached; shares invalidate it). | `data/data_live_role_test.go` `TestLiveStreamFollowsRoleChanges` |
| F15 | Low | `data` export | Collection CSV exports wrote viewer-entered text verbatim. Values like `=HYPERLINK(…)` or DDE ran as formulas in the editor's spreadsheet. | Fixed. Text cells, column names and author names get the same apostrophe guard as the audit export. | `data/data_live_role_test.go` `TestCSVExportDefusesFormulas` |
| F16 | Low | `mcp` CIMD fetch | **Blind SSRF.** The client-metadata fetch (the URL is chosen by any MCP client) blocked only RFC 1918, loopback and link-local. It could reach 100.64/10 (Tailscale/CGNAT), NAT64, 0/8 and similar ranges, and the status code was echoed back. | Fixed. Those ranges are blocked too. | `mcp/security_test.go` |
| F17 | Low | `sites` delete | Trashing a site renames it to `trash-<id>-<slug>`, and the error from that rename was ignored. Anyone could take that address first (directly, or via a title like "Trash 12 payroll"), and the owner's delete would report success while the site stayed live. | Fixed. `trash-` addresses are reserved and the error is returned. | `sites/security_test.go` `TestTrashAddressesAreReserved` |

**By severity:** 3 High, 7 Medium, 7 Low. All 17 were fixed; F8 is mitigated with a per-deploy budget.

## Reviewed and found sound

These areas were reviewed and in several cases probed.

**Page safety (`X-Bunker-Page`)**
- The page-safe check is enforced in `core.App.Handle` for every op, including `Handler` ops.
- Every non-op mux handler is either public protocol plumbing or does its own checks.
- The shell path guard resolves `..` and `%2e`, then requires the site's own API prefix (covered by `sdk.test.mjs`).
- The remote MCP in-process dispatch runs as the token's own user through the same op wrappers (scopes, admin checks).

**Sandbox exfiltration**
- The shell's own CSP `frame-src 'self'` stops a sandboxed page from navigating its frame to an external origin. Verified in Chromium: a self-navigation to another origin never left the browser.
- So neither data in the URL nor the SDK `MessageChannel` port (re-sent on frame load) reaches a third-party page.

**Content serving**
- Tickets are HMAC-signed, bound to site, version, viewer and expiry, and access is re-checked on every request.
- Draft tickets require editor access.
- The `sandbox` CSP is sent on raw content, raw error pages and upload errors.
- Uploaded SVG is sandboxed. PDF is the documented exception.
- The `uploads.content` API endpoint forces `attachment`.

**OAuth**
- Only PKCE S256 is accepted.
- Redirect URIs must match exactly; only loopback URIs may vary the port.
- The consent CSRF token is a MAC bound to the session, client, redirect and challenge.
- Codes are single-use (`DELETE … RETURNING`).
- Refresh tokens rotate, and reuse revokes the whole family.
- Admin scope is granted to admins only, and scopes can only narrow on refresh.
- The consent page sends `frame-ancestors 'none'` and XFO DENY.

**Data and storage**
- Data queries are not open to SQL injection: field names are checked against a regex and JSON paths are bound parameters.
- Sort is limited to the validated field set.
- Read rules are applied in SQL (`author_id=?`), including count, export and cursors.
- `CleanPath`, the restore-archive path checks (regular files and directories only) and the CLI `pull` path checks are all sound.

**Outbound requests and secrets**
- Connections enforce SSRF rules at dial time on the resolved IP, use no proxy and block cross-host redirects.
- Connection secrets are never returned and appear in the audit log by header name only.
- The AI key is masked.

**Dashboard and server-rendered pages**
- The dashboard has no `innerHTML` sink with user data (the only uses are static SVG strings). It sets a strict CSP (`script-src 'self'`) and `frame-ancestors 'none'`.
- Notification links are always `/s/<slug>/…`.
- Server-rendered auth, consent, error and "need access" pages escape their output.

## Residual risks (accepted, with rationale)

**Sandbox and site authors**
- **Malicious site authors** can do anything the *viewer* could do inside that one site through the SDK bridge:
  - read and write data;
  - call M365 Graph as the viewer (within the admin's scope allowlist);
  - use the site's connections, AI quota and notifications;
  - trigger Entra incremental-consent prompts.

  This is the product model. It is audited, and Bunker refuses cross-site, deploy, sharing and settings calls.
- **Exfiltration channels** depend on the browser:
  - Chromium blocks external frame navigation (via the shell's `frame-src`) and, from version 152, WebRTC (via `Connection-Allowlist`).
  - On Firefox and Safari, WebRTC/STUN leaks remain possible (see RESEARCH §2.3).
  - Admins who need a hard guarantee can enforce Edge/Chrome.
- **Single-origin mode**: user content shares the app's origin and is isolated only by the iframe sandbox and the CSP `sandbox` directive. PDFs are served inline without a sandbox because PDF viewers refuse one. Recommendation: set `BUNKER_CONTENT_URL` to a separate origin.
- **Content tickets are bearer URLs** for up to 12 h. Anyone holding one sees that site's content, including uploads under that viewer's rules. Pages never see other sites' tickets.

**First run and network**
- **Loopback setup**: a same-host proxy that rewrites `Host` to `localhost` and adds *no* forwarding headers is still indistinguishable from a local request. Recommendation: set `BUNKER_SETUP_CODE`, or finish setup before exposing the server.
- **TrustProxy defaults on when TLS is off.** A server exposed *directly* (no proxy) still believes the single XFF value a client sends. Behind multi-proxy chains, rate limits key on the nearest proxy. Recommendation: `BUNKER_TRUST_PROXY=false` when there is no proxy.

**Identity**
- **Group names typed by admins** (allowed-groups gate, `group:Finance` rules, shares by typed name) still match display names, which Entra users may be able to create. Recommendation: use group object IDs (the share picker already does) and restrict group creation in Entra.
- **Phishing**: device-code and DCR client names are chosen by the client (e.g. "Claude"). The approval and consent pages show the client name and, for OAuth, the redirect host. This is inherent to the protocols.
- **Generic OIDC** accepts an email without an `email_verified` claim. Shares and admin entries by email trust the IdP's email.

**DoS and storage**
- **Markdown CPU** is bounded but not eliminated: the worst case is about 20 s of CPU per deploy. Deploys are authenticated and audited.
- **The blob store is global and content-addressed.** `blobs.missing` confirms whether a given sha256 exists. `sites.commit` can reference any existing blob whose full hash is known, and full hashes are shown only to editors of the site holding the blob.

**Other**
- **Logout CSRF**: `POST /auth/logout` has no `X-Bunker` check. SameSite=Lax keeps the session cookie off cross-site posts, so the worst case is a cleared cookie.
- **Admin-trusted configuration**: admins can point AI, connection and OIDC endpoints anywhere. Backups include `secret.key` by default (admin-only, documented).
- **Untested browsers**: Firefox and WebKit could not be run here. The `frame-src` navigation block follows the CSP spec there too, but it was only measured in Chromium.
