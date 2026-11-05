---
title: Build with AI
description: Describe an app in plain words and let Bunker build it.
sidebar: { order: 1 }
---

The builder is a chat on the left and a live preview on the right.

## Writing a good request
Say **who** uses it, **what** they do, and **what they should see**.

> Expense pre-approval form. Staff enter amount, category and reason. Anyone in *Finance-Approvers* can approve or reject. Requesters see the status of their own requests only.

Bunker understands its own features, so you can ask for:
- Saving data ("keep a list of all submissions")
- Live updates ("show new orders without refreshing")
- People ("fill in my name automatically", "only managers can approve")
- Files ("let people attach a receipt")
- AI ("summarise all feedback into three themes")
- Microsoft 365 ("show the rows from the *Projects* SharePoint list")

## Changing things
Ask in plain words, one change at a time: "Move the total to the top", "Use our green", "Make it work on phones". Each change updates the preview. Nothing goes live until you click **Publish**.

## Code tab
If you or a colleague know HTML, the **Code** tab shows and edits the files directly.

## What the builder can't do
Pages can't call outside websites or APIs. That's deliberate. See [Sandbox & CSP](/bunker/docs/security/sandbox/). If you need outside data, ask your admin about [Microsoft 365 access](/bunker/docs/admin/m365/).
