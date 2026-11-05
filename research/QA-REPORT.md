# QA walkthrough: real-user journeys (November 2026)

A fresh-eyes pass through Bunker as a non-technical person would meet it:
personal install without dev mode, inviting a friend, dropping every file
type, the eight templates with two people, the AI builder against a fake
OpenAI-compatible provider, sharing and access requests, the CLI, phones,
dark mode and failure states. Every issue below was fixed unless marked
**open**. Screenshots were taken at deviceScaleFactor 1 (before: `qa/before/`,
after: `qa/after/` in the session scratchpad).

## 1. First run and invites

| Issue | Fix |
|---|---|
| After creating the admin account, setup sent you to a sign-in page to type the same password again. | Setup signs you in right away; "Open Bunker" goes to the welcome page. |
| Invite and reset links in Admin → People were **relative** (`/auth/invite?token=…`) when `BUNKER_URL` wasn't known, so a pasted link didn't open for the friend. | The dialog shows a full address. |
| Invite page said "Join Bunker" + a bare email; a short password led to a dead-end error page. | "Welcome to Bunker — choose a password to finish setting up your account for …"; short password keeps the form with the reason (422). |
| Used, expired and malformed links all said "This link has expired" with a "Try again" link to sign-in. | Three clear messages: "You already joined" (sign-in button with email prefilled), "This link has expired", "This link doesn't work" (410/404). |
| Sign-in pages used Georgia/system fonts; error text failed contrast; after a wrong password focus went back to the email. | Brand fonts (font-src 'self'), darker error text, password field focused, "Sign in to Bunker". |
| The internal "Bunker / templates@bunker.local (Disabled)" account showed up in Admin → People. | Hidden from users.list. |
| Welcome steps squeezed title and description side by side. | Title above description (CSS selector was hitting nested divs). |
| AI setup link in the dark Describe card was nearly invisible. | Light link color. |

## 2. Dropping files

All 13 types rendered in the sandbox (HTML, folder, zip, Markdown, Markdown
folder, CSV, xlsx, images, PDF, ipynb, JSX, CDN Tailwind + Chart.js, Babel).

