---
title: Roadmap
description: What's in Bunker today, what's next, and what comes later.
sidebar: { order: 1 }
---

Bunker is built in the open. Pages for features that aren't finished carry a **Preview** badge in these docs.

## In the code today

- **Accounts:** built-in email + password accounts with invites and reset links; optional Microsoft Entra ID (manual, or automatic setup with a device code or a Cloud Shell script), Google Workspace and OIDC sign-in; groups, admins, allowed groups, guest blocking; personal access tokens; device sign-in.
- **Apps:** upload files, folders, zips or JSON; build-output detection; root-path rewriting; built-in front-end libraries with CDN rewrite; versions, drafts, diffs, restore; duplicate and zip export; trash; transfer; templates flag and eight built-in templates; SPA, 404 and start-page settings.
- **App types:** Markdown docs sites and slides, CSV/TSV/Excel table apps, Jupyter notebooks, PDF (pdf.js), JSON viewer, image galleries, video and audio players, text pages, single-file React/JSX/TSX/TS (esbuild + Preact compat), unbuilt Vite projects and `text/babel` pages.
- **Sharing:** people, groups, everyone; optional public links (off until an admin allows them); access requests; notifications; view stats.
- **SDK:** `bunker.me`, `db` (rules, queries, live updates, export), `files`, `storage` (plus a working `localStorage`), `room` and presence, `ai`, `m365` (delegated SharePoint, Excel, OneDrive and Graph reads), `connections` (SSRF-safe internal APIs), `notify`, `toast`, `download`, `open`.
- **AI:** Anthropic, OpenAI, Azure OpenAI and OpenAI-compatible providers; daily allowances; the AI builder (edits the draft and offers to fix errors the preview reports); `bunker.ai` for pages.
- **Agents:** the CLI, local stdio MCP and remote MCP with OAuth, MCP Apps (Bunker apps open inside Claude, VS Code Copilot and Microsoft 365 Copilot), `/llms.txt`, `/api/v1/ops`, OpenAPI.
- **Security:** opaque-origin sandbox, signed content tickets, CSP, Connection-Allowlist, secret scanning, optional separate content domain, audit log with search, CSV/JSON export, webhook and syslog streaming, and retention.
- **Dashboard:** home with drag and drop, the builder chat with live preview, data, files, versions and settings tabs, the share dialog, people and AI admin.
- **Data in:** inbound webhooks that add JSON or form posts to an app's collection (Power Automate, Zapier, scripts).
- **Admin:** usage stats, sharing and size policy, moving someone's sites when they leave, idle-app archiving, Microsoft 365 settings (scope allowlist, per-app approval, SharePoint site list), connections.
- **Operations:** single binary, SQLite, one-click deploys (Azure, Render, Railway, Fly, Docker, Linux installer), automatic HTTPS, Prometheus metrics, backup download, scheduled snapshots, `bunker backup` and `bunker restore`; on a desktop the first start opens the setup page in the browser.

## Next

- Dashboard settings for audit streaming (the API works today; automatic Entra setup is in Setup and Admin → Sign-in).
- Editable table apps: CSV and Excel uploads that people can add rows to.
- First tagged release with signed binaries and a public container image.
- Arrow-key navigation inside the feedback template's star rating.

## Later

- Directory groups for Google Workspace.
- Litestream replication for platforms without block disks.

Follow progress and discuss on [GitHub](https://github.com/kucukkanat/bunker).
