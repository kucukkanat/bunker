---
title: What is Bunker?
description: Bunker is a self-hosted platform where anyone in your organization can build small internal apps and share them safely.
sidebar: { order: 1 }
---

:::caution[In development]
Bunker is being built in the open. These docs describe **planned** behavior, and names and options may change before the first release.
:::

Bunker is a self-hosted platform for **internal apps**: request forms, trackers, dashboards, calculators, and the many other small tools teams usually build in spreadsheets.

Anyone in your organization can create one. They describe what they need in a chat, Bunker's AI builds it, and they share it with chosen coworkers. It runs on your servers behind Microsoft Entra ID, and every page is sandboxed.

## Who it's for

- **Builders** are anyone with an idea: HR, finance, operations, sales. No code needed. Start with [Build with AI](/bunker/docs/build/ai-builder/).
- **Admins** run Bunker and set the rules. Start with [Install](/bunker/docs/admin/install/).
- **Security reviewers** need to know what a page can and can't do. Read the [security model](/bunker/docs/security/model/).
- **Developers** can deploy from their own tools with the [CLI and MCP](/bunker/docs/cli/cli/).

## What a Bunker site can do

| Capability | How |
|---|---|
| Know who is viewing | [`bunker.me()`](/bunker/docs/sdk/me/) |
| Save and sync data live | [`bunker.db`](/bunker/docs/sdk/db/) |
| Store files | [`bunker.files`](/bunker/docs/sdk/files/) |
| Use AI | [`bunker.ai`](/bunker/docs/sdk/ai/) |
| Read SharePoint, Excel, OneDrive | [`bunker.m365`](/bunker/docs/sdk/m365/) |
| Remember settings | [`bunker.storage`](/bunker/docs/sdk/storage/) |

## What makes it different

- **Private by default.** Sites are shared per person or group, with separate viewer and editor roles.
- **Contained.** A page can't reach the internet, read cookies, or see other sites.
- **Recoverable.** Every change is a version you can restore.
- **Accountable.** Admins get a full audit log.
- **Yours.** One Go binary and a SQLite file. MIT licensed.
