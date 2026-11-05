---
title: llms.txt and the API
description: Machine-readable docs for agents, the operation list and the OpenAPI document.
sidebar: { order: 2 }
---

Agents don't need these docs. They can ask Bunker directly.

| URL | What |
|---|---|
| `/llms.txt` | A short guide for agents: how to authenticate, deploy, the sandbox rules and the SDK |
| `/llms-full.txt` | The same, plus every API operation with its parameters |
| `/api/v1/agent-guide` | The full agent guide (authentication, deploy, sandbox, SDK, operations) |
| `/api/v1/ops` | Every operation as JSON: name, method, path, summary, parameters, and whether it's page-safe or destructive |
| `/api/v1/openapi.json` | An OpenAPI document generated from the same list |
| `/api/v1/libraries` | The built-in front-end libraries |

## Everything in the UI is in the API

Bunker has one rule for its own code: every dashboard action is an **operation** in a single registry. The REST API, the CLI (`bunker api`, `bunker ops`), both MCP servers and the list above are generated from it. There are no hidden dashboard-only endpoints.

```sh
curl -s https://bunker.example.com/api/v1/ops | jq '.ops[] | {name, method, path, summary}'
```

```json
{ "name": "sites.deploy", "method": "POST", "path": "/api/v1/sites/{site}/deploy",
  "summary": "Upload files (zip, folder, single file or JSON) and publish them" }
```

## Calling the API

- **Tokens:** `Authorization: Bearer bk_...`. No CSRF header needed.
- **Browser sessions:** unsafe requests (POST, PUT, PATCH, DELETE) must send `X-Bunker: 1` and come from Bunker's own origin.
- **Errors** are JSON `{"error": {"code", "message"}}`, with plain-English messages that say what to do next.
- **Rate limits:** 1,200 requests a minute per person (burst 300) and 30 a minute per IP for anonymous writes (`BUNKER_RATE_USER_PER_MIN`, `BUNKER_RATE_PUBLIC_PER_MIN`). Over the limit you get `429` with `Retry-After`.
