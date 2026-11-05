---
title: Data at rest
description: What Bunker stores, where, and what is encrypted.
sidebar: { order: 3 }
---

Everything lives in the data folder on your server (`BUNKER_DATA_DIR`). Nothing is sent to the Bunker project; there is no telemetry.

| Data | Where | Protection |
|---|---|---|
| People, apps, versions, sharing, settings, audit | `bunker.db` (SQLite, WAL mode) | File permissions (the service runs as its own user, `UMask=0077`, data dir `0700`) |
| App data (`bunker.db` collections), per-person storage | `bunker.db` | Same |
| App files and uploads | `blobs/<aa>/<sha256>` | Same. Deduplicated by content hash |
| Built-in account passwords | `bunker.db` | **argon2id** hashes, never the password |
| Session cookies, personal tokens, invite and reset links, OAuth codes | `bunker.db` | Stored only as **SHA-256 hashes** of random tokens |
| SSO client secret, AI API keys, other secret settings | `bunker.db` | **AES-256-GCM** with the server key |
| Entra delegated access and refresh tokens | `bunker.db` | **AES-256-GCM** |
| Sign-in state during login | short-lived cookie | AES-256-GCM, 10-minute lifetime |
| Server key | `secret.key` (`0600`) or `BUNKER_SECRET_KEY` | Keep it out of backups that others can read, or set it from a secret store |
| TLS certificates | `certs/` | When `BUNKER_TLS=auto` |

## Encrypting the disk

Bunker doesn't encrypt the database file itself. Use disk encryption: Azure managed disks (encrypted by default), LUKS on Linux, or your platform's encrypted volumes.

## Deleting data

- Deleting an app moves it to the trash; after `BUNKER_TRASH_DAYS` (30) it is purged with its versions and data. Files no other version uses are garbage-collected.
- Disabling a person stops all their sessions, tokens and content tickets immediately. Their apps stay, and an admin can transfer them (`sites.transfer_all`).

## AI providers

Prompts go to the AI provider the admin chose, from the server. By default Bunker keeps only metadata about AI calls (who, app, model, token counts). See [AI providers](/bunker/docs/admin/ai/) for keeping prompts inside your tenant or network.
