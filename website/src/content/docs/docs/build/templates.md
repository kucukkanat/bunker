---
title: Templates
description: Offer an app as a starting point for others.
sidebar: { order: 5 }
---

Any editor can mark an app as a **template**:

```sh
curl -X PATCH https://bunker.example.com/api/v1/sites/expense-form \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"isTemplate": true}'
```

Templates are listed for everyone at `GET /api/v1/sites?filter=templates`, sorted by title.

:::caution[What others can see]
Everyone signed in can see a template's **title, description and icon** in the list. Its files and data stay private until you share it. Don't put anything sensitive in the name of an app you mark as a template.
:::

## Using a template

1. Open the template (it has to be shared with you, at least as a viewer, to look inside).
2. Click **Use this template**. Bunker copies its files, not its data, into a new app that you own.

Under the hood this is `sites.duplicate` (`POST /api/v1/sites/{site}/duplicate`), which editors can also use to copy any app.

## Built-in templates

Bunker is getting a small set of starter templates (Preview) (request form with approvals, team tracker, sign-up sheet, dashboard over a CSV, simple docs site). They are written to the [SDK](/bunker/docs/sdk/overview/) and the sandbox rules, so they are good examples to learn from.
