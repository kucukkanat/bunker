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

There is no public link. Every visitor must be signed in.

## Access requests

Someone who opens an app they can't see gets a **You need access** page. The page looks the same whether or not the app exists, so addresses can't be probed. They can add a note and click **Ask for access**:

- The owner gets a notification with **Approve** / **Deny** (`access.list`, `access.decide`).
- Approving adds them as a viewer (or editor, if you choose) and notifies them.
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
