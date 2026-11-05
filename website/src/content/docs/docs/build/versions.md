---
title: Versions and drafts
description: Every publish is a version. Drafts let you preview changes before anyone sees them.
sidebar: { order: 4 }
---

## Versions

Every publish creates a numbered **version**: an upload, an AI edit you published, a restore. One version is **live**.

| Do this | Operation |
|---|---|
| List versions with author, message, size and file count | `versions.list` — `GET /api/v1/sites/{site}/versions` |
| See which files changed (against the previous version, another version, or the draft with `against=0`) | `versions.diff` — `GET /api/v1/sites/{site}/versions/{n}/diff` |
| Open an old version without restoring it (editors) | `/s/<app>/?v=3` |
| Make an old version live again | `versions.restore` — `POST /api/v1/sites/{site}/versions/{n}/restore` |
| Download a version as a zip | `GET /api/v1/sites/{site}/export.zip?version=3` |

Restoring **creates a new version** with the old files, so history only grows and you can always undo the undo.

Versions are cheap: files are stored once by their content hash, so a version that changes one file adds one file.

Give a version a message with `message` on deploy or publish ("Added CSV export"). Without one, it's called "Version N".

## Drafts

A **draft** is a working copy that only editors see.

- The AI builder always edits the draft.
- Deploy with `publish=false` to upload a draft instead of going live.
- Edit single files in the draft: `files.write` (`PUT /api/v1/sites/{site}/files/{path}`) and `files.delete`. `files.list` and `files.read` show the draft if there is one, else the live version.
- Preview at `/s/<app>/?draft=1`. Viewers who try this see the live version.
- **Publish** the draft as the next version (`drafts.publish`, with an optional `message`), or **discard** it (`drafts.discard`).

Open apps hear about a new live version and can reload (the shell gets a `deploy` event).
