---
title: Audit log and streaming
description: What Bunker records, where it's stored, and how to export it or stream it to your SIEM.
sidebar: { order: 5 }
---

Every security-relevant action is written to the `audit` table in Bunker's database as it happens.

## What's in a record

| Field | |
|---|---|
| `ts` | Unix time |
| `actor_id`, `actor_email` | Who did it (empty for anonymous attempts) |
| `event` | What happened, for example `site.share` |
| `site_id`, `target` | Which app, and what was acted on (a person, a group, a file, a setting) |
| `ip` | Client IP (from `X-Forwarded-For` when `BUNKER_TRUST_PROXY` is on) |
| `meta` | JSON details. Actions taken with a personal or OAuth token carry `"via": "token"` |

Indexed by time, app, actor and event, so filtering stays fast on big logs.

## Events

| Area | Events |
|---|---|
| Sign-in | `auth.login`, `auth.denied` (wrong password, rate limited, not in an allowed group), `auth.logout_everywhere`, `auth.invite`, `auth.invite_accepted`, `auth.reset_link`, `auth.password_change`, `setup.denied` |
| Admin | `admin.config` (sign-in and other settings), `admin.user_update`, `admin.transfer_sites`, `auth.local`, `ai.configure`, `ai.test` |
| Apps | `site.create`, `site.publish`, `site.version`, `site.rename`, `site.delete`, `site.restore_trash`, `site.transfer`, `site.export`, `site.view` (once per person per hour) |
| Sharing | `site.share`, `site.unshare`, `access.request` |
| Data and files | `db.rule`, `db.drop`, `db.import`, `db.export`, `upload.create`, `upload.delete`, `upload.rule`, `upload.blocked` |
| AI | `ai.call` (person, app, model, tokens; prompts only if `logContent` is on), `ai.create`, `builder.reset` |
| Tokens and agents | `token.create`, `token.revoke`, `oauth.authorize`, `oauth.refresh_reuse` (a reused refresh token: the whole token family is revoked) |
| Notifications | `notify.send` |

## Reading it today

The audit table lives in `bunker.db` in the data folder. To read it directly, open a **copy** (or use read-only mode) with any SQLite tool:

```sh
sqlite3 -readonly /var/lib/bunker/bunker.db \
  "SELECT datetime(ts,'unixepoch'), actor_email, event, target FROM audit ORDER BY id DESC LIMIT 50;"
```

## Admin page, export and streaming

:::note[Preview]
The audit page in the dashboard (filter by person, app, event and date), CSV and JSON export, and streaming are being built now.
:::

- **Export** as CSV or JSON for a date range from *Admin → Audit*.
- **Stream** every event as it happens to syslog (RFC 5424, UDP/TCP/TLS) or an HTTPS webhook, for Splunk, Sentinel, Elastic or any SIEM. Bunker's core already has a hook that receives every event; the streaming outputs plug into it.