| Issue | Fix |
|---|---|
| Refused uploads (key found, program inside, too big) only flashed a toast that vanished; the drop zone said "That didn't work." | The drop zone itself explains: "We found a password or key in your files — index.html (line 1) looks like an Anthropic API key. Remove it and try again…", "That upload has a program in it", "That's too big for one site (limit)", "Bunker isn't answering". |
| Upload warnings (e.g. internet libraries that won't load) were returned but never shown. | Shown as a "Heads up" toast after the upload (home and workspace). |
| Site names: a folder became "About" (first page alphabetically), Markdown "meeting notes", images "forest", PDF "hello". | Start page's `<title>`, then Markdown front-matter title / first `# Heading`, then the folder name, "Gallery" for images, capitalized file name. Unit test added. |
| `sk-ant-…` keys were reported as "a OpenAI API key"; messages used `file:line` and capitalized file names ("Index.html"). | Anthropic matched first, "an", "index.html (line 1)", file names keep their case (dashboard and CLI). |
| **CSV / Excel tables rendered header and rows side by side** (kit.css `.grid` made the table a CSS grid). | `table.grid{display:table}`; also undid kit.css's centered header, carded article and uppercase table headers on generated pages. e2e check added. |
| Markdown: hidden `#` anchor indented headings; a short single page was pushed left by an empty contents column. | Anchor after the heading text; no empty column. |

## 3. Templates with two people

| Issue | Fix |
|---|---|
| Requests & approvals: the site owner (an admin) never saw "To approve" — the page looked for `me.isAdmin`, which the page API never sends. | Owner/editors approve; approvers-group membership via `me.inGroup`. Verified: friend requests, owner approves, friend sees "Approved by Ada". |
| Team tracker: a viewer couldn't move a teammate's card ("you can only change entries you added yourself"); the fix needed a data-rule change most people won't find. | Built-in templates carry data rules (tracker `cards`, meeting notes `meetings` → open) and copies inherit their template's rules. Verified live in two browsers. Test added. |
| Tracker "Add" button taller than its input; Done column cut off in the preview. | Tidied. |

## 4. AI builder (fake OpenAI-compatible server, streaming + tool calls)

Describe → builder streaming → files → preview → follow-up → publish all worked.

| Issue | Fix |
|---|---|
| Fallback site name "Page Where My Team" when the model doesn't suggest one. | "Book the meeting room" (keeps what people do). Unit test added. |
| Admin → AI hints were Azure-only for every provider. | Hints follow the provider (local model name, optional key, Azure deployment). |
| **Open:** the preview is a blank white frame until the first file is written. | — |

## 5. Sharing and access requests

Share dialog, typeahead, notifications, "Ask for access", bell → share dialog
with Approve, and org-wide all worked end to end.

Added on request: **public links** (switch with warning + confirm, "Public"
pill on cards and workspace, Admin → Sharing toggle) and **webhooks** in the
Data tab (URL shown once with copy + curl example, calls/last used, delete).
Found while doing it: dates in Data rows showed "Invalid Date" (ISO strings) — fixed.

## 6. CLI

| Issue | Fix |
|---|---|
| `bunker login` printed "Open /device?code=…" and `bunker deploy` printed "/s/site/" when the server didn't know its address. | Full addresses based on the server you signed in to. |
| Device page: "Go back to bunker cli on vm" (lower-cased). | Keeps the tool's name. |

## 7. Phones and dark mode

| Issue | Fix |
|---|---|
| **Admin pages on a phone zoomed out to ~800px**; the Invite button was off screen. The one-column grid track grew with the admin tab row. The e2e check missed it because the layout viewport grows too. | `minmax(0,1fr)`; list rows wrap; e2e compares against the 390px device width and covers audit and templates. |
| Dark mode: no issues found on home, workspace, admin, templates, welcome, sign-in. | — |

## 8. Failure states

| Issue | Fix |
|---|---|
| Server stopped while on the dashboard: no sign anything was wrong; stacked duplicate toasts; a view that failed to load showed bare text. | "Can't reach Bunker. Trying again…" pill and "You're back online"; identical toasts replace each other; friendly "This page didn't load" with Try again. |
| On SIGTERM the server kept live streams open until the 10 s shutdown timeout, so open tabs didn't notice a restart. | Streams end 1.5 s into shutdown. |
| Expired access-request form returned a lowercase plain-text error. | Plain sentence. |

## Coordinator requests

- Setup / Admin → Sign-in: "Set up automatically with Microsoft" drives
  `/setup/entra/auto/start` + `/poll` (code with copy/open, status, done),
  Azure Cloud Shell script fallback, client secret expiry in Admin → Sign-in.
- Admin → Audit "Export CSV" uses `/admin/audit/export`.
- Admin → Sharing uses `/admin/policy` (org-wide, editors share, public
  links, size and site limits, "set by the server" locks).

## Still open

- Builder preview is blank until the first file is written.
- Invites "Waiting to join" can't re-send a link (the old token is hashed;
  re-inviting would drop the groups, which the list doesn't return).
- Generated pages and templates fall back to Georgia (the sandbox can't load
  the brand fonts).

# Round 2 (November 2026): features added since round 1

Walked through for real in Chromium (Playwright) against fresh servers:
`bunker demo`, `bunker dev`, and a non-dev server set up from `/setup`.
Fakes: a 40-line asyncio SMTP server, a `http.server` answering
`/v1/models` on 11434 (Ollama). Fixed unless marked **open**.

## `bunker demo`

| Issue | Fix |
|---|---|
| The offsite poll said "4 votes" but every option showed 0: the seeded votes ("Lisbon", "Berlin") weren't options of the template. | Votes use the template's options (Mountain lodge 2, Lake cabins 1, City hotel 1). |
| "Lunch on Friday" had no orders and wasn't shared, so teammates' data never showed there. | Shared with Team; Maya, Tom and Léa have ordered. |
| Every demo site was "Owner": the "Shared with me" part of Home never appeared. | Maya owns a **Kudos wall** shared with you and the team, with three kudos. |
| Terminal said only "running in dev mode". | "Bunker demo is running with example apps, teammates and data", plus the data folder. |
| `/auth/dev?as=maya@example.com` (no `name`) renamed Maya to "maya" and dropped her Team group, so kudos showed "from bob" etc. | Switching to an existing person keeps their name and groups unless new ones are given. |

Kanban, requests (owner sees "To approve 3"), Q3 budget table, handbook: all fine.

## Templates (owner, then a second person at 390px)

All 16 open without console errors as owner and as a viewer, no horizontal
scroll at 390px. Viewer interactions checked: inventory "Add a few
examples" (open rule), OKR "Add an example", room booking (owner adds rooms,
viewer sees the grid), kudos posted by the viewer shows for the owner.
Templates page subtitle said "from your coworkers" above 16 cards "by
Bunker": now "by Bunker and your coworkers".

