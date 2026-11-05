---
title: SDK overview
description: The bunker.js API available to every page.
sidebar: { order: 1 }
---

Every page gets `window.bunker` automatically. Every method returns a Promise and runs with the **viewer's** permissions.

```html
<script>
  const me = await bunker.me();
  document.querySelector('h1').textContent = `Hi ${me.name}`;
</script>
```

| Module | Purpose |
|---|---|
| [`bunker.me()`](/bunker/docs/sdk/me/) | Current viewer |
| [`bunker.db`](/bunker/docs/sdk/db/) | Collections with live updates |
| [`bunker.files`](/bunker/docs/sdk/files/) | Uploads |
| [`bunker.ai`](/bunker/docs/sdk/ai/) | Model calls |
| [`bunker.m365`](/bunker/docs/sdk/m365/) | Microsoft 365 data |
| [`bunker.storage`](/bunker/docs/sdk/storage/) | Per-viewer key/value storage |

## Errors
Failures throw a `BunkerError` with a `code` field: `forbidden`, `not_found`, `quota_exceeded`, `invalid`, `consent_required`, or `unavailable`.
