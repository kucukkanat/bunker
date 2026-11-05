// End-to-end UI test for the Bunker dashboard.
//
//   go build -o /tmp/bkui ./cmd/bunker
//   NODE_PATH=/home/user/bunker/website/node_modules node test/e2e/ui.test.mjs
//
// Env: BUNKER_BIN (default /tmp/bkui), SHOTS (screenshot folder, optional),
// PORT (default 18090; PORT+1 is used for the first-run /setup server).
import { createRequire } from 'node:module';
import { spawn } from 'node:child_process';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import assert from 'node:assert/strict';

function loadPlaywright() {
  const tries = [import.meta.url, ...(process.env.NODE_PATH || '').split(':').filter(Boolean).map((p) => join(p, 'x.js')), '/home/user/bunker/website/package.json', '/opt/node-tools/node_modules/x.js'];
  for (const t of tries) { try { return createRequire(t)('playwright'); } catch {} }
  throw new Error('playwright not found: set NODE_PATH to a node_modules folder that has it');
}
const { chromium } = loadPlaywright();

const BIN = process.env.BUNKER_BIN || '/tmp/bkui';
const PORT = +(process.env.PORT || 18090);
const BASE = `http://127.0.0.1:${PORT}`;
const BASE2 = `http://127.0.0.1:${PORT + 1}`;
const SHOTS = process.env.SHOTS || '';
if (SHOTS) mkdirSync(SHOTS, { recursive: true });
const tmp = mkdtempSync(join(tmpdir(), 'bk-e2e-'));
const procs = [];

