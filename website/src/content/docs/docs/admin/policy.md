---
title: Policy and limits
description: Who can sign in, how apps can be shared, and the size, count and rate limits you can change.
sidebar: { order: 4 }
---

Bunker's defaults suit most teams. These are the knobs. Settings are stored in the database and can be pinned with environment variables (`BUNKER_` + the key in capitals, dots as underscores); a pinned value wins.

## People and sign-in

| Setting | Default | |
|---|---|---|
| `auth.local` / `BUNKER_AUTH_LOCAL` | on | Built-in accounts. Can only be turned off once company sign-in works ([details](/bunker/docs/sign-in/built-in/#adding-company-sign-in-later)) |
| `BUNKER_ALLOWED_GROUPS` | | Only members of these groups can sign in with Entra or OIDC |
| `BUNKER_ALLOW_GUESTS` | `false` | Allow Entra guest accounts |
| `auth.allowed_domains` | | Email domains allowed for Google and OIDC |
| `admins` / `BUNKER_ADMINS` | | Emails and groups who are always admins |
| `BUNKER_SESSION_DAYS` | 14 | Session lifetime; slides forward with use |
| `BUNKER_TOKEN_MAX_DAYS` | 90 | Longest lifetime of a personal access token |

Admins manage people in *Admin → People*: invite (`invites.create`), make admin or disable (`users.update`), reset passwords (`users.reset_password`), and move all of someone's apps to another person when they leave (`sites.transfer_all`).

## Sharing

| Setting | Default | |
|---|---|---|
| `sharing.org_wide` / `BUNKER_SHARING_ORG_WIDE` | `true` | Owners may make an app viewable by everyone. `false` turns that off |
| `sharing.editors_can_share` / `BUNKER_SHARING_EDITORS_CAN_SHARE` | `true` | Editors may share. `false` leaves sharing to owners |

There is no public sharing. Every request needs a signed-in person.

## Uploads and apps

| | Default | Variable |
|---|---|---|
| One upload | 100 MB | `BUNKER_UPLOAD_MAX_MB` |
| One app's files | 100 MB | `BUNKER_SITE_MAX_MB` |
| Uploads parsed at once | 4 | `BUNKER_UPLOAD_CONCURRENCY` |
| Apps per person | 500 | `BUNKER_MAX_SITES_PER_USER` |
| Days in the trash | 30 | `BUNKER_TRASH_DAYS` |
| Secret scanning | on | `BUNKER_ALLOW_SECRETS=true` turns it off (not recommended) |

## App data

| | Default | Variable |
|---|---|---|
| One document | 1 MB | `BUNKER_DB_DOC_MAX_KB` |
| Data per app | 100 MB | `BUNKER_DB_MAX_MB` |
| Documents per app | 250,000 | `BUNKER_DB_MAX_DOCS` |
| Collections per app | 200 | `BUNKER_DB_MAX_COLLECTIONS` |
| Uploaded files per app | 2 GB, 20,000 files | `BUNKER_UPLOADS_SITE_MAX_MB`, `BUNKER_UPLOADS_MAX_FILES` |
| Per-person storage | 64 KB a value, 1,000 keys, 5 MB | `BUNKER_KV_MAX_VALUE_KB`, `BUNKER_KV_MAX_KEYS`, `BUNKER_KV_MAX_TOTAL_MB` |
| Live streams | 20 per person, 20,000 total | `BUNKER_EVENTS_MAX_PER_USER`, `BUNKER_EVENTS_MAX` |

## Rates

| | Default | Variable |
|---|---|---|
| API requests per signed-in person | 1,200 a minute (burst 300) | `BUNKER_RATE_USER_PER_MIN` |
| Anonymous writes per IP (sign-in, setup) | 30 a minute (burst 20) | `BUNKER_RATE_PUBLIC_PER_MIN` |
| Password attempts | 10 per 15 minutes per IP and per email | |
| AI | daily tokens per person and per app | [AI providers](/bunker/docs/admin/ai/) |
