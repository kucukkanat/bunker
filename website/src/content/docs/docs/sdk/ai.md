---
title: bunker.ai
description: Call the admin-configured model from a page.
sidebar: { order: 5 }
---

```js
const reply = await bunker.ai.chat([
  { role: 'system', content: 'Summarise feedback into 3 themes.' },
  { role: 'user', content: feedback.join('\n') },
]);
console.log(reply.text);

for await (const chunk of bunker.ai.stream(messages)) out.textContent += chunk;

const data = await bunker.ai.json(messages, {
  schema: { type: 'object', properties: { sentiment: { enum: ['positive', 'neutral', 'negative'] } } },
});
```

Calls count against the viewer's and the site's [quotas](/bunker/docs/admin/ai/#limits). If AI is disabled, the call throws `unavailable`.
