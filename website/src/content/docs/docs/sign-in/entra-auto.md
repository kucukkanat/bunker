---
title: Microsoft Entra ID, automatic setup
description: Let Bunker register itself in your Entra tenant with a device code, or paste one script into Azure Cloud Shell.
sidebar: { order: 2 }
---

App registration is the slowest part of connecting any self-hosted app to Microsoft Entra ID. Bunker can do it for you. You need someone with the **Cloud Application Administrator**, **Application Administrator** or **Global Administrator** role.

Before you start, set `BUNKER_URL` to the address people will use (for example `https://bunker.example.com`). The sign-in redirect is built from it.

## How it works

1. **Start.** Bunker asks Microsoft for a device code:

   ```sh
   curl -X POST https://bunker.example.com/api/v1/setup/entra/auto/start \
     -H 'Content-Type: application/json' -d '{"code": "ABCD-EFGH"}'
   # {"flowId": "…", "userCode": "F7K2QW9PL", "verificationUri": "https://microsoft.com/devicelogin", "expiresIn": 900, "interval": 5, …}
   ```

   `code` is the [setup code](/bunker/docs/start/quickstart/) from the server log. It isn't needed on the server itself (`localhost`) or when you are already signed in as a Bunker admin. Once Bunker is set up (for example with built-in accounts), only admins can run it: add `-H "Authorization: Bearer $TOKEN"`, or use `bunker api setup.entra.auto.start` and `bunker api setup.entra.auto.poll --flowId …`.

2. **Sign in.** Open `https://microsoft.com/devicelogin`, enter the `userCode`, and sign in with your admin work account. You have 15 minutes.

3. **Poll.** Ask Bunker every `interval` seconds whether you're done:

   ```sh
   curl -X POST https://bunker.example.com/api/v1/setup/entra/auto/poll \
     -H 'Content-Type: application/json' -d '{"flowId": "…", "code": "ABCD-EFGH"}'
   # {"pending": true, "status": "waiting"}  … then:
   # {"done": true, "tenantId": "…", "clientId": "…", "secretExpires": "2028-11-05", "loginUrl": "/auth/login", "warnings": []}
   ```

4. **Done.** Bunker used that one-time, delegated sign-in to create everything, then threw the token away:
   - an app registration named **Bunker (your-host)**, single tenant (`AzureADMyOrg`), with the redirect URI `https://<your-bunker>/auth/callback` and security groups in the sign-in token,
   - a client secret valid for two years, stored encrypted in Bunker,
   - the service principal,
   - tenant-wide consent for `openid profile email offline_access User.Read User.ReadBasic.All GroupMember.Read.All`, so nobody sees a consent prompt.

   Sign-in switches to Entra. If Bunker has no admins yet, the person who signed in becomes one. The setup code is cleared.

The device sign-in uses Microsoft's public **Microsoft Graph Command Line Tools** client and asks only for `Application.ReadWrite.All` and `DelegatedPermissionGrant.ReadWrite.All` (plus `User.Read`) for that one session. Bunker never asks for application (app-only) permissions. Each step is audited: `setup.entra.start`, then `admin.config` on success or `setup.entra.failed`.

:::note
The setup page in the dashboard doesn't have a button for this yet. Use the two calls above, or the script below.
:::

If your role can't grant tenant-wide consent, setup still finishes with a warning: people are asked to consent on their first sign-in, until a Global Administrator grants it under *Enterprise applications → Bunker → Permissions*.

## If your tenant blocks it

Some tenants disable the Graph Command Line Tools app or put it behind Conditional Access. Bunker tells you (`blocked` or `conditional_access`). Then use the **Azure Cloud Shell script**. It needs the same admin role and does the same work with the `az` CLI:

```sh
curl -s https://bunker.example.com/api/v1/setup/entra/script | jq -r .script
```

1. Copy the script.
2. Open [Azure Cloud Shell](https://shell.azure.com) in Bash mode and paste it.
3. It prints a **Tenant ID**, **Client ID** and **Client secret**. Paste them into Bunker's setup page (or *Admin → Sign-in*), as in the [manual setup](/bunker/docs/sign-in/entra-manual/).

The same answer also has the `redirectUri`, the `permissions` list and a `portalUrl` if you'd rather click through the Entra portal.

## Common errors

| Code | What to do |
|---|---|
| `expired` | The code expired or the flow finished. Start again |
| `declined` | Sign-in was cancelled. Start again and accept the request |
| `blocked` | Your tenant blocks the Graph Command Line Tools app. Use the Cloud Shell script |
| `conditional_access` | A Conditional Access policy stopped the sign-in. Use a compliant device, or the script |
| `wrong_tenant` | You signed in as a guest. Use an account from the organization itself |
| `personal_account` | That's a personal Microsoft account. Use your work account |
| `invalid` (`BUNKER_URL`) | Bunker doesn't know its address. Set `BUNKER_URL` and restart |

## Renewing the secret

The client secret expires after two years. Before then, add a new secret to the app registration in the Entra portal (*App registrations → Bunker → Certificates & secrets*) and paste it into *Admin → Sign-in*. Running the automatic setup again also works: it creates a fresh app registration and switches Bunker to it, so delete the old one afterwards.
