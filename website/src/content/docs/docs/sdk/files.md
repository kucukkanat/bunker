---
title: bunker.files
description: Upload and serve files.
sidebar: { order: 4 }
---

```js
const input = document.querySelector('input[type=file]');
const file = await bunker.files.upload(input.files[0], { folder: 'receipts' });
// { id, name, size, type, url, _author, _created }

img.src = file.url;                         // usable in <img>, <video>, <a>
const all = await bunker.files.list({ folder: 'receipts' });
await bunker.files.delete(file.id);
```

Files follow the site's sharing. Uploads use the same [data rules](/bunker/docs/sdk/db/#data-rules) as collections, set with `bunker.files.rule(...)`. Allowed types and size limits come from the admin.
