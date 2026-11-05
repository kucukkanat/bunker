---
title: bunker.room
description: Live, ephemeral broadcast and presence for cursors, "who's online", timers and quizzes.
sidebar: { order: 6 }
---

A **room** is a live channel inside one app. Messages go to everyone in the room right now and are **not stored**.

```js
const room = bunker.room('standup');

await room.send({ type: 'cursor', x, y });       // broadcast to everyone in the room
const off = room.subscribe(msg => {               // also room.on(...)
  // { data, from: { id, name }, ts }
});

const people = await room.presence({ status: 'here' });  // join and share your state
room.onPresence(members => {
  // [{ id, name, state }, ...]
});
const now = await room.members();
await room.leave();
```

- Good for live cursors, "who's online", a shared timer, a quiz, a game.
- Anything that must be kept goes in [`bunker.db`](/bunker/docs/sdk/db/), which has its own live updates.
- `from` is filled in by the server, so nobody can pretend to be someone else.
- `presence()` keeps you listed by refreshing every 15 seconds; the SDK leaves the room when the page closes.
- Rooms share the tab's single server-sent events stream.

## API

| Operation | Route (under `/api/v1/sites/{site}`) |
|---|---|
| `rooms.send` | `POST /rooms/{room}` `{data}` |
| `rooms.presence` | `POST /rooms/{room}/presence` `{state}` |
| `rooms.members` | `GET /rooms/{room}/presence` |
| `rooms.leave` | `DELETE /rooms/{room}/presence` |
| `events.stream` | `GET /events?ch=room:<name>,presence:<name>` |

Live streams are capped at 20 per person and 20,000 per server (`BUNKER_EVENTS_MAX_PER_USER`, `BUNKER_EVENTS_MAX`).