## GitHub import

Pasting `…/tree/master/brand` on Home works (toast → Import → site).

| Issue | Fix |
|---|---|
| Site was named "brand" (folder name, lowercase) though the README says "Bunker brand kit". | Name comes from the start page's `<title>` / README heading, else the capitalized folder or repo name. Test updated. |
| Pasting the link into the search box (the obvious text field on Home) did nothing. | A GitHub link pasted there offers the import too; folder links say "this GitHub folder". |

## Public links

Admin → Sharing toggle, Share dialog switch with confirm, "Public" pills: fine.

| Issue | Fix |
|---|---|
| **A public site that uses data (e.g. a poll) was blank for visitors**: `bunker.me()` failed with 401, so the page never rendered; the shell kept retrying `/events` and `/kv` (5 × 401 per load). | The shell knows the visitor is a guest: `bunker.me()` returns `{id:0, name:"Guest", guest:true}`, storage starts empty, live updates aren't opened, other calls fail at once with "This is a public page: you can look, but saving or loading its data needs a Bunker account." Templates show "You're viewing a public page. Sign in to Bunker to take part…" and render empty. Badge panel says "Public page · not signed in" and offers "Sign in to Bunker". |
| Share dialog still said "Only you can open this site right now." after turning the public link (or org-wide view) on. | Says anyone with the public link / everyone in the organization can view it. |
| "They can't sign in or change data" undersold it: they can't *see* data either. | "They see the page only: its data, files and AI need a Bunker account." Docs (sharing.md) describe the guest `me()`. |

Existing installs keep their copies of built-in templates (never overwritten),
so the template banner only reaches new installs; the shell part applies everywhere.

## Webhooks

Create → URL once with copy + curl example → JSON and form posts both 201.

| Issue | Fix |
|---|---|
| **Rows didn't appear until a reload** (docs promise live updates). | Data tab subscribes to its collections and to webhook target collections, and refreshes (keeping the selected collection, not while a cell is being edited). |

## Email

Admin → Email with `smtp://127.0.0.1:PORT`: "Email is on", test email arrives
("Bunker email works", text + HTML).

| Issue | Fix |
|---|---|
| **Invite email had no usable link** when the server didn't know its address (reached by IP, no `BUNKER_URL`; the learned URL ignores IPs): text said `Join Bunker: /auth/invite?token=…`, HTML had no button. Same for reset links. | Invite/reset emails use the address the admin is using; emails never print a relative link. Unit test added. Docs explain which address links use and that a password-less local relay works without STARTTLS. |
| **Open**: invite emails end with "You can turn off email notifications in Settings" to someone who has no account yet. | — |

## AI: "Found on this machine"

Fake Ollama detected: "Use Ollama on this machine (llama3.2:3b)" → AI on,
endpoint and models filled, Home's "needs an API key" note gone. Fine.
**Open** (minor): it picks the first model; a coder model would suit the builder better.

## Admin overview

"Recently changed sites" was full of built-in templates on a new install; they're left out now.
Update notice: not testable from outside (`LatestReleaseURL` is a test-only variable), skipped.

## First run (non-dev, 390px, dark)

| Issue | Fix |
|---|---|
| Setup step 1: the two sign-in choice cards stuck out of their card (page 395px wide). | Grid column `minmax(0,1fr)`, text can shrink. |
| Welcome page: "Make my first site" + "Go to Home" overflowed the card. | Buttons wrap; padding scales on small screens; it greets by first name like the modal. |
| New site → Upload said "A single HTML file, a folder, or a .zip." | Lists spreadsheets, Markdown, images, PDFs as Home does. |

Docs checked against behavior: webhooks, admin/email, sharing (public links),
drop-anything (GitHub import), quickstart (demo), templates (rules are
changed in the Data tab, not "settings"); wording fixed where it differed.

Suites: `go test ./internal/...`, `ui.test.mjs` (23), `sdk.test.mjs` (69), `cli.test.sh` (21) all pass.
