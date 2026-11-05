---
title: Configuration reference
description: Every setting, as environment variable or bunker.toml key.
sidebar: { order: 7 }
---

Each setting can be given as an environment variable or in `bunker.toml` (lowercase, without the `BUNKER_` prefix, grouped by section).

| Variable | Default | Description |
|---|---|---|
| `BUNKER_URL` | required | Public HTTPS address |
| `BUNKER_LISTEN` | `:443` | Listen address |
| `BUNKER_TLS` | `auto` | `auto` (Let's Encrypt), `off`, or `file` |
| `BUNKER_TLS_CERT` / `_KEY` | | Paths when `BUNKER_TLS=file` |
| `BUNKER_DATA_DIR` | `/data` | Database and files |
| `BUNKER_ENTRA_TENANT_ID` | required | Tenant ID |
| `BUNKER_ENTRA_CLIENT_ID` | required | App registration ID |
| `BUNKER_ENTRA_CLIENT_SECRET` | required | Client secret |
| `BUNKER_ADMINS` | required | Admin emails or groups |
| `BUNKER_ALLOWED_GROUPS` | all members | Who can sign in |
| `BUNKER_ALLOW_GUESTS` | `false` | Allow B2B guests |
| `BUNKER_ORG_WIDE_SHARING` | `true` | Allow "everyone can view" |
| `BUNKER_SITE_MAX_MB` | `100` | Files per site |
| `BUNKER_UPLOAD_MAX_MB` | `25` | Single upload |
| `BUNKER_AI_*` | | See [AI providers](/bunker/docs/admin/ai/) |
| `BUNKER_M365_SCOPES` | empty (off) | See [Microsoft 365](/bunker/docs/admin/m365/) |
| `BUNKER_AUDIT_*` | | See [Audit log](/bunker/docs/admin/audit/) |
| `BUNKER_BACKUP_URL` | | See [Backups](/bunker/docs/admin/backups/) |
| `BUNKER_SECRET_KEY` | generated | Encrypts stored tokens. Keep it with your backups. |
