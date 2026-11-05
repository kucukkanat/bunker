// Accessibility check for the Bunker dashboard, sign-in pages, site shell
// and generated app pages. Runs axe-core (WCAG 2.0/2.1/2.2 A + AA rules)
// on every screen in light mode, dark mode and at phone width, plus a few
// keyboard checks (skip link, dialog focus trap / Escape / focus return).
// Fails on serious or critical axe violations.
//
//   go build -o /tmp/bka11y ./cmd/bunker
//   npm i --prefix /tmp/axe axe-core@4          # once; or have axe-core on NODE_PATH
//   NODE_PATH=/opt/node-tools/node_modules AXE_PATH=/tmp/axe/node_modules/axe-core \
//     node test/e2e/a11y.test.mjs
//
// Env: BUNKER_BIN (default /tmp/bka11y), PORT (default 18431; PORT+1 serves
// a non-dev instance for /setup, /auth/login and invite links), AXE_PATH
// (folder or axe.min.js), A11Y_VERBOSE=1 to also list moderate/minor issues.
import { createRequire } from 'node:module';
import { spawn } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, existsSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const roots = [import.meta.url, ...(process.env.NODE_PATH || '').split(':').filter(Boolean).map((p) => join(p, 'x.js')),
  '/home/user/bunker/website/package.json', '/opt/node-tools/node_modules/x.js'];
function load(name) {
  for (const t of roots) { try { return createRequire(t)(name); } catch {} }
  return null;
}
function resolve(name) {
  for (const t of roots) { try { return createRequire(t).resolve(name); } catch {} }
  return null;
}
const pw = load('playwright');
if (!pw) throw new Error('playwright not found: set NODE_PATH to a node_modules folder that has it');
let axePath = process.env.AXE_PATH || '';
if (axePath && existsSync(axePath) && statSync(axePath).isDirectory()) axePath = join(axePath, 'axe.min.js');
if (!axePath) axePath = resolve('axe-core/axe.min.js') || '';
if (!axePath || !existsSync(axePath)) throw new Error('axe-core not found: npm i axe-core@4 somewhere and set AXE_PATH (or NODE_PATH)');
const AXE = readFileSync(axePath, 'utf8');

const BIN = process.env.BUNKER_BIN || '/tmp/bka11y';
const PORT = +(process.env.PORT || 18431);
const BASE = `http://127.0.0.1:${PORT}`;
const BASE2 = `http://127.0.0.1:${PORT + 1}`;
const VERBOSE = !!process.env.A11Y_VERBOSE;
const tmp = mkdtempSync(join(tmpdir(), 'bk-a11y-'));
const procs = [];

