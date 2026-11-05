---
title: AI providers
description: Connect Claude, OpenAI, Azure OpenAI or a local model, choose models and set daily allowances.
sidebar: { order: 1 }
---

Bunker's [AI builder](/bunker/docs/build/ai-builder/) and [`bunker.ai`](/bunker/docs/sdk/ai/) use one provider that an admin chooses. Keys stay on the server, encrypted, and never reach a browser. Without a provider, everything except the AI features works.

## Providers

| `provider` | Default builder model | Default page model | Notes |
|---|---|---|---|
| `anthropic` (Claude) | `claude-sonnet-5-5` | `claude-haiku-5-5` | Endpoint `https://api.anthropic.com`, `anthropic-version` `2023-06-01` |
| `openai` | `gpt-5` | `gpt-5-mini` | Endpoint `https://api.openai.com/v1` |
| `azure-openai` | your deployment | your deployment | Set `endpoint` to your resource URL and the models to your **deployment names**. `api-version` `2024-10-21` by default |
| `openai-compatible` | set it | set it | Ollama, LM Studio, vLLM and similar. Endpoint `http://localhost:11434/v1` by default; key optional |
| `none` | | | AI off |

With only an API key set, Bunker recognises Anthropic (`sk-ant-...`) and OpenAI (`sk-...`) keys by their shape.

## Configure

*Admin → AI* in the dashboard, or `ai.config.update`:

```sh
curl -X PUT https://bunker.example.com/api/v1/admin/ai \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"provider":"anthropic","apiKey":"sk-ant-...","userDailyTokens":2000000}'
```

| Field | Default | |
|---|---|---|
| `provider` | `none` | See above |
| `apiKey` | | Stored encrypted; never shown again (`ai.config.get` returns it masked). An empty string removes it |
| `endpoint` | per provider | Base URL |
| `apiVersion` | per provider | Azure `api-version` or `anthropic-version` |
| `builderModel` | per provider | Model (Azure: deployment) for the AI builder |
| `pageModel` | per provider | Model for `bunker.ai` in pages. Pick a fast, cheap one |
| `userDailyTokens` | 2,000,000 | Per person per day. `0` = no limit |
| `siteDailyTokens` | 5,000,000 | Per app per day. `0` = no limit |
| `maxRequestTokens` | 16,000 | Largest single page request. `0` = no limit |
| `logContent` | `false` | Keep prompts and answers in the audit log (otherwise only metadata) |

**Test before saving:** `ai.config.test` (`POST /api/v1/admin/ai/test`) sends a tiny prompt with the settings you give it, saved or not.

Allowances reset at midnight UTC. People see what's left in the builder and in `GET /api/v1/ai/status`.

## Environment variables

Every field can be pinned from the environment. Pinned fields can't be changed in the dashboard.

```sh
BUNKER_AI_PROVIDER=azure-openai
BUNKER_AI_API_KEY=...
BUNKER_AI_ENDPOINT=https://my-resource.openai.azure.com
BUNKER_AI_API_VERSION=2024-10-21
BUNKER_AI_BUILDER_MODEL=gpt-5-deployment
BUNKER_AI_PAGE_MODEL=gpt-5-mini-deployment
BUNKER_AI_USER_DAILY_TOKENS=2000000
BUNKER_AI_SITE_DAILY_TOKENS=5000000
BUNKER_AI_MAX_REQUEST_TOKENS=16000
BUNKER_AI_LOG_CONTENT=false
```

## Usage

`ai.usage` (`GET /api/v1/admin/ai/usage?days=30`, up to 366) shows token use per person and per app. Every AI call is also in the [audit log](/bunker/docs/admin/audit/) with who, which app, the model and the token counts.

## Keeping data where you want it

- **Azure OpenAI** keeps prompts in your Azure tenant and region.
- **`openai-compatible`** with Ollama or vLLM on your own hardware keeps prompts inside your network. Bunker calls the endpoint from the server, so it can be a private address.
