---
title: bunker.storage
description: A localStorage replacement for sandboxed pages.
sidebar: { order: 7 }
---

Sandboxed pages have no `localStorage`. Use `bunker.storage` for small per-viewer preferences, such as the selected tab or a draft.

```js
await bunker.storage.set('theme', 'dark');
const theme = await bunker.storage.get('theme');   // null if missing
await bunker.storage.remove('theme');
```

Values are stored per viewer, per site, up to 64 KB each. Shared data belongs in [`bunker.db`](/bunker/docs/sdk/db/).
