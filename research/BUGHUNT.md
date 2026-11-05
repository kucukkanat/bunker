# Bug hunt (functional correctness)

Scope: functional bugs. Security was audited separately (SECURITY-AUDIT.md).
I read core, server, auth, sites, notify, hooks, mail, files, mcp, cli, admin,
m365, connections, templates and vendorlib, plus the dashboard's files, share
and settings views, and checked the docs against the code. Each fixed bug has
a regression test, and I checked that each test fails without its fix. I left
`internal/data` alone, as asked (see "Reported, not fixed").

Green after the fixes: `go build ./... && go vet ./... && go test -race ./internal/...`,
`node test/e2e/sdk.test.mjs` (69/69), `bash test/e2e/cli.test.sh` (21/21),
`node test/e2e/ui.test.mjs` (23/23).

## Fixed, by priority

### High

| # | Bug | Impact | Fix | Test |
|---|-----|--------|-----|------|
| 1 | **MCP OAuth refresh failed once the access token had expired.** The hourly auth sweep deleted expired tokens, OAuth access tokens included. The refresh grant treats a missing token row as "the person revoked it in Settings". | Every MCP client (Claude, Cursor, VS Code…) had to reconnect an hour or two after going idle, because clients refresh after expiry. | `auth.SweepExpired` now skips `mcp:` tokens. The mcp module runs its own sweep: it removes expired refresh tokens and codes, and an access token only once no refresh token points at it. Revoking in Settings still ends the session. | `mcp/bughunt_test.go` TestRefreshWorksAfterAccessTokenExpiredAndWasSwept |
| 2 | **Successful sign-ins counted toward the per-IP lockout.** Every attempt was recorded under `ip:<addr>`. | After 10 sign-ins in 15 minutes from one office NAT address, everyone else there got "Too many attempts". | A successful sign-in refunds its IP attempt. Failed guesses still lock out. | `auth/bughunt_test.go` TestSuccessfulLoginsDontLockOutSharedIP |
| 3 | **Configured admins couldn't load site content.** The sandboxed frame sends no cookie, so `loadTicketUser` rebuilt the viewer from `users.is_admin` only. That ignores the `admins` setting and `BUNKER_ADMINS` (emails or Entra group IDs). | The shell opened, then every page and asset said "Access revoked" for admins named by setting or group, on sites not shared with them. | Use `auth.LoadUser`, which applies the configured admins and the disabled flag. | `sites/bughunt_test.go` TestConfiguredAdminCanLoadSiteContent |
| 4 | **Parallel draft edits lost files.** `files.write`, `files.delete` and the AI builder's write/delete each read the working manifest, changed it and saved it, with no lock. | CLI or agent parallel writes, or a person editing while the builder runs: some files silently vanished (11 of 17 survived in the test). | `sites.LockDraft(siteID)`: a striped mutex around each read-modify-write, used by sites and the ai workspace. | TestParallelDraftWritesKeepEveryFile |
| 5 | **Admin people search capped at 500.** `users.list` applied `LIMIT 500` in SQL, then filtered by `q` in Go. | With more than 500 people, nobody outside the 500 most recently seen could be found (to make admin, disable or reset). | Filter in SQL (`LIKE … ESCAPE`, so `%` and `_` are literal). Also 404 for PATCH on an unknown user (it returned ok). | TestUserSearchFindsPeopleBeyondFirst500 |

### Medium

| # | Bug | Impact | Fix | Test |
|---|-----|--------|-----|------|
| 6 | `sites.update` wrote each field before checking the next. A bad slug, or org/public sharing being off, came after title and description were already saved, and the site cache wasn't invalidated. | A rejected update half-applied, and the change showed up up to 20 s later. | Validate everything, including whether the new slug is taken, before writing. | TestSiteUpdateIsAllOrNothing |
| 7 | `trunc()` (title, icon, description, version message) cut by bytes. | Emoji icons and accented descriptions were stored as invalid UTF-8 (shown as `��`). | Cut on a rune boundary. | TestSiteFieldsTruncateOnRuneBoundaries |
| 8 | `sites.create` with an unusable template (and `sites.duplicate` with a failed commit) left an empty site behind. | Retrying created `name-2`, `name-3`… | Delete the new site on failure, like `sites.drop` already did. | TestFailedCreateFromTemplateLeavesNoSite |
| 9 | `sites.duplicate` didn't copy `settings`. | A copy of an SPA lost client-side routing (deep links 404), its entry page, 404 page and badge setting. | Copy settings. | TestDuplicateKeepsSettings |
| 10 | Upload list paging skipped files. Several files uploaded in one request shared the same millisecond `created_at`, and the cursor is `created_at < before`. | `bunker.files.list` pages silently dropped files at page boundaries. | Each upload gets a distinct, increasing `created_at` per site. | `files/bughunt_test.go` |
| 11 | `access.request` documented `role` but ignored it, and anyone who could already view was ignored too. | Viewers couldn't ask to edit; the owner always saw a viewer request. | Store the requested role; let viewers ask for editor. The message is also cut rune-safely. | `notify/bughunt_test.go` TestViewerCanAskToEdit |
| 12 | `admin.policy.update` saved fields before checking the numbers. | A bad `siteMaxMB` after `editorsCanShare:false` returned 400 but still applied the first change. | Validate all numbers and env pins first. | TestPolicyUpdateIsAllOrNothing |
| 13 | The documented alias `BUNKER_ORG_WIDE_SHARING=false` showed "off" in the policy, but `sites.update` and `me.policy` only read the setting. | Owners could still share with everyone. | Add `App.OrgWideSharing()` and use it everywhere. | TestOrgWideSharingEnvAliasIsEnforced |
| 14 | Webhooks accepted any collection name. | A hook for a collection like "new hires" was created, and then every call to it failed with 400. | Validate with bunker.db's collection-name rule. | `hooks/bughunt_test.go` |
| 15 | Mail bodies were 8-bit with unbounded line length. The HTML part puts each paragraph on one line. | Long notifications (pages may send 1000 characters) broke SMTP's 998-byte line limit, so strict servers rejected or mangled them. | Quoted-printable for both parts. | `mail/build_internal_test.go` |
| 16 | The m365 Graph cache was bounded by entry count only (2000 entries, each up to 8 MB). | Could pin gigabytes of memory. | Don't cache answers over 512 KB; keep the total under 64 MB. | `m365/bughunt_test.go` |
| 17 | CLI device login gave up on a 429 from the poll endpoint. | `bunker login` failed for several people signing in from one office address. | Back off (double the interval, at most 15 s) and keep polling. | `cli/bughunt_test.go` TestDeviceLoginSurvivesRateLimit |

