---
title: Built-in accounts and invites
description: Email and password accounts that work out of the box, with invite links and reset links. No SSO needed.
sidebar: { order: 1 }
---

Bunker works out of the box with **built-in accounts**: an email address and a password, stored in Bunker. You don't need Microsoft, Google or any identity provider. This is the right choice for personal projects, families, clubs and small teams, and a fine way to start in a company before IT connects SSO.

## The first admin

On a fresh install, open `/setup`, choose **Just me or my team** and enter your email, name and a password. That account is the admin.

- From the machine itself (localhost) the setup page needs **no code**.
- From anywhere else, enter the one-time **setup code** from the logs or `setup-code.txt` in the data folder. This stops a stranger from claiming a fresh server before you do.

The same step as an API call (`setup.configure`):

```sh
curl -X POST https://bunker.example.com/api/v1/setup -H 'Content-Type: application/json' \
  -d '{"provider":"local","code":"BKRT-WXQP","email":"you@example.com","name":"You","password":"a-long-passphrase"}'
```

## Inviting people

There is no public sign-up. People join only through an **invite link** that an admin creates:

*Admin → People → Invite*, or `invites.create`:

```sh
curl -X POST https://bunker.example.com/api/v1/admin/invites \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"email":"sam@example.com","name":"Sam","groups":["Design"],"days":7}'
```

```json
{ "link": "https://bunker.example.com/auth/invite?token=…", "email": "sam@example.com",
  "emailed": false, "note": "Send this link to them. It works once." }
```

- Send the link however you like: chat, email, a note on their desk. When [email](/bunker/docs/admin/email/) is set up, Bunker also emails it to them (`"emailed": true`).
- The link works **once** and expires after `days` (default 7, at most 30). Creating a new invite for the same email replaces the old one.
- `groups` puts the person in named groups, so you can [share by group](/bunker/docs/build/sharing/) without SSO.
- `isAdmin: true` makes them an admin.
- `invites.list` (`GET /api/v1/admin/invites`) shows pending invites.

## Forgotten passwords

An admin makes a **reset link** (`users.reset_password`, `POST /api/v1/admin/users/{id}/reset-password`). It works once and expires after **2 days**. People change their own password in their settings (`me.password`); doing so signs out their other sessions.

## How passwords are protected

| | |
|---|---|
| Hashing | **argon2id**, 19 MiB memory, 2 iterations, 1 lane, 16-byte random salt (OWASP parameters) |
| Length | 10 to 200 characters. A short sentence works well |
| Guessing | 10 failed attempts per 15 minutes per IP address and per email, then a pause (successful sign-ins don't count, so an office behind one address isn't locked out) |
| Unknown emails | Checked against a dummy hash, so a wrong email takes as long as a wrong password |
| Login CSRF | Sign-in and invite forms only accept same-origin posts |
| Invite and reset tokens | Random, stored only as a hash, single use |
| Audit | `auth.login`, `auth.denied`, `auth.invite`, `auth.reset_link`, `auth.password_change` |

## Adding company sign-in later

When your organization is ready, connect [Microsoft Entra ID](/bunker/docs/sign-in/entra-auto/), [Google Workspace](/bunker/docs/sign-in/google/) or [any OIDC provider](/bunker/docs/sign-in/oidc/). Built-in accounts keep working alongside it: the sign-in page shows both. Admins can open the company sign-in directly with `/auth/login?sso=1`.

Then, if you want everyone on their work account, turn built-in accounts off (*Admin → Sign-in*, or `auth.local.update`):

```sh
curl -X PUT https://bunker.example.com/api/v1/admin/auth/local \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"enabled":false}'
```

Bunker refuses to turn them off until company sign-in is configured, so nobody gets locked out. You can also pin it with `BUNKER_AUTH_LOCAL=off`.
