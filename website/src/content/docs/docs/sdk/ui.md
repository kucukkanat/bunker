---
title: Notify, toast, download, open
description: Send in-app notifications, show a toast, save a generated file and open external links safely.
sidebar: { order: 10 }
---

## bunker.notify

```js
await bunker.notify({
  to: ['ada@example.com', 'Finance-Approvers'],
  title: 'New request from Sam',
  body: '£120 taxi',
  path: '/#/requests/42',
});
```

Sends an in-app Bunker notification to people (by email) and groups (by name). **Only recipients who can open this app receive it**; others are skipped. `path` opens that place in the app when they click it.

Notifications are rate limited. Send them on real events (a new request, a decision), never in loops. API: `notify.send`, `POST /api/v1/sites/{site}/notify`.

## bunker.toast

```js
bunker.toast('Saved');
```

A short message shown by Bunker at the bottom of the window.

## bunker.download

```js
bunker.download('report.csv', csvText, 'text/csv');
```

Saves a file the app generated (a CSV export, a PDF, an image). Use this instead of building `blob:` links yourself; it works the same in every browser inside the sandbox.

## bunker.open

```js
bunker.open('https://example.com/policy');
```

Pages can't open popups or navigate away. `bunker.open` (`http`, `https`, `mailto` and `tel` links only) asks the shell, which asks **"Leave Bunker?"** (showing the destination host) and then opens the link in a new tab with `noopener` and `noreferrer`. Ordinary `<a href="https://...">` links, `target="_blank"` links and `window.open()` in your app are routed through it automatically. Links between your own pages (`href="about.html"`) work normally.

## Page title and address

The shell keeps the browser's address bar and tab title in sync with your app: when your app changes `document.title` or navigates between its own pages (including hash routes like `#/requests/42`), the link people copy points to the same place.

## bunker.on

`bunker.on(channel, fn)` listens to any live channel (`db:<collection>`, `room:<name>`, `presence:<name>`, `deploy`) and returns a function that stops listening. The helpers above use it for you.
