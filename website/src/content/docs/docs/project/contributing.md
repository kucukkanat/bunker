---
title: Contributing
description: How to build Bunker, run the tests and work on these docs.
sidebar: { order: 3 }
---

Bunker is MIT licensed and built in the open. Read [`PLAN.md`](https://github.com/kucukkanat/bunker/blob/master/PLAN.md) for the product and [`CONTRACT.md`](https://github.com/kucukkanat/bunker/blob/master/CONTRACT.md) for the engineering rules. Open an issue before large changes. Report security problems [privately](/bunker/docs/security/reporting/).

## The server

```sh
go test ./...
CGO_ENABLED=0 go build -trimpath -o bunker ./cmd/bunker
./bunker dev          # http://127.0.0.1:8080, signed in as a local admin
```

Pure Go, no CGO, few dependencies. Each feature is a module under `internal/<name>` that registers itself and adds one line to `cmd/bunker/modules.go`.

House rules, in short:

- **Every UI action is an operation** (`core.Op`) with a name, route, summary and parameters. The CLI, MCP and `/api/v1/ops` are generated from them. No hidden dashboard-only endpoints.
- Operations reachable from app pages set `PageSafe: true`. Never deploy, share or settings.
- Errors are plain English and say what to do next.
- Audit every security-relevant action.
- Performance matters: bounded caches, indexed SQL, streamed bodies, no goroutine leaks.

## These docs

```sh
cd website
npm ci
npm run dev        # http://localhost:4321/bunker/
npm run build
```

Docs are Markdown in `website/src/content/docs/docs/` (Astro + Starlight). The landing page is `website/src/pages/index.astro` with `website/src/styles/landing.css`. Every push to `master` that touches `website/` deploys to GitHub Pages.

When you document behaviour, check it against the code. If it isn't shipped yet, mark the page with a Preview badge (`sidebar: { badge: { text: 'Preview', variant: 'caution' } }`).
