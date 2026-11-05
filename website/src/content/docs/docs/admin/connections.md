---
title: Connections
description: Give apps safe, audited access to internal APIs through named connections with SSRF protection.
sidebar: { order: 3, badge: { text: 'Preview', variant: 'caution' } }
---

:::note[Preview]
Connections are being built now. This page describes how they work.
:::

Apps can't reach your network. A **connection** is an admin-defined door to one API: a name, a base URL, credentials and a policy. Apps call it by name with [`bunker.connections.fetch`](/bunker/docs/sdk/connections/), and Bunker makes the request from the server.

## Define one

*Admin → Connections* (`/api/v1/admin/connections`):

| Field | Example | |
|---|---|---|
| Name | `hr-api` | What apps use |
| Base URL | `https://hr.internal.example.com/api/v2` | Requests can only go to paths under it |
| Credentials | a header such as `Authorization: Bearer ...` or `X-API-Key` | Stored encrypted; never sent to the page |
| Methods | `GET` only, or `GET, POST` | Others are refused |
| Apps | all apps, or a list | Which apps may use it |
| Private addresses | off | See below |

## SSRF protection

Server-side requests are the classic way to reach things you didn't mean to expose. Bunker:

- only sends requests to the connection's base URL (the page chooses the path, never the host),
- resolves DNS itself and **refuses private, loopback, link-local and cloud metadata addresses** (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `127.0.0.0/8`, `169.254.0.0/16` including `169.254.169.254`, `::1`, `fc00::/7`, `fe80::/10`) unless you turn on **Private addresses** for that connection,
- pins the checked IP for the connection so DNS rebinding can't swap it, and re-checks every redirect,
- caps response size and time,
- strips cookies and hop-by-hop headers.

Internal APIs usually live on private addresses, so turning that option on for a specific connection is normal. It applies to that connection's host only.

## Audit and limits

Every call is in the [audit log](/bunker/docs/admin/audit/) with the person, the app, the connection, the method, the path and the status. Calls are rate limited per person.
