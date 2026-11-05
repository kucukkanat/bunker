---
title: 60-second quickstart
description: Run Bunker on your own computer, create your account, publish your first app and share it.
sidebar: { order: 2 }
---

This takes about a minute. You'll run Bunker on your own computer, create your account, publish a page and open it. No Microsoft, Google or SSO needed.

## 1. Start Bunker

```sh
# Linux / macOS. Without sudo it just puts the binary in ~/.local/bin.
curl -fsSL https://raw.githubusercontent.com/kucukkanat/bunker/master/install.sh | sh
bunker serve
```

Or with Docker:

```sh
docker run -p 8080:8080 -v bunker:/data ghcr.io/kucukkanat/bunker
```

Bunker prints a box with a **setup URL and a one-time code**.

## 2. Create your account

Open `http://localhost:8080/setup`. Choose **Just me or my team** and enter your email, name and a password (10 characters or more). You're now the admin.

The same step as one API call:

```sh
curl -X POST http://localhost:8080/api/v1/setup -H 'Content-Type: application/json' \
  -d '{"provider":"local","email":"you@example.com","name":"You","password":"a-long-passphrase"}'
```

- When you open the setup page **on the same machine** (localhost), you don't need the code.
- With Docker, requests arrive through Docker's network, so type the code from the logs (`docker logs <container>`). It's also in `setup-code.txt` in the data folder.

:::tip[Just kicking the tyres?]
`bunker dev` skips sign-in entirely: everyone who opens it is a local admin. Use it on your own laptop only.

**Just want to look around?** `bunker demo` is `bunker dev` pre-filled with example apps (a live kanban board, requests with approvals, a poll, a spreadsheet turned into a table app, a Markdown handbook), four teammates and their data. It keeps its data in `./bunker-demo-data`, separate from a real install.
:::

## 3. Make something

Save this as `index.html`, or use any HTML file you already have (one from ChatGPT or Claude is fine):

```html
<!doctype html>
<title>Team lunch vote</title>
<h1>Where should we eat on Friday?</h1>
<p>Built in a minute with Bunker.</p>
```

## 4. Publish it

**In the browser:** drop the file on the Bunker home page. You get a link right away.

**From a terminal** (works today): every Bunker action is also an API call. Send a personal access token (Settings → Tokens), or, in `bunker dev`, just the `X-Bunker: 1` header:

```sh
curl -X POST http://localhost:8080/api/v1/drop \
  -H "Authorization: Bearer $TOKEN" \
  -F 'files=@index.html' -F 'title=Lunch vote'
```

The reply contains the new app's address:

```json
{ "version": 1, "files": 1, "slug": "lunch-vote",
  "url": "http://localhost:8080/s/lunch-vote/" }
```

Open it. Your page runs inside Bunker's sandbox, with a small Bunker badge in the corner.

:::tip[Folders and zips work too]
Send several `files=@...` fields, or one `.zip`. Bunker finds `index.html`, even inside `dist/` or `build/`, and skips junk like `.DS_Store` and `node_modules/`.
:::

## 5. Share it

New apps are private. Invite a friend or coworker first (Admin → People, or `POST /api/v1/admin/invites`). They get a one-time link to set their password. Then share the app with them:

```sh
curl -X POST http://localhost:8080/api/v1/sites/lunch-vote/members \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"type":"user","id":"sam@example.com","role":"viewer"}'
```

Anyone signed in who isn't on the list sees **You need access** with an **Ask for access** button. You get a notification and can let them in with one click.

## Next

- [Put it on a server](/bunker/docs/deploy/one-click/) so others can reach it.
- [Drop anything](/bunker/docs/build/drop-anything/): every file type Bunker understands.
- [Describe an app to the AI builder](/bunker/docs/build/ai-builder/).
- [Concepts](/bunker/docs/start/concepts/): sites, versions, drafts, roles.
