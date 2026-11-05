---
title: Upload a site
description: Drop in HTML from anywhere.
sidebar: { order: 2 }
---

Already have a page, for example from ChatGPT, Claude, or a colleague?

1. Click **New site → Upload**.
2. Drop a single `.html` file, a folder, or a `.zip`.
3. Bunker checks it and shows a preview. Publish when ready.

## What gets checked
- **Size and type.** Web files, images, fonts, PDFs and video only, within the admin's limits.
- **Secrets.** Uploads containing what look like API keys or passwords are blocked, with a pointer to the line.
- **External calls.** Scripts or styles loaded from other websites won't work in the sandbox. Bunker lists them and offers to bundle common libraries locally.

`index.html` is the entry page. Other files are served relative to it.
