---
title: Sandbox limits
description: What an app can't do inside Bunker's sandbox, and what to do instead.
sidebar: { order: 11 }
---

Apps run in a sandboxed iframe with an opaque origin and a strict Content Security Policy. These limits are what keep people's data safe. Here is what that means when you build.

| You can't | Do this instead |
|---|---|
| Call outside websites or APIs (`fetch('https://...')`) | A [server function](/bunker/docs/build/server-functions/) with `fetch` and a secret, [`bunker.connections`](/bunker/docs/sdk/connections/) for company APIs, [`bunker.ai`](/bunker/docs/sdk/ai/) for AI, [`bunker.m365`](/bunker/docs/sdk/m365/) for Microsoft 365 |
| Load scripts, styles or fonts from CDNs | Use the [built-in libraries](/bunker/docs/build/drop-anything/#built-in-libraries) at `/_/lib/...` (CDN links to them are rewritten automatically), or bundle files with your app. Use system fonts |
| Show images from other websites | Upload them with the app, or with [`bunker.files`](/bunker/docs/sdk/files/) |
| Open popups | [`bunker.open(url)`](/bunker/docs/sdk/ui/#bunkeropen); external links, `target="_blank"` and `window.open()` are routed through it automatically |
| Navigate the top window | Link between your own pages normally |
| Submit forms to a URL | Handle `submit` in JavaScript (`event.preventDefault()`) and save with [`bunker.db`](/bunker/docs/sdk/db/) |
| Use real cookies, IndexedDB or service workers | [`bunker.storage`](/bunker/docs/sdk/storage/) (per person) or `bunker.db` (shared). `localStorage`, `sessionStorage` and `document.cookie` are [shimmed](/bunker/docs/sdk/storage/#localstorage-sessionstorage-and-cookies-just-work) so libraries keep working |
| Use the camera, microphone, location, USB, serial, HID, payments, screen capture or clipboard reading | Not available. File inputs and drag-and-drop work |
| Put API keys or tokens in files | They're blocked at upload. Store them as [function secrets](/bunker/docs/build/server-functions/#secrets), or let Bunker call services for you |
| Run code when nobody has the page open | A [server function](/bunker/docs/build/server-functions/) on a schedule, after a write or from a webhook |
| Embed `<object>`/`<embed>`, or set `<base>` | Not needed for normal pages |
| Use WebRTC | Not available (blocked by `Connection-Allowlist` on Chromium and Edge, and removed by the SDK elsewhere) |
| Be framed by other websites | Only Bunker can show your app |

## What does work

- **Fetching your own files**: `fetch('data.json')`, `fetch('./reports/q3.csv')`, ES modules, workers, WebAssembly (`'wasm-unsafe-eval'` is allowed).
- **Inline scripts and styles**, `eval` (some frameworks need it), `blob:` and `data:` URLs for images, media and workers.
- **Alerts, confirms and prompts** (`allow-modals`) and **downloads** (`allow-downloads`).
- **Client-side routing**: turn on [SPA mode](/bunker/docs/build/site-settings/).
- **Root-relative paths** like `/assets/app.js`: rewritten to your app's prefix in HTML and CSS. Paths built in JavaScript at run time are not rewritten, so prefer relative paths (`assets/app.js`).

## Server functions have their own limits

Code in `api/` runs on the server, not in this sandbox: no DOM, but `fetch` and secrets. Its memory, time and call limits are in [Server functions](/bunker/docs/build/server-functions/#limits).

## Why it's built like this

An app is code written by a coworker, a friend or an AI, and opened by other people while they're signed in. The sandbox makes sure that code can only reach what Bunker hands it, with the viewer's permission, through calls Bunker checks. See the [threat model](/bunker/docs/security/threat-model/).
