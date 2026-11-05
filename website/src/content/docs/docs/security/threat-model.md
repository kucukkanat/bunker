---
title: Threat model
description: What Bunker protects against, how, and the risks that remain. Written for security reviewers.
sidebar: { order: 1 }
---

Bunker runs code that people wrote (or that an AI wrote for them) and shows it to other people while they are signed in. Since server functions, some of that code also runs on the server. The design assumes **app code is untrusted**, in the browser and on the server, and **app authors are known people** (an invited account or a member of your organization).

## What we protect

1. **Sessions and credentials:** the viewer's Bunker session, personal tokens, and delegated Microsoft 365 tokens.
2. **Data across apps:** one app must not read or change another app's files, data or settings.
3. **Data the viewer can see:** an app must not send it somewhere else.
4. **The server and your network:** apps, including their server functions, must not reach internal services, the host or Bunker's own memory and files.
5. **Who gets in:** only people you invited or your identity provider allows.

## Who might attack

| Actor | Example |
|---|---|
| A malicious or careless **app author** | Publishes a page that tries to steal viewers' sessions or exfiltrate what they see |
| A **viewer** | Uses the browser console to call APIs they shouldn't, or loosen data rules |
| **Someone outside** | Tries to sign in, guess passwords, claim a fresh server, or probe app addresses |
| A **compromised dependency** in an app | A library that phones home |
| An **agent** with a token | Does more than the person intended |

## Controls

| Threat | Control |
|---|---|
| App reads the Bunker session | Opaque-origin sandbox (no `allow-same-origin`); session cookie is `HttpOnly`; content served with path tickets, never cookies |
| App calls Bunker's API as the viewer | Requests from the frame carry no cookies. SDK calls go through the shell, which only forwards to this app's own API and marks them `X-Bunker-Page`; the server allows only page-safe operations (no deploy, share, settings, rules) |
| App reaches another app | Different ticket per app and viewer; server checks app, viewer and role on every request; `frame-ancestors` stops nesting |
| App sends data to the internet | CSP `default-src 'none'` with only `'self'` sources and `connect-src 'self'`; `form-action 'none'`; no popups; `Connection-Allowlist` on Chromium and Edge |
| App holds or leaks keys | Pages never receive tokens; uploads with API keys are refused; AI and connection keys stay on the server, encrypted |
| Viewer loosens data rules | Rule changes are editor-only operations that pages can't call |
| SSRF through connections | Named connections with fixed base URLs and path prefixes; private and metadata IPs blocked by default, checked on the dialed IP after DNS resolution, redirects limited to the same host, proxies bypassed, 5 MB and per-app rate limits |
| Microsoft 365 overreach | Delegated tokens only, GET only, known Graph paths mapped to scopes on an admin allowlist, optional per-app approval and SharePoint site list, every Graph call audited |
| Wrong people sign in | Invite-only built-in accounts; single-tenant Entra, Workspace domain for Google, public email domains refused; optional allowed groups; guests off by default |
| Password guessing | argon2id; 10 attempts per 15 minutes per IP and email; constant-time checks |
| Claiming a fresh server | One-time setup code, except from localhost |
| Probing app addresses | "You need access" page is identical whether or not the app exists |
| CSRF | `X-Bunker` header and same-origin check on cookie-authenticated writes; same-origin check on sign-in forms |
| Agents doing too much | Tokens are per person with scopes and expiry; OAuth refresh tokens rotate and reuse revokes the family; everything audited with `via: token` |
| Server function escapes to the host | Each run is a fresh QuickJS interpreter compiled to WebAssembly, run by wazero inside Bunker's process. No file system, no sockets, no environment; the only way out is a JSON message to Bunker, which allows page-safe operations, secret reads and policy-checked `fetch` |
| Server function exhausts the server | Memory cap per run (64 MB), wall-clock limit (30 s calls, 120 s triggers), 1 MB input and output, 64 KB of logs, 1,000 host calls, 4 runs at once per app and a server-wide cap; over capacity is `429 busy`, never a queue |
| Server function does more than the viewer could | Runs with the caller's rights by default, through the same page-safe operations and collection rules as the page. Elevated functions use the owner's rights, are badged and audited (`functions.deployed`) |
| Server function reaches the internal network | Outbound `fetch` refuses private, loopback, link-local and metadata addresses on the dialed IP after DNS; admins can restrict it to an allowlist or turn it off (`functions.outbound.refused` is audited) |
| Function secrets leak | Per app, write-only, AES-GCM at rest under the server key, handed only to a running function of that app; never served, never returned by any API, never in function files (secret scanning still applies) |
| Function source leaks | `api/` function files and `_` modules are never served to browsers |
| A runaway app or author | Admins can turn functions off globally, per app or per person (every app they own), with a reason the person sees |
| Uploaded executables | Blocked by extension and by content sniffing |
| Zip slip | Paths with `..` rejected; zip size and file count capped |

