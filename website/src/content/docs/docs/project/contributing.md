---
title: Contributing
description: How to help.
sidebar: { order: 3 }
---

Bunker is MIT licensed and built in the open.

- Read [`PLAN.md`](https://github.com/kucukkanat/bunker/blob/master/PLAN.md) for the architecture.
- Open an issue before large changes.
- Report security problems privately (see the [security model](/bunker/docs/security/model/#reporting-a-vulnerability)).

## Working on these docs
```sh
cd website
npm install
npm run dev
```
Docs live in `website/src/content/docs/docs/`. The marketing page is `website/src/pages/index.astro`. Every push to `master` deploys to GitHub Pages.
