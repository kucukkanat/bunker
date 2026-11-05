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
4. **Pages never hold keys.** Data, AI and Microsoft 365 calls go through Bunker with the viewer's own session. Uploads that contain API keys are refused.
5. **Everything is recorded.** Sign-ins, publishes, shares, views and admin changes go to the audit log.

## What works today, and what's in preview

Bunker is young and built in the open. Pages and features that are still being built carry a **Preview** badge. The rest is in the code now.

| In the code now | Preview |
|---|---|
| The dashboard: drag-and-drop home, builder chat with live preview, data, files, versions, sharing, admin | App types beyond HTML (Markdown, CSV/Excel, notebooks, PDF, JSX, images) |
| Built-in accounts (argon2id, invites, reset links) and optional Entra ID, Google Workspace or OIDC sign-in | |
| Sites at `/s/<name>/`; uploads of files, folders, zips and JSON | |
| Versions, drafts, restore, file-level diffs, duplicate, zip export | `bunker.m365` (delegated Microsoft 365) and connections to internal APIs |
| Sharing with people, groups or everyone; access requests; notifications | Audit page, export and streaming; admin statistics |
| The `window.bunker` SDK: data with rules and live updates, files, storage, rooms, AI, notify | Entra auto-setup, `bunker backup`, template gallery |
| AI providers (Anthropic, OpenAI, Azure OpenAI, OpenAI-compatible), daily quotas, the AI builder | |
| CLI, local and remote MCP with OAuth, `/llms.txt` | |
| Built-in front-end libraries with CDN rewrite | |
| Sandbox, signed content tickets, isolation headers, secret scanning, optional content domain | |
| Personal access tokens, device sign-in, audit log, rate limits, Prometheus metrics, automatic HTTPS | |

Every action in Bunker is an API operation, listed live at `GET /api/v1/ops`. Anything the dashboard does, a script can do today.
