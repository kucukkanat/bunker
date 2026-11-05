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
| Admin | `admin.config` (sign-in, policy, Microsoft 365, connections, streaming and other settings, with what changed), `admin.user_update`, `admin.transfer_sites`, `auth.local`, `ai.configure`, `ai.test` |
| Apps | `site.create`, `site.publish`, `site.version`, `site.rename`, `site.delete`, `site.restore_trash`, `site.transfer`, `site.export`, `site.view` (once per person per hour) |
| Sharing | `site.share`, `site.unshare`, `access.request` |
| Data and files | `db.rule`, `db.drop`, `db.import`, `db.export`, `upload.create`, `upload.delete`, `upload.rule`, `upload.blocked` |
| AI | `ai.call` (person, app, model, tokens; prompts only if `logContent` is on), `ai.create`, `builder.reset` |
| Tokens and agents | `token.create`, `token.revoke`, `oauth.authorize`, `oauth.refresh_reuse` (a reused refresh token: the whole token family is revoked) |
| Microsoft 365 | `m365.call` (person, app, Graph path, status, time) |
| Connections | `connection.call` (person, app, connection, method, path, status, bytes, time), `connection.rate_limited` |
| Setup | `setup.entra.start`, `setup.entra.failed` |
| Operations | `admin.audit.export`, `admin.backup`, `admin.backup.failed` |
| Notifications | `notify.send` |

## Search

*Admin → Audit log* filters by text, event type and time range. The same search is the `admin.audit` operation:

```sh
bunker api admin.audit --event 'site.*' --user ada@example.com --since 2026-10-01 --limit 200
```

```sh
curl -H "Authorization: Bearer $TOKEN" \
  "https://bunker.example.com/api/v1/admin/audit?event=site.share,site.unshare&site=team-lunch"
```

| Filter | |
|---|---|
| `event` | One or more event names, comma separated. End with `*` for a prefix: `site.*`, `auth.*` |
| `user` | The actor's email or user ID |
| `site` | The app's address or ID |
| `q` | Text in the target or the details |
| `since`, `before` | A date (`2026-10-01`), a date and time (`2026-10-01T09:00:00Z`) or unix seconds. `before` is exclusive |
| `limit` | Default 100, at most 1,000 |
| `beforeId` | For the next page: pass the `next` value from the previous answer |

The answer is `{ "events": [...], "next": 12345 }`, newest first. `next` is only there when there are more.

## Export

`admin.audit.export` streams every matching event, oldest first, with the same filters:

```sh
curl -H "Authorization: Bearer $TOKEN" -o audit.csv \
  "https://bunker.example.com/api/v1/admin/audit/export?format=csv&since=2026-10-01"
```

- `format=csv` (default): `id, time, actor, actor_id, event, site, site_id, target, ip, details`. Opens cleanly in Excel; cells that could run as formulas are defused.
- `format=json`: one JSON array.
- `format=ndjson`: one event per line, for log tools.

Every export is itself audited as `admin.audit.export`.

## Streaming to a SIEM

Send every event, as it happens, to an HTTPS webhook, a syslog server, or both:

```sh
bunker api admin.audit.streaming.update --data '{
  "webhook": "https://siem.example.com/ingest/bunker?token=xxxx",
  "syslog": "tcp://siem.example.com:6514"
}'
```

- **Webhook:** `POST` with `{"source": "bunker", "events": [...]}` as JSON. Events are batched every 2 seconds (or 200 at a time). Failed batches are retried 3 times on network errors, `429` and `5xx`. The URL must be `https://` and is stored encrypted.
- **Syslog:** `udp://host:514` or `tcp://host:6514` (port 514 if you leave it out). RFC 5424 lines with the event as JSON in the message; TCP uses octet counting (RFC 6587).
- Streaming never slows Bunker down. Up to 10,000 events wait in memory; past that, events are dropped and counted. They are still in the database.
- `bunker api admin.audit.streaming.get` shows the targets (webhook credentials hidden), events `sent`, `dropped` and the `lastError`.
- Send `""` to turn a target off.

You can also pin them from the environment: `BUNKER_AUDIT_WEBHOOK`, `BUNKER_AUDIT_SYSLOG`.

## Retention

Events older than **365 days** are deleted once a day (in small batches, so nothing locks up). Change it with:

```sh
bunker api admin.audit.streaming.update --data '{"retentionDays": 730}'
```

`0` keeps everything. The environment variable is `BUNKER_AUDIT_RETENTION_DAYS`. Stream or export first if you need a longer archive than you keep in Bunker.

## Reading the database directly

The audit table lives in `bunker.db` in the data folder. To read it with SQLite tools, open a **copy**, or use read-only mode:

```sh
sqlite3 -readonly /var/lib/bunker/bunker.db \
  "SELECT datetime(ts,'unixepoch'), actor_email, event, target FROM audit ORDER BY id DESC LIMIT 50;"
```
