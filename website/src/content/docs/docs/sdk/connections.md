---
title: bunker.connections
description: Call company APIs that an admin has set up as named connections, without keys in the page.
sidebar: { order: 9, badge: { text: 'Preview', variant: 'caution' } }
---

Pages can't reach the internet or your network. To use an internal system (an HR API, a ticketing tool, a data warehouse endpoint), an admin defines a **connection** with a name, a base URL and credentials. Pages then call it by name:

```js
const res = await bunker.connections.fetch('hr-api', {
  method: 'GET',
  path: '/people',
  query: { team: 'Sales' },
});
// { status, data }
```

- Only connection names the admin created work, and the admin chooses which apps may use each one.
- The base URL and credentials stay on the server. The page sends only a method, a path under the base URL, a query and a body.
- Bunker refuses requests to private, loopback, link-local and cloud metadata addresses unless the admin explicitly allows them for that connection, and re-checks the address after DNS resolution and on redirects (SSRF protection).
- Every call is audited and rate limited.

See [Admin → Connections](/bunker/docs/admin/connections/) to set one up.

## API

`POST /api/v1/sites/{site}/connections/{name}/fetch` with `{ method, path, query, body }`.
