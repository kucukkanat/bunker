---
title: Groups and admins
description: Where groups come from, who becomes an admin, and how to limit who can sign in.
sidebar: { order: 6 }
---

## Where groups come from

| Sign-in | Groups |
|---|---|
| Built-in accounts | Set on the invite (`groups: ["Design"]`) |
| Microsoft Entra ID | Read from Microsoft Graph at every sign-in (`/me/transitiveMemberOf`). Nested groups count. No 200-group limit |
| Generic OIDC | The ID token's `groups` claim |
| Google Workspace | None yet |

Groups are refreshed each time someone signs in. Sessions last 14 days and slide forward while people use Bunker (`BUNKER_SESSION_DAYS`).

You can refer to a group by its **name** or its **ID** wherever Bunker asks for one: sharing, `BUNKER_ALLOWED_GROUPS` and `BUNKER_ADMINS`.

## Who is an admin

- **Built-in accounts:** the person who completes the setup page, plus anyone invited with `isAdmin: true`.
- **Company sign-in on a fresh server:** the first person to sign in, unless you set an admin list.
- **The admin list** (`admins` setting, or `BUNKER_ADMINS`): comma-separated emails and group names or IDs. Everyone who matches is an admin, every time they sign in. For example:
  ```sh
  BUNKER_ADMINS=it-lead@example.com,Bunker Admins
  ```
- Admins can make others admin, or disable an account, from *Admin → People* (`users.update`). Disabling signs that person out everywhere at once and stops their tokens and content tickets.

Admins can open every site. That's deliberate: someone has to be able to help, and to clean up. Admin actions are in the [audit log](/bunker/docs/admin/audit/).

## Limit who can sign in

- **Built-in accounts:** only invited people can sign in.
- **Entra / OIDC:** set `BUNKER_ALLOWED_GROUPS=Staff,Contractors-Approved`. Anyone outside those groups is refused with a clear message, and the attempt is audited.
- **Entra guests** are refused unless `BUNKER_ALLOW_GUESTS=true`.
- **Google / OIDC:** set allowed email domains.

## Signing everyone out

`POST /api/v1/me/logout-everywhere` ends all of your own sessions. Disabling a person (admin) ends all of theirs.
