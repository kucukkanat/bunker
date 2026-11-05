---
title: Microsoft Entra ID, automatic setup
description: Let Bunker register itself in your Entra tenant with a device code, in about a minute.
sidebar: { order: 2, badge: { text: 'Preview', variant: 'caution' } }
---

:::note[Preview]
Automatic Entra setup is being built now. Until it ships, use the [manual setup](/bunker/docs/sign-in/entra-manual/), which takes about two minutes.
:::

App registration is the slowest part of connecting any self-hosted app to Microsoft Entra ID. Bunker can do it for you.

## How it works

1. In *Admin → Sign-in → Microsoft Entra ID*, click **Set up automatically** (or run `bunker setup entra` on the server).
2. Bunker shows a code. Open `https://microsoft.com/devicelogin` and sign in as someone with the **Cloud Application Administrator** role (Application Administrator or Global Administrator also work).
3. Bunker uses that one-time, delegated sign-in to create everything, then forgets the token:
   - an app registration named **Bunker**, single tenant (`AzureADMyOrg`), with the redirect URI `https://<your-bunker>/auth/callback`,
   - a client secret (stored encrypted in Bunker),
   - the service principal,
   - tenant-wide consent for the sign-in scopes `openid email profile offline_access User.Read User.ReadBasic.All GroupMember.Read.All`.
4. Sign-in switches on. You keep your built-in admin account until you [turn built-in accounts off](/bunker/docs/sign-in/built-in/#adding-company-sign-in-later).

The device sign-in uses Microsoft's public **Microsoft Graph Command Line Tools** client and asks only for `Application.ReadWrite.All` and `DelegatedPermissionGrant.ReadWrite.All` for that one session. Bunker never asks for application (app-only) permissions.

## If your tenant blocks it

Some tenants disable the Graph Command Line Tools app or put it behind Conditional Access. Use the [manual setup](/bunker/docs/sign-in/entra-manual/) instead; the result is the same.

## Renewing the secret

Client secrets expire (the default is two years). Re-run the automatic setup with **Rotate secret** before then. Bunker's admin page shows the expiry date.
