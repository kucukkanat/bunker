---
title: Configuration reference
description: Every BUNKER_* environment variable the server and CLI read, with defaults.
sidebar: { order: 7 }
---

All configuration is environment variables. You usually need none: Bunker picks sensible defaults and detects its URL on common platforms. With the Linux installer, put them in `/etc/bunker/bunker.env` and `systemctl restart bunker`.

**Saved settings can be pinned.** Any setting stored in the database (`auth.provider`, `sharing.org_wide`, `ai.provider`, ...) can be fixed from the environment as `BUNKER_` + the key in capitals, with dots and dashes as underscores: `sharing.org_wide` → `BUNKER_SHARING_ORG_WIDE`. A pinned value always wins.

## Server

| Variable | Default | |
|---|---|---|
| `BUNKER_URL` | detected, else learned from the first request | Public URL, e.g. `https://bunker.example.com`. Needed for sign-in redirects. Detected from `RENDER_EXTERNAL_URL`, `RAILWAY_PUBLIC_DOMAIN`, `FLY_APP_NAME`, `WEBSITE_HOSTNAME` (Azure App Service), `KOYEB_PUBLIC_DOMAIN`, `HEROKU_APP_DEFAULT_DOMAIN_NAME`, `APP_URL` (DigitalOcean) |
| `BUNKER_LISTEN` | `:8080` (`:$PORT` if `PORT` is set, `127.0.0.1:8080` in dev, `:443` with automatic TLS) | Listen address |
| `PORT` | | Set by PaaS platforms; Bunker listens on it |
| `BUNKER_DATA_DIR` | `/data` if it exists, else `./bunker-data` | Database, files, keys, certificates. **Back this up** |
| `BUNKER_CONTENT_URL` | | Serve app content from a [separate origin](/bunker/docs/deploy/content-domain/) |
| `BUNKER_TRUST_PROXY` | `true` when TLS is off or `PORT` is set | Read client IPs from `X-Forwarded-For` |
| `BUNKER_SECRET_KEY` | generated into `secret.key` | Key for encrypting secrets and signing content tickets (any string; hashed to 256 bits) |
| `BUNKER_SETUP_CODE` | generated | Fixed first-run setup code |
| `BUNKER_DEV` | `false` | Single-user dev mode, no sign-in (`bunker dev` sets it). Never in production |
| `BUNKER_LOG_FORMAT` | `json` | `text` for human-readable logs |
| `BUNKER_DEBUG` | `false` | Debug logging |
| `BUNKER_METRICS_TOKEN` | | Bearer token for `/metrics` from other machines (loopback always works) |

## TLS

