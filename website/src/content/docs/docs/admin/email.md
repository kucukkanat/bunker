---
title: Email
description: Optional SMTP so invites, password resets and notifications arrive by email.
sidebar: { order: 8 }
---

Email is **optional**. Without it, you copy invite and reset links yourself and notifications stay in the bell. With an SMTP server, Bunker also emails:

- invite links and password-reset links (the admin still sees the link),
- notifications: something shared with you, access requests and answers, and messages apps send with [`bunker.notify`](/bunker/docs/sdk/ui/).

People can turn notification emails off in *Settings → Email*.

## Set it up

In *Admin → Email*, or with environment variables:

```sh
BUNKER_SMTP_URL=smtp://user%40corp.com:password@smtp.office365.com:587
BUNKER_SMTP_FROM="Bunker <bunker@corp.com>"
```

| Scheme | Port | Security |
|---|---|---|
| `smtp://` | 587 (default) | STARTTLS. Bunker refuses to send a password over a connection without STARTTLS unless you add `?insecure=1`. |
| `smtps://` | 465 (default) | TLS from the start |

A local relay without a password (`smtp://127.0.0.1:25`) is fine: with no password to protect, Bunker sends even if the relay offers no STARTTLS.

Links in emails use your server's address (`BUNKER_URL`, or the one Bunker learned from your first visit). If Bunker doesn't know it yet, for example because you reach it by IP, invite and reset emails use the address you're using in the dashboard.

URL-encode special characters in the username and password (`@` → `%40`). Click **Send me a test email** to check. Mail goes out from a background queue with three retries; failures are logged.

Common servers: Microsoft 365 `smtp.office365.com:587` (needs SMTP AUTH enabled for the mailbox), Google Workspace `smtp.gmail.com:587` (app password), Amazon SES, SendGrid (`smtp://apikey:<key>@smtp.sendgrid.net:587`), Postmark, Mailgun.

## API

```sh
bunker api admin.mail.update --smtpUrl 'smtps://bunker:secret@mail.example.com' --from 'Bunker <bunker@example.com>'
bunker api admin.mail.test
bunker api me.email_notifications --enabled false
```
