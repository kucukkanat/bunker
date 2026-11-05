---
title: AI providers
description: Choose the model behind the builder and bunker.ai.
sidebar: { order: 3 }
---

Bunker doesn't ship with a model. The admin picks one, and its keys never leave the server.

| Provider | `BUNKER_AI_PROVIDER` | Required settings |
|---|---|---|
| Anthropic Claude | `anthropic` | `BUNKER_AI_API_KEY`, `BUNKER_AI_MODEL` |
| OpenAI | `openai` | `BUNKER_AI_API_KEY`, `BUNKER_AI_MODEL` |
| Azure OpenAI | `azure-openai` | `BUNKER_AI_ENDPOINT`, `BUNKER_AI_API_KEY`, `BUNKER_AI_DEPLOYMENT` |
| Ollama / OpenAI-compatible | `openai-compatible` | `BUNKER_AI_ENDPOINT`, `BUNKER_AI_MODEL` |

You can set different models for the **builder** (`BUNKER_AI_BUILDER_MODEL`, a capable coding model) and for **pages** (`BUNKER_AI_PAGE_MODEL`, a fast, cheaper model).

## Limits
- `BUNKER_AI_USER_DAILY_TOKENS`: per-person daily budget.
- `BUNKER_AI_SITE_DAILY_TOKENS`: per-site daily budget for `bunker.ai` calls.
- `BUNKER_AI_MAX_REQUEST_TOKENS`: size cap per call.

## Logging
Every AI call is written to the [audit log](/bunker/docs/admin/audit/) with user, site, model and token counts. Set `BUNKER_AI_LOG_CONTENT=true` to also keep prompts and responses (off by default).

To turn AI off completely, set `BUNKER_AI_PROVIDER=none`. Upload and the code editor still work.
