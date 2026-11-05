---
title: Custom 404, SPA and other settings
description: Single-page app routing, a custom not-found page, a different start page, and hiding the Bunker badge.
sidebar: { order: 6 }
---

Each app has a few settings. Editors change them in the app's settings, or with `sites.update`:

```sh
curl -X PATCH https://bunker.example.com/api/v1/sites/my-app \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"settings": {"spa": true, "notFound": "", "entry": "", "hideBadge": false}}'
```

| Setting | Default | What it does |
|---|---|---|
| `spa` | `false` | **Single-page app.** Unknown paths without a file extension (`/orders/42`) serve `index.html`, so client-side routers (React Router, Vue Router) work on reload and deep links |
| `notFound` | `""` | **Custom 404 page.** A file path like `missing.html`. Without it, a `404.html` in the app is used if present. The status stays 404 |
| `entry` | `""` | **Start page.** Serve this file at `/s/<app>/` instead of `index.html` |
| `hideBadge` | `false` | Hide the small Bunker badge for viewers. Editors always see it, because it holds the edit and share controls |

Send the whole `settings` object: it replaces the previous one.

## How paths resolve

For a request to `/s/<app>/<path>`, Bunker tries, in order:

1. the exact file,
2. `<path>/index.html`,
3. `<path>.html`,
4. `index.html`, if `spa` is on and the path has no extension,
5. the custom 404 page, or `404.html`.

So `/s/<app>/about` finds `about.html` or `about/index.html`. Query strings are passed to your page unchanged.

## Other app details

`sites.update` also changes the `title` (1 to 120 characters), `description`, `icon` (an emoji), `orgView` (owner only) and `isTemplate`. Changing the address (`slug`) is owner only; old links stop working, so tell people.
