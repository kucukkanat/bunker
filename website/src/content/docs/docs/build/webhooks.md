---
title: Inbound webhooks
description: Send data into an app from Power Automate, Zapier, a form or a script.
sidebar: { order: 7 }
---

A webhook is a secret URL that adds whatever JSON it receives to one of your app's [collections](/bunker/docs/sdk/db/). Use it to feed an app from other tools: a Power Automate flow when someone is hired, a Zapier zap for new form entries, a nightly script, a monitoring alert. The data is simply saved, and every open page that subscribes to the collection updates live. If you'd rather check, reshape or forward what arrives, point the webhook at a [server function](#send-it-to-a-function) instead.

## Create one

In the workspace open **Data → Webhooks → New webhook**, give it a name ("Power Automate: new hires") and pick the collection. Copy the URL: it's shown **once**.

Or from the CLI:

```sh
bunker api hooks.create hires --name "Power Automate: new hires" --collection people
```

## Send data

```sh
# one record
curl -X POST https://bunker.example.com/hooks/bkh_... \
  -H 'Content-Type: application/json' -d '{"name":"Grace","team":"Platform"}'

# up to 100 records at once
curl -X POST https://bunker.example.com/hooks/bkh_... \
  -H 'Content-Type: application/json' -d '[{"name":"Linus"},{"name":"Ken"}]'

# plain HTML form posts work too
curl -X POST https://bunker.example.com/hooks/bkh_... -d 'name=Barbara&team=Design'
```

The answer is `201 {"ok":true,"added":1,"ids":["..."]}`. The records show up in **Data** right away, without a reload, with the webhook's creator under "Added by".

## Send it to a function

A webhook can target one of the app's [server functions](/bunker/docs/build/server-functions/) instead of a collection. In **Data → Webhooks → New webhook**, choose **Function** and pick it. Or:

```sh
bunker api hooks.create hires --name "GitHub: new issues" --function github
```

- The JSON body becomes the function's `input`; `ctx.trigger` is `{ type: 'webhook', hook }`.
- It runs as the person who created the webhook (or the owner, if the function is elevated), with the longer trigger time limit.
- Whatever the function returns is sent back as the HTTP response body, as JSON. If it throws, the caller gets an error status with the message.
- It always runs the **live** version of the function.

```js
// api/github.js
export default async function (event) {
  if (event.action !== 'opened') return { skipped: true };
  await bunker.db.collection('issues').add({ title: event.issue.title });
  return { saved: true };
}
```

## Rules and limits

- Records are written **as the person who created the webhook**, through the collection's normal [access rules](/bunker/docs/sdk/db/#access-rules). If that person loses edit access or leaves, the webhook stops working.
- Fields starting with `_` are Bunker's (`_id`, `_author`, `_created`...) and are ignored if sent.
- 1 MB and 100 records per request, 120 requests a minute per webhook, 20 webhooks per site.
- Anyone who has the URL can add data, so treat it like a password. Delete the webhook to revoke it (`bunker api hooks.delete hires 3`).
- Calls are counted per webhook (last used, total records) and creating or deleting one is in the audit log.
