---
title: What is Bunker?
description: Bunker is a self-hosted, open-source private app cloud for you, your team or your whole company. Drop a file or describe an app and get a private, shareable web app.
sidebar: { order: 1 }
---

Bunker is **your own private app cloud**, for you, your team or your whole company. Drop in a file or folder, or describe an app to the AI builder, and get a web app at a link like `bunker.example.com/s/holiday-tracker`. You choose who can open it.

It runs on your own machine as **one Go binary and one data folder**, under the MIT license.

## Two ways to sign in

- **Built-in accounts** work out of the box. You create the first admin account on the setup page and invite people with one-time links. Nothing else to set up. Good for personal projects, families, clubs and small teams. [Built-in accounts and invites](/bunker/docs/sign-in/built-in/).
- **Company sign-in** is optional. When you're ready, switch on [Microsoft Entra ID](/bunker/docs/sign-in/entra-auto/), [Google Workspace](/bunker/docs/sign-in/google/) or any [OIDC provider](/bunker/docs/sign-in/oidc/). Then, if you like, turn built-in accounts off so everyone uses their work account.

## What people build with it

- A request form with an approval step, for HR or facilities.
- A shared tracker made from a spreadsheet someone already keeps.
- A dashboard a data analyst exported as HTML or a notebook.
- A one-page report, a slide deck, a PDF handbook.
- An app with a little backend: a nightly digest, a call to a payment or ticketing API, a check viewers can't skip.
- A personal tool, a family meal planner, a club sign-up sheet.
- Anything a coding agent wrote that "just needs to be online, privately".

## Who it's for

| You are | Start with |
|---|---|
| Someone with an idea (no code needed) | [60-second quickstart](/bunker/docs/start/quickstart/), then [Drop anything](/bunker/docs/build/drop-anything/) |
| The person who runs it | [Deploy](/bunker/docs/deploy/one-click/), then [Sign-in](/bunker/docs/sign-in/built-in/) |
| A security reviewer | [Threat model](/bunker/docs/security/threat-model/) and [Sandbox and headers](/bunker/docs/security/sandbox/) |
| A developer or agent | [CLI](/bunker/docs/cli/reference/), [MCP and agents](/bunker/docs/agents/mcp/), [SDK](/bunker/docs/sdk/overview/) |

## How it stays safe

1. **Only people you let in.** Built-in accounts exist only by invite. Company sign-in is single-tenant for Entra, needs your Workspace domain for Google, and Bunker refuses settings that would let personal accounts in.
2. **Private by default.** A new app is visible to its owner. Share it with people or groups as viewer or editor.
3. **Every app is sandboxed.** Apps run in an iframe with an opaque origin. They can't read the Bunker session, other apps or the internet.
4. **Pages never hold keys.** Data, AI and Microsoft 365 calls go through Bunker with the viewer's own session. Uploads that contain API keys are refused. Keys for outside services live in write-only secrets that only the app's server functions can use.
5. **Server code is sandboxed too.** [Server functions](/bunker/docs/build/server-functions/) run in a fresh WebAssembly sandbox for each call, with memory and time limits and the caller's own rights. Admins can turn them off, or limit where they can connect.
6. **Everything is recorded.** Sign-ins, publishes, shares, views and admin changes go to the audit log.

## What works today

Bunker is young and built in the open. Everything below is in the code now. If a page describes something that isn't finished, it carries a **Preview** badge. See the [roadmap](/bunker/docs/project/roadmap/) for what's next.

| Area | In the code now |
|---|---|
| Dashboard | Drag-and-drop home, builder chat with live preview, data, files, versions, sharing, templates, admin |
| Sign-in | Built-in accounts (argon2id, invites, reset links); Entra ID (manual or [automatic setup](/bunker/docs/sign-in/entra-auto/)), Google Workspace or OIDC |
| Apps | Sites at `/s/<name>/`; uploads of files, folders, zips and JSON; versions, drafts, restore, file-level diffs, duplicate, zip export |
| [App types](/bunker/docs/build/drop-anything/) | HTML, Markdown docs and slides, CSV/TSV/Excel table apps, notebooks, PDF, JSON, images, video and audio, text, single-file React/JSX/TSX, unbuilt Vite projects, `text/babel` pages |
| [Templates](/bunker/docs/build/templates/) | Eight built-in starters, and any app can be offered as a template |
| Sharing | People, groups or everyone; access requests; notifications |
| SDK | `window.bunker`: data with rules and live updates, files, storage, rooms, AI, notify, [Microsoft 365](/bunker/docs/sdk/m365/), [connections](/bunker/docs/sdk/connections/) |
| Backend | [Server functions](/bunker/docs/build/server-functions/) in `api/`: called from pages, on a schedule, after writes or from webhooks, with secrets and outbound `fetch` |
| AI | Anthropic, OpenAI, Azure OpenAI, OpenAI-compatible; daily quotas; the AI builder |
| Agents | CLI, local and remote MCP with OAuth, `/llms.txt` |
| Admin | Usage stats, policy, [audit search, export and SIEM streaming](/bunker/docs/admin/audit/), retention, [backups and scheduled snapshots](/bunker/docs/admin/backups/) |
| Security | Sandbox, signed content tickets, isolation headers, secret scanning, SSRF-safe connections, optional content domain |
| Operations | Personal access tokens, device sign-in, rate limits, Prometheus metrics, automatic HTTPS |

Every action in Bunker is an API operation, listed live at `GET /api/v1/ops`. Anything the dashboard does, a script can do today.
