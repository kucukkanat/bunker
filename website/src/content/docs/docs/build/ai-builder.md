---
title: AI builder
description: Describe an app in plain words and let Bunker's AI build it as a draft. How it works and how to get good results.
sidebar: { order: 2 }
---

If your admin has [connected an AI provider](/bunker/docs/admin/ai/), you can describe an app and Bunker builds it.

:::note[Where to find it]
Open an app and choose **Build**: chat on one side, a live preview of the draft on the other. Or start from **New → Describe it** on the home page, or `bunker ai "..."` in a terminal.
:::

## How it works

- **Start from a sentence.** "A holiday tracker where people request days off and managers in HR approve them." Bunker creates a new app, picks a name and an emoji, and builds the first version (`ai.create`, `POST /api/v1/ai/create`).
- **Keep talking.** "Make the approve button green." "Add a CSV export." Each message edits the app (`builder.run`, `POST /api/v1/sites/{site}/builder`).
- **It edits, it doesn't regenerate.** The AI reads your files and makes targeted edits (`list_files`, `read_file`, `edit_file`, `write_file`, `delete_file`, `search_files`). Small changes stay small.
- **Everything lands in the draft.** Nothing goes live until you publish. Preview at `/s/<app>/?draft=1`. Don't like it? Discard the draft, or restore any [version](/bunker/docs/build/versions/).
- **One build at a time per app**, up to 25 steps and 4 minutes. If it stops early, what's done is saved: ask it to continue.
- **The conversation is remembered** per app (`builder.history`). Start fresh with `builder.reset`; your files stay.
- **Attach text files** (a CSV sample, a spec) with the `files` field.

Progress streams as server-sent events: `status` ("Thinking"), `text`, `file` (path, created/changed/deleted), then `done` with a summary, the changed files, the preview URL and your remaining allowance. Errors arrive as `error` with a plain-English message.

## Your daily allowance

Each person and each app has a daily token allowance set by the admin (defaults: 2,000,000 per person, 5,000,000 per app). The builder shows how much is left. It resets at midnight UTC. `GET /api/v1/ai/status` tells you whether AI is on, which models are used and your allowance.

## Tips for good results

- **Say who uses it and what they do.** "Managers approve; everyone else only sees their own requests" is better than "add permissions".
- **Name the data.** "Each request has a person, start date, end date, type (vacation, sick, other) and a status."
- **One change per message** when refining. It's faster and easier to undo.
- **Describe what you see when something's wrong.** "The calendar is empty after I submit a request."
- **Ask for the boring parts.** Empty states, a CSV export, a print view, mobile layout.
- **Know the sandbox.** Apps can't call outside websites or load scripts from the internet; the builder knows this and uses [built-in libraries](/bunker/docs/build/drop-anything/#built-in-libraries) and the [SDK](/bunker/docs/sdk/overview/) instead. If you need company data, ask your admin for a [connection](/bunker/docs/admin/connections/).

## Privacy

Prompts and files you attach are sent to the AI provider your admin chose. By default Bunker logs only metadata (who, which app, how many tokens); admins can turn on full content logging (`ai.log_content`). Keys never reach the browser.
