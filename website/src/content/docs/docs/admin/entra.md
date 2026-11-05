---
title: Microsoft Entra ID
description: Register Bunker in your tenant.
sidebar: { order: 2 }
---

1. In the Entra admin center, go to **App registrations → New registration**.
2. Name it `Bunker`. Under **Supported account types**, choose **Accounts in this organizational directory only**. Bunker rejects any other option.
3. Add the redirect URI `https://bunker.corp.com/auth/callback` (Web).
4. Under **Certificates & secrets**, create a client secret.
5. Under **Token configuration**, add the **groups** claim (Security groups). Bunker also reads groups through Graph for users in many groups.
6. Under **API permissions**, add Microsoft Graph delegated permissions `openid`, `profile`, `email`, `offline_access` and `User.Read`, then **Grant admin consent**.

Set the tenant ID, client ID and secret as shown in [Install](/bunker/docs/admin/install/).

## Who can sign in
By default, any member of the tenant can. To limit Bunker to a group, set `BUNKER_ALLOWED_GROUPS`, or enable **Assignment required** on the Enterprise application. Guest (B2B) accounts are blocked unless you set `BUNKER_ALLOW_GUESTS=true`.

## Admins
`BUNKER_ADMINS` takes emails or group names. Admins can see and manage every site and the audit log.