function startServer(port, dev) {
  const dir = join(tmp, 'data' + port);
  mkdirSync(dir);
  const env = { ...process.env, BUNKER_DATA_DIR: dir, BUNKER_LISTEN: '127.0.0.1:' + port };
  delete env.BUNKER_DEV;
  const p = spawn(BIN, dev ? ['dev'] : ['serve'], { env, stdio: ['ignore', 'ignore', 'ignore'] });
  procs.push(p);
}
async function waitUp(base) {
  for (let i = 0; i < 100; i++) {
    try { const r = await fetch(base + '/api/v1/setup'); if (r.ok) return; } catch {}
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error('server did not start: ' + base);
}

// ---------- axe ----------
const found = new Map(); // rule|target -> {impact, rule, help, where:Set, target, html}
let audits = 0;
async function axeFrame(frame, where) {
  audits++;
  const res = await frame.evaluate(async (src) => {
    if (!window.axe) (0, eval)(src);
    const r = await window.axe.run(document, {
      iframes: false,
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa', 'best-practice'] },
      rules: { 'region': { enabled: false } },
      resultTypes: ['violations'],
    });
    return r.violations.map((v) => ({ id: v.id, impact: v.impact, help: v.help, nodes: v.nodes.map((n) => ({ target: n.target.join(' '), html: n.html.slice(0, 160), summary: n.failureSummary })) }));
  }, AXE);
  for (const v of res) {
    for (const n of v.nodes) {
      const k = v.id + '|' + n.target;
      const e = found.get(k) || { impact: v.impact, rule: v.id, help: v.help, target: n.target, html: n.html, summary: n.summary, where: new Set() };
      e.where.add(where);
      found.set(k, e);
    }
  }
}
// Audit a page and every frame in it (site shells hold a sandboxed frame).
async function audit(page, where) {
  await page.waitForLoadState('networkidle').catch(() => {});
  await page.waitForTimeout(400); // entrance animations
  for (const f of page.frames()) {
    if (f.url() === 'about:blank' || !f.url()) continue;
    const label = f === page.mainFrame() ? where : where + ' > frame ' + new URL(f.url()).pathname.replace(/~r\/[^/]+\//, '');
    try { await axeFrame(f, label); } catch (e) { if (f === page.mainFrame()) throw e; }
  }
}

// ---------- keyboard checks ----------
const failures = [];
function fail(msg) { failures.push(msg); }
async function activeInfo(page) {
  return page.evaluate(() => {
    const a = document.activeElement;
    return a ? { tag: a.tagName, text: (a.getAttribute('aria-label') || a.textContent || '').trim().slice(0, 40), inDialog: !!a.closest('[role=dialog],[role=alertdialog],dialog'), id: a.id, cls: a.className } : null;
  });
}

const BASES = [BASE, BASE2];
startServer(PORT, true);
startServer(PORT + 1, false);
let browser;
try {
  await Promise.all(BASES.map(waitUp));
  browser = await pw.chromium.launch();
  const init = () => { try { localStorage.setItem('bk.welcomed', '1'); } catch {} };
  const light = await browser.newContext({ viewport: { width: 1360, height: 860 } });
  await light.addInitScript(init);
  const page = await light.newPage();
  await page.goto(BASE + '/');
  await page.getByRole('heading', { level: 1 }).waitFor();

  // ---------- seed ----------
  const api = (method, path, body) => page.evaluate(async ([m, p, b]) => {
    const r = await fetch(p, { method: m, headers: { 'Content-Type': 'application/json', 'X-Bunker': '1' }, body: b ? JSON.stringify(b) : undefined });
    return r.json().catch(() => ({}));
  }, [method, path, body]);
  const px = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==';
  const seeds = {
    html: { 'index.html': '<!doctype html><html lang="en"><head><title>Team Lunch</title></head><body><main><h1>Team lunch</h1><p>Pick your lunch.</p></main></body></html>' },
    markdown: { 'notes.md': '# Quarterly planning\n\nSome **notes** with a [link](https://example.com).\n\n## Goals\n\n- One\n- Two\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n```js\nconst x = 1;\n```\n' },
    csv: { 'sales.csv': 'Region,Amount,Date\nNorth,10,2026-01-02\nSouth,20,2026-02-03\nEast,30,2026-03-04\n' },
    gallery: { 'a.png': { base64: px }, 'b.png': { base64: px }, 'c.png': { base64: px } },
    json: { 'data.json': '{"name":"Ada","tags":["a","b"],"nested":{"x":1}}' },
    text: { 'readme.txt': 'Plain text file.\nSecond line.' },
  };
  const slugs = {};
  for (const [k, files] of Object.entries(seeds)) {
    const r = await api('POST', '/api/v1/drop', { files, title: 'A11y ' + k });
    slugs[k] = r.slug || r.site?.slug;
    if (!slugs[k]) throw new Error('seed failed for ' + k + ': ' + JSON.stringify(r).slice(0, 200));
  }
  const tpls = await api('GET', '/api/v1/sites?filter=templates');
  const tplList = (tpls.sites || tpls.items || tpls || []).filter((s) => s && s.slug);
  if (tplList.length) {
    const r = await api('POST', '/api/v1/sites', { title: 'From template', template: tplList[0].slug });
    if (r.slug) slugs.fromTemplate = r.slug;
  }
  // A second person to share with.
  const other = await browser.newContext();
  await (await other.newPage()).goto(BASE + '/auth/dev?as=ada@example.com&name=Ada%20Lovelace&groups=Finance');
  await other.close();
  const dev = await api('POST', '/api/v1/auth/device', { client: 'cli' });

  const s = slugs.html;
  const screens = [
    ['home', '/'], ['new', '/new'], ['templates', '/templates'], ['trash', '/trash'],
    ['settings', '/settings'], ['settings-tokens', '/settings/tokens'], ['settings-connect', '/settings/connect'],
    ['admin', '/admin'], ['admin-people', '/admin/people'], ['admin-ai', '/admin/ai'], ['admin-sign-in', '/admin/sign-in'], ['admin-audit', '/admin/audit'], ['admin-functions', '/admin/functions'],
    ['welcome', '/welcome'], ['signed-out', '/auth/signed-out'], ['not-found', '/sites/nope-nothing-here'], ['spa-404', '/nothing/here'],
    ['device', '/device?code=' + (dev.userCode || 'ABCD-EFGH')],
    ...['build', 'files', 'data', 'functions', 'versions', 'settings'].map((t) => ['ws-' + t, `/sites/${s}?tab=${t}`]),
  ];
  const appSites = Object.entries(slugs).filter(([k]) => k !== 'html').map(([k, v]) => ['site-' + k, `/s/${v}/`]);
  const tplSites = tplList.map((t) => ['template-' + t.slug, `/s/${t.slug}/`]);

  async function runScreens(pg, mode, list) {
    for (const [name, path] of list) {
      await pg.goto((path.startsWith('http') ? '' : BASE) + path);
      await audit(pg, `${name} [${mode}]`);
    }
  }

  // Light, full set incl. every site/app page
  await runScreens(page, 'light', [...screens, ...appSites, ...tplSites, ['site-html', `/s/${s}/`]]);

  // Dialogs and overlays (light)
  async function dialogs(pg, mode) {
    await pg.goto(BASE + `/sites/${s}`);
    await pg.locator('.ws-top').getByRole('button', { name: 'Share' }).click();
    await pg.getByRole('dialog').waitFor();
    await audit(pg, `share-dialog [${mode}]`);
    await pg.keyboard.press('Escape');
    await pg.goto(BASE + '/');
    await pg.locator('.site-card').first().waitFor();
    await pg.keyboard.press('Control+k');
    await pg.getByLabel('Search sites and actions').fill('a');
    await audit(pg, `palette [${mode}]`);
    await pg.keyboard.press('Escape');
    await pg.goto(BASE + '/');
    await pg.locator('.site-card').first().waitFor();
    await pg.keyboard.press('?');
    await pg.getByRole('heading', { name: 'Keyboard shortcuts' }).waitFor();
    await audit(pg, `shortcuts [${mode}]`);
    await pg.keyboard.press('Escape');
    await pg.goto(BASE + `/sites/${s}?tab=data`);
    const nh = pg.getByRole('button', { name: 'New webhook' });
    if (await nh.count()) { await nh.click(); await pg.getByRole('dialog').waitFor(); await audit(pg, `webhook-dialog [${mode}]`); await pg.keyboard.press('Escape'); }
    await pg.goto(BASE + `/sites/${s}?tab=settings`);
    await pg.getByRole('button', { name: 'Move to trash' }).click();
    await pg.getByRole('dialog').waitFor();
    await audit(pg, `confirm-dialog [${mode}]`);
    await pg.keyboard.press('Escape');
  }
  await dialogs(page, 'light');

  // ---------- keyboard: skip link, dialog trap, Escape, focus return ----------
  await page.goto(BASE + '/');
  await page.locator('.site-card').first().waitFor();
  await page.keyboard.press('Tab');
  const first = await activeInfo(page);
  if (!first || !/skip/i.test(first.text)) fail('first Tab stop on Home is not a skip link (got ' + JSON.stringify(first) + ')');
  else {
    await page.keyboard.press('Enter');
    const after = await page.evaluate(() => document.activeElement?.closest('main,#main,[role=main]') != null || document.activeElement?.id === 'main');
    if (!after) fail('skip link does not move focus to the main content');
  }
  await page.goto(BASE + `/sites/${s}`);
  const shareBtn = page.locator('.ws-top').getByRole('button', { name: 'Share' });
  await shareBtn.focus();
  await page.keyboard.press('Enter');
  await page.getByRole('dialog').waitFor();
  await page.waitForTimeout(100);
  if (!(await activeInfo(page))?.inDialog) fail('opening the share dialog does not move focus into it');
  for (let i = 0; i < 25; i++) {
    await page.keyboard.press('Tab');
    if (!(await activeInfo(page))?.inDialog) { fail('Tab leaves the share dialog (focus not trapped)'); break; }
  }
  await page.keyboard.press('Shift+Tab');
  if (!(await activeInfo(page))?.inDialog) fail('Shift+Tab leaves the share dialog');
  await page.keyboard.press('Escape');
  await page.waitForTimeout(250);
  if (await page.getByRole('dialog').count()) fail('Escape does not close the share dialog');
  const back = await activeInfo(page);
  if (!back || !/share/i.test(back.text)) fail('focus does not return to the Share button after closing (got ' + JSON.stringify(back) + ')');

  // Focus visible: every tabbable element on Home shows an outline or box-shadow when focused.
  await page.goto(BASE + '/');
  await page.locator('.site-card').first().waitFor();
  const noRing = [];
  for (let i = 0; i < 40; i++) {
    await page.keyboard.press('Tab');
    const r = await page.evaluate(() => {
      const a = document.activeElement;
      if (!a || a === document.body) return null;
      const cs = getComputedStyle(a);
      const ring = (cs.outlineStyle !== 'none' && parseFloat(cs.outlineWidth) > 0) || (cs.boxShadow && cs.boxShadow !== 'none');
      return ring ? '' : (a.tagName + '.' + a.className + ' ' + (a.getAttribute('aria-label') || a.textContent || '').trim().slice(0, 30));
    });
    if (r) noRing.push(r);
  }
  if (noRing.length) fail('no visible focus indicator on: ' + [...new Set(noRing)].join(' | '));

  // ---------- dark ----------
  const dark = await browser.newContext({ viewport: { width: 1360, height: 860 }, colorScheme: 'dark' });
  await dark.addInitScript(init);
  const dp = await dark.newPage();
  await runScreens(dp, 'dark', [...screens, ...appSites, ...tplSites.slice(0, 3)]);
  await dialogs(dp, 'dark');

  // ---------- phone ----------
  const phone = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
  await phone.addInitScript(init);
  const mp = await phone.newPage();
  await runScreens(mp, '390px', [...screens.filter(([n]) => !n.startsWith('admin-') || n === 'admin-people'), ...appSites.slice(0, 3)]);

  // ---------- 200% zoom: a 1280x860 window at 200% lays out at 640x430 CSS px ----------
  const zoom = await browser.newContext({ viewport: { width: 640, height: 430 }, deviceScaleFactor: 2 });
  await zoom.addInitScript(init);
  const zp = await zoom.newPage();
  for (const [name, path] of screens) {
    await zp.goto(BASE + path);
    await zp.waitForLoadState('networkidle').catch(() => {});
    await zp.waitForTimeout(200);
    const over = await zp.evaluate(() => document.documentElement.scrollWidth - innerWidth);
    if (over > 1) fail(`200% zoom: ${name} scrolls sideways by ${over}px`);
  }
  await zoom.close();

  // ---------- non-dev instance: setup, sign-in, invite ----------
  for (const [ctxName, opts] of [['light', {}], ['dark', { colorScheme: 'dark' }], ['390px', { viewport: { width: 390, height: 844 }, isMobile: true }]]) {
    const c = await browser.newContext(opts);
    const p = await c.newPage();
    await p.goto(BASE2 + '/setup');
    await p.getByRole('heading').first().waitFor();
    await audit(p, `setup-1 [${ctxName}]`);
    if (ctxName === 'light') {
      await p.getByRole('button', { name: /Company sign-in/ }).click();
      await audit(p, `setup-company [${ctxName}]`);
      await p.getByRole('button', { name: /Microsoft Entra ID/ }).click();
      await p.getByText('Do it by hand instead').click();
      await audit(p, `setup-entra [${ctxName}]`);
      await p.getByRole('button', { name: 'Back' }).click();
      await p.getByRole('button', { name: 'Back' }).click();
      await p.getByRole('button', { name: /Just me or my team/ }).click();
      await audit(p, `setup-local [${ctxName}]`);
      // Error association: a mismatched password shows an error tied to the field.
      await p.getByLabel('Your email').fill('owner@example.com');
      await p.getByLabel('Your name').fill('Olive Owner');
      await p.getByLabel('Password', { exact: true }).fill('a long enough password');
      await p.getByLabel('Type the password again').fill('a different password!');
      await p.getByRole('button', { name: 'Create my account' }).click();
      await p.waitForTimeout(300);
      await audit(p, `setup-local-error [${ctxName}]`);
      const errOk = await p.evaluate(() => {
        const err = document.querySelector('[role=alert], .err, .error, [aria-live=assertive]');
        const inv = document.querySelector('[aria-invalid=true]');
        return { err: !!err && !!err.textContent.trim(), inv: !!inv, desc: !!inv && !!inv.getAttribute('aria-describedby') };
      });
      if (!errOk.err) fail('setup: a form error is not announced (no role=alert / live region)');
      if (!errOk.inv) fail('setup: the field in error has no aria-invalid');
      await p.getByLabel('Type the password again').fill('a long enough password');
      await p.getByRole('button', { name: 'Create my account' }).click();
      await p.getByRole('heading', { name: 'You’re all set' }).waitFor();
      await audit(p, `setup-done [${ctxName}]`);
      const inv = await p.evaluate(async () => {
        const r = await fetch('/api/v1/admin/invites', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Bunker': '1' }, body: JSON.stringify({ email: 'friend@example.com' }) });
        return r.json();
      });
      globalThis.inviteLink = inv.link;
    }
    await c.close();
  }
  for (const [ctxName, opts] of [['light', {}], ['dark', { colorScheme: 'dark' }], ['390px', { viewport: { width: 390, height: 844 }, isMobile: true }]]) {
    const c = await browser.newContext(opts);
    const p = await c.newPage();
    await p.goto(BASE2 + '/auth/login');
    await audit(p, `sign-in [${ctxName}]`);
    await p.locator('input[name=email]').fill('owner@example.com');
    await p.locator('input[name=password]').fill('wrong password here');
    await p.locator('button[type=submit]').click();
    await p.waitForLoadState();
    await audit(p, `sign-in-error [${ctxName}]`);
    if (globalThis.inviteLink) {
      const u = new URL(globalThis.inviteLink, BASE2);
      await p.goto(BASE2 + u.pathname + u.search);
      await audit(p, `invite [${ctxName}]`);
      await p.locator('input[name=name]').fill('Friend');
      await p.locator('input[name=password]').fill('short');
      await p.evaluate(() => document.querySelector('input[name=password]').removeAttribute('minlength'));
      await p.locator('button[type=submit]').click();
      await p.waitForLoadState();
      await audit(p, `invite-error [${ctxName}]`);
    }
    await p.goto(BASE2 + '/auth/invite?token=nope');
    await audit(p, `invite-gone [${ctxName}]`);
    await c.close();
  }
} catch (e) {
  console.log('not ok - a11y run crashed: ' + (e.stack || e));
  process.exitCode = 1;
} finally {
  await browser?.close();
  for (const p of procs) p.kill();
  rmSync(tmp, { recursive: true, force: true });
}

// ---------- report ----------
const order = { critical: 0, serious: 1, moderate: 2, minor: 3 };
const all = [...found.values()].sort((a, b) => order[a.impact] - order[b.impact] || a.rule.localeCompare(b.rule));
const bad = all.filter((v) => v.impact === 'critical' || v.impact === 'serious');
const shown = VERBOSE ? all : bad;
for (const v of shown) {
  const w = [...v.where];
  console.log(`${v.impact.toUpperCase()} ${v.rule}: ${v.help}\n  ${v.target}\n  ${v.html}\n  ${(v.summary || '').split('\n').slice(0, 3).join(' / ')}\n  on ${w.slice(0, 6).join(', ')}${w.length > 6 ? ` (+${w.length - 6})` : ''}`);
}
for (const f of failures) console.log('KEYBOARD ' + f);
if (process.env.A11Y_JSON) (await import('node:fs')).writeFileSync(process.env.A11Y_JSON, JSON.stringify(all.map((v) => ({ ...v, where: [...v.where] })), null, 1));
const counts = Object.fromEntries(Object.keys(order).map((k) => [k, all.filter((v) => v.impact === k).length]));
console.log(`\n${audits} audits; violations (unique element+rule): ${JSON.stringify(counts)}; keyboard failures: ${failures.length}`);
if (bad.length || failures.length) process.exitCode = 1;
else if (!process.exitCode) console.log('ok - no serious or critical accessibility violations');
