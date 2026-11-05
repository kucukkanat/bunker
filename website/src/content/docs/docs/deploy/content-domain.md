---
title: Separate content domain
description: Serve app content from its own origin with BUNKER_CONTENT_URL for isolation that doesn't depend on the sandbox alone.
sidebar: { order: 7 }
---

By default, Bunker serves everything from one hostname. App content is still isolated: it runs in a sandboxed iframe with an opaque origin and is fetched through signed tickets (see [Sandbox and headers](/bunker/docs/security/sandbox/)).

If you can add a second hostname, you get a second, independent wall. This is the model large platforms use for user content.

```sh
BUNKER_URL=https://bunker.example.com
BUNKER_CONTENT_URL=https://usercontent.example.com
```

Point both names at the same Bunker (same server, same proxy, a certificate for each).

## What changes

- The shell page at `https://bunker.example.com/s/<site>/` loads the iframe from `https://usercontent.example.com/s/<site>/~r/<ticket>/...`.
- The content host serves **only** ticketed content (`/s/<site>/~r/...` and `/s/<site>/~f/...`) and platform assets under `/_/`. Everything else, including the API, sign-in and the dashboard, returns 404 there.
- Sessions are never accepted on the content host. Bunker's cookie isn't scoped to it.
- The content CSP's `frame-ancestors` becomes your `BUNKER_URL`, so only Bunker can frame app content.
- The shell page's `frame-src` allows the content origin.

## Pick a separate registrable domain if you can

`usercontent.example.com` is good. `example-usercontent.com` is better: a different registrable domain means a different "site" to the browser, with no shared cookies at all, and process isolation in browsers that isolate by site.
