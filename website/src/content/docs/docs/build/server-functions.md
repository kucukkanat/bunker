---
title: Server functions
description: Put a little server code next to your pages. Run it on demand, on a schedule, after data changes or from a webhook, with secrets and outbound fetch.
sidebar: { order: 8 }
---

A **server function** is a JavaScript or TypeScript file in your app's `api/` folder. Bunker runs it on the server when a page calls it, on a schedule, after a write to a collection, or when a webhook arrives. There's nothing to set up: no build step, no container, no separate service. Drop the file, publish, call it.

Each call runs in a fresh sandbox inside Bunker, and by default it acts with **the viewer's own rights**, exactly like the page that called it.

## When to use one

Most apps don't need any. A page can already save data, upload files, use AI and read Microsoft 365 through [`window.bunker`](/bunker/docs/sdk/overview/). Reach for a function when you need something a page can't do:

| You want to | Use |
|---|---|
| Save, list and show data | The page SDK ([`bunker.db`](/bunker/docs/sdk/db/)) |
| Call an outside API with a key (Stripe, GitHub, a weather service) | A function with a [secret](#secrets) and [`fetch`](#outbound-fetch) |
| Do something every night or every hour | A function with [`config.schedule`](#on-a-schedule) |
| React when someone adds or changes a record | A function with [`config.onWrite`](#after-a-write) |
| Turn a webhook into something more than "add a record" | A [webhook that targets a function](#from-a-webhook) |
| Check or combine data in a way viewers can't tamper with | A function, optionally [elevated](#viewer-or-owner) |
| Keep logic private (pricing rules, scoring) | A function: its code is never sent to browsers |

## A first function

```js
// api/hello.js
export default async function (input, ctx) {
  return { message: `Hello, ${ctx.user.name}!`, youSent: input };
}
```

Call it from any page of the same app:

```js
const res = await bunker.fn('hello', { color: 'green' });
// { message: "Hello, Ada Lovelace!", youSent: { color: "green" } }
```

Publish (or [deploy a draft](/bunker/docs/build/versions/) and open `?draft=1`) and that's it. The first argument of `bunker.fn` is the function's name: the file path under `api/` without the extension.

A slightly more real one, saving an order and checking it on the server:

```ts
// api/orders/place.ts
type Input = { meal: string; qty: number };

export default async function (input: Input, ctx) {
  if (!input.meal || input.qty < 1 || input.qty > 10) {
    throw new Error('Pick a meal and a quantity from 1 to 10.');
  }
  const order = await bunker.db.collection('orders').add({ ...input, status: 'new' });
  console.log('order placed', order._id, 'by', ctx.user.email);
  return { id: order._id };
}
```

```js
await bunker.fn('orders/place', { meal: 'Pasta', qty: 2 });
```

## Files and names

| File | What it is |
|---|---|
| `api/hello.js` | Function `hello` |
| `api/orders/refund.ts` | Function `orders/refund` (folders are fine) |
| `api/report.mjs`, `api/report.mts` | Function `report` |
| `api/_shared.js`, `api/_lib/money.ts` | **Shared modules**: anything starting with `_` can be imported but not called |
| `api/prices.json` | Not a function: an ordinary static file, served as before |

- A function is a `.js`, `.mjs`, `.ts` or `.mts` file under `api/` with an **`export default`** (or `export { x as default }`) function. `.d.ts` files are ignored. A code file without a default export isn't a function and is served like any other file. It receives `(input, ctx)` and returns any JSON value (up to 1 MB), or a Promise of one.
- Function files, and the `_` modules of an app that has at least one function, are **never served to browsers**. Opening `/s/<app>/api/hello.js` gives a 404, so the code and anything in it stay on the server.
- **TypeScript** works as is. Types are stripped when you publish; nothing is type-checked.
- **Imports** of your own files work, relative to the file: `import { total } from './_lib/money.ts'`. Bunker bundles each function when you publish (ES modules, ES2023). There's **no npm**: bare imports like `import dayjs from 'dayjs'` are refused. Copy a small library into `api/_lib/` if you need one. `import()` with a computed name (like `import(name)`) isn't allowed either.
- If a function can't be bundled (a syntax error, a missing import), the publish still goes through and the dashboard shows the error next to that function. Calls to it fail with `function_error` until you fix it.

## The `ctx` argument

```js
ctx = {
  user:    { id, email, name, groups: [...] },   // who the function runs as
  site:    { slug, title, version, draft },      // draft: true when running draft code
  trigger: { type: 'call' },                      // or schedule / write / webhook, see below
  elevated: false,
}
```

`ctx.user` is the person behind the run: whoever called it, wrote the record or created the webhook, and the owner for [schedules](#on-a-schedule). It stays the same when the function is [elevated](#viewer-or-owner), so an elevated function still knows who asked. `bunker.me()` returns the person whose rights the function is using.

## Configuration

Export a `config` object next to the function. Every field is optional.

```js
export const config = {
  elevated: false,   // run as the app's owner instead of the caller
  schedule: '',      // cron, e.g. '0 7 * * 1-5'
  onWrite: [],       // collections, e.g. ['orders']
  timeoutMs: 0,      // shorter than the admin's limit, if you like
};
```

`config` must be plain JSON data (no functions or other values that don't survive `JSON.stringify`). Bunker reads it when you publish by loading the module for up to 1 second, without calling your function and without access to `bunker` or `fetch`.

## Viewer or owner

By default a function runs **as the person who called it**. It can read and change exactly what that person could from the page, through the same [collection rules](/bunker/docs/sdk/db/#access-rules). A viewer can't use your function to do more than they're allowed.

With `elevated: true` the function runs **as the app's owner**. Use this when the function itself is the guard: it checks the input, then writes to a collection viewers can only read.

```js
// api/vote.js — the "votes" collection is set to `read`, so viewers can't write to it directly
export const config = { elevated: true };

export default async function ({ option }, ctx) {
  const votes = bunker.db.collection('votes');
  if (!['tea', 'coffee'].includes(option)) throw new Error('Choose tea or coffee.');
  await votes.set(String(ctx.user.id), { option });   // one vote per person
  return { ok: true };
}
```

Elevated functions still only use the same page-safe operations as pages: they can't deploy, share, change rules or settings, or reach another app. Every function file is listed in the app's **Functions** tab with an "elevated" badge, and publishing a change to the set of functions is [audited](/bunker/docs/admin/audit/) as `functions.deployed`.

:::caution
An elevated function can do anything the owner can do inside this app. Check its input as carefully as you would on any server.
:::

## Drafts

Calls from a draft preview (`?draft=1`, editors only) run the **draft** code, so you can try a change before anyone else sees it. Everyone else, and every schedule, write trigger and webhook, runs the **live** version.

## Triggers

### On a schedule

```js
// api/nightly.js
export const config = { schedule: '0 2 * * *' };   // every day at 02:00 UTC

export default async function (input, ctx) {
  // ctx.trigger = { type: 'schedule', at: '2026-11-05T02:00:00Z', schedule: '0 2 * * *' }
  const open = await bunker.db.collection('requests').all({ where: { status: 'open' } });
  if (open.length) await bunker.notify({ to: [ctx.user.email], title: `${open.length} open requests` });
}
```

- Standard 5-field cron (`minute hour day month weekday`, with `*`, lists, ranges and `/` steps), or `@hourly`, `@daily`, `@weekly` and `@monthly`. Always in **UTC**. The most often is every minute.
- `input` is the same object as `ctx.trigger`.
- Runs as the **owner**, with the longer trigger time limit (120 s by default).

### After a write

```js
// api/on-order.js
export const config = { onWrite: ['orders'] };

export default async function (input, ctx) {
  // ctx.trigger = { type: 'write', collection: 'orders', op: 'add', id, doc }
  if (ctx.trigger.op !== 'add') return;
  await bunker.rooms('kitchen').send({ new: ctx.trigger.doc.meal });
}
```

- `input` is the same object as `ctx.trigger`. `op` is `add`, `update` or `delete` (a `set` reports `add` for a new record and `update` for an existing one); `doc` is left out for deletes.
- Runs **after** the add, set, update or delete is saved, in the background. It can't stop or change the write; the person who wrote doesn't wait for it.
- Runs as **the person who wrote** (or the owner, when elevated).
- Writes made inside a write trigger **don't fire triggers again**, so a function can't loop on itself.
- Writes from pages, other functions, webhooks and the CLI all count, as long as they go through the single-record API (add, set, update, delete). Bulk imports (`db.import`, `POST /db/{coll}/_import`) **don't** fire write triggers.

### From a webhook

An [inbound webhook](/bunker/docs/build/webhooks/) can target a function instead of a collection. The JSON body (or a form post, as an object) becomes `input`, `ctx.trigger` is `{ type: 'webhook', hook }`, it runs as the person who created the webhook with the normal call time limit (30 s by default), and the function's return value is sent back as the HTTP response.

```js
// api/github.js
export default async function (event, ctx) {
  if (event.action !== 'opened') return { skipped: true };
  await bunker.db.collection('issues').add({ title: event.issue.title, url: event.issue.html_url });
  return { saved: true };
}
```

## Secrets

Keep API keys out of your files (uploads that contain them are refused anyway) and store them as **secrets** instead. Each app has its own.

- In the app's **Functions → Secrets** tab, or `bunker secrets set STRIPE_KEY` (the value is read from the terminal, so it stays out of your shell history).
- Names are capitals, digits and `_`, starting with a letter (`STRIPE_KEY`, `GH_TOKEN_2`). Values up to 16 KB.
- Secrets are **write-only**: nobody, not even the owner or an admin, can read a value back. You can replace or delete it. Changes are audited.
- They're encrypted at rest (AES-GCM) and only handed to your function while it runs:

```js
const key = await bunker.secrets.get('STRIPE_KEY');   // a string, or null if not set
```

Pages can't read secrets at all.

## Outbound fetch

Functions have a global `fetch` for calling other services:

```js
export default async function ({ city }) {
  const key = await bunker.secrets.get('WEATHER_KEY');
  const res = await fetch(`https://api.example-weather.com/v1/now?q=${encodeURIComponent(city)}`, {
    headers: { Authorization: `Bearer ${key}` },
  });
  if (!res.ok) throw new Error(`The weather service answered ${res.status}.`);
  return await res.json();
}
```

What it can reach depends on your admin's **outbound policy**:

| Policy | Functions can fetch |
|---|---|
| `internet` (default) | Public hosts on the internet. Private, loopback and cloud-metadata addresses are always refused |
| `allowlist` | Only the hosts your admin listed |
| `off` | Nothing. Use [`bunker.connections`](/bunker/docs/sdk/connections/) for company APIs |

For company-internal APIs, [connections](/bunker/docs/admin/connections/) are still the right tool: the admin keeps the key and the base URL, and they work from pages too. A refused fetch throws an error that says why. Requests the outbound policy refuses are audited as `functions.outbound.refused`.

See the [server SDK reference](/bunker/docs/sdk/server/) for everything that's available inside a function.

## Logs and test runs

Open the app's **Functions** tab in the dashboard:

- **Every function** with its triggers, whether it's elevated, and any bundling error.
- **Test run:** pick a function, type JSON input, run it against the draft or the live version, and see the output, the time it took and the logs.
- **Runs:** the last 200 runs of the app, with who triggered them, how, how long they took, the error if any and their `console` output.
- **Live logs:** `console.log` lines as they happen, for editors.

Only editors see logs, errors and stack traces. Viewers get a plain message.

## CLI

```sh
bunker fn                                   # list this app's functions
bunker fn call hello '{"color":"green"}'    # call one and print the output
bunker fn call orders/place @order.json --draft
bunker fn runs --name nightly               # recent runs of one function
bunker fn logs                              # recent runs with their logs
bunker fn logs --follow                     # stream live logs
bunker secrets                              # list secret names (never values)
bunker secrets set STRIPE_KEY               # prompts for the value (or pipe it in)
bunker secrets rm STRIPE_KEY
```

Inside a folder with `bunker.json` the app is picked automatically; elsewhere pass the app's address first (`bunker fn ls team-lunch`, `bunker fn call team-lunch hello`, `bunker secrets set team-lunch STRIPE_KEY`). The same operations are available as `bunker api fn.list`, `fn.call`, `fn.runs`, `secrets.list`, `secrets.set` and `secrets.delete`, and as [MCP tools](/bunker/docs/agents/mcp/).

## Limits

Your admin can change these. A function can ask for a shorter time limit with `config.timeoutMs`, never a longer one.

| | Default |
|---|---|
| Memory per run | 64 MB |
| Time per call | 30 s |
| Time per schedule or write trigger run | 120 s (webhook runs use the call limit) |
| Input and return value | 1 MB each |
| Logs per run | 64 KB streamed live (the rest is cut); the first 8 KB is kept in the run history |
| Bunker calls per run (`bunker.*`, `fetch`, secrets) | 1,000 |
| `fetch` response body | 5 MB |
| Runs kept in the history | The last 200 per app |
| Secrets | 100 per app, 16 KB each |
| Runs at once, per app | 4 |
| Runs at once, whole server | 4 or twice the CPU count, whichever is more |

## Errors

`bunker.fn` throws a [`BunkerError`](/bunker/docs/sdk/overview/#errors) like every SDK call:

| `code` | Status | Meaning |
|---|---|---|
| `unauthorized` | 401 | Not signed in. Signed-out visitors of a public link can't call functions |
| `too_large` | 413 | The input is more than 1 MB |
| `functions_off` | 403 | Server functions are turned off for this server, this app or its owner. The message says why |
| `not_found` | 404 | No function with that name in this version |
| `busy` | 429 | Too many runs at once. Try again in a moment |
| `timeout` | 504 | The function ran out of time |
| `function_error` | 500 | The function threw, ran out of memory, returned too much, made too many calls or couldn't be bundled. Editors also get `err.stack` |

When your function throws an `Error`, its message is what viewers see, so write it for people: `throw new Error('That room is already booked.')`.

## Troubleshooting

| Problem | Try |
|---|---|
| `not_found` for a function you just added | Did you publish? Live pages only see published functions. In a draft preview, check the file has an `export default` |
| My file is served as text, or is now a 404 | Files in `api/` with `export default` are functions and are no longer served. Move static files elsewhere or rename them to `.json` |
| `functions_off` | An admin turned functions off for the server, for you or for this app. The message and the dashboard banner give the reason |
| A bare import fails at publish | There's no npm, and functions can't call each other through the API: share code with `_` modules. Copy the library into `api/_lib/` and import it with a relative path |
| `fetch` is refused | The outbound policy doesn't allow that host, or it's a private address. Ask your admin, or use a [connection](/bunker/docs/sdk/connections/) |
| A schedule never runs | It uses the **live** version and **UTC**. Check the app is published and the cron line in the Functions tab |
| My write trigger doesn't fire for writes made by another trigger | That's on purpose: triggers only go one level deep |
| `forbidden` inside a function | It runs with the caller's rights. Check the collection rule, or make it `elevated` if the function is the guard |
| `window is not defined` | Functions aren't browsers. See [what's available](/bunker/docs/sdk/server/) |

## How it's isolated

Every run gets a brand-new JavaScript engine (QuickJS compiled to WebAssembly) inside Bunker's own process, with its own memory cap and clock. It has no file system, no network and no access to the server except the calls Bunker hands it, and it's thrown away when the run ends. Details for reviewers are in the [threat model](/bunker/docs/security/threat-model/#server-functions) and [Sandbox and headers](/bunker/docs/security/sandbox/#server-functions).
