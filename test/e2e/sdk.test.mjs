// End-to-end test of the page SDK (bunker.js) and shell (shell.js) in a real
// browser. Builds the binary, runs `bunker dev` on a free port with a temp
// data dir, deploys test pages and checks every SDK call inside the
// sandboxed frame, plus realtime between two signed-in people.
//
//   node test/e2e/sdk.test.mjs        (needs Go and Playwright's Chromium)
import { spawn, execFileSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { mkdtempSync, rmSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import net from 'node:net';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..');
const require = createRequire(import.meta.url);

function loadPlaywright() {
  const roots = [...(process.env.NODE_PATH || '').split(':').filter(Boolean), join(ROOT, 'website', 'node_modules'), '/home/user/bunker/website/node_modules', '/opt/node22/lib/node_modules', '/opt/node-tools/node_modules'];
  try { roots.push(execFileSync('npm', ['root', '-g'], { encoding: 'utf8' }).trim()); } catch {}
  for (const r of roots) {
    try { return require(require.resolve('playwright', { paths: [r] })); } catch {}
  }
  throw new Error('playwright not found; set NODE_PATH to a node_modules that has it');
}
const { chromium } = loadPlaywright();

// ---------- tiny test harness ----------
let failures = 0, passes = 0;
function check(name, ok, detail) {
  if (ok) { passes++; console.log('  ok   ' + name); }
  else { failures++; console.log('  FAIL ' + name + (detail !== undefined ? '  →  ' + JSON.stringify(detail) : '')); }
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function until(fn, ms = 5000, step = 50) {
  const end = Date.now() + ms;
  for (;;) {
    try { const v = await fn(); if (v) return v; } catch {}
    if (Date.now() > end) return null;
    await sleep(step);
  }
}

function freePort() {
  return new Promise((res, rej) => {
    const s = net.createServer();
    s.listen(0, '127.0.0.1', () => { const p = s.address().port; s.close(() => res(p)); });
    s.on('error', rej);
  });
}

// ---------- test pages ----------
const PNG = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==';

const INDEX = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>SDK test</title><link rel="stylesheet" href="/_/kit.css"></head><body><main><h1>SDK test</h1>
<p><a id="ext" href="https://example.com/x">external link</a> <a id="inner" href="page2.html">page 2</a></p><img id="img" alt=""></main>
<script>
// Grab the shell's channel too, to probe the shell's own path guard.
var rawPort = null;
addEventListener('message', function (e) { if (e.data && e.data.type === 'init' && e.ports[0]) rawPort = e.ports[0]; });
function rawCall(path) {
  return new Promise(function (resolve) {
    var id = 900000 + Math.floor(Math.random() * 1000);
    rawPort.addEventListener('message', function h(e) { if (e.data && e.data.id === id) { rawPort.removeEventListener('message', h); resolve(e.data); } });
    rawPort.postMessage({ bk: 1, type: 'api', id: id, method: 'GET', path: path });
  });
}
const R = window.__results = [];
function rec(name, ok, detail) { R.push({ name, ok: !!ok, detail: detail === undefined ? null : detail }); }
async function t(name, fn) {
  try { const v = await fn(); rec(name, v === true, v === true ? undefined : v === false ? 'returned false' : v); }
  catch (e) { rec(name, false, (e && (e.code + ': ' + e.message)) || String(e)); }
}
async function rejects(name, fn, codes) {
  try { await fn(); rec(name, false, 'did not throw'); }
  catch (e) { rec(name, e instanceof BunkerError && (!codes || codes.includes(e.code)), e && e.code + ': ' + e.message); }
}
// localStorage must work synchronously at load time.
let lsEarly = null;
try { localStorage.setItem('early', 'yes'); lsEarly = localStorage.getItem('early'); } catch (e) { lsEarly = 'threw ' + e.message; }

(async function main() {
  await t('ready', () => bunker.ready.then(() => true));
  await t('stdlib unaffected', () => typeof fetch === 'function' && typeof bunker.db.collection === 'function');
  await t('site info', () => bunker.site.slug === 'sdk-test' && bunker.site.role === 'admin' && bunker.site.id > 0 || bunker.site);
  await t('me', async () => { const me = await bunker.me(); return me.name === 'You (dev)' && me.role === 'admin' && typeof me.id === 'number' || me; });
  await t('me.inGroup', async () => (await bunker.me.inGroup('Dev Team')) === true && (await bunker.me.inGroup('Nope')) === false);

  // db
  const orders = bunker.db.collection('orders');
  let id;
  await t('db.add', async () => { const { id: i, ...d } = await orders.add({ meal: 'Pasta', qty: 2 }); id = i; return d._id === i && d.meal === 'Pasta' && /T.*Z$/.test(d._created) || d; });
  await t('db.get', async () => (await orders.get(id)).qty === 2);
  await t('db.get missing → null', async () => (await orders.get('nope-nope')) === null);
  await t('db.update', async () => { const d = await orders.update(id, { qty: 3 }); return d.qty === 3 && d.meal === 'Pasta'; });
  await t('db.set', async () => { const d = await orders.set(id, { meal: 'Salad' }); return d.meal === 'Salad' && d.qty === undefined; });
  await orders.add({ meal: 'Soup', qty: 1 });
  await orders.add({ meal: 'Stew', qty: 5 });
  await t('db.list', async () => { const p = await orders.list({ limit: 2 }); return p.items.length === 2 && typeof p.next === 'string'; });
  await t('db.list page 2', async () => { const p = await orders.list({ limit: 2 }); const p2 = await orders.list({ limit: 2, after: p.next }); return p2.items.length === 1 && p2.next === null; });
  await t('db.query', async () => { const r = await orders.query({ where: { _author: 'me', qty: { $gte: 1 } }, orderBy: '-qty' }); return r.length === 2 && r[0].meal === 'Stew' && r.items === r; });
  await t('db.count', async () => (await orders.count()) === 3 && (await orders.count({ meal: 'Soup' })) === 1);
  await t('db.export csv', async () => { const c = await orders.export('csv'); return typeof c === 'string' && c.startsWith('_id,') && c.includes('Stew'); });
  await t('db.export json', async () => { const j = await orders.export('json'); return Array.isArray(j) && j.length === 3; });
  await t('db.subscribe', async () => {
    let got = null;
    const off = orders.subscribe((ch) => { if (ch.type === 'added' && ch.doc.meal === 'Live') got = ch; });
    await new Promise((r) => setTimeout(r, 300));
    await orders.add({ meal: 'Live' });
    for (let i = 0; i < 60 && !got; i++) await new Promise((r) => setTimeout(r, 50));
    off();
    return !!got && !!got.doc.id;
  });
  await t('db.delete', async () => { await orders.delete(id); return (await orders.get(id)) === null; });
  await rejects('db.rule is not page-safe', () => orders.rule('private'), ['forbidden']);
  await rejects('db invalid field', () => orders.query({ where: { 'a b': 1 } }), ['invalid']);

  // files
  let file;
  await t('files.upload', async () => {
    const bin = Uint8Array.from(atob('${PNG}'), (c) => c.charCodeAt(0));
    file = await bunker.files.upload(new Blob([bin], { type: 'image/png' }), { folder: 'pics', name: 'dot.png' });
    return file.id && file.type === 'image/png' && file.folder === 'pics' && /~f\\//.test(file.url) || file;
  });
  await t('files url renders in <img>', () => new Promise((res) => {
    const img = document.getElementById('img');
    img.onload = () => res(img.naturalWidth === 1);
    img.onerror = () => res(false);
    img.src = file.url;
  }));
  await t('files.list', async () => { const l = await bunker.files.list({ folder: 'pics' }); return l.length === 1 && l[0].id === file.id && l.items === l || { n: l.length, ids: l.map((x) => x.id), want: file.id, items: l.items === l }; });
  await t('files.read', async () => (await bunker.files.read(file.id)).size === 70);
  await t('files.url(id)', () => bunker.files.url(file.id).includes('/~f/') && bunker.files.url(file) === file.url);
  await rejects('files blocked type', () => bunker.files.upload(new File(['MZ'], 'x.exe')), ['blocked_type']);
  await t('files.delete', async () => { await bunker.files.delete(file.id); return (await bunker.files.list({ folder: 'pics' })).length === 0; });

  // storage
  await t('storage set/get', async () => { await bunker.storage.set('theme', { dark: true }); return (await bunker.storage.get('theme')).dark === true; });
  await t('storage keys', async () => { const k = await bunker.storage.keys(); return k.includes('theme') && !k.some((x) => x.startsWith('ls:')); });
  await t('storage remove', async () => { await bunker.storage.remove('theme'); return (await bunker.storage.get('theme')) === null; });

  // shims
  rec('localStorage works at load', lsEarly === 'yes', lsEarly);
  await t('localStorage write-behind', async () => {
    localStorage.setItem('color', 'teal');
    localStorage.count = '7';
    for (let i = 0; i < 40; i++) { await new Promise((r) => setTimeout(r, 100)); if ((await bunker.storage.keys('ls:')).includes('ls:count')) break; }
    const k = await bunker.storage.keys('ls:');
    return k.includes('ls:color') && k.includes('ls:early') && localStorage.length >= 3 && Object.keys(localStorage).includes('color') || k;
  });
  await t('sessionStorage works', () => { sessionStorage.setItem('a', 1); return sessionStorage.getItem('a') === '1'; });
  await t('document.cookie works', () => { document.cookie = 'k=v; path=/'; return document.cookie.includes('k=v'); });
  rec('WebRTC disabled', typeof RTCPeerConnection === 'undefined' && typeof window.webkitRTCPeerConnection === 'undefined');

  // rooms
  const room = bunker.room('lobby');
  await t('room send/subscribe', async () => {
    let got = null;
    const off = room.subscribe((m) => { if (m.data && m.data.hello) got = m; });
    await new Promise((r) => setTimeout(r, 300));
    await room.send({ hello: 1 });
    for (let i = 0; i < 60 && !got; i++) await new Promise((r) => setTimeout(r, 50));
    off();
    return !!got && got.from.name === 'You (dev)';
  });
  await t('room presence', async () => {
    let seen = null;
    const off = room.onPresence((people) => { if (people.some((p) => p.state && p.state.status === 'here')) seen = people; });
    await new Promise((r) => setTimeout(r, 300));
    const members = await room.presence({ status: 'here' });
    for (let i = 0; i < 60 && !seen; i++) await new Promise((r) => setTimeout(r, 50));
    off();
    return members.length === 1 && !!seen;
  });

  // forwarded services (not configured in this build/instance): must fail as BunkerError
  await rejects('ai.chat → BunkerError', () => bunker.ai.chat('hi'));
  await rejects('ai.stream → BunkerError', async () => { for await (const c of bunker.ai.stream('hi')) void c; });
  await rejects('m365.graph → BunkerError', () => bunker.m365.graph('/me'));
  await rejects('connections.fetch → BunkerError', () => bunker.connections.fetch('hr', { path: '/x' }));

  // shell guard
  await t('shell rejects paths outside the site API', async () => {
    const a = await rawCall('/../../me');
    const b = await rawCall('/%2e%2e/%2e%2e/admin/users');
    const c = await rawCall('//evil.example/x');
    return !a.ok && !b.ok && !c.ok && a.error.code === 'forbidden' || [a, b, c];
  });
  await t('platform ops refused for pages', async () => {
    const r = await rawCall('/../../../api/v1/sites');
    return !r.ok;
  });

  // UI helpers (checked from outside)
  bunker.toast('Hello from the page');
  document.title = 'SDK OK';
  history.pushState({}, '', 'deep/path?x=1#frag');
  window.__done = true;
})();
</script></body></html>`;

const PAGE2 = `<!doctype html><html><head><meta charset="utf-8"><title>Page 2</title></head><body><p id="v"></p>
<script>window.__early = localStorage.getItem('color'); bunker.ready.then(function () { window.__ls = localStorage.getItem('color'); window.__done2 = true; });</script></body></html>`;

const LIVE = `<!doctype html><html><head><meta charset="utf-8"><title>Live</title></head><body>
<script>
window.__events = [];
bunker.db.collection('chat').subscribe(function (c) { __events.push({ ch: 'chat', type: c.type, doc: c.doc }); });
bunker.db.collection('secret').subscribe(function (c) { __events.push({ ch: 'secret', type: c.type, doc: c.doc }); });
bunker.room('lobby').subscribe(function (m) { __events.push({ ch: 'room', data: m.data, from: m.from }); });
bunker.room('lobby').onPresence(function (people, ev) { __events.push({ ch: 'presence', type: ev.type, people: people }); });
bunker.ready.then(function () { window.__live = true; });
</script></body></html>`;

// ---------- run ----------
const dir = mkdtempSync(join(tmpdir(), 'bunker-e2e-'));
const bin = join(dir, 'bunker');
let server, browser;
async function main() {
  console.log('building…');
  execFileSync('go', ['build', '-o', bin, './cmd/bunker'], { cwd: ROOT, stdio: 'inherit', env: { ...process.env, CGO_ENABLED: '0' } });
  const port = await freePort();
  const base = `http://127.0.0.1:${port}`;
  server = spawn(bin, ['dev'], {
    env: { ...process.env, BUNKER_DATA_DIR: join(dir, 'data'), BUNKER_LISTEN: `127.0.0.1:${port}`, BUNKER_URL: base, BUNKER_ADMINS: 'nobody@example.test' },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let log = '';
  server.stdout.on('data', (d) => (log += d));
  server.stderr.on('data', (d) => (log += d));
  const up = await until(() => fetch(base + '/healthz').then((r) => r.ok), 15000, 100);
  if (!up) throw new Error('server did not start:\n' + log);

  const api = async (method, path, body, headers = {}) => {
    const r = await fetch(base + path, { method, headers: { 'X-Bunker': '1', 'Content-Type': 'application/json', ...headers }, body: body && JSON.stringify(body) });
    const t = await r.text();
    if (!r.ok) throw new Error(`${method} ${path}: ${r.status} ${t}`);
    return t ? JSON.parse(t) : null;
  };
  const files = { 'index.html': INDEX, 'page2.html': PAGE2, 'live.html': LIVE };
  const drop = await api('POST', '/api/v1/drop?title=SDK%20Test&slug=sdk-test', { files });
  check('deploy via /api/v1/drop', drop.slug === 'sdk-test', drop);
  await api('POST', '/api/v1/sites/sdk-test/members', { type: 'user', id: 'bob@example.test', role: 'viewer' });
  await api('PUT', '/api/v1/sites/sdk-test/db/secret/_rule', { rule: 'private' });

  browser = await chromium.launch();
  const ctxA = await browser.newContext({ acceptDownloads: true });
  const page = await ctxA.newPage();
  const consoleErrors = [];
  page.on('pageerror', (e) => consoleErrors.push(e.message));
  await page.goto(base + '/s/sdk-test/');
  const frameOf = (p, name) => until(() => p.frames().find((f) => f.url().includes('/~r/') && (!name || f.url().includes(name))), 10000);
  let frame = await frameOf(page);
  check('site frame loaded', !!frame);
  await frame.waitForFunction(() => window.__done === true, null, { timeout: 30000 });
  const results = await frame.evaluate(() => window.__results);
  console.log('\nSDK calls inside the sandboxed frame:');
  for (const r of results) check(r.name, r.ok, r.detail);

  console.log('\nShell:');
  check('toast shown', !!(await until(() => page.locator('.bk-toast', { hasText: 'Hello from the page' }).count(), 3000)));
  check('title synced', !!(await until(async () => (await page.title()) === 'SDK OK', 3000)));
  check('URL bar synced', !!(await until(() => page.url().endsWith('/s/sdk-test/deep/path?x=1#frag'), 3000)), page.url());
  check('kit.css applied', await frame.evaluate(() => /Georgia|Fraunces/.test(getComputedStyle(document.querySelector('h1')).fontFamily)));

  const dl = page.waitForEvent('download', { timeout: 5000 }).catch(() => null);
  await frame.evaluate(() => bunker.download('report.csv', 'a,b\n1,2\n', 'text/csv'));
  const d = await dl;
  check('bunker.download', d && d.suggestedFilename() === 'report.csv', d && d.suggestedFilename());

  await frame.evaluate(() => bunker.open('https://example.com/docs'));
  check('bunker.open asks before leaving', !!(await until(() => page.locator('.bk-dialog', { hasText: 'example.com' }).count(), 3000)));
  await page.locator('.bk-dialog button', { hasText: 'Stay here' }).click();
  await frame.click('#ext');
  check('external link click is routed to the shell', !!(await until(() => page.locator('.bk-dialog', { hasText: 'example.com' }).count(), 3000)));
  const popup = ctxA.waitForEvent('page', { timeout: 3000 }).catch(() => null);
  await page.locator('.bk-dialog button', { hasText: 'Open link' }).click();
  const pop = await popup;
  check('confirmed link opens a new tab', !!pop);
  if (pop) await pop.close();

  // badge
  const btn = page.locator('.bk-badge-btn');
  check('badge shown', (await btn.count()) === 1);
  await btn.focus();
  await page.keyboard.press('Enter');
  check('badge opens with keyboard', (await btn.getAttribute('aria-expanded')) === 'true' && (await page.locator('.bk-panel a', { hasText: 'Edit app' }).count()) === 1);
  await page.keyboard.press('Escape');
  check('badge closes with Escape', (await btn.getAttribute('aria-expanded')) === 'false');

  // localStorage hydration on the next page load
  await page.goto(base + '/s/sdk-test/page2.html');
  frame = await frameOf(page, 'page2');
  await frame.waitForFunction(() => window.__done2 === true, null, { timeout: 10000 });
  check('localStorage hydrated from bunker.storage', (await frame.evaluate(() => window.__ls)) === 'teal', await frame.evaluate(() => [window.__early, window.__ls]));

  console.log('\nRealtime between two people:');
  const ctxB = await browser.newContext();
  const pageB = await ctxB.newPage();
  await pageB.goto(base + '/auth/dev?as=bob@example.test&name=Bob&next=' + encodeURIComponent('/s/sdk-test/live.html'));
  const fB = await frameOf(pageB, 'live');
  await fB.waitForFunction(() => window.__live === true, null, { timeout: 10000 });
  check('viewer has no Edit link', (await pageB.locator('.bk-panel a', { hasText: 'Edit app' }).count()) === 0);
  await page.goto(base + '/s/sdk-test/live.html');
  const fA = await frameOf(page, 'live');
  await fA.waitForFunction(() => window.__live === true, null, { timeout: 10000 });
  await sleep(400); // let both EventSources connect

  await fA.evaluate(() => bunker.db.collection('chat').add({ text: 'hi from A' }));
  const gotChat = await until(() => fB.evaluate(() => __events.some((e) => e.ch === 'chat' && e.type === 'added' && e.doc.text === 'hi from A')), 5000);
  check('B receives A\'s new doc', !!gotChat);
  await fA.evaluate(() => bunker.db.collection('secret').add({ pin: 1234 }));
  await fA.evaluate(() => bunker.room('lobby').send({ wave: true }));
  const gotRoom = await until(() => fB.evaluate(() => __events.some((e) => e.ch === 'room' && e.data && e.data.wave && e.from.id)), 5000);
  check('B receives A\'s room message', !!gotRoom);
  check('private collection: B never sees A\'s doc', !(await fB.evaluate(() => __events.some((e) => e.ch === 'secret'))));
  await fB.evaluate(() => bunker.db.collection('secret').add({ pin: 1 }));
  check('private collection: B sees own doc', !!(await until(() => fB.evaluate(() => __events.some((e) => e.ch === 'secret' && e.doc.pin === 1)), 5000)));
  await fA.evaluate(() => bunker.room('lobby').presence({ typing: true }));
  check('B sees A join presence', !!(await until(() => fB.evaluate(() => __events.some((e) => e.ch === 'presence' && (e.type === 'join' || e.type === 'update') && e.people.some((p) => p.state && p.state.typing))), 5000)));

  // a new version while B has the page open → toast with Reload
  await api('POST', '/api/v1/sites/sdk-test/deploy', { files: { ...files, 'extra.txt': 'v2' } });
  check('new version toast with Reload', !!(await until(() => pageB.locator('.bk-toast', { hasText: 'new version' }).locator('button', { hasText: 'Reload' }).count(), 5000)));

  // draft preview ribbon
  await api('POST', '/api/v1/sites/sdk-test/deploy', { files: { ...files, 'draft.txt': 'd' }, publish: false });
  await page.goto(base + '/s/sdk-test/?draft=1');
  check('draft ribbon', !!(await until(() => page.locator('.bk-ribbon', { hasText: 'Draft preview' }).count(), 3000)));

  check('no uncaught errors in the shell', consoleErrors.length === 0, consoleErrors);
}

main().catch((e) => { failures++; console.error('\nERROR', e && e.stack || e); }).finally(async () => {
  if (browser) await browser.close().catch(() => {});
  if (server) server.kill('SIGTERM');
  await sleep(300);
  if (existsSync(dir)) rmSync(dir, { recursive: true, force: true });
  console.log(`\n${passes} passed, ${failures} failed`);
  process.exit(failures ? 1 : 0);
});
