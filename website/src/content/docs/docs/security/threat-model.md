---
title: Threat model
description: What Bunker protects against, how, and the risks that remain. Written for security reviewers.
sidebar: { order: 1 }
---

Bunker runs code that people wrote (or that an AI wrote for them) and shows it to other people while they are signed in. The design assumes **app code is untrusted** and **app authors are known people** (an invited account or a member of your organization).

## What we protect

1. **Sessions and credentials:** the viewer's Bunker session, personal tokens, and delegated Microsoft 365 tokens.
2. **Data across apps:** one app must not read or change another app's files, data or settings.
3. **Data the viewer can see:** an app must not send it somewhere else.
4. **The server and your network:** apps must not reach internal services or the host.
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
| SSRF through connections | Named connections with fixed base URLs; private and metadata IPs blocked by default, DNS pinned, redirects re-checked (connections are Preview) |
| Microsoft 365 overreach | Delegated tokens only, scopes on an admin allowlist, every Graph call audited (Preview) |
| Wrong people sign in | Invite-only built-in accounts; single-tenant Entra, Workspace domain for Google, public email domains refused; optional allowed groups; guests off by default |
| Password guessing | argon2id; 10 attempts per 15 minutes per IP and email; constant-time checks |
| Claiming a fresh server | One-time setup code, except from localhost |
| Probing app addresses | "You need access" page is identical whether or not the app exists |
| CSRF | `X-Bunker` header and same-origin check on cookie-authenticated writes; same-origin check on sign-in forms |
| Agents doing too much | Tokens are per person with scopes and expiry; OAuth refresh tokens rotate and reuse revokes the family; everything audited with `via: token` |
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

## Out of scope

- A compromised server or a malicious Bunker admin.
- Browser zero-days that break the sandbox itself.
- Your identity provider's own security.
