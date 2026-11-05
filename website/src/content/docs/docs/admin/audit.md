---
title: Audit log
description: What Bunker records and how to export it.
sidebar: { order: 5 }
---

Bunker records:

| Event | Details |
|---|---|
| `auth.login`, `auth.denied` | user, IP, reason |
| `site.create`, `site.publish`, `site.restore`, `site.delete` | site, version, actor |
| `site.share`, `site.unshare`, `site.transfer` | site, target user/group, role |
| `site.view` | site, viewer (sampled per viewer per hour) |
| `ai.call` | site, user, model, tokens (content optional) |
| `m365.call` | site, user, Graph resource, status |
| `upload.blocked` | site, user, reason (e.g. secret detected) |
| `admin.config` | setting changed, old → new (secrets redacted) |

## Access
**Admin → Audit log** lets you filter by user, site, event or date. Export as CSV or JSON.

## Streaming
Set `BUNKER_AUDIT_SYSLOG=udp://siem.corp.com:514` or `BUNKER_AUDIT_WEBHOOK=https://…` to stream events as JSON lines.

Retention is controlled by `BUNKER_AUDIT_RETENTION_DAYS` (default 365).
