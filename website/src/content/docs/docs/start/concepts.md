---
title: Core concepts
description: Sites, versions, roles, and the sandbox.
sidebar: { order: 3 }
---

**Site:** one app with its own address (`/s/<slug>`), files, database, uploads and sharing list.

**Owner:** the person who created the site. Owners can share, rename, transfer, or delete it.

**Roles:**

| Role | Open the site | Use data and files | Edit and publish | Share | Delete |
|---|---|---|---|---|---|
| Viewer | ✓ | ✓ (within [data rules](/bunker/docs/sdk/db/#data-rules)) | | | |
| Editor | ✓ | ✓ | ✓ | | |
| Owner | ✓ | ✓ | ✓ | ✓ | ✓ |
| Admin | ✓ | ✓ | ✓ | ✓ | ✓ |

**Version:** a snapshot of a site's files, made on every publish. You can restore any version. Data and uploads are not versioned.

**Sandbox:** the locked frame every site runs in. The page can only talk to Bunker, and Bunker checks the viewer's role on every request. See [Sandbox & CSP](/bunker/docs/security/sandbox/).

**SDK:** the `bunker.js` script that gives pages identity, data, files, AI and Microsoft 365 access. It's added automatically to every page.