## Residual risks (honest list)

These are known and accepted, with mitigations:

1. **WebRTC and navigation exfiltration on Firefox and Safari.** CSP can't stop WebRTC (the `webrtc` directive isn't enforced by major engines as of late 2026), and no CSP directive stops a frame from navigating itself to `https://evil/?data=...`. `Connection-Allowlist` blocks both on **Chromium and Edge 152+**, which most managed fleets run. On other browsers Bunker removes the WebRTC constructors (bypassable from a fresh frame) and relies on authors being known people and the audit log. If this matters to you, require Edge or Chrome through device management.
2. **DNS prefetch and resource hints** may leak a hostname on browsers without `Connection-Allowlist`.
3. **A malicious author can see what their app's viewers enter or can read through the SDK.** That's inherent: an app is software you choose to use. Share-by-default-private and the audit log limit the blast radius. Don't put sensitive data into apps you don't trust.
4. **Admins can open every app.** Deliberate, for support and clean-up. Admin actions are audited.
5. **Single host by default.** Isolation rests on the sandbox and tickets. For a second independent wall, use a [separate content domain](/bunker/docs/deploy/content-domain/), ideally on a different registrable domain.
6. **Template titles are visible to everyone.** Marking an app as a template lists its name, description and icon for all signed-in people.
7. **One server, one disk.** Availability depends on that machine. Back up the data folder ([Backups](/bunker/docs/admin/backups/)).
8. **Secret scanning is pattern-based.** It catches common key formats, not every secret.
9. **Functions can send data out.** With the default `internet` policy a function can post what its caller can read to any public host. That's the same trust as risk 3 (an author can see what their viewers see), now without the browser's limits. Use the `allowlist` or `off` [outbound policy](/bunker/docs/admin/server-functions/#outbound-policy) if that's not acceptable, and watch `functions.deployed`.
10. **Elevated functions are only as careful as their author.** An elevated function that trusts its input can let a viewer do what only the owner should. Bunker limits it to the owner's rights within that one app.
11. **The WebAssembly runtime is a boundary we depend on.** A bug in wazero or the QuickJS build could break isolation. Both are pinned (QuickJS by checksum), pure Go and memory-safe on the host side; admins who don't need functions can turn them off.

## Server functions

Server functions add one trust boundary: untrusted JavaScript running **on the server**. The layers, from outside in:

1. **Who may run code.** Only people who can publish an app. Admins can turn functions off for the server, a person or an app.
2. **Whose rights.** A run acts as the person behind it (the caller, the writer, the webhook creator) or, when the author marks it elevated, as the app's owner. Schedules run as the owner. Either way only page-safe operations of that one app are reachable, through the same authorization as a page.
3. **The WebAssembly sandbox.** Each run is a new QuickJS instance compiled to WASI and run by wazero (pure Go, no CGO). It has its own linear memory, capped; no access to Bunker's memory, files, environment or network. Its only channel is a line-based JSON protocol on stdin/stdout that Bunker answers in-process. It's discarded after the run.
4. **Time, size and count.** Every run has a wall-clock limit, input, output and log caps, and a cap on host calls; concurrency is bounded per app and per server.
5. **Outbound.** `fetch` goes through Bunker, which applies the admin's policy and refuses private addresses after DNS resolution.
6. **Secrets.** Write-only, encrypted at rest, scoped to one app, only readable by that app's running functions.
7. **Records.** Publishing a change to an app's functions, secret changes, settings, blocks and refused outbound calls are all audited; each app keeps its last 200 runs with logs.

Function code is bundled at publish with the embedded esbuild; there's no npm, and imports of QuickJS's own `std`/`os` modules are refused.

## Out of scope

- A compromised server or a malicious Bunker admin.
- Browser zero-days that break the sandbox itself.
- Your identity provider's own security.
