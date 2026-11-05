---
title: bunker.ai
description: Ask the admin's AI provider from a page, for summaries, classification, drafting and extraction.
sidebar: { order: 7 }
---

```js
const reply = await bunker.ai.chat([
  { role: 'system', content: 'Summarize in 3 bullet points.' },
  { role: 'user', content: text },
]);
reply.text;

const short = await bunker.ai.chat('Suggest a name for a lunch club');   // a string is one user message

for await (const chunk of bunker.ai.stream(messages)) out.textContent += chunk;

const data = await bunker.ai.json(messages, {
  schema: { type: 'object', properties: { sentiment: { enum: ['positive', 'neutral', 'negative'] } } },
});
```

- Uses the provider and the **page model** your admin chose (for example Claude Haiku or GPT-5 mini). Keys stay on the server.
- Calls count against the **viewer's** and the **app's** daily allowance. Call AI on a button press, not on every keystroke.
- Handle `quota_exceeded` and `unavailable` (no provider set up) gracefully.
- `json` resolves to the parsed value. With a `schema`, the answer follows it. Bunker retries once if the model's JSON doesn't parse, then throws `invalid`.
- Breaking out of a `for await` loop over `stream` cancels the request.
- Every call is audited with metadata (who, app, model, tokens). Full prompts are logged only if the admin turned that on.

## Request body

| Field | |
|---|---|
| `messages` | `[{ role: 'user' \| 'assistant' \| 'system', content }]` |
| `prompt` | A single question, instead of `messages` |
| `system` | Instructions |
| `json` | `true` to answer as JSON |
| `schema` | A JSON Schema the answer must follow (implies `json`) |
| `maxTokens` | Longest answer (default 1024, capped by the admin's per-request limit) |
| `stream` | `true` for server-sent events: `text` chunks, then `done` |

The reply is `{ text, stopReason, model, usage }`. JSON answers add `json` (the parsed value), or `json: null` with `jsonError` if the model still did not return valid JSON.
