---
title: Connections
description: Give apps safe, audited access to internal APIs through named connections with SSRF protection.
sidebar: { order: 3 }
---

Apps can't reach your network or the internet. A **connection** is an admin-defined door to one API: a name, a base URL, secret headers and a policy. Apps call it by name with [`bunker.connections.fetch`](/bunker/docs/sdk/connections/), and Bunker makes the request from the server.

## Define one

In *Admin → Connections*, or with the API (`admin.connections.create`):

```sh
bunker api admin.connections.create --data '{
  "name": "hr-api",
  "description": "People and teams from the HR system",
  "baseUrl": "https://hr.internal.example.com/api/v2",
  "headers": { "Authorization": "Bearer hr-xxxxxxxx" },
  "allowedMethods": ["GET"],
  "allowedPathPrefixes": ["/people", "/teams"],
  "sites": ["team-directory", "org-chart"],
  "allowPrivate": true
}'
```

| Field | Default | |
|---|---|---|
| `name` | | What apps use. Lowercase letters, numbers, `-` and `_`, up to 41 characters |
| `description` | | Shown to app builders |
| `baseUrl` | | `https://` only, no query string. Requests can only go to paths under it |
| `headers` | none | Secret headers added to every call, such as `Authorization` or `X-API-Key`. Stored encrypted; shown as `••••••` afterwards. Send `null` for a header to remove it |
| `allowedMethods` | `["GET"]` | Any of `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `HEAD` |
| `allowedPathPrefixes` | `["/"]` | Paths apps may call. `/people` allows `/people` and `/people/...`; `/v1/report*` allows anything starting with it |
| `sites` | none | App addresses allowed to use it, or `["*"]` for all apps. **Empty means no app can use it yet** |
| `rateLimitPerMinute` | `60` | Calls per minute, per app. Up to 6,000 |
| `timeoutMs` | `15000` | 500 to 60,000 |
| `allowPrivate` | `false` | Allow private network addresses (see below) |

Manage them with:

| Operation | Route |
|---|---|
| `admin.connections.list` | `GET /api/v1/admin/connections` |
| `admin.connections.create` | `POST /api/v1/admin/connections` |
| `admin.connections.get` | `GET /api/v1/admin/connections/{name}` |
| `admin.connections.update` | `PUT /api/v1/admin/connections/{name}` (send only what changes) |
| `admin.connections.delete` | `DELETE /api/v1/admin/connections/{name}` |

To let one more app use a connection:

```sh
bunker api admin.connections.update hr-api --data '{"sites": ["team-directory", "org-chart", "onboarding"]}'
```

Secrets are never returned. To keep a stored header while you change other fields, leave it out, or send `""` or `"••••••"` as its value.

## SSRF protection

Server-side requests are the classic way to reach things you didn't mean to expose. Bunker:

- only sends requests to the connection's base URL. The page chooses the path, never the host,
- rejects paths with `..`, `.`, `//`, backslashes or anything outside `allowedPathPrefixes`,
- checks the **exact IP it connects to**, after DNS resolution, so DNS rebinding can't sneak past. It refuses private, loopback, link-local, cloud metadata, carrier-grade NAT, documentation, multicast and reserved ranges (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `127.0.0.0/8`, `169.254.0.0/16` including `169.254.169.254`, `100.64.0.0/10`, `::1`, `fc00::/7`, `fe80::/10` and more) unless `allowPrivate` is on,
- follows at most 5 redirects, and only to `https://` on the same host,
- never uses the server's `HTTP_PROXY` settings, so the check sees the real target,
- lets pages set only harmless headers (`Accept`, `Accept-Language`, `Content-Type`, `Cache-Control`, `If-Match`, `If-None-Match`, `If-Modified-Since`, `Prefer`). Cookies and auth headers from the page are refused, and the connection's secret headers always win,
- passes back only safe response headers (content type, caching, rate-limit and paging headers). `Set-Cookie` never reaches the page.

Internal APIs usually live on private addresses, so turning on `allowPrivate` for a specific connection is normal. It applies to that connection only.

## Audit and limits

| | |
|---|---|
| Response size | 5 MB. Bigger answers fail with `too_large` |
| Time | `timeoutMs` per call (default 15 seconds) |
| Rate | `rateLimitPerMinute` per app and connection (default 60). Over it, apps get `quota_exceeded` |

Every call is in the [audit log](/bunker/docs/admin/audit/) as `connection.call`, with the person, the app, the connection, the method, the path, the status, the size and the time. Blocked rate-limit hits are `connection.rate_limited`. Creating, changing and deleting connections is `admin.config`; the header names are recorded, never their values.