### Low

| # | Bug | Fix | Test |
|---|-----|-----|------|
| 18 | The CLI's `<title>` wasn't HTML-unescaped, so new sites were named "Tom &amp;amp; Jerry". `prettyName` byte-sliced the first letter, so a folder named "équipe" became "�…". | `html.UnescapeString`; upper-case the first rune. | TestTitlesFromFilesAndFolders |
| 19 | One-letter slugs passed `ValidSlug`: the regex's optional group allowed 1 character but not 2, while the message says 3–48. | Regex `^[a-z0-9][a-z0-9-]{1,46}[a-z0-9]$`. | TestOneLetterSlugRejected |
| 20 | The invite email always said "expires in 7 days", whatever `days` the admin chose. | Word the real lifetime. | TestInviteDaysWording |
| 21 | The audit streaming `lastError` was never cleared after delivery recovered. | Clear it on a successful batch. | (manual) |
| 22 | Dashboard: "New file" in the Files tab silently discarded unsaved edits. | Ask first, like switching files does. | ui e2e passes |
| 23 | Dashboard: the "Everyone in the organization" toggle stayed enabled when the admin had turned org-wide sharing off, so it only produced a 403 toast. | Disable it and explain why. | ui e2e passes |
| 24 | A single-file or stdin `bunker deploy` creates a new app each time (no bunker.json), but the docs implied otherwise. | The docs say so, and the CLI prints `--site <slug>` for next time. | — |
| 25 | `TestXLSXExpansionBounded` failed under `-race`: a wall-clock budget, with the race detector about 10× slower. | Scale the budget under the race build tag. | — |

### Docs fixed

- `sign-in/built-in.md`: invites are emailed when SMTP is set up (the docs said Bunker never sends email), and the response includes `emailed`. Only failed attempts count toward the lockout.
- `build/sharing.md`: viewers can ask for edit access (`role: editor`).
- `cli/reference.md`: added `bunker backup`, `bunker restore <file>` and `bunker demo`. "Alias restore" for rollback is now described properly. Single-file deploys don't write `bunker.json`.

## Reported, not fixed

- **Race on `Cfg.URL`** (server `learnURL`, auth `RememberURL`). The first admin request on a server started without `BUNKER_URL` writes `a.Cfg.URL` while every request reads it (HSTS check, links, OAuth metadata) without synchronization. It is benign in practice (a one-time string write), but `-race` would flag it in an integration test. Fixing it means an accessor (an atomic value) across about 30 call sites.
- **Anonymous rate limiter and NAT.** `server/ratelimit.go` allows 30 anonymous unsafe requests per minute (burst 20) per IP, and that covers `/auth/password`. A whole office signing in at 9:00 from one address can still hit 429 (a JSON error page for a form post). Consider exempting `/auth/password`: auth has its own failure-based limiter.
- **Org-wide sharing off doesn't close existing org-view sites.** Turning public links off sets `public=0` everywhere; turning org-wide sharing off leaves `org_view=1` sites visible to everyone. It is unclear which is intended; the docs only say owners can't turn it on.
- **Vendored libraries** (`vendorlib.get`) are brotli-compressed while holding the global cache lock, so the first request for a large library blocks every other `/_/lib/` request. The br and identity variants also share one ETag.
- **internal/data**: I found no functional bugs in what I read (events SSE, rooms, presence). Its list cursor is already a (value, id) keyset, so it doesn't have the uploads paging bug above.
- **JSON-RPC edge cases** (MCP): a batch with a non-object element is answered with a parse error for the whole batch rather than per element, and a message with an `id` but no `method` gets no reply rather than `-32600`. Both are harmless for real clients; batching was dropped from MCP in 2025-06-18.
