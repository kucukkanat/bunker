---
title: Security model
description: What Bunker protects, from whom, and how.
sidebar: { order: 1 }
---

Bunker is built on one assumption: **every page might be badly written**, by a person or by an AI. The platform has to stay safe anyway.

## Threats we design for
| Threat | Example | Defense |
|---|---|---|
| Outsiders | Someone outside the company opens a link | Entra ID single-tenant sign-in on every request. No public links. |
| Over-sharing | A finance tracker is visible to everyone | Private by default. Per-site viewer and editor roles. Org-wide sharing can be disabled. |
| Data exfiltration | A page sends data to an outside server | Sandboxed iframe with `connect-src 'none'`. The only way out is Bunker's API. |
| Cross-site attacks | Site A reads Site B's data or the user's session | Opaque-origin sandbox: no cookies, no storage, no same-origin access. |
| Leaked secrets | An API key pasted into page code | No keys needed in pages. Uploads are scanned for secrets. |
| Privilege escalation via M365 | A page reads a SharePoint site the viewer can't | Delegated tokens only. Graph enforces the viewer's own rights. Scopes are allowlisted. |
| AI abuse | A page burns the AI budget | Per-user and per-site quotas. Every call is audited. |
| Mistakes | An edit breaks a working app | Versions with one-click restore. |
| Insider misuse | Someone views data they shouldn't | Role check on every API call, plus the audit log. |

## Request path
1. The browser loads `https://bunker.corp.com/s/<site>`. Bunker checks the session cookie (`HttpOnly`, `Secure`, `SameSite=Lax`) and the viewer's role on the site.
2. Bunker serves a small **shell** page that embeds the site in a sandboxed iframe (see [Sandbox & CSP](/bunker/docs/security/sandbox/)).
3. The site's `bunker.js` sends requests to the shell with `postMessage`.
4. The shell checks the message's source frame and passes the request to Bunker's API with the viewer's session.
5. The API checks *(site, viewer, role, data rule, quota)* again before doing anything.

The page never holds a credential. Even a compromised page can do only what the current viewer is allowed to do on **that one site**.

## Data at rest
- SQLite in WAL mode on the VM's disk. Use disk encryption on the VM.
- Graph refresh tokens and AI keys are encrypted with `BUNKER_SECRET_KEY` (AES-256-GCM).
- Backups contain everything. Protect them like the server.

## Reporting a vulnerability
Please report privately through GitHub Security Advisories on the [repository](https://github.com/kucukkanat/bunker/security). Do not open a public issue.
