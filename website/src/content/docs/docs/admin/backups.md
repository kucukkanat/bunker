---
title: Backups & upgrades
description: Keep your data safe and Bunker current.
sidebar: { order: 6 }
---

All state lives in `/data`:
- `bunker.db`: SQLite (sites, versions, sharing, site databases, audit log)
- `files/`: uploads and site files

## Continuous backup
Bunker can replicate SQLite continuously to S3-compatible storage or Azure Blob:
```sh
BUNKER_BACKUP_URL=abs://bunker-backups/prod
```
Restore with `bunker restore --from abs://bunker-backups/prod`.

## Snapshot backup
`bunker backup /backups/bunker-$(date +%F).tar.gz` makes a consistent copy while Bunker is running.

## Upgrades
Replace the binary or image and restart. Migrations run automatically, and Bunker backs up `bunker.db` before migrating.
