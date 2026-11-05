---
title: Drop anything
description: Every kind of file Bunker turns into an app, what happens on upload, and the limits.
sidebar: { order: 1 }
---

Drop a file, a folder or a zip on the Bunker home page (or send it to `POST /api/v1/drop`), and Bunker turns it into an app. Updating an existing app works the same way with `POST /api/v1/sites/{site}/deploy`.

## What you can drop

| You drop | You get | Status |
|---|---|---|
| A single `.html` file | That page, published as it is | In the code |
| A folder or `.zip` with `index.html` | A multi-page site. One wrapping folder is removed (`my-site/index.html` → `index.html`) | In the code |
| A project folder with build output | The build output: Bunker looks in `dist/`, `build/`, `out/`, `public/`, `_site/`, `site/` and `docs/` for an `index.html` | In the code |
| A Vite / React / Astro build that uses `/assets/...` paths | Works: root-relative URLs in HTML and CSS are rewritten to the app's own prefix | In the code |
| HTML that loads React, Vue, Alpine, Chart.js, Tailwind... from a CDN | Works: links to jsDelivr, unpkg, cdnjs, Tailwind's play CDN and code.jquery.com are swapped for [built-in copies](#built-in-libraries) | In the code |
| A single `.jsx` / `.tsx` component | A running React app, no build step | Preview |
| `.md` Markdown | A clean, readable page; a folder of Markdown becomes a small docs site | Preview |
| `.md` with `---` between sections | Slides you present from the browser | Preview |
| `.csv`, `.tsv`, `.xlsx` | A shared table app: search, sort, and add or edit rows according to permissions, backed by the app's database | Preview |
| `.json`, `.geojson` | A readable viewer | Preview |
| `.ipynb` notebook | The notebook rendered with its outputs and charts | Preview |
| `.pdf` | A built-in viewer that works inside the sandbox | Preview |
| Images (`.png`, `.jpg`, `.gif`, `.webp`, `.avif`, `.svg`) | A gallery | Preview |
| Video and audio (`.mp4`, `.webm`, `.mov`, `.mp3`, `.wav`, ...) | A player page | Preview |
| A sentence: "a holiday tracker where managers approve requests" | An app built by the [AI builder](/bunker/docs/build/ai-builder/) | In the code (API) |

Until a file type gets its own app, files without an HTML page are still stored and served, so links to them work.

## What Bunker does on every upload

1. **Cleans paths.** Backslashes become slashes, leading slashes go, and any path with `..` is rejected (no "zip slip").
2. **Skips junk:** `.DS_Store`, `Thumbs.db`, `desktop.ini`, `._*` files, and `.git/`, `node_modules/`, `__MACOSX/`, `.svn/`, `.hg/`, `.idea/`, `.vscode/` folders.
3. **Blocks executables:** `.exe`, `.dll`, `.msi`, `.bat`, `.cmd`, `.ps1`, `.scr`, `.com`, `.vbs`, `.jar`, `.app`, `.dmg`, `.pkg`, `.deb`, `.rpm`, `.sh`.
4. **Scans for secrets.** If a text file contains something that looks like an AWS key, GitHub token, OpenAI or Anthropic key, Slack token, Stripe secret key, Google API key, private key, Azure storage key or Bunker token, the upload is refused with the file and line number. Pages never need keys: Bunker calls services for you.
5. **Stores each file once** by its SHA-256 hash, and makes a compressed copy for fast serving.
6. **Publishes a new version** (or saves a draft with `publish=false`) and tells you about anything odd in `warnings`.

## Limits

| | Default | Setting |
|---|---|---|
| One upload | 100 MB | `BUNKER_UPLOAD_MAX_MB` |
| One app (all files) | 100 MB | `BUNKER_SITE_MAX_MB` |
| Files in a zip | 20,000 | |
| Path length | 400 characters | |
| Apps per person | 500 | `BUNKER_MAX_SITES_PER_USER` |

## Upload formats (API)

`POST /api/v1/drop` (new app) and `POST /api/v1/sites/{site}/deploy` (existing app) accept:

- **multipart/form-data** with one or more files. Use the form field name `file:<relative/path>` to keep folder structure. Extra fields: `title`, `slug`, `message`, `publish`.
- **a .zip** as one of those files.
- **JSON**: `{"files": {"index.html": "<!doctype html>...", "logo.png": {"base64": "..."}}, "message": "...", "publish": true}`.

## Built-in libraries

Pages can't load scripts from the internet. Bunker ships popular libraries and serves them from `/_/lib/<name>/<file>`; CDN links are rewritten to them at publish time. The current list is at `GET /api/v1/libraries`:

| Library | Version | Global |
|---|---|---|
| react, react-dom | 18.3.1 | `React`, `ReactDOM` |
| vue | 3.5.44 | `Vue` |
| alpinejs | 3.17.5 | `Alpine` |
| htmx.org | 2.0.11 | `htmx` |
| jquery | 3.7.1 | `jQuery` |
| tailwindcss (browser build) | 4.3.4 | |
| bootstrap (JS + CSS) | 5.3.8 | `bootstrap` |
| chart.js, chartjs-adapter-date-fns | 4.6.1, 3.0.0 | `Chart` |
| d3 | 7.9.2 | `d3` |
| dayjs | 1.11.23 | `dayjs` |
| lodash | 4.18.1 | `_` |
| marked | 14.1.4 | `marked` |
| papaparse | 5.7.0 | `Papa` |
| sortablejs | 1.15.7 | `Sortable` |
| canvas-confetti | 1.9.4 | `confetti` |

Web fonts from Google Fonts and similar can't load in the sandbox. Bundle font files with your app, or use system fonts.
