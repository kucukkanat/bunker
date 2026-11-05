---
title: MCP for Claude, Cursor and ChatGPT
description: Connect your coding agent or chat assistant to Bunker with just the URL, or run a local MCP server.
sidebar: { order: 1 }
---

Bunker speaks the **Model Context Protocol**, so agents can create apps, deploy files, edit, share and roll back, with **your** permissions and nothing more.

## Remote MCP: just the URL

Add a connector (Claude, ChatGPT) or an MCP server (Cursor, VS Code, Claude Code) with this URL:

```
https://bunker.example.com/mcp
```

The client discovers everything else:

1. Its first request gets `401` with `WWW-Authenticate: Bearer resource_metadata=".../.well-known/oauth-protected-resource/mcp"`.
2. It reads the protected-resource metadata (`/.well-known/oauth-protected-resource` and `/.well-known/oauth-protected-resource/mcp`) and the authorization-server metadata (`/.well-known/oauth-authorization-server`, also at `/.well-known/openid-configuration`).
3. It identifies itself with a **client ID metadata document** (an https URL as `client_id`), or registers itself at `/oauth/register`.
4. Your browser opens **Bunker's consent page** (`/oauth/authorize`). You sign in as usual (built-in account or company SSO) and see which app is asking and where it will send you back. Plain-http redirect URIs are only accepted for `localhost` / `127.0.0.1`.
5. The client exchanges the code at `/oauth/token` (PKCE `S256` only, bound to the `/mcp` resource). Access tokens last **1 hour** (`BUNKER_OAUTH_ACCESS_TTL`, in seconds); refresh tokens **rotate** on every use, and reusing an old one revokes the whole family. Revoke at `/oauth/revoke` or from your token list.

```json title=".mcp.json (Cursor, VS Code, Claude Code)"
{ "mcpServers": { "bunker": { "url": "https://bunker.example.com/mcp" } } }
```

```sh
claude mcp add --transport http bunker https://bunker.example.com/mcp
```

Bunker speaks the stateless **2026-07-28** protocol and the earlier session-based versions (2025-11-25, 2025-06-18, 2025-03-26, 2024-11-05), so new and older clients both work.

## Local MCP: stdio

For agents that run on your machine, using your CLI login:

```sh
bunker login https://bunker.example.com
claude mcp add bunker -- bunker mcp
```

```json title=".mcp.json"
{ "mcpServers": { "bunker": { "command": "bunker", "args": ["mcp"] } } }
```

## Tools

| Tool | |
|---|---|
| `create_site_from_html` | Publish an HTML page as a new app |
| `deploy_files` | Deploy files to an app (publish or draft) |
| `edit_file` | Edit one file of an app |
| `build_with_ai` | Ask Bunker's AI builder to build or change an app |
| `get_sdk_docs` | The SDK, sandbox and deploy rules |
| One tool per API operation | Generated from `GET /api/v1/ops`: list, share, versions, restore, data, stats... |
| `fn_list`, `fn_call`, `fn_runs` | List an app's [server functions](/bunker/docs/build/server-functions/), call one, and read recent runs with their logs (handy for an agent fixing its own function) |
| `secrets_list`, `secrets_set`, `secrets_delete` | Manage an app's function secrets. Values can be set, never read |

Destructive operations (delete, unshare, discard) are marked so clients can ask you first.

**Resources:** `bunker://docs/agent-guide`, `bunker://docs/sdk`, `bunker://docs/sandbox`, and `bunker://sites/<app>` for each app you can open.

## Safety

- The agent acts as you. It can't see apps you can't see.
- Page-only limits still apply: an app's code can never deploy or share; only you, through the agent, can. The same goes for server functions.
- An agent can store a secret you give it, but can never read one back.
- Every action is in the [audit log](/bunker/docs/admin/audit/), marked as coming from a token.
