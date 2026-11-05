---
title: Example agent prompt
description: A prompt to give a coding agent so it builds Bunker apps that work in the sandbox the first time.
sidebar: { order: 3 }
---

`bunker init` writes guidance like this into `AGENTS.md` for you, and agents connected over [MCP](/bunker/docs/agents/mcp/) can read it from `get_sdk_docs` or `/llms.txt`. If your agent can't, paste this into its instructions.

```md title="AGENTS.md"
# Building for Bunker

This folder is a Bunker app: plain HTML, CSS and JavaScript served at
https://bunker.example.com/s/<app>/ inside a sandbox. Deploy with
`bunker deploy` (bunker.json remembers the app) or the MCP tool `deploy_files`.

## Sandbox rules
- index.html at the root is the start page. Use relative paths (style.css, not /style.css).
- No internet from the page: no CDNs, external fonts, images, scripts or APIs.
  fetch() only reaches this app's own files.
- Libraries are built in. Load them from /_/lib/, for example
  <script src="/_/lib/chart.js/chart.umd.min.js"></script>.
  Full list: GET /api/v1/libraries. No npm, no bare imports, no JSX build step.
- No popups or target="_blank". Use bunker.open(url) for external links.
- localStorage works (saved per person). Use bunker.storage for preferences, bunker.db for shared data.
- Forms don't submit to URLs: handle submit in JS and save with bunker.db.
- Never put API keys or tokens in files. Uploads containing them are rejected.

## SDK (window.bunker, already loaded; don't add a script tag)
- bunker.me() → { id, name, email, groups, role, canEdit }; bunker.me.inGroup(name)
- bunker.db.collection(name): add, get, set, update, delete, list → {items,next},
  query → array (.next), all, count, export('csv'), subscribe
  Rules: own (default) | private | read | append | open | group:<Name>.
  Pages can't change rules: set them with `bunker api db.rule --site <app> --coll <c> --rule <r>`.
- bunker.files.upload(file, { folder }) → { id, url }; list, get, read(id, 'text'), delete
- bunker.storage.get/set/remove (64 KB per value, per person)
- bunker.room(name): send, subscribe, presence, onPresence
- bunker.ai.chat(messages) / json(messages, { schema }) / stream(messages)
- bunker.notify({ to, title, body, path }), bunker.toast(text),
  bunker.download(name, data, type), bunker.open(url)
- Errors: err.code (forbidden, not_found, invalid, quota_exceeded,
  consent_required, unavailable) and a human err.message. Show the message.

## Style
- <link rel="stylesheet" href="/_/kit.css"> for clean defaults (light and dark).
- Mobile first, readable at 360px, accessible labels and focus states.
- Friendly empty, loading and error states.

## Workflow
1. Make changes. 2. `bunker deploy --draft`.
3. Check https://bunker.example.com/s/<app>/?draft=1. 4. Publish when it works.
Every publish is a version; `bunker rollback <app>` undoes a bad one.
```

Full details: [SDK reference](/bunker/docs/sdk/overview/) and [Sandbox limits](/bunker/docs/sdk/limits/).
