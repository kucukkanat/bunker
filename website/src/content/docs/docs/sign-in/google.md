---
title: Google Workspace
description: Let people sign in with their Google Workspace account, limited to your domain.
sidebar: { order: 4 }
---

## 1. Create an OAuth client

In the [Google Cloud console](https://console.cloud.google.com/apis/credentials), in a project owned by your Workspace organization:

1. *OAuth consent screen*: user type **Internal**. That alone limits sign-in to your Workspace.
2. *Credentials → Create credentials → OAuth client ID*, type **Web application**.
3. **Authorized redirect URI:** `https://<your-bunker>/auth/callback`
4. Copy the **client ID** and **client secret**.

## 2. Connect Bunker

*Admin → Sign-in → Google Workspace*: paste the client ID and secret and enter your **Workspace domain**, for example `example.com`. You can list several domains, separated by commas.

The domain is required. Bunker refuses Google sign-in without one, and refuses public domains like `gmail.com`, `outlook.com` or `icloud.com`, so personal accounts can never get in.

## What Bunker checks

- The ID token's `hd` (hosted domain) claim must be one of your domains. When you have one domain, Bunker also sends `hd=` to Google so the account picker only offers work accounts.
- PKCE, state and nonce on every sign-in.

## Groups

Google doesn't put group memberships in its sign-in token. To share by group with Google sign-in, use groups on [built-in accounts](/bunker/docs/sign-in/built-in/) or share with people by email. Directory groups for Google are on the [roadmap](/bunker/docs/project/roadmap/).
