---
title: bunker.db
description: Shared JSON collections for each app, with access rules, queries, counts, export and live updates.
sidebar: { order: 3 }
---

Each app has its own database of **collections** of JSON documents, shared by everyone who can open the app. No setup: a collection is created the first time you add to it.

```js
const orders = bunker.db.collection('orders');

const doc = await orders.add({ meal: 'Pasta', qty: 2 });   // the stored doc, with _id (and an id alias)
await orders.update(doc._id, { qty: 3 });                    // merge fields; null removes a field
await orders.set(doc._id, { meal: 'Salad' });                // create or replace with a chosen id
await orders.delete(doc._id);                                // also .remove()
const one = await orders.get(doc._id);                       // null if missing

const page = await orders.list({ limit: 50, after: cursor }); // { items: [...], next }
const mine = await orders.query({
  where: { _author: 'me', status: 'open' },
  orderBy: '-_created',
  limit: 100,
});                                                            // an array, with .items and .next too
const everything = await orders.all({ where: { status: 'open' } }); // pages through up to 10,000
const open = await orders.count({ status: 'open' });
const csv = await orders.export('csv');                     // or 'json'

const names = await bunker.db.list();                        // collections with rules and sizes
```

## Documents

Every document gets these fields automatically:

| Field | |
|---|---|
| `_id` | Document ID (also readable as `doc.id`) |
| `_author` | Numeric Bunker user ID of the person who created it (compare with `bunker.me().id`) |
| `_authorName` | Their name |
| `_created`, `_updated` | ISO timestamps |

Store dates as ISO strings and money as numbers.

## Queries

- `where` matches fields exactly. The value `'me'` means the current viewer (handy with `_author`). `query({ status: 'open' })` is shorthand for `query({ where: { status: 'open' } })`.
- `orderBy` (or `order`) is a field name; prefix `-` for descending.
- `limit` and `after` page through results. `next` is `null` on the last page.

## Live updates

```js
const stop = orders.subscribe(change => {
  // { type: 'added' | 'updated' | 'deleted', doc }
});
// later: stop();
```

Use `subscribe` instead of polling. All subscriptions in a tab share one server-sent events stream.

## Access rules

Each collection has one rule. **Editors and owners can always do everything.** For viewers:

| Rule | Viewers can | Good for |
|---|---|---|
| `own` (default) | Read everything, add, change and delete **their own** documents | Most apps |
| `private` | Add, and see and change **only their own** documents | Personal submissions an editor reviews |
| `read` | Only read | Content the editors maintain |
| `append` | Add, and read only their own; no changes | Feedback, sign-ups where people mustn't see each other |
| `open` | Read, add, change and delete anything | Shared boards |
| `group:<Name>` | Members of that group get full access; everyone else reads | Approvers, app admins |

**Pages can't change rules.** `collection.rule()` from a page throws `forbidden`, so a viewer can never loosen the rules with the browser console. Editors set rules:

- in the app's **Data** tab in the dashboard,
- with the CLI: `bunker api db.rule --site lunch --coll orders --rule private`,
- or by asking the [AI builder](/bunker/docs/build/ai-builder/), which sets them while it builds.

## Limits

| | Default | Setting |
|---|---|---|
| One document | 1 MB | `BUNKER_DB_DOC_MAX_KB` |
| All data of one app | 100 MB | `BUNKER_DB_MAX_MB` |
| Documents per app | 250,000 | `BUNKER_DB_MAX_DOCS` |
| Collections per app | 200 | `BUNKER_DB_MAX_COLLECTIONS` |

## API

| Operation | Route (under `/api/v1/sites/{site}`) |
|---|---|
| `db.collections` | `GET /db` |
| `db.list` / `db.add` | `GET` / `POST /db/{coll}` (query: `where` JSON, `order`, `limit`, `after`) |
| `db.query` | `POST /db/{coll}/_query` |
| `db.count` | `GET /db/{coll}/_count` |
| `db.get` / `db.set` / `db.update` / `db.delete` | `GET` / `PUT` / `PATCH` / `DELETE /db/{coll}/{id}` |
| `db.export` | `GET /db/{coll}/_export?format=csv\|json` |
| `db.rule` (editors, not from pages) | `PUT /db/{coll}/_rule` |
| `db.import` (editors) | `POST /db/{coll}/_import` (CSV or a JSON array) |
| `db.drop` (editors) | `DELETE /db/{coll}` |
| `events.stream` | `GET /events?ch=db:<coll>` (server-sent events) |
