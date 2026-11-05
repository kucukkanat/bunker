---
title: Generic OIDC
description: Connect Okta, Auth0, Keycloak, Authentik, JumpCloud or any OpenID Connect provider.
sidebar: { order: 5 }
---

Any provider that speaks OpenID Connect works.

## 1. Create a client in your provider

- Type: **web application** / confidential client, authorization code flow.
- Redirect URI: `https://<your-bunker>/auth/callback`
- Scopes: `openid profile email offline_access`. Add `groups` (or your provider's equivalent) if you want to share by group.

## 2. Connect Bunker

*Admin → Sign-in → OIDC*:

| Field | Example |
|---|---|
| Issuer URL | `https://example.okta.com`, `https://auth.example.com/realms/staff` |
| Client ID / secret | from your provider |
| Allowed email domains | `example.com` (optional but recommended) |

The issuer must be `https://`. Bunker fetches `<issuer>/.well-known/openid-configuration` before saving, so a typo fails right away.

## What Bunker checks

- PKCE, state and nonce; the ID token is verified with the provider's keys.
- If the token says `email_verified: false`, sign-in is refused.
- If you set allowed domains, the email's domain must match. Public domains like `gmail.com` can't be added.
- Groups come from the token's `groups` claim, as names or IDs. Use them to [share by group](/bunker/docs/build/sharing/), restrict who can sign in (`BUNKER_ALLOWED_GROUPS`) or choose admins.

## Environment variables

Saved settings can be pinned from the environment (`BUNKER_` + the setting key in capitals):

```sh
BUNKER_AUTH_PROVIDER=oidc
BUNKER_AUTH_ISSUER=https://auth.example.com/realms/staff
BUNKER_AUTH_CLIENT_ID=bunker
BUNKER_AUTH_CLIENT_SECRET=...
BUNKER_AUTH_ALLOWED_DOMAINS=example.com
```
