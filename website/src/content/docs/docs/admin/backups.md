---
title: Backups and upgrades
description: Everything Bunker stores is in one folder. How to back it up, restore it and upgrade safely.
sidebar: { order: 6 }
---

## What's in the data folder

Everything lives in `BUNKER_DATA_DIR`: `/var/lib/bunker` (Linux installer), `/data` (containers), else `./bunker-data`.

| Path | |
|---|---|
| `bunker.db`, `bunker.db-wal`, `bunker.db-shm` | The SQLite database (WAL mode): people, apps, versions, sharing, app data, settings, audit |
| `blobs/` | App files and uploads, stored by SHA-256, with `.br` and `.gz` copies |
| `secret.key` | The key that encrypts secrets and signs content tickets. **Back it up with the rest**, or set `BUNKER_SECRET_KEY` yourself |
| `certs/` | Let's Encrypt certificates, when `BUNKER_TLS=auto` |
| `setup-code.txt` | Only until setup is done |

## Back up

The simplest safe backup is a consistent database copy plus the files:

```sh
# 1. A consistent copy of the database while Bunker runs
sqlite3 /var/lib/bunker/bunker.db "VACUUM INTO '/backup/bunker.db'"
# 2. Files and the key (blobs are immutable, so rsync is cheap)
rsync -a /var/lib/bunker/blobs /var/lib/bunker/secret.key /backup/
```

Or stop Bunker briefly and copy the whole folder. Disk snapshots (Azure managed disk, EBS, a Fly volume snapshot) also work: SQLite in WAL mode recovers cleanly from a crash-consistent snapshot.

:::note[Preview]
A built-in `bunker backup` / `bunker restore` command (database `VACUUM INTO` plus the blobs in one archive) and an admin download button are being built now.
:::

## Restore

Stop Bunker, put `bunker.db`, `blobs/` and `secret.key` back into an empty data folder, and start it. Database migrations run automatically.

## Upgrade

1. Back up.
2. Replace the binary or image:
   - Linux installer: run the same `curl ... | sudo sh` again (or `bunker update`).
   - Docker: `docker pull ghcr.io/kucukkanat/bunker` and recreate the container.
   - Render, Railway, Fly: redeploy.
3. Bunker migrates the database on start. `GET /healthz` returns `{"ok": true, "version": "..."}` when it's ready.

Pin a version with `--version v1.2.3` (installer), an image tag (`ghcr.io/kucukkanat/bunker:v1.2.3`) or `bunkerVersion` (Azure).

## Monitoring

- `GET /healthz`: `200` when the database answers.
- `GET /metrics`: Prometheus text format (requests by status class, latency, in-flight requests, people, apps, versions, database connections, goroutines, heap, uptime, build version). Open from the machine itself (loopback), or anywhere with `Authorization: Bearer $BUNKER_METRICS_TOKEN`. Otherwise it answers 404.
- Logs are JSON on stderr (`BUNKER_LOG_FORMAT=text` for humans, `BUNKER_DEBUG=true` for more). Requests slower than 2 seconds are logged.