| Variable | Default | |
|---|---|---|
| `BUNKER_TLS` | `auto` on a bare machine with an `https` URL; otherwise `off` | `auto`, `file` or `off`. [How it's chosen](/bunker/docs/deploy/proxy/#how-bunker-picks-a-tls-mode) |
| `BUNKER_TLS_CERT`, `BUNKER_TLS_KEY` | | Certificate and key files (implies `file`) |
| `BUNKER_ACME_EMAIL` | | Contact email for Let's Encrypt |
| `BUNKER_ACME_DIRECTORY` | Let's Encrypt | Another ACME directory URL |
| `BUNKER_HTTP_LISTEN` | `:80` | ACME challenges and the HTTPS redirect |

## Sign-in

| Variable | Default | |
|---|---|---|
| `BUNKER_AUTH_LOCAL` | `on` | Built-in accounts (`off` only once company sign-in works) |
| `BUNKER_AUTH_PROVIDER` | | `entra`, `google` or `oidc` |
| `BUNKER_ENTRA_TENANT_ID` (or `BUNKER_AUTH_TENANT_ID`) | | Entra tenant ID. `common`, `organizations` and `consumers` are refused |
| `BUNKER_ENTRA_CLIENT_ID` (or `BUNKER_AUTH_CLIENT_ID`) | | Client ID (any provider) |
| `BUNKER_ENTRA_CLIENT_SECRET` (or `BUNKER_AUTH_CLIENT_SECRET`) | | Client secret (any provider) |
| `BUNKER_ENTRA_SCOPES` (or `BUNKER_AUTH_ENTRA_SCOPES`) | `User.Read User.ReadBasic.All GroupMember.Read.All` | Extra delegated Graph scopes requested at sign-in |
| `BUNKER_AUTH_ISSUER` | | OIDC issuer URL (`https://` only) |
| `BUNKER_AUTH_ALLOWED_DOMAINS` | | Email domains for Google (required) and OIDC (optional) |
| `BUNKER_ALLOWED_GROUPS` (or `BUNKER_AUTH_ALLOWED_GROUPS`) | | Only these groups can sign in (Entra, OIDC) |
| `BUNKER_ALLOW_GUESTS` (or `BUNKER_AUTH_ALLOW_GUESTS`) | `false` | Allow Entra guest accounts |
| `BUNKER_ADMINS` | | Emails and groups who are always admins |
| `BUNKER_SESSION_DAYS` | `14` | Session lifetime (sliding) |
| `BUNKER_TOKEN_MAX_DAYS` | `90` | Longest personal access token lifetime |
| `BUNKER_OAUTH_ACCESS_TTL` | `3600` | MCP OAuth access token lifetime, in seconds |
| `BUNKER_OAUTH_CIMD_ALLOW_PRIVATE` | `false` | Let MCP clients' metadata documents live on private addresses |

## Sharing and apps

| Variable | Default | |
|---|---|---|
| `BUNKER_SHARING_ORG_WIDE` (or `BUNKER_ORG_WIDE_SHARING`) | `true` | Owners may share with everyone |
| `BUNKER_SHARING_EDITORS_CAN_SHARE` | `true` | Editors may share |
| `BUNKER_UPLOAD_MAX_MB` | `100` | Largest upload (deploys and `bunker.files`) |
| `BUNKER_UPLOAD_CONCURRENCY` | `4` | Uploads parsed in memory at once |
| `BUNKER_SITE_MAX_MB` | `100` | Largest app (all files of one version) |
| `BUNKER_MAX_SITES_PER_USER` | `500` | Apps per person |
| `BUNKER_TRASH_DAYS` | `30` | Days before trashed apps are purged |
| `BUNKER_ALLOW_SECRETS` | `false` | Turn off secret scanning of uploads |
| `BUNKER_NO_TEMPLATES` | `false` | Don't create the [built-in templates](/bunker/docs/build/templates/#built-in-templates) |

Admins can also change sharing, sizes, apps per person and allowed groups at runtime with `admin.policy.update` (see [Policy and limits](/bunker/docs/admin/policy/)). A variable set here pins the value.

## App data

| Variable | Default | |
|---|---|---|
| `BUNKER_DB_DOC_MAX_KB` | `1024` | One document |
| `BUNKER_DB_MAX_MB` | `100` | All data of one app |
| `BUNKER_DB_MAX_DOCS` | `250000` | Documents per app |
| `BUNKER_DB_MAX_COLLECTIONS` | `200` | Collections per app |
| `BUNKER_UPLOADS_SITE_MAX_MB` | `2048` | Uploaded files per app |
| `BUNKER_UPLOADS_MAX_FILES` | `20000` | Uploaded file count per app |
| `BUNKER_KV_MAX_VALUE_KB` | `64` | Per-person storage: one value |
| `BUNKER_KV_MAX_KEYS` | `1000` | Per-person storage: keys per app |
| `BUNKER_KV_MAX_TOTAL_MB` | `5` | Per-person storage: total per app |
| `BUNKER_EVENTS_MAX_PER_USER` | `20` | Live event streams per person |
| `BUNKER_EVENTS_MAX` | `20000` | Live event streams in total |

## Rate limits

| Variable | Default | |
|---|---|---|
| `BUNKER_RATE_USER_PER_MIN` | `1200` | API requests per signed-in person (burst 300) |
| `BUNKER_RATE_PUBLIC_PER_MIN` | `30` | Anonymous writes per IP (burst 20) |

## AI

| Variable | Default | |
|---|---|---|
| `BUNKER_AI_PROVIDER` | `none` | `anthropic`, `openai`, `azure-openai`, `openai-compatible`, `none` |
| `BUNKER_AI_API_KEY` | | Provider key |
| `BUNKER_AI_ENDPOINT` | per provider | Base URL |
| `BUNKER_AI_API_VERSION` | per provider | Azure `api-version` / `anthropic-version` |
| `BUNKER_AI_BUILDER_MODEL` | per provider | Model for the builder |
| `BUNKER_AI_PAGE_MODEL` | per provider | Model for `bunker.ai` |
| `BUNKER_AI_USER_DAILY_TOKENS` | `2000000` | Per person per day (`0` = no limit) |
| `BUNKER_AI_SITE_DAILY_TOKENS` | `5000000` | Per app per day |
| `BUNKER_AI_MAX_REQUEST_TOKENS` | `16000` | Largest page request |
| `BUNKER_AI_LOG_CONTENT` | `false` | Keep prompts and answers in the audit log |

## Microsoft 365

See [Microsoft 365 data](/bunker/docs/admin/m365/). All of these can also be set in *Admin → Microsoft 365*.

| Variable | Default | |
|---|---|---|
| `BUNKER_M365_ENABLED` | on when scopes are set | `true` or `false` |
| `BUNKER_M365_SCOPES` | | Delegated Graph scopes pages may use, comma separated (`Sites.Read.All,Files.Read.All`). Setting it alone turns Microsoft 365 on |
| `BUNKER_M365_REQUIRE_APPROVAL` | `false` | Only approved apps may use Microsoft 365 |
| `BUNKER_M365_APPROVED_SITES` | | Approved app addresses, comma separated |
| `BUNKER_M365_ALLOWED_SITES` | any | SharePoint site URLs pages may read, comma separated |

## Audit log

See [Audit log and streaming](/bunker/docs/admin/audit/).

| Variable | Default | |
|---|---|---|
| `BUNKER_AUDIT_RETENTION_DAYS` | `365` | Delete events older than this. `0` keeps everything |
| `BUNKER_AUDIT_WEBHOOK` | | `https://` URL that receives events as JSON batches |
| `BUNKER_AUDIT_SYSLOG` | | `udp://host:514` or `tcp://host:6514` |

## Backups

See [Backups and upgrades](/bunker/docs/admin/backups/#scheduled-snapshots).

| Variable | Default | |
|---|---|---|
| `BUNKER_BACKUP_DIR` | off | Folder for scheduled snapshots. Relative paths are inside the data folder |
| `BUNKER_BACKUP_EVERY` | `24h` | How often (Go duration, at least `1h`) |
| `BUNKER_BACKUP_KEEP` | `7` | Snapshots to keep |

## CLI

| Variable | |
|---|---|
| `BUNKER_SERVER` | Server URL (instead of `bunker login`) |
| `BUNKER_TOKEN` | Personal access token (for CI) |
| `BUNKER_PROFILE` | Which saved login to use |
| `BUNKER_CONFIG` | Config file path (default `~/.config/bunker/config.json`, honouring `XDG_CONFIG_HOME`) |
| `BUNKER_NO_BROWSER` | Don't open the browser |
| `BUNKER_NO_CLIPBOARD` | Don't copy URLs to the clipboard |
| `BUNKER_UPDATE_REPO`, `BUNKER_UPDATE_API` | Where `bunker update` looks for releases (mirrors) |
| `NO_COLOR`, `FORCE_COLOR` | Output colors |

## Installer

`BUNKER_VERSION`, `BUNKER_INSTALL_DIR`, `BUNKER_NO_SERVICE=1`, `BUNKER_DOWNLOAD_URL` and, written into `/etc/bunker/bunker.env` on first install, `BUNKER_URL`, `BUNKER_LISTEN`, `BUNKER_SETUP_CODE`. See [Linux installer](/bunker/docs/deploy/linux/).

## Docker build

`BUNKER_RUN_UID` (build arg, default `65532`): the user the image runs as. `0` for platforms whose disks are owned by root.
