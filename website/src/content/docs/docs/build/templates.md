---
title: Templates
description: Offer an app as a starting point for others.
sidebar: { order: 5 }
---

Any editor can mark an app as a **template**: open its **Settings** and turn on **Offer as a template**, or:

```sh
curl -X PATCH https://bunker.example.com/api/v1/sites/expense-form \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"isTemplate": true}'
```

Templates are listed for everyone at `GET /api/v1/sites?filter=templates`, sorted by title.

:::caution[What others can see]
Everyone signed in can see a template's **title, description and icon** in the list, and can **start a copy of its published files**, even if you never shared the app with them. Its data, drafts and older versions stay private. Don't mark an app as a template if its files hold anything sensitive.
:::

## Using a template

1. Open **Templates** in the dashboard sidebar, or list them with `bunker sites ls --filter templates`.
2. Click **Use this template**. Bunker copies the published files, not the data, into a new app that you own.

From a script, that's `sites.create` with a `template`:

```sh
bunker api sites.create --title "Design team board" --template template-tracker
```

```sh
curl -X POST https://bunker.example.com/api/v1/sites \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"title": "Design team board", "template": "template-tracker"}'
```

To copy an app that isn't a template, editors use `sites.duplicate` (`POST /api/v1/sites/{site}/duplicate`). It copies the working files, including the draft.

## Built-in templates

Bunker ships eight starter templates. They are created once, on first start, and everyone signed in can open them and **Use this template**.

| Template | Address | What it does | Uses |
|---|---|---|---|
| ✅ Requests & approvals | `template-requests` | A request form with an approvers group, clear statuses and a full history | `bunker.db` (`requests`, `decisions`) |
| 🗂️ Team tracker | `template-tracker` | A live kanban board: drag cards between columns, see changes instantly | `bunker.db` (`cards`), `bunker.room` |
| 📝 Sign-up sheet | `template-signup` | Slots with limits for events, shifts or volunteering. No double booking | `bunker.db` (`signups`) |
| 💬 Feedback collector | `template-feedback` | Ratings and comments with live results and an AI summary | `bunker.db` (`feedback`), `bunker.ai` |
| 📊 Quick poll | `template-poll` | One question, one vote each, results update live | `bunker.db` (`votes`) |
| 📈 Simple dashboard | `template-dashboard` | KPIs and charts from a CSV, a SharePoint list or sample data | Inline SVG charts, CSV, optional [`bunker.m365`](/bunker/docs/sdk/m365/) |
| 👋 Team directory & who's out | `template-directory` | Profiles, skills and a shared calendar of time away | `bunker.db` (`people`, `away`) |
| 🗒️ Meeting notes & decisions | `template-meetings` | Notes, decisions and action items you can find again later | `bunker.db` (`meetings`), `bunker.ai` |

Each is a single `index.html` written to the [SDK](/bunker/docs/sdk/overview/) and the sandbox rules, so they are good examples to learn from. Opened outside Bunker, they run in a demo mode with in-memory data.

### How they're managed

- They are owned by a disabled system account called **Bunker** and shared with everyone as viewers.
- Bunker seeds them **once**. If an admin edits or deletes one, it stays that way after upgrades.
- If any template already exists when Bunker first starts, nothing is seeded.
- To skip them, set `BUNKER_NO_TEMPLATES=true`. Bunker then seeds nothing on start.