function startServer(port, dev) {
  const dir = join(tmp, 'data' + port);
  mkdirSync(dir);
  const env = { ...process.env, BUNKER_DATA_DIR: dir, BUNKER_LISTEN: '127.0.0.1:' + port };
  delete env.BUNKER_DEV;
  const p = spawn(BIN, dev ? ['dev'] : ['serve'], { env, stdio: ['ignore', 'ignore', 'pipe'] });
  let log = '';
  p.stderr.on('data', (d) => { log += d; });
  procs.push(p);
  return { p, log: () => log };
}
async function waitUp(base) {
  for (let i = 0; i < 100; i++) {
    try { const r = await fetch(base + '/api/v1/setup'); if (r.ok) return; } catch {}
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error('server did not start: ' + base);
}

const errors = [];
let step = 0;
process.on('unhandledRejection', () => {});
async function check(name, fn) {
  step++;
  const t = Date.now();
  try { await fn(); console.log(`ok ${step} - ${name} (${Date.now() - t}ms)`); }
  catch (e) {
    console.log(`not ok ${step} - ${name}\n  ${String(e.message || e).split('\n').slice(0, 4).join('\n  ')}`);
    if (errors.length) console.log('  console errors:\n  ' + errors.join('\n  '));
    for (const p of current) await p.screenshot({ path: join(SHOTS || tmpdir(), 'FAIL-' + step + '.png') }).catch(() => {});
    process.exitCode = 1;
    throw e;
  }
}
async function shot(page, name) {
  if (!SHOTS) return;
  await page.waitForTimeout(350); // let entrance animations settle
  await page.screenshot({ path: join(SHOTS, name + '.png') });
}
const current = new Set();
function watch(page, label) {
  current.add(page);
  page.on('close', () => current.delete(page));
  page.on('console', (m) => {
    if (m.type() !== 'error') return;
    const t = m.text();
    // Optional APIs that aren't installed answer 404; the UI hides those features.
    if (/Failed to load resource: the server responded with a status of 404/.test(t)) return;
    errors.push(`[${label}] ${t}`);
  });
  page.on('pageerror', (e) => errors.push(`[${label}] pageerror: ${e.message}`));
}

const srv = startServer(PORT, true);
const srv2 = startServer(PORT + 1, false);
let browser;
try {
  await Promise.all([waitUp(BASE), waitUp(BASE2)]);
  browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1360, height: 860 }, deviceScaleFactor: 1 });
  await ctx.addInitScript(() => { try { localStorage.setItem('bk.welcomed', '1'); } catch {} });
  const page = await ctx.newPage();
  watch(page, 'desktop');

  // A second person, so sharing has someone to find.
  const other = await browser.newContext();
  await (await other.newPage()).goto(BASE + '/auth/dev?as=ada@example.com&name=Ada%20Lovelace&groups=Finance');
  await other.close();

  await check('home loads with greeting, drop zone and describe box', async () => {
    await page.goto(BASE + '/');
    await page.getByRole('heading', { level: 1 }).waitFor();
    assert.match(await page.locator('h1').innerText(), /Good (morning|afternoon|evening)/);
    await page.getByTestId('dropzone').waitFor();
    await page.getByLabel('Describe the app you want').waitFor();
    await page.getByText('Your sites will live here').waitFor();
    await shot(page, 'home-empty');
  });

  let slug;
  await check('dropping a folder creates a site and opens the workspace', async () => {
    const dir = join(tmp, 'Team Lunch');
    mkdirSync(join(dir, 'css'), { recursive: true });
    writeFileSync(join(dir, 'index.html'), '<!doctype html><html><head><title>Team Lunch</title><link rel="stylesheet" href="css/site.css"></head><body><h1>Team lunch orders</h1><p>Pick your lunch for Friday.</p></body></html>');
    writeFileSync(join(dir, 'css', 'site.css'), 'body{font-family:system-ui;background:#f7f4ec;padding:40px}h1{color:#1f3a2e}');
    await page.getByTestId('pick-folder').setInputFiles(dir);
    await page.waitForURL(/\/sites\/[a-z0-9-]+/);
    slug = new URL(page.url()).pathname.split('/')[2];
    await page.locator('.ws-top h1').getByText('Team Lunch').waitFor();
    await page.getByText('Live · version 1').waitFor();
    await page.frameLocator('.pv-frame iframe').frameLocator('#bk-frame').getByText('Team lunch orders').waitFor();
    await shot(page, 'workspace-build');
  });

  await check('a synthetic drop of files on Home also works', async () => {
    await page.goto(BASE + '/');
    await page.getByTestId('dropzone').waitFor();
    await page.evaluate(() => {
      const dt = new DataTransfer();
      dt.items.add(new File(['<!doctype html><title>Quick Note</title><h1>Note</h1>'], 'note.html', { type: 'text/html' }));
      const z = document.querySelector('[data-testid=dropzone]');
      z.dispatchEvent(new DragEvent('dragenter', { dataTransfer: dt, bubbles: true }));
      z.dispatchEvent(new DragEvent('drop', { dataTransfer: dt, bubbles: true, cancelable: true }));
    });
    await page.waitForURL(/\/sites\/quick-note/);
    await page.goto(BASE + '/');
    await page.getByRole('heading', { name: 'My sites' }).waitFor();
    await page.locator('.site-card').first().waitFor();
    await shot(page, 'home');
  });

  await check('search filters the home page ("/" focuses it)', async () => {
    await page.locator('body').click({ position: { x: 5, y: 400 } });
    await page.keyboard.press('/');
    await page.keyboard.type('lunch');
    await page.getByRole('heading', { name: 'Results' }).waitFor();
    assert.equal(await page.locator('.site-card').count(), 1);
  });

  await check('files tab: edit and save creates a draft', async () => {
    await page.goto(BASE + '/sites/' + slug + '?tab=files');
    const ta = page.getByLabel('File contents');
    await ta.waitFor();
    await ta.fill('<!doctype html><html><head><title>Team Lunch</title></head><body><h1>Lunch v2</h1></body></html>');
    await page.getByRole('button', { name: 'Save' }).click();
    await page.getByText('Draft — not published yet').waitFor();
    await page.frameLocator('.pv-frame iframe').frameLocator('#bk-frame').getByText('Lunch v2').waitFor();
    await shot(page, 'workspace-files');
  });

  await check('publish makes the draft live', async () => {
    await page.locator('.ws-top').getByRole('button', { name: 'Publish' }).click();
    await page.getByText('Live · version 2').waitFor();
  });

  await check('share dialog finds a person and adds them', async () => {
    await page.locator('.ws-top').getByRole('button', { name: 'Share' }).click();
    const dlg = page.getByRole('dialog');
    await dlg.getByLabel('Add people or groups').fill('ada');
    await page.locator('#share-ta .option').filter({ hasText: 'Ada Lovelace' }).click();
    await dlg.locator('.person').filter({ hasText: 'Ada Lovelace' }).waitFor();
    await shot(page, 'share');
    await dlg.getByRole('button', { name: 'Done' }).click();
    await dlg.waitFor({ state: 'detached' });
  });

  await check('settings tab renames the site', async () => {
    await page.getByRole('tab', { name: 'Settings' }).click();
    const name = page.locator('#set-title');
    await name.waitFor();
    await name.fill('Friday Lunch');
    await page.getByRole('button', { name: 'Save changes' }).click();
    await page.locator('.ws-top h1').getByText('Friday Lunch').waitFor();
    await shot(page, 'workspace-settings');
  });

  await check('versions tab restores version 1', async () => {
    await page.getByRole('tab', { name: 'Versions' }).click();
    const v1 = page.locator('.ver').filter({ hasText: 'Version 1 ·' });
    await v1.getByRole('button', { name: 'What changed' }).count(); // v1 has no diff button
    await page.locator('.ver').filter({ hasText: 'Version 2 ·' }).getByRole('button', { name: 'What changed' }).click();
    await page.locator('.diff').getByText('index.html').first().waitFor();
    await v1.getByRole('button', { name: 'Preview' }).click();
    await page.getByText('Previewing version 1').waitFor();
    await shot(page, 'workspace-versions');
    await v1.getByRole('button', { name: 'Restore' }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Restore version 1' }).click();
    await page.getByText('Live · version 3').waitFor();
    await page.frameLocator('.pv-frame iframe').frameLocator('#bk-frame').getByText('Team lunch orders').waitFor();
  });

  await check('data tab degrades gracefully', async () => {
    await page.getByRole('tab', { name: 'Data' }).click();
    await page.locator('.tab-panel .empty').waitFor();
  });

  await check('device size toggles resize the preview', async () => {
    await page.getByRole('button', { name: 'Phone size' }).click();
    await page.waitForTimeout(450);
    const w = await page.locator('.pv-frame').evaluate((e) => e.getBoundingClientRect().width);
    assert.ok(w <= 400, 'phone preview is narrow: ' + w);
    await page.getByRole('tab', { name: 'Build' }).click();
    await shot(page, 'workspace-phone');
    await page.getByRole('button', { name: 'Desktop size' }).click();
  });

  await check('access tokens: create shows the token once', async () => {
    await page.goto(BASE + '/settings/tokens');
    await page.getByLabel('Token name').fill('Laptop CLI');
    await page.getByRole('button', { name: 'Create token' }).click();
    const tok = page.getByTestId('new-token');
    await tok.waitFor();
    assert.match(await tok.innerText(), /^bk_/);
    await page.locator('.list .item').filter({ hasText: 'Laptop CLI' }).waitFor();
    await shot(page, 'settings-tokens');
  });

  await check('device approval page', async () => {
    const r = await (await fetch(BASE + '/api/v1/auth/device', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ client: 'cli' }) })).json();
    await page.goto(BASE + '/device?code=' + r.userCode);
    await page.getByRole('heading', { name: /wants to sign in as you/ }).waitFor();
    await shot(page, 'device');
    await page.getByRole('button', { name: 'Approve' }).click();
    await page.getByRole('heading', { name: 'You’re signed in' }).waitFor();
    const t = await (await fetch(BASE + '/api/v1/auth/device/token', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ deviceCode: r.deviceCode }) })).json();
    assert.match(t.token, /^bk_/);
  });

  await check('command palette and shortcuts', async () => {
    await page.goto(BASE + '/');
    await page.locator('.site-card').first().waitFor();
    await page.keyboard.press('Control+k');
    await page.getByLabel('Search sites and actions').fill('friday');
    await page.locator('.palette .option').first().waitFor();
    await shot(page, 'palette');
    await page.keyboard.press('Enter');
    await page.waitForURL(/\/sites\//);
    await page.goto(BASE + '/');
    await page.locator('.site-card').first().waitFor();
    await page.keyboard.press('?');
    await page.getByRole('heading', { name: 'Keyboard shortcuts' }).waitFor();
    await page.keyboard.press('Escape');
  });

  await check('trash: delete and restore', async () => {
    await page.goto(BASE + '/sites/quick-note?tab=settings');
    await page.getByRole('button', { name: 'Move to trash' }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Move to trash' }).click();
    await page.waitForURL(BASE + '/');
    await page.goto(BASE + '/trash');
    await page.locator('.list .item').filter({ hasText: 'Quick Note' }).waitFor();
    await shot(page, 'trash');
    await page.getByRole('button', { name: 'Restore' }).click();
    await page.getByText('The trash is empty').waitFor();
  });

  const screens = [
    ['new', '/new', 'Make a new site'], ['templates', '/templates', 'Templates'], ['settings', '/settings', 'Settings'],
    ['connect', '/settings/connect', 'Connect your tools'], ['admin', '/admin', 'Admin'], ['admin-people', '/admin/people', 'Admin'],
    ['admin-ai', '/admin/ai', 'Admin'], ['admin-signin', '/admin/sign-in', 'Admin'], ['admin-audit', '/admin/audit', 'Admin'],
    ['welcome', '/welcome', 'Welcome to Bunker'], ['signed-out', '/auth/signed-out', 'You’re signed out'],
  ];
  await check('every screen renders', async () => {
    for (const [n, p, hd] of screens) {
      await page.goto(BASE + p);
      await page.getByRole('heading', { name: hd, exact: true }).first().waitFor();
      await page.waitForLoadState('networkidle');
      await shot(page, n);
    }
  });

  await check('first-run setup (no dev mode): built-in account', async () => {
    const p2 = await ctx.newPage();
    watch(p2, 'setup');
    await p2.goto(BASE2 + '/');
    await p2.waitForURL(/\/setup$/);
    await p2.getByRole('heading', { name: 'Welcome! Let’s set up Bunker' }).waitFor();
    await shot(p2, 'setup-1');
    await p2.getByRole('button', { name: /Company sign-in/ }).click();
    await p2.getByRole('button', { name: /Microsoft Entra ID/ }).click();
    await p2.getByRole('button', { name: /Set up automatically with Microsoft/ }).waitFor();
    await p2.getByText('Do it by hand instead').click();
    await p2.getByTestId('redirect-uri').waitFor();
    assert.match(await p2.getByTestId('redirect-uri').innerText(), /\/auth\/callback$/);
    await shot(p2, 'setup-entra');
    await p2.getByRole('button', { name: 'Back' }).click();
    await p2.getByRole('button', { name: 'Back' }).click();
    await p2.getByRole('button', { name: /Just me or my team/ }).click();
    await p2.getByLabel('Your email').fill('owner@example.com');
    await p2.getByLabel('Your name').fill('Olive Owner');
    await p2.getByLabel('Password', { exact: true }).fill('a long enough password');
    await p2.getByLabel('Type the password again').fill('a long enough password');
    await shot(p2, 'setup-local');
    await p2.getByRole('button', { name: 'Create my account' }).click();
    await p2.getByRole('heading', { name: 'You’re all set' }).waitFor();
    await shot(p2, 'setup-done');
    // Signed in right away: no second password prompt.
    await p2.getByRole('link', { name: /Open Bunker/ }).click();
    await p2.waitForURL(/\/welcome$/);
    await p2.getByRole('heading', { name: 'Welcome to Bunker' }).waitFor();
    await p2.close();
  });

  // Phone-sized screenshots.
  const m = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 1, isMobile: true, hasTouch: true });
  await m.addInitScript(() => { try { localStorage.setItem('bk.welcomed', '1'); } catch {} });
  const mp = await m.newPage();
  watch(mp, 'mobile');
  await check('phone layout has no horizontal scroll', async () => {
    for (const [n, p] of [['home', '/'], ['new', '/new'], ['workspace', '/sites/' + slug], ['settings', '/settings/tokens'], ['admin', '/admin/people'], ['audit', '/admin/audit'], ['templates', '/templates'], ['connect', '/settings/connect']]) {
      await mp.goto(BASE + p);
      await mp.waitForLoadState('networkidle');
      await mp.waitForTimeout(300);
      // On phones the layout viewport grows to fit wide content, so compare with the device width too.
      const over = await mp.evaluate(() => Math.max(document.documentElement.scrollWidth, innerWidth) - 390);
      assert.ok(over <= 1, `${p} overflows by ${over}px`);
      await shot(mp, 'm-' + n);
    }
    await mp.goto(BASE + '/sites/' + slug);
    await mp.getByRole('button', { name: 'Preview', exact: true }).click();
    await shot(mp, 'm-workspace-preview');
    await mp.getByRole('button', { name: 'Edit', exact: true }).click();
    await mp.locator('.ws-top').getByRole('button', { name: 'Share' }).click();
    await shot(mp, 'm-share');
    await mp.goto(BASE2 + '/setup');
    await mp.getByRole('heading').first().waitFor();
    await shot(mp, 'm-setup');
  });

  const dk = await browser.newContext({ viewport: { width: 1360, height: 860 }, deviceScaleFactor: 1, colorScheme: 'dark' });
  await dk.addInitScript(() => { try { localStorage.setItem('bk.welcomed', '1'); } catch {} });
  const dp = await dk.newPage();
  watch(dp, 'dark');
  await check('dark mode renders', async () => {
    for (const [n, p] of [['home', '/'], ['workspace', '/sites/' + slug + '?tab=versions'], ['setup', BASE2 + '/setup']]) {
      await dp.goto(p.startsWith('http') ? p : BASE + p);
      await dp.waitForLoadState('networkidle');
      const bg = await dp.evaluate(() => getComputedStyle(document.body).backgroundColor);
      assert.equal(bg, 'rgb(18, 23, 20)');
      await shot(dp, 'dark-' + n);
    }
  });

  await check('no console errors or CSP violations', async () => {
    assert.deepEqual(errors, []);
  });
} catch { process.exitCode = 1; } finally {
  await browser?.close();
  for (const p of procs) p.kill();
  rmSync(tmp, { recursive: true, force: true });
}
