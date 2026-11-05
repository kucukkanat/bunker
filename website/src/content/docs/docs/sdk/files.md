---
title: bunker.files
description: Upload files and images from an app, list, read and show them, with the same access rules as the app.
sidebar: { order: 4 }
---

```js
const f = await bunker.files.upload(input.files[0], { folder: 'receipts' });
// { id, name, size, type, url, _author, _created, ... }

img.src = f.url;                                   // works in <img>, <a>, <video>, <audio>
const list = await bunker.files.list({ folder: 'receipts', mine: true });  // array with .next
const info = await bunker.files.get(f.id);
const text = await bunker.files.read(f.id, 'text'); // or 'json', or a Blob by default
await bunker.files.delete(f.id);                    // also .remove()
```

- `upload` takes a `File`, a `Blob`, a string, or an array of files (then it returns an array). Give a Blob a name with `{ name: 'photo.png' }`.
- File inputs and drag-and-drop work in the sandbox. Use `bunker.files` to keep what people upload.
- To attach files to records, store the file's `id` (and `url`) in a [`bunker.db`](/bunker/docs/sdk/db/) document.
- `url` is a signed content URL (`/s/<app>/~f/<ticket>/<id>/file`) that only works for people who can open the app, and only for a few hours. Save the `id`; get a fresh URL with `bunker.files.url(id)` or from `list`.
- Video and audio support seeking (HTTP range requests).

## Who can see and remove uploads

Uploads have one rule per app, with the same names as [data rules](/bunker/docs/sdk/db/#access-rules) (`own` by default: everyone sees all files, people remove only their own, editors remove any). Editors change it with `uploads.rule`; pages can't (`files.rule()` throws `forbidden`).

## Limits

| | Default | Setting |
|---|---|---|
| One file | 100 MB | `BUNKER_UPLOAD_MAX_MB` |
| All uploads of one app | 2 GB | `BUNKER_UPLOADS_SITE_MAX_MB` |
| Files per app | 20,000 | `BUNKER_UPLOADS_MAX_FILES` |

Allowed types are images, PDF, video, audio, Office documents, text/CSV and zip. Files need an extension, and anything that looks like a program is refused (and audited as `upload.blocked`).

## API

| Operation | Route (under `/api/v1/sites/{site}`) |
|---|---|
| `uploads.list` | `GET /uploads` (`folder`, `mine`, `limit`, `before`) |
| `uploads.upload` | `POST /uploads` (multipart, optional `folder`) |
| `uploads.get` | `GET /uploads/{id}` |
| `uploads.content` | `GET /uploads/{id}/content` |
| `uploads.delete` | `DELETE /uploads/{id}` |
| `uploads.rule` (editors) | `PUT /uploads/_rule` |
