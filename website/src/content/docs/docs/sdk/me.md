---
title: bunker.me
description: Who is viewing the app, their role and the groups that matter to this app.
sidebar: { order: 2 }
---

```js
const me = await bunker.me();
// { id: 42, name: 'Ada Lovelace', email: 'ada@example.com',
//   groups: ['Finance-Approvers'], role: 'viewer', canEdit: false }

if (await bunker.me.inGroup('Finance-Approvers')) {
  showApproveButtons();
}
```

| Field | |
|---|---|
| `id` | The person's numeric Bunker user ID (the same value as `_author` on documents and uploads) |
| `name`, `email` | From the built-in account or the sign-in provider |
| `groups` | Only the groups **this app uses**: groups it is shared with, or named in a `group:` data rule. The rest of someone's memberships stay private |
| `role` | The viewer's role on this app: `viewer`, `editor`, `owner` or `admin` |
| `canEdit` | `true` for editors, owners and admins |

`me.inGroup(nameOrId)` asks Bunker whether the viewer is in any group, by name or ID, without revealing the full list.

Use `role` and `canEdit` to show or hide controls. Don't rely on them for security: the server enforces roles and [collection rules](/bunker/docs/sdk/db/#access-rules) on every call anyway.

The result of `bunker.me()` is cached for the page's lifetime.

API: `GET /api/v1/sites/{site}/me` (`me.site`) and `GET /api/v1/sites/{site}/me/groups/{group}` (`me.in_group`).
