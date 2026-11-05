---
title: bunker.me
description: Who is viewing.
sidebar: { order: 2 }
---

```js
const me = await bunker.me();
// {
//   id: "8f1c…",            // stable Entra object ID
//   name: "Ada Lovelace",
//   email: "ada@corp.com",
//   groups: ["Finance", "London Office"],
//   role: "viewer"           // viewer | editor | owner | admin on this site
// }

if (await bunker.me.inGroup('Finance-Approvers')) showApproveButtons();
```

`groups` only lists groups that are used in this site's sharing or data rules. To check any other group, use `inGroup()`.
