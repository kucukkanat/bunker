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

Bunker makes a consistent backup while it runs: a SQLite snapshot (`VACUUM INTO`) plus the files and the key, in one `.tar.gz`. Blobs never change once written, so copying them live is safe. `.br` and `.gz` copies are left out; Bunker rebuilds them when needed.

### Download one now

```sh
curl -X POST -H "Authorization: Bearer $TOKEN" -OJ \
  https://bunker.example.com/api/v1/admin/backup
# saves bunker-backup-20261105-021500.tar.gz
```

Or `bunker api admin.backup > bunker-backup.tar.gz`. The archive includes `secret.key` so encrypted settings (sign-in secret, AI keys, connection headers) restore too. **Keep it as safe as the server.** Add `?includeKey=false` to leave the key out, if you keep it elsewhere or set `BUNKER_SECRET_KEY`.

### Scheduled snapshots

Set a folder and Bunker writes a snapshot there on a schedule, and deletes old ones:

| Variable | Default | |
|---|---|---|
| `BUNKER_BACKUP_DIR` | off | Folder for snapshots. A relative path is inside the data folder (`backups` → `/var/lib/bunker/backups`) |
| `BUNKER_BACKUP_EVERY` | `24h` | How often, as a Go duration (`6h`, `12h`, `168h`). At least `1h` |
| `BUNKER_BACKUP_KEEP` | `7` | How many snapshots to keep |

```sh
# /etc/bunker/bunker.env
BUNKER_BACKUP_DIR=/mnt/backups/bunker
BUNKER_BACKUP_EVERY=12h
BUNKER_BACKUP_KEEP=14
```

- Files are named `bunker-YYYYMMDD-HHMMSS.tar.gz` (UTC) and always include `secret.key`.
- The first check runs a minute after start. If the newest snapshot is older than `BUNKER_BACKUP_EVERY`, a new one is written.
- A failed snapshot is logged and audited as `admin.backup.failed`, and retried after 15 minutes.
- Put the folder on another disk or a mounted share. A snapshot on the same disk won't save you from losing that disk.

See the snapshots and settings, or write one now:

```sh
bunker api admin.backups.list   # {"enabled": true, "dir": "...", "every": "12h0m0s", "keep": 14, "backups": [...]}
bunker api admin.backups.run    # writes one now
```

### By hand

Without Bunker's help, take a consistent database copy plus the files:

```sh
sqlite3 /var/lib/bunker/bunker.db "VACUUM INTO '/backup/bunker.db'"
rsync -a /var/lib/bunker/blobs /var/lib/bunker/secret.key /backup/
```

Or stop Bunker briefly and copy the whole folder. Disk snapshots (Azure managed disk, EBS, a Fly volume snapshot) also work: SQLite in WAL mode recovers cleanly from a crash-consistent snapshot.

## Restore

1. Stop Bunker.
2. Unpack the backup into an **empty** data folder:

   ```sh
   sudo mkdir -p /var/lib/bunker && cd /var/lib/bunker
   sudo tar -xzf /mnt/backups/bunker/bunker-20261105-021500.tar.gz
   sudo chown -R bunker:bunker /var/lib/bunker
   ```

   If you left the key out of the backup, put `secret.key` back too (or set the same `BUNKER_SECRET_KEY`).
3. Start Bunker. Database migrations run automatically.

The `bunker-backup.json` file in the archive just describes the backup (format, Bunker version, date). You can delete it after restoring.

## Upgrade

1. Back up.
2. Replace the binary or image:
   - Linux installer: run the same `curl ... | sudo sh` again (or `bunker update`).
   - Docker: `docker pull ghcr.io/kucukkanat/bunker` and recreate the container.
   - Render, Railway, Fly: redeploy.
3. Bunker migrates the database on start. `GET /healthz` returns `{"ok": true, "version": "..."}` when it's ready.

Pin a version with `--version v1.2.3` (installer), an image tag (`ghcr.io/kucukkanat/bunker:v1.2.3`) or `bunkerVersion` (Azure).

## Monitoring

- *Admin* (the overview page) and `bunker api admin.stats`: people (active today and this week), admins, apps, templates, apps in the trash, new apps this week, versions, file storage, database size, audit events, the 10 most-viewed apps (last 30 days; `--days 90` for another window), the Bunker version and how many audit events streaming had to drop.
- `GET /healthz`: `200` when the database answers.
- `GET /metrics`: Prometheus text format (requests by status class, latency, in-flight requests, people, apps, versions, database connections, goroutines, heap, uptime, build version). Open from the machine itself (loopback), or anywhere with `Authorization: Bearer $BUNKER_METRICS_TOKEN`. Otherwise it answers 404.
- Logs are JSON on stderr (`BUNKER_LOG_FORMAT=text` for humans, `BUNKER_DEBUG=true` for more). Requests slower than 2 seconds are logged.
