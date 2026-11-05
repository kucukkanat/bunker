---
title: Microsoft Entra ID, manual setup
description: Register Bunker in the Entra admin center and connect it, step by step.
sidebar: { order: 3 }
---

You need an Entra role that can create app registrations (Application Developer is enough to register; **Cloud Application Administrator** to grant admin consent).

## 1. Register the app

In the [Entra admin center](https://entra.microsoft.com), go to *App registrations → New registration*:

- **Name:** `Bunker`
- **Supported account types:** *Accounts in this organizational directory only* (single tenant)
- **Redirect URI:** platform **Web**, value `https://<your-bunker>/auth/callback`. Bunker's setup page and `GET /api/v1/setup` (`redirectUri`) show the exact value.

Copy the **Application (client) ID** and the **Directory (tenant) ID**.

## 2. Create a secret

*Certificates & secrets → New client secret.* Copy the **Value** (not the Secret ID). It is shown once.

## 3. Grant consent (recommended)

*API permissions → Add a permission → Microsoft Graph → Delegated*: add `User.ReadBasic.All` and `GroupMember.Read.All`, then **Grant admin consent**.

| Scope | Why |
|---|---|
| `openid`, `profile`, `email`, `offline_access` | Sign-in |
| `User.Read` | Your profile and your group memberships |
| `User.ReadBasic.All` | The people picker in the share dialog |
| `GroupMember.Read.All` | Group names in the share dialog |

Without admin consent, people are asked to consent themselves, and the picker may only find people who already signed in. Change the list with `BUNKER_ENTRA_SCOPES` (space or comma separated).

## 4. Connect Bunker

As an admin: *Admin → Sign-in → Microsoft Entra ID*, paste the tenant ID, client ID and secret, and click **Connect**. Bunker checks that `https://login.microsoftonline.com/<tenant>/v2.0` is reachable before saving.

Or set environment variables (they take precedence over saved settings):

```sh
BUNKER_ENTRA_TENANT_ID=00000000-0000-0000-0000-000000000000
BUNKER_ENTRA_CLIENT_ID=11111111-1111-1111-1111-111111111111
BUNKER_ENTRA_CLIENT_SECRET=...
```

On a brand-new server you can also choose Entra directly on the setup page instead of **Just me or my team**. Then the first person to sign in becomes the admin, unless you list admins (see [Groups and admins](/bunker/docs/sign-in/groups-admins/)).

## What Bunker checks at sign-in

- **Single tenant only.** The tenant ID can't be `common`, `organizations` or `consumers`; Bunker refuses to save that. The token's `tid` must match your tenant.
- **No guests** unless you allow them (`BUNKER_ALLOW_GUESTS=true`). Entra guest accounts (`acct = 1`) are refused.
- **PKCE, state and nonce** on every sign-in. The ID token is verified against your tenant's keys.
- People are identified by their Entra **object ID** (`oid`), not their email, so a renamed account stays the same person.
- **Groups** are read from Microsoft Graph (`/me/transitiveMemberOf`), so nested groups work and the 200-group token limit doesn't matter.
- The delegated token is stored **encrypted** (AES-256-GCM) and only used for the people picker, groups and, if you enable it, [Microsoft 365 data](/bunker/docs/admin/m365/).

## Optional: only some people

Restrict Bunker to members of certain groups with `BUNKER_ALLOWED_GROUPS` (names or object IDs, comma separated) or the same field in the admin page. Others see "You're not in a group that can use Bunker", and the attempt is audited as `auth.denied`.
