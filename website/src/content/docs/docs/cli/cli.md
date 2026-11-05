---
title: CLI
description: Deploy from a terminal.
sidebar: { order: 1 }
---

```sh
bunker login https://bunker.corp.com   # opens Entra sign-in, stores a personal token
bunker init                            # adds bunker.json and AGENTS.md (SDK notes for AI coding tools)
bunker dev                             # local preview with an emulated SDK
bunker deploy                          # publish the current folder as a new version
bunker deploy --site friday-lunch -m "Add drinks"
bunker versions friday-lunch
bunker restore friday-lunch 12
bunker share friday-lunch "London Office" --role viewer
```

Personal tokens act as you, with your permissions. List and revoke them in **Settings → Tokens**. Admins can set a maximum token lifetime.
