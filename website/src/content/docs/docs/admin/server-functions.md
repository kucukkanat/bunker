---
title: Server functions
description: Turn server functions on or off, set their limits and outbound policy, block a person or an app, and see what gets audited.
sidebar: { order: 3.5 }
---

[Server functions](/bunker/docs/build/server-functions/) let app owners run a little JavaScript on the server: on demand from a page, on a schedule, after data changes or from a webhook. They're **on by default**. Each run happens in a fresh WebAssembly sandbox inside Bunker, with a memory cap and a time limit, and acts with the rights of the person who triggered it (or the app's owner, when the function is marked elevated).

Everything on this page is in **Admin → Functions**, and in the API as `admin.functions.get` and `admin.functions.update` (`GET` / `PUT /api/v1/admin/functions`).

## On or off

The main switch turns server functions off for the whole server. While it's off:

- apps still load and pages keep working; only function calls, schedules, write triggers and webhooks that target a function stop,
- `bunker.fn` throws `functions_off` with a plain message,
- function files are still never served to browsers.

Turning it back on resumes everything, with no redeploy.

## Limits

| Setting | Default | |
|---|---|---|
| `memoryMb` | `64` | Memory per run |
| `timeoutMs` | `30000` | Time per call from a page or the CLI |
| `triggerTimeoutMs` | `120000` | Time per schedule, write trigger or webhook run |
| `siteConcurrency` | `4` | Runs at once for one app |
| `globalConcurrency` | 4 or twice the CPU count, whichever is more | Runs at once on the whole server |

Over capacity, calls fail fast with `429 busy` instead of queueing, so one busy app can't slow down the rest. These are fixed for every run: input and output 1 MB each, 64 KB of logs and 1,000 calls to Bunker per run, and the last 200 runs kept per app.

## Outbound policy

Functions have a `fetch` for calling other services. You choose what it can reach:

| `outbound` | Functions can reach |
|---|---|
| `internet` (default) | Public hosts. Private (RFC 1918, unique-local), loopback, link-local and cloud-metadata addresses are always refused, checked on the address actually dialed after DNS, with the same guard as [connections](/bunker/docs/admin/connections/) |
| `allowlist` | Only the hosts in `allowlist`: exact names (`api.stripe.com`) or a suffix (`*.example.com`). Private addresses are refused even when listed |
| `off` | Nothing. Functions can still use [connections](/bunker/docs/admin/connections/) you define |

Every refused request is audited as `functions.outbound.refused` with the app, the person and the host.

`internet` is the default because the most common reason to write a function is calling a public API with a key. If your network policy says servers mustn't call out freely, choose `allowlist` or `off`. Connections are always the better fit for internal APIs: you keep the key and the base URL, and they work the same from pages.

```sh
bunker api admin.functions.update --data '{"outbound":"allowlist","allowlist":["api.stripe.com","*.github.com"]}'
```

## Block a person or an app

Sometimes one app misbehaves, or you'd like to pause someone's server code while you talk to them. Block it instead of turning everything off:

- **An app:** only that app's functions stop.
- **A person:** functions stop in **every app they own**, including ones they create later, until you unblock them.

Give a reason. It's shown to the people affected, so write it for them: "Paused while we review the nightly export job. Talk to IT."

What they see:

- a **banner** on their dashboard with your reason, and a badge on each affected app,
- `functions_off` errors from `bunker.fn` that include the reason,
- the app itself still loads, with its data and files; only function calls and triggers stop.

Viewers of a blocked app see the plain error message from the app, nothing more.

| Operation | Route |
|---|---|
| `admin.functions.blocks` | `GET /api/v1/admin/functions/blocks` → `{ items: [{ kind, id, label, reason, by, at }] }` |
| `admin.functions.block` | `PUT /api/v1/admin/functions/blocks/{kind}/{id}` with `{ reason }`. `kind` is `user` (a user ID) or `site` (an app address) |
| `admin.functions.unblock` | `DELETE /api/v1/admin/functions/blocks/{kind}/{id}` |

```sh
bunker api admin.functions.block --kind site --id lunch-orders --reason "Too many calls to an outside API. Talk to IT."
bunker api admin.functions.unblock --kind site --id lunch-orders
```

Any signed-in person can check their own state with `functions.status` (`GET /api/v1/functions/status`), which is what drives the banner.

## Pinning settings

Every setting here can be pinned from the environment, like any saved setting ([how](/bunker/docs/admin/configuration/#server-functions)): `BUNKER_FUNCTIONS_ENABLED=false`, `BUNKER_FUNCTIONS_OUTBOUND=allowlist`, and so on. A pinned value wins over the dashboard.

## Audit events

| Event | When |
|---|---|
| `functions.deployed` | A publish changed an app's set of functions. `meta` lists the names and which are elevated, scheduled or write-triggered |
| `functions.secret.set`, `functions.secret.delete` | An editor stored, replaced or removed a secret (the name only, never the value) |
| `functions.settings` | An admin changed the switch, limits or outbound policy |
| `functions.block`, `functions.unblock` | An admin blocked or unblocked a person or an app, with the reason |
| `functions.outbound.refused` | A function's `fetch` was refused by the outbound policy |

Search them with `event=functions.*` in the [audit log](/bunker/docs/admin/audit/).

## What to keep an eye on

- **Elevated functions** act as the app's owner. They're shown with a badge in each app's Functions tab, and `functions.deployed` tells you when one appears.
- **Secrets** belong to each app and can't be read back, not even by admins. When someone leaves and their apps move to a colleague, the secrets move with the app.
- **Outbound traffic** is where data can leave. If that matters to you, use `allowlist`. The [threat model](/bunker/docs/security/threat-model/#server-functions) covers what's left.
