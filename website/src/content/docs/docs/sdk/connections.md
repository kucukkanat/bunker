---
title: bunker.connections
description: Call company APIs that an admin has set up as named connections, without keys in the page.
sidebar: { order: 9 }
---

Pages can't reach the internet or your network. To use an internal system (an HR API, a ticketing tool, a data warehouse endpoint), an admin defines a **connection** with a name, a base URL and credentials. Pages then call it by name:

```js
const res = await bunker.connections.fetch('hr-api', {
  method: 'GET',          // default GET
  path: '/people',        // under the connection's base URL
  query: { team: 'Sales', tag: ['a', 'b'] },
});

if (res.ok) render(res.json);
```

## Options

| Option | |
|---|---|
| `method` | `GET` (default), `POST`, `PUT`, `PATCH`, `DELETE` or `HEAD`, if the connection allows it |
| `path` | Starts with `/`. No `?`, `#` or spaces: put query values in `query` |
| `query` | An object. Arrays repeat the key (`tag=a&tag=b`) |
| `body` | An object or array is sent as JSON; a string is sent as text |
| `headers` | Only `Accept`, `Accept-Language`, `Content-Type`, `Cache-Control`, `If-Match`, `If-None-Match`, `If-Modified-Since` and `Prefer` |

## The answer

```js
{
  status: 200,
  ok: true,                 // status below 400
  headers: { 'content-type': 'application/json', etag: '"abc"' },
  body: '{"people":[...]}', // text, or base64 for binary
  encoding: 'text',         // or 'base64'
  json: { people: [...] }   // when the answer is JSON
}
```

An error status from the API (say `404`) is a normal answer with `ok: false`. The call itself only throws when Bunker refuses or can't reach the API:

| `err.code` | Means |
|---|---|
| `not_found` | No connection with that name |
| `forbidden` | This app isn't allowed to use it, the method or path isn't allowed, or the address is private and the admin hasn't allowed that |
| `invalid` | A bad path or a header pages can't set |
| `quota_exceeded` | Too many calls from this app (default 60 a minute) |
| `too_large` | The answer is over 5 MB |
| `timeout` | The API didn't answer in time |
| `upstream` | The API couldn't be reached or tried to redirect elsewhere |

## What keeps it safe

- Only connection names the admin created work, and the admin chooses which apps may use each one.
- The base URL and credentials stay on the server. The page sends only a method, a path under the base URL, a query and a body.
- Bunker refuses requests to private, loopback, link-local and cloud metadata addresses unless the admin explicitly allows them for that connection. It checks the address after DNS resolution and on redirects (SSRF protection).
- Every call is audited and rate limited.

See [Admin → Connections](/bunker/docs/admin/connections/) to set one up.

## API

| Operation | Route |
|---|---|
| `connections.list` | `GET /api/v1/sites/{site}/connections`: the connections this app may use, with their methods and paths |
| `connections.fetch` | `POST /api/v1/sites/{site}/connections/{name}/fetch` with `{ method, path, query, headers, body }` (or `bodyBase64` for binary) |

```sh
bunker api connections.fetch --site team-directory --name hr-api --path /people
```
