---
title: Drop anything
description: Every kind of file Bunker turns into an app, what happens on upload, and the limits.
sidebar: { order: 1 }
---

Drop a file, a folder or a zip on the Bunker home page (or send it to `POST /api/v1/drop`), and Bunker turns it into an app. Updating an existing app works the same way with `POST /api/v1/sites/{site}/deploy`.

## What you can drop

| You drop | You get | Kind |
|---|---|---|
| A single `.html` file | That page, published as it is | `html` |
| A folder or `.zip` with `index.html` | A multi-page site. One wrapping folder is removed (`my-site/index.html` → `index.html`) | `html` |
| A project folder with build output | The build output: Bunker looks in `dist/`, `build/`, `out/`, `public/`, `_site/`, `site/` and `docs/` for an `index.html` | `html` |
| A Vite / React / Astro build that uses `/assets/...` paths | Works: root-relative URLs in HTML and CSS are rewritten to the app's own prefix | `html` |
| HTML that loads React, Vue, Alpine, Chart.js, Tailwind... from a CDN | Works: links to jsDelivr, unpkg, cdnjs, Tailwind's play CDN and code.jquery.com are swapped for [built-in copies](#built-in-libraries) | `html` |
| HTML with `<script type="text/babel">` (common in AI-made pages) | The JSX is compiled at publish time, the Babel script tag is dropped, and React comes from the built-in copy. `src="app.jsx"` files are inlined. `data-presets="typescript"` or a `.tsx` file switches on TypeScript | `react` |
| An unbuilt Vite project (`index.html` with `<script type="module" src="/src/main.tsx">`) | The entry is bundled with esbuild and the script tag points at the bundle. No `npm install` needed | `react` |
| A single `.jsx`, `.tsx`, `.ts` or `.js` file, or a small project with `App.jsx`, `main.tsx`, `src/index.tsx`... | A running app. Bundled with esbuild; the default export (or `App`) is mounted into `#root` unless your code renders itself. [Details](#react-and-typescript) | `react` |
| One `.md` file | A clean, readable page with a table of contents, highlighted code and copy buttons | `markdown` |
| A folder of `.md` files | A small docs site: sidebar, previous and next links, links between `.md` files fixed up. [Details](#markdown-and-docs-sites) | `markdown` |
| A `.md` file with `slides: true` (or `marp: true`) in its front matter | A slide deck, one slide per `---`. [Details](#slides) | `slides` |
| `.csv`, `.tsv`, `.xlsx`, `.xlsm` | A table app: search, sort, filter per column, column stats, a chart, and CSV or JSON export. Several files or sheets become tabs. [Details](#tables) | `table` |
| `.ipynb` notebook (one or several) | The notebook rendered with its saved outputs: text, tables, images, SVG and HTML output (in a sandboxed frame). It is not re-run | `notebook` |
| `.pdf` (one or several) | A [pdf.js](https://mozilla.github.io/pdf.js/) viewer with zoom, page count and a download button; several PDFs become tabs | `pdf` |
| Images (`.png`, `.jpg`, `.gif`, `.webp`, `.avif`, `.svg`, `.bmp`, `.ico`), optionally with videos | A gallery with a lightbox (keyboard and swipe). Audio files are listed below it | `gallery` |
| Video and audio only (`.mp4`, `.webm`, `.mov`, `.m4v`, `.ogv`, `.mp3`, `.wav`, `.ogg`, `.m4a`, `.aac`, `.flac`, `.opus`) | A player with a playlist and download link | `media` |
| A single `.json` or `.geojson` file | A collapsible tree viewer with find, expand all and a raw view. Invalid JSON is shown as text | `json` |
| `.txt`, `.text`, `.log` files | A readable page per file (monospaced when it looks like code or columns) | `text` |
| Anything else without an HTML page | A plain list of the files, so links to them work | `files` |
| A sentence: "a holiday tracker where managers approve requests" | An app built by the [AI builder](/bunker/docs/build/ai-builder/) | |

The upload tells you what it made in `kind` (for example `"kind": "table"`).

### How Bunker picks

Bunker only converts an upload that has **no HTML page**. If there is an `.html` file anywhere, your HTML wins and nothing is generated (except compiling `text/babel` scripts and bundling an unbuilt Vite entry).

Otherwise the first match wins:

1. Any Markdown file → docs site or slides. (A React project with a `README.md` and no `index.html` becomes docs. Add an `index.html`, or leave the README out.)
2. A script entry (`index.tsx`, `main.jsx`, `App.tsx`, `src/main.tsx`, ... or the only script file) → bundled app.
3. Any notebook → notebook pages.
4. Only spreadsheets (plus CSS, text or JSON files) → table app.
5. Only PDFs (plus text) → PDF viewer.
6. Images, with or without video and audio → gallery.
7. Only video and audio → player.
8. Exactly one JSON file → JSON viewer.
9. Only text files → text pages.

Generated support files go in `_bunker/` inside the app. Nothing loads from the internet.

### Try it

```sh
# The CLI: one file or a folder
bunker deploy sales.xlsx
bunker deploy ./handbook        # a folder of Markdown

# The API
curl -X POST https://bunker.example.com/api/v1/drop \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@sales.xlsx"
# {"slug":"sales","kind":"table","version":1,"url":"https://bunker.example.com/s/sales/",...}
```

### Markdown and docs sites

- GitHub-flavoured Markdown: tables, task lists, footnotes, definition lists, smart quotes, heading anchors.
- `README.md` or `index.md` becomes the home page. Other files become `name.html`, keeping their folders.
- The page title comes from front matter `title:`, else the first `#` heading, else the file name.
- Order pages with front matter `order:` (also `nav_order`, `sidebar_position`, `weight`) or a number prefix in the file name (`01-intro.md`). Root pages come first, then folders A to Z.
- `site:` in the home page's front matter names the whole site.
- Links to other `.md` files are rewritten to the generated pages, `#anchors` included.
- Raw HTML in Markdown is kept.

### Slides

Turn a Markdown file into a deck with front matter:

```md
---
slides: true
title: Q3 review
---

# Q3 review
Finance team

---

## Revenue
- Up 12%
- Two new regions

---

<!-- class: lead -->
## Questions?
```

- Each `---` line starts a new slide (not inside code blocks).
- Arrow keys, Space, Page Up and Page Down move; Home and End jump; `f` toggles full screen. Click the left or right of a slide, or swipe on a phone.
- Slides scale to fit 16:9. The address ends in `#3` for slide 3, so you can link to a slide.
- A short first slide is styled as a title slide. `lead: false` turns that off. `<!-- class: name -->` adds a class to one slide.
- Slide numbers show by default. `paginate: false` hides them.
- In a docs folder, a file with `slides: true` becomes a deck page and the rest stay docs.

### Tables

- The first non-empty row is the header. Column types (number, date, yes/no, text) are detected, including currency signs and `%`.
- CSV separators are detected (comma, semicolon or tab). Files saved by Excel in Windows-1252 are read correctly.
- `.xlsx` sheets are read as values: formulas show their last saved result, dates use the cell format. Every sheet becomes a tab.
- Up to 100,000 rows per sheet, and 2 million cells per workbook.
- The table app is a viewer. The data is baked into the page at publish time. To change it, upload a new file (that's a new [version](/bunker/docs/build/versions/)). For a shared table people edit, start from a [template](/bunker/docs/build/templates/) or ask the [AI builder](/bunker/docs/build/ai-builder/); those use [`bunker.db`](/bunker/docs/sdk/db/).

### React and TypeScript

Single components and small projects are bundled at publish time with [esbuild](https://esbuild.github.io/), inside Bunker. There's no `npm install`, so imports resolve to what Bunker ships:

| Import | Comes from |
|---|---|
| `react`, `react-dom`, `react-dom/client`, `react/jsx-runtime` | [Preact](https://preactjs.com/) 10.26.9 through `preact/compat` (small, and it runs almost all React code) |
| `preact`, `preact/hooks`, `preact/compat`, `htm`, `htm/preact` | Preact 10.26.9 and htm 3.1.1 |
| `chart.js`, `d3`, `dayjs`, `lodash`, `marked`, `papaparse`, `vue`... | The [built-in libraries](#built-in-libraries), loaded from `/_/lib/` |
| `./Button`, `./data.json`, `./styles.css`, `./logo.png` | Your own files. CSS is bundled; images and fonts are inlined |

Any other package fails the upload with a clear message (`the package "axios" isn't available in Bunker`). Pages reach data and services through the [SDK](/bunker/docs/sdk/overview/) instead. CSS imports from packages, such as `bootstrap/dist/css/bootstrap.css`, use the built-in copy when there is one and are skipped otherwise.

If a build fails, the upload is refused with the file, line and error (`422 build_failed`), and nothing is published.

`text/babel` pages are different: they keep real React 18.3.1 from the built-in libraries, and are compiled script by script.

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
| leaflet | 1.9.4 | `L` (no map tiles offline: use `L.CRS.Simple` with your own image, e.g. a floor plan) |

Web fonts from Google Fonts and similar can't load in the sandbox. Bundle font files with your app, or use system fonts.
