---
title: Concepts
description: Sites, versions, drafts, roles, the sandbox and the SDK, in plain words.
sidebar: { order: 3 }
---

## Site (or app)

A **site** is one app: a set of files with a name and an address, `/s/<address>/`. Addresses use 3 to 48 lowercase letters, numbers and dashes. Sites also have a title, an icon, a description and [settings](/bunker/docs/build/site-settings/).

Deleting a site moves it to the **trash** for 30 days (`BUNKER_TRASH_DAYS`), where it can be restored.

## Version and draft

Every publish creates a **version**, numbered 1, 2, 3 and so on. One version is **live**: it's what people see. Editors can open any older version (`?v=3`) and **restore** it, which creates a new version with the old content. Nothing is ever overwritten.

A **draft** is unpublished work. Editors preview it at `/s/<address>/?draft=1`. Publishing turns the draft into the next version. Discarding throws it away.

Files are stored once, by their content hash. A new version that changes one file only stores that one file.

## Roles

| Role | Can |
|---|---|
| **Viewer** | Open the app and use it |
| **Editor** | Change files, publish, restore versions, share as viewer or editor (unless an admin turned that off) |
| **Owner** | Everything an editor can, plus change the address, share with the whole organization, transfer and delete |
| **Admin** | Run Bunker. Admins can open every site |

You can share with a **person** (by email), a **group** (from Entra ID or your OIDC provider), or **everyone in the organization**. A person gets the highest role that any of these gives them.

## The shell and the sandbox

When someone opens `/s/<address>/`, Bunker sends a small **shell** page. The shell shows your app in a sandboxed iframe whose files come from a signed, short-lived URL. The app runs with an **opaque origin**: it has no cookies, no browser storage, and no way to call the internet or reach other apps. [Details](/bunker/docs/security/sandbox/).

## The SDK

Inside the sandbox, `window.bunker` (the SDK) is how an app gets things done: who is viewing, saving data, uploading files, using AI, reading Microsoft 365. Each call goes to the shell, which forwards it to Bunker with the viewer's session. Bunker checks the site, the viewer and their role every time. See the [SDK reference](/bunker/docs/sdk/overview/).

## Operations

Every action in Bunker, from "publish" to "share" to "restore", is an **operation** with a name like `sites.deploy` and a route like `POST /api/v1/sites/{site}/deploy`. The dashboard, the [CLI](/bunker/docs/cli/reference/) and the [MCP tools](/bunker/docs/agents/mcp/) all use the same list, from `GET /api/v1/ops`. There are no hidden dashboard-only actions.

## Tokens

Scripts and agents sign in with a **personal access token** (`bk_...`) that belongs to a person. Tokens have scopes (`read`, `write`, `admin`) and expire after at most 90 days (`BUNKER_TOKEN_MAX_DAYS`). The CLI gets one through a **device sign-in**: it shows a code, and you approve it in the browser at `/device`.
