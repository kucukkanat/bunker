---
title: Roadmap
description: What's in Bunker today, what's being built now, and what comes next.
sidebar: { order: 1 }
---

Bunker is built in the open. Pages for features that aren't finished carry a **Preview** badge in these docs.

## In the code today

- **Accounts:** built-in email + password accounts with invites and reset links; optional Microsoft Entra ID, Google Workspace and OIDC sign-in; groups, admins, allowed groups, guest blocking; personal access tokens; device sign-in.
- **Apps:** upload files, folders, zips or JSON; build-output detection; root-path rewriting; built-in front-end libraries with CDN rewrite; versions, drafts, diffs, restore; duplicate and zip export; trash; transfer; templates flag; SPA, 404 and start-page settings.
- **Sharing:** people, groups, everyone; access requests; notifications; view stats.
- **SDK:** `bunker.me`, `db` (rules, queries, live updates, export), `files`, `storage` (plus a working `localStorage`), `room` and presence, `ai`, `notify`, `toast`, `download`, `open`.
- **AI:** Anthropic, OpenAI, Azure OpenAI and OpenAI-compatible providers; daily allowances; the AI builder (edits the draft); `bunker.ai` for pages.
- **Agents:** the CLI, local stdio MCP and remote MCP with OAuth, `/llms.txt`, `/api/v1/ops`, OpenAPI.
- **Security:** opaque-origin sandbox, signed content tickets, CSP, Connection-Allowlist, secret scanning, optional separate content domain, audit log.
- **Dashboard:** home with drag and drop, the builder chat with live preview, data, files, versions and settings tabs, the share dialog, people and AI admin.
- **Operations:** single binary, SQLite, one-click deploys (Azure, Render, Railway, Fly, Docker, Linux installer), automatic HTTPS, Prometheus metrics.

## Being built now

- App types: Markdown, slides, CSV and Excel table apps, notebooks, PDF, single-file JSX/TSX, image and media galleries.
- `bunker.m365`: delegated SharePoint, Excel and OneDrive access with an admin scope allowlist.
- Connections to internal APIs with SSRF protection.
- Audit search, CSV/JSON export, syslog and webhook streaming; admin statistics and settings pages.
- `bunker backup` / `restore`.
- Entra auto-setup with a device code.
- A template gallery.

## Later

- MCP Apps: open Bunker apps inside Claude, Copilot and other MCP hosts.
- Directory groups for Google Workspace.
- Idle-app archiving and ownership hand-over when someone leaves.
- Litestream replication for platforms without block disks.

Follow progress and discuss on [GitHub](https://github.com/kucukkanat/bunker).
