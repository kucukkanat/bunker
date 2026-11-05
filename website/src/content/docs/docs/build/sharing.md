---
title: Sharing and access requests
description: Share an app with people, groups or everyone, handle access requests, and transfer ownership.
sidebar: { order: 3 }
---

New apps are **private**: only you (and admins) can open them.

## Share with people and groups

Open the app's **Share** dialog (or use `sharing.add`, `POST /api/v1/sites/{site}/members`):

| Field | Values |
|---|---|
| `type` | `user` (an email address) or `group` (a group name or ID) |
| `id` | `sam@example.com`, `Finance`, or an Entra group object ID |
| `role` | `viewer` or `editor` |

```sh
curl -X POST https://bunker.example.com/api/v1/sites/q3-budget/members \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"type":"group","id":"Finance","role":"viewer"}'
```

- You can share with someone who hasn't signed in yet: the share applies the moment they do.
- People you share with get a **notification** in Bunker.
- `sharing.list` shows who has access; `sharing.remove` (`DELETE /members/{type}/{id}`) takes it away. Removal applies to the very next request, because content URLs are re-checked against current access.
- With company sign-in, the share dialog searches your directory (people and groups). With built-in accounts, it searches people who already have an account.

**Who can share:** owners always. Editors too, as viewer or editor, unless an admin sets `sharing.editors_can_share` to `false`.

## Share with everyone

The owner can turn on **Everyone can view** (`orgView: true` in `sites.update`). Everyone who can sign in to your Bunker can then open it. Admins can turn this option off for the whole server (`sharing.org_wide = false`).

Every visitor must be signed in, unless the owner turns on a public link (below).

## Public links (anyone with the link)

For personal projects and things you want to show the world, an owner can make a site **public**: anyone with the link can open it, with no account and no sign-in. This is **off until an admin allows it** in *Admin → Sharing* (`publicLinks` in the [policy](/bunker/docs/admin/policy/)).

- Visitors see the published version only. They can't sign in through the site, use its data, files, AI or notifications, and they can't see drafts or other sites.
- The page is still sandboxed exactly like for signed-in viewers.
- Views are counted as an anonymous total per hour (no personal data).
- Turn it off per site in the Share dialog, or for everyone at once by switching the policy off: every public link closes immediately.

```sh
bunker api sites.update portfolio --public true
```

## Access requests

Someone who opens an app they can't see gets a **You need access** page. The page looks the same whether or not the app exists, so addresses can't be probed. They can add a note and click **Ask for access**:

- The owner gets a notification with **Approve** / **Deny** (`access.list`, `access.decide`).
- Approving adds them as a viewer (or editor, if you choose) and notifies them.
- Someone who can already view can ask to edit: `access.request` with `{"role": "editor"}` (`POST /api/v1/sites/{site}/access-requests`).
- Requests are audited (`access.request`).

## Notify people from your app

An app can notify people who have access to it, for example "Your request was approved" (`notify.send`, `POST /api/v1/sites/{site}/notify`, page-safe). Recipients are emails and/or group names; only people who can open the app receive it. See [`bunker.notify`](/bunker/docs/sdk/ui/#bunkernotify).

## Views and visitors

Owners and editors can see how many people opened the app (`sites.stats`, last 30 days by default). A view is counted once per person per hour.

## Transfer, duplicate, delete

- **Transfer** (owner): give the app to someone who has signed in at least once (`sites.transfer`). Admins can move all of a person's apps at once, for example when someone leaves.
- **Duplicate** (editors): make a copy of the files (not the data) as a new app you own (`sites.duplicate`).
- **Export:** download any version as a zip (`GET /api/v1/sites/{site}/export.zip`).
- **Delete** (owner): moves the app to the trash for 30 days; restore it from there (`sites.restore`).
