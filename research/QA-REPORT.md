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
