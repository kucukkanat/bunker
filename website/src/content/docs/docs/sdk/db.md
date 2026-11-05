---
title: bunker.db
description: JSON collections with queries and live updates.
sidebar: { order: 3 }
---

Each site has its own database of **collections** of JSON documents.

```js
const orders = bunker.db.collection('orders');

const { id } = await orders.add({ meal: 'Pasta', drink: 'Water' });
await orders.update(id, { drink: 'Juice' });
await orders.set(id, { meal: 'Salad' });          // replace
await orders.delete(id);

const doc  = await orders.get(id);
const page = await orders.list({ limit: 50, after: cursor });
const mine = await orders.query({ where: { _author: 'me' }, orderBy: '-_created' });

const stop = orders.subscribe((change) => {
  // { type: 'added' | 'updated' | 'deleted', doc }
});
```

Every document gets `_id`, `_author`, `_created` and `_updated` automatically.

## Data rules
Each collection has a rule that decides what viewers may do. Editors and owners can always do everything.

| Rule | Viewers can |
|---|---|
| `own` (default) | read all, add, change/delete their own |
| `private` | add, and read/change only their own |
| `read` | read only |
| `append` | add only (e.g. anonymous-ish feedback; author still recorded for audit) |
| `group:<name>` | full access if in the group, otherwise read |

```js
await bunker.db.collection('approvals').rule('group:Finance-Approvers');
```
Only editors can change rules.

## Limits
1 MB per document, 100 MB per site by default. The admin can change both.
