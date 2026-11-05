---
title: Sandbox and headers
description: Exactly how Bunker isolates app content, with the sandbox flags and every header it sends.
sidebar: { order: 2 }
---

These values are copied from the code (`internal/sites/serve.go`, `internal/server/server.go`). If they ever differ, the code wins: please [tell us](/bunker/docs/security/reporting/).

## Two documents per app

Opening `https://bunker.example.com/s/<app>/` returns a small **shell** page. The app itself loads inside it:

```html
<iframe id="bk-frame" src="/s/<app>/~r/<ticket>/index.html"
  sandbox="allow-scripts allow-forms allow-modals allow-downloads"
  allow="clipboard-write; fullscreen"></iframe>
<script src="/_/shell.js"></script>
```

**No `allow-same-origin`**, so the app runs with an **opaque origin**: no access to Bunker's cookies, `localStorage` or DOM, and every request it makes is cross-origin to Bunker. **No `allow-popups`** and **no `allow-top-navigation`**: it can't open windows or navigate the page around it.

With a [separate content domain](/bunker/docs/deploy/content-domain/), the iframe `src` points at `BUNKER_CONTENT_URL` instead.

## Content tickets

App files are served only under a ticket in the path:

```
/s/<app>/~r/<site>-<version>-<viewer>-<expiry>-<mac>/<path>    app files
/s/<app>/~f/<ticket>/<upload-id>/file                           uploaded files
```

- The MAC is HMAC-SHA256 (truncated to 144 bits) under the server's secret key, over the app, version (0 = draft), viewer and expiry.
- Tickets expire after 6 to 12 hours (expiry is bucketed into 6-hour windows so URLs stay cacheable).
- On **every** request Bunker re-checks that the viewer still exists, isn't disabled and still has access to the app. Revoking a share takes effect on the next request.
- Draft tickets (version 0) only work for editors.
- HTML documents fetched as a top-level page (not as an iframe) additionally require the session cookie, so a leaked ticket URL opened directly shows "Open this page through Bunker" to anyone else.

Sandboxed frames can't send cookies on subresource requests, which is why access is carried in the path and not in a cookie.

## Headers on all app content

```http
Content-Security-Policy: sandbox allow-scripts allow-forms allow-modals allow-downloads;
  default-src 'none';
  script-src 'self' 'unsafe-inline' 'unsafe-eval' 'wasm-unsafe-eval' blob:;
  style-src 'self' 'unsafe-inline';
  img-src 'self' data: blob:;
  media-src 'self' data: blob:;
  font-src 'self' data:;
  worker-src 'self' blob:;
  connect-src 'self';
  form-action 'none';
  frame-ancestors 'self';            (your BUNKER_URL when a content domain is used)
  base-uri 'none';
  frame-src 'self' blob: data:;
  object-src 'none'
Connection-Allowlist: (response-origin)
X-Content-Type-Options: nosniff
Referrer-Policy: no-referrer
Cross-Origin-Resource-Policy: cross-origin
Access-Control-Allow-Origin: *
Permissions-Policy: camera=(), microphone=(), geolocation=(), usb=(), serial=(), hid=(),
  payment=(), display-capture=(), clipboard-read=()
Vary: Sec-Fetch-Dest
```

Why each one:

| Header | Purpose |
|---|---|
| CSP `sandbox ...` | Enforces the sandbox even if someone opens a content URL directly, as a top-level page. Sent on **every** content response, including SVG and other non-HTML files |
| `default-src 'none'` + `'self'` sources | Pages load only their own files and Bunker's built-in libraries. No CDNs, no external images, fonts or scripts |
| `'unsafe-inline'`, `'unsafe-eval'`, `'wasm-unsafe-eval'` | Needed by inline scripts, Alpine, Vue's template compiler, Tailwind's browser build and WebAssembly. Acceptable because the sandbox, not the CSP, is the boundary |
| `connect-src 'self'` | `fetch('data.json')` works. Requests carry no cookies, and other apps' files need their own tickets, so this can't reach the API or other apps |
| `form-action 'none'`, `base-uri 'none'`, `object-src 'none'` | No form posts anywhere, no base-URL tricks, no plugins |
| `frame-ancestors` | Only Bunker can frame app content |
| `Connection-Allowlist: (response-origin)` | On Chromium and Edge 152+, blocks WebRTC, navigations, prefetch and redirects to anything but Bunker's own origin. CSP can't block WebRTC |
| `Access-Control-Allow-Origin: *`, `CORP: cross-origin` | Requests from an opaque origin are cross-origin (`Origin: null`); module scripts and fonts need CORS. Safe because access is in the ticket, never in credentials |
| `Referrer-Policy: no-referrer` | Ticket URLs never leak in `Referer` |
| `Permissions-Policy` | No camera, microphone, location, USB, serial, HID, payments, screen capture or clipboard reading |

Caching: published versions `Cache-Control: private, max-age=86400, immutable` with an `ETag` from the content hash; drafts `no-cache`. Error pages from the content path use `Content-Security-Policy: sandbox; default-src 'none'; style-src 'unsafe-inline'`.

## The shell page

```http
Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline';
  img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src 'self' [content origin];
  frame-ancestors 'self'; base-uri 'none'; form-action 'self'
X-Frame-Options: SAMEORIGIN
Cache-Control: no-store
```

No inline scripts are allowed in Bunker's own pages.

The shell talks to the app over a `MessageChannel` it hands to its own iframe on every load, after checking `event.source === iframe.contentWindow` (an origin check would be useless: every sandboxed frame's origin is the string `"null"`). It forwards SDK calls only to `/api/v1/sites/<this app>/...`, with `X-Bunker: 1` and `X-Bunker-Page: 1`. The server rejects any operation not marked page-safe when `X-Bunker-Page` is present.

## Every response

```http
X-Content-Type-Options: nosniff
Referrer-Policy: same-origin                 (no-referrer on app content)
Cross-Origin-Opener-Policy: same-origin      (removed on app content)
Permissions-Policy: camera=(), microphone=(), geolocation=(), payment=(), usb=()
Strict-Transport-Security: max-age=31536000  (when BUNKER_URL is https)
```

## Defense in depth inside the page

The SDK, injected as the first script of every HTML file, also:

- removes `RTCPeerConnection`, `RTCDataChannel` and related constructors (a speed bump for browsers without Connection-Allowlist; a determined page can get them back from a fresh frame, so this is not relied on),
- routes external links, `target="_blank"` and `window.open()` through a "Leave Bunker?" confirmation,
- installs in-memory `localStorage`, `sessionStorage` and `document.cookie` so libraries don't crash.

## Session and CSRF

- Session cookie `bunker_session`: random 256-bit token, stored only as a hash, `HttpOnly`, `SameSite=Lax`, `Secure` on https, 14-day sliding expiry.
- Cookie-authenticated `POST`/`PUT`/`PATCH`/`DELETE` requests must carry `X-Bunker: 1`, and a present `Origin` must be Bunker's own. Browsers can't add custom headers cross-site without a CORS preflight, which Bunker never grants.
- Bearer tokens (`bk_...`) skip the CSRF check, because browsers never attach them automatically.
