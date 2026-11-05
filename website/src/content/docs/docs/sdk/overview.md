---
title: SDK overview
description: window.bunker gives sandboxed apps a database, files, storage, live rooms, AI and notifications, with the viewer's own permissions.
sidebar: { order: 1 }
---

Apps run in a sandbox with no cookies, no network and no keys. **`window.bunker`** is how they get things done. Every call runs with the **viewer's own permissions**, and Bunker checks the app, the viewer and their role on every call.

## Setup: nothing

`bunker.js` is added to the top of every HTML page automatically, and `window.bunker` exists before your own scripts run. Don't add a script tag for it.

```html
<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Lunch orders</title>
<link rel="stylesheet" href="/_/kit.css">   <!-- optional: Bunker's clean default styles, light and dark -->
<ul id="list"></ul>
<script type="module">
  const me = await bunker.me();
  const orders = bunker.db.collection('orders');
  const render = async () => {
    const { items } = await orders.list({ limit: 100 });
    list.innerHTML = items.map(o => `<li>${o._authorName}: ${o.meal}</li>`).join('');
  };
  orders.subscribe(render);   // live for everyone
  render();
</script>
```

`await bunker.ready` (or the `bunker:ready` event) resolves once the connection to Bunker is up and `localStorage` has been restored. Calls made earlier are queued, so you rarely need it.

## Reference

| | |
|---|---|
| [`bunker.me()`](/bunker/docs/sdk/me/) | Who is viewing, their role and relevant groups |
| [`bunker.db`](/bunker/docs/sdk/db/) | Shared JSON collections with access rules and live updates |
| [`bunker.files`](/bunker/docs/sdk/files/) | Upload, list, read and serve files and images |
| [`bunker.storage`](/bunker/docs/sdk/storage/) | Per-person key/value storage, plus working `localStorage` |
| [`bunker.room()`](/bunker/docs/sdk/rooms/) | Live broadcast and presence |
| [`bunker.ai`](/bunker/docs/sdk/ai/) | Chat, JSON and streaming answers from the admin's AI provider |
| [`bunker.m365`](/bunker/docs/sdk/m365/) | SharePoint lists, Excel ranges, OneDrive files, delegated (Preview) |
| [`bunker.connections`](/bunker/docs/sdk/connections/) | Admin-defined company APIs (Preview) |
| [`notify`, `toast`, `download`, `open`](/bunker/docs/sdk/ui/) | Notifications and small helpers |
| [Sandbox limits](/bunker/docs/sdk/limits/) | What pages can't do, and what to do instead |

`bunker.site` holds `{ slug, id, title, role, version, draft, icon }` for the current app.

## Errors

Every method returns a Promise. Failures throw a `BunkerError` (also `window.BunkerError`) with a machine-readable `code`, the HTTP `status`, and a plain-English `message` you can show as is:

| `code` | Meaning |
|---|---|
| `forbidden` | The viewer's role or the collection rule doesn't allow it |
| `not_found` | No such document, file or app |
| `invalid` | Bad input |
| `quota_exceeded` | A daily or rate limit was hit |
| `consent_required` | Microsoft 365 needs the viewer's consent |
| `unavailable` | The feature isn't set up (for example no AI provider), or the page was opened outside Bunker |

```js
try { await orders.add(data); }
catch (err) { status.textContent = err.message; }
```

## How it works

When the page loads, the SDK asks the Bunker shell around it for a private `MessageChannel`. The shell:

- accepts the request only from its own iframe (`event.source === iframe.contentWindow`) and binds the channel to this app,
- forwards calls only to the app's own API (`/api/v1/sites/<app>/...`),
- adds the viewer's session, the CSRF header and `X-Bunker-Page: 1`.

Bunker only lets **page-safe** operations through that bridge: data, files, storage, AI, Microsoft 365, rooms, events, notify, connections and `me`. A page can never deploy, share, change data rules or settings, or touch another app, even if its code tries.

Live updates (database changes, rooms, presence, new versions) arrive over **one** server-sent events stream per tab, owned by the shell.

Every SDK call is an ordinary operation in `GET /api/v1/ops` marked `pageSafe`, so scripts and the [CLI](/bunker/docs/cli/reference/) can call the same endpoints with a token.
