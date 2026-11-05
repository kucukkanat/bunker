// Server function: pages call it with bunker.fn('contact', { name, email, topic, message }).
// It runs on the Bunker server as the site owner (elevated), so people can
// send a message without being able to read anyone else's, and the checks
// below can't be skipped from the browser.
export const config = { elevated: true };

const TOPICS = ['Question', 'Request', 'Feedback', 'Something is broken'];
const MAX_MESSAGE = 4000;
const WAIT_SECONDS = 60; // one message per person per minute

export default async function (input, ctx) {
  input = input && typeof input === 'object' ? input : {};
  const name = clean(input.name, 120) || ctx.user.name || '';
  const email = clean(input.email, 200).toLowerCase() || ctx.user.email || '';
  const topic = TOPICS.includes(input.topic) ? input.topic : TOPICS[0];
  const message = clean(input.message, MAX_MESSAGE + 1);

  if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) fail('Please enter a valid email address.');
  if (message.length < 10) fail('Please write a little more (at least 10 characters).');
  if (message.length > MAX_MESSAGE) fail('Please keep your message under ' + MAX_MESSAGE + ' characters.');

  const messages = bunker.db.collection('messages');
  const found = await messages.query({ where: { fromId: ctx.user.id }, orderBy: '-_created', limit: 1 });
  const last = (Array.isArray(found) ? found : found.items || [])[0];
  if (last && Date.now() - millis(last._created) < WAIT_SECONDS * 1000) {
    fail('Thanks, we already got your message. You can send another one in a minute.');
  }

  // fromId and fromEmail come from the signed-in account, not the form.
  const doc = await messages.add({ name, email, topic, message, status: 'new', fromId: ctx.user.id, fromEmail: ctx.user.email });

  // Tell the app's owner in Bunker, and optionally in a chat channel.
  const owner = await bunker.me();
  const title = 'New message from ' + (name || email);
  await bunker.notify({ to: [owner.email], title, body: topic + ': ' + message.slice(0, 140), path: '/#inbox' }).catch(() => {});
  const hook = await bunker.secrets.get('NOTIFY_WEBHOOK'); // add it in the app's Functions settings to post to Slack or Teams
  if (hook) {
    const res = await fetch(hook, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ text: title + ' (' + topic + ')' }) });
    if (!res.ok) console.warn('The webhook answered', res.status);
  }
  console.log('message', doc._id, 'from', ctx.user.email);
  return { id: doc._id };
}

function clean(v, max) {
  return String(v == null ? '' : v).replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f]/g, '').trim().slice(0, max);
}

function millis(t) {
  if (typeof t === 'number') return t < 1e12 ? t * 1000 : t;
  return Date.parse(t) || 0;
}

function fail(message) {
  throw new Error(message);
}
