---
title: Sandbox & CSP
description: Exactly what a page can and can't do.
sidebar: { order: 2 }
---

## The frame
User files are served from `/s/<site>/raw/…` and only render inside:

```html
<iframe
  sandbox="allow-scripts allow-forms allow-popups allow-downloads"
  src="/s/<site>/raw/index.html">
</iframe>
```

There's no `allow-same-origin`, so the page gets an **opaque origin**. As a result:
- it can't read or send Bunker's cookies,
- it can't access `localStorage`, `IndexedDB` or `document.cookie` (use [`bunker.storage`](/bunker/docs/sdk/storage/)),
- it can't script the parent page or other sites.

Opening a `/raw/` URL directly returns `403` unless the request comes from Bunker's shell. Every raw response also sends `Content-Security-Policy: sandbox …`, so the page is sandboxed even if it is opened some other way.

## Content Security Policy
```
default-src 'none';
script-src 'self' 'unsafe-inline' 'unsafe-eval';
style-src 'self' 'unsafe-inline';
img-src 'self' data: blob:;
media-src 'self' blob:;
font-src 'self' data:;
connect-src 'none';
form-action 'none';
frame-ancestors 'self';
base-uri 'none';
```

Inline scripts are allowed because AI-written pages use them. That is safe here because `connect-src 'none'`, `form-action 'none'` and the opaque origin leave no path for data to leave except through the shell.

## What this means for builders
| Works | Doesn't |
|---|---|
| Plain HTML/CSS/JS, bundled libraries | Loading scripts from CDNs |
| Charts, tables, forms, canvas | `fetch()` to any URL |
| `bunker.db`, `files`, `ai`, `m365` | Cookies, `localStorage` |
| Links that open in a new tab | Embedding other websites |

Bunker hosts a small library of common packages (charting, date pickers, markdown) at `/lib/`, which pages may load.
