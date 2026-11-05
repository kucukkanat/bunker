---
title: bunker.storage
description: Per-person key/value storage that follows the viewer across devices, and working localStorage, sessionStorage and cookies.
sidebar: { order: 5 }
---

Sandboxed apps have an opaque origin, so the browser's real cookies, `localStorage`, `sessionStorage` and IndexedDB are off limits there. `bunker.storage` is the place for small, personal things: preferences, a half-filled form, the last tab someone opened.

```js
await bunker.storage.set('view', { density: 'compact' });  // any JSON
const v = await bunker.storage.get('view');                 // the value, or null
await bunker.storage.remove('view');                        // also .delete()
const keys = await bunker.storage.keys();                   // optionally keys('prefix')
await bunker.storage.clear();
```

- **Per viewer, per app.** Nobody else can read it, not even the app's editors.
- **Follows the person** to other devices and browsers.
- For anything shared, use [`bunker.db`](/bunker/docs/sdk/db/).

## localStorage, sessionStorage and cookies just work

Many libraries touch storage when they load and would crash in an opaque origin. The SDK installs working versions:

| | Behaviour |
|---|---|
| `localStorage` | Synchronous, like the real one. Saved per person in `bunker.storage` (keys prefixed `ls:`) and restored on the next visit, on any device. Restored before `bunker.ready` resolves |
| `sessionStorage` | In memory for the life of the page |
| `document.cookie` | In memory for the life of the page; never sent anywhere |

So existing code that uses `localStorage` keeps working, and its data follows the person.

## Limits

| | Default | Setting |
|---|---|---|
| One value | 64 KB | `BUNKER_KV_MAX_VALUE_KB` |
| Keys per person per app | 1,000 | `BUNKER_KV_MAX_KEYS` |
| Total per person per app | 5 MB | `BUNKER_KV_MAX_TOTAL_MB` |

## API

`kv.list` `GET /kv` · `kv.get` `GET /kv/{key}` · `kv.set` `PUT /kv/{key}` · `kv.delete` `DELETE /kv/{key}` · `kv.clear` `DELETE /kv`, all under `/api/v1/sites/{site}`.
