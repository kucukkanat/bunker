---
title: MCP server
description: Let Claude Code, Cursor and other AI tools build Bunker sites.
sidebar: { order: 2 }
---

`bunker mcp` runs a Model Context Protocol server over stdio, using your CLI login.

```json
{
  "mcpServers": {
    "bunker": { "command": "bunker", "args": ["mcp"] }
  }
}
```

## Tools
| Tool | Does |
|---|---|
| `list_sites` | Sites you can edit |
| `get_site` | Files and metadata |
| `deploy` | Publish files as a new version |
| `restore` | Roll back to a version |
| `share` | Change sharing |
| `sdk_docs` | Returns SDK reference for the model |

Every MCP action is audited as you.
