---
title: Server SDK
description: The global bunker object inside server functions, how it differs from the page SDK, fetch, console and timers, and bunker.fn for calling functions from a page.
sidebar: { order: 10.5 }
---

Inside a [server function](/bunker/docs/build/server-functions/), the global **`bunker`** works like `window.bunker` in a page: the same names, the same method shapes, the same [errors](/bunker/docs/sdk/overview/#errors). Calls go to the same page-safe operations, with the rights of the person the function runs as.

## Calling a function from a page: `bunker.fn`

```js
const out = await bunker.fn('orders/refund', { orderId: 'a1b2', reason: 'Late' });
```

- The name is the path under `api/` without the extension: `api/orders/refund.ts` → `'orders/refund'`.
- `input` is any JSON value (up to 1 MB). The Promise resolves to whatever the function returned.
- In a draft preview (`?draft=1`) it runs the draft code automatically.
- Failures throw a `BunkerError` with `code` `functions_off`, `not_found`, `busy`, `timeout` or `function_error` ([what each means](/bunker/docs/build/server-functions/#errors)). For editors, `err.stack` holds the function's stack trace.

Operation `fn.call`: `POST /api/v1/sites/{site}/functions/{name}` with `{ input, draft? }` → `{ output, durationMs }`. Any viewer of the app can call it; it's page-safe.

## What's available in a function

| | Same as the page? | Notes |
|---|---|---|
| [`bunker.db`](/bunker/docs/sdk/db/) | Yes | `collection(name)` with `add`, `get`, `set`, `update`, `delete`, `list`, `query`, `all`, `count`, `export`. No `subscribe` |
| [`bunker.storage`](/bunker/docs/sdk/storage/) | Yes | The run-as person's own key/value storage for this app |
| [`bunker.files`](/bunker/docs/sdk/files/) | Mostly | `list`, `get`, `read`, `delete`, and `upload(name, data, { type, folder })` where `data` is a string or a `Uint8Array` |
| [`bunker.ai`](/bunker/docs/sdk/ai/) | Mostly | `chat` and `json`. No `stream` |
| [`bunker.m365`](/bunker/docs/sdk/m365/) | Yes | As the run-as person. Needs Microsoft Entra sign-in |
| [`bunker.connections`](/bunker/docs/sdk/connections/) | Yes | `fetch(name, { method, path, query, body })` |
| [`bunker.rooms(name).send(data)`](/bunker/docs/sdk/rooms/) | Send only | Broadcast to open pages. No subscribing or presence |
| [`bunker.notify`](/bunker/docs/sdk/ui/#bunkernotify) | Yes | Same rules: only people who can open the app receive it |
| [`bunker.me()`](/bunker/docs/sdk/me/) | Yes | The person whose rights the function is using (the owner when elevated or scheduled) |
| `bunker.site` | Yes | `{ slug, title, version, draft }` |
| `bunker.secrets.get(name)` | **Server only** | The secret's value as a string, or `null` |
| `fetch(url, init)` | **Server only** | Outbound HTTP, [subject to the admin's policy](#fetch) |
| `console.log/info/warn/error` | Different | Goes to the run's logs in the dashboard |
| `setTimeout`, `clearTimeout` | Yes | Within the run's time limit |

Not available: `window`, `document`, the DOM, `localStorage`, `bunker.toast`, `bunker.download`, `bunker.open`, `bunker.fn` (functions don't call each other; share code through `_` modules instead), live subscriptions and streaming answers. There's no Node.js or Deno API either: no `require`, `process`, `fs` or `Buffer`. Standard modern JavaScript (ES2023) is all there: `JSON`, `Date`, `Math`, typed arrays, `async`/`await` and so on.

## Differences from the page SDK

- **Whose rights.** A page always acts as the viewer. A function acts as the caller by default, and as the owner when `config.elevated` is true or it runs on a schedule. Rules apply either way; elevated functions just use the owner's role.
- **No browser.** There are no `File` or `Blob` objects: pass strings or `Uint8Array`s, and read files as `'text'` or `'json'`.
- **One run, then gone.** Nothing stays in memory between calls. Keep state in `bunker.db` or `bunker.storage`.
- **Bounded.** Each run has a memory cap, a time limit and at most 1,000 calls to Bunker (`bunker.*`, `fetch` and secrets together). See [Limits](/bunker/docs/build/server-functions/#limits).

## fetch

```js
const res = await fetch('https://api.github.com/repos/kucukkanat/bunker', {
  headers: { Authorization: `Bearer ${await bunker.secrets.get('GH_TOKEN')}` },
});
const repo = await res.json();
```

`fetch(url, { method, headers, body })` returns a Promise of a small response object:

| | |
|---|---|
| `ok`, `status` | As in the browser |
| `headers.get(name)` | One response header |
| `text()`, `json()`, `arrayBuffer()` | The body |

`body` may be a string or a `Uint8Array` (use `JSON.stringify` for JSON). Only `http` and `https` URLs are allowed, and which hosts are reachable depends on the admin's [outbound policy](/bunker/docs/admin/server-functions/#outbound-policy): any public host by default, only listed hosts with an allowlist, or nothing. Private, loopback and cloud-metadata addresses are always refused, checked on the address actually dialed after DNS. A refused call throws an error that names the host and the reason, and is audited as `functions.outbound.refused`.

## console

`console.log`, `info`, `warn` and `error` write a line to the run's log, with its level. Editors see logs in the app's **Functions** tab (also live) and with `bunker fn logs`. Each run keeps up to 64 KB of logs. Never log secrets.

## Timers

`setTimeout(fn, ms)` and `clearTimeout(id)` work as usual, and `await new Promise(r => setTimeout(r, 500))` is a fine way to wait between retries. The run ends when your function's Promise settles: timers still pending then don't fire. `setInterval` is not available.

## Operations

| Operation | Route (under `/api/v1/sites/{site}`) | Who |
|---|---|---|
| `fn.call` | `POST /functions/{name}` | Viewers (page-safe) |
| `fn.list` | `GET /functions` | Editors |
| `fn.runs` | `GET /functions/_runs?name=&limit=` | Editors |
| `secrets.list` | `GET /secrets` | Editors |
| `secrets.set` / `secrets.delete` | `PUT` / `DELETE /secrets/{name}` | Editors, not from pages |
| `events.stream` | `GET /events?ch=logs` (live logs) | Editors |
