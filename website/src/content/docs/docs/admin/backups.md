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

### With the CLI

`bunker backup [file]` downloads a consistent archive (database `VACUUM INTO` snapshot, blobs and `secret.key`) from the server you're signed in to. It needs an admin token with the `admin` scope. Add `--no-key` to leave `secret.key` out.

```bash
bunker backup /backup/bunker-$(date +%F).tar.gz
```

## Restore

Stop Bunker, then on the server run `bunker restore <file>`. It unpacks the archive into `BUNKER_DATA_DIR`, refuses while a server still answers on `BUNKER_LISTEN`, asks before replacing existing data (`--yes` skips the question) and keeps the old database as `bunker.db.pre-restore-<time>`. Start Bunker again afterwards; migrations run automatically.

```bash
sudo systemctl stop bunker
sudo -u bunker BUNKER_DATA_DIR=/var/lib/bunker bunker restore /backup/bunker-2026-11-05.tar.gz
sudo systemctl start bunker
```

By hand: put `bunker.db`, `blobs/` and `secret.key` back into an empty data folder and start Bunker.

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
