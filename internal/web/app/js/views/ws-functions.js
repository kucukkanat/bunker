// Functions tab: the app's server functions (api/<name>.js), test runs,
// recent runs, live logs and secrets.
import { h, icon, clear, empty, toast, confirmDialog, relTime, fullTime, skeletons, codeBlock, busy, plural, modal } from '../ui.js';
import { get, post, put, del, maybe, site as sp, enc, filePath, list } from '../api.js';
import { state } from '../state.js';
import { scopeText, blockDialog, unblock, adminBlocks } from '../functions.js';

const EXAMPLE = `// api/hello.js
export default async function (input, ctx) {
  return { message: 'Hello, ' + (input?.name || ctx.user.name) + '!' };
}`;
const PAGE_EXAMPLE = `const res = await bunker.fn('hello', { name: 'Sam' });
console.log(res.message); // "Hello, Sam!"`;
const SECRET_NAME = /^[A-Z][A-Z0-9_]{0,63}$/;
const TRIGGERS = { call: 'Called', schedule: 'Schedule', write: 'Data change', webhook: 'Webhook', test: 'Test run' };

export const ms = (n) => (n == null ? '' : n < 1000 ? Math.round(n) + ' ms' : (n / 1000).toFixed(n < 10000 ? 1 : 0).replace(/\.0$/, '') + ' s');
const userName = (u) => (u && typeof u === 'object' ? u.name || u.email : u) || '';
const trigName = (t) => { const k = t && typeof t === 'object' ? t.type : t; return TRIGGERS[k] || k || ''; };
const pretty = (v) => { if (v === undefined) return ''; try { return JSON.stringify(v, null, 2); } catch { return String(v); } };
const logLines = (logs) => {
  if (!logs) return [];
  if (typeof logs === 'string') return logs.split('\n').filter(Boolean).map((line) => ({ line }));
  return list(logs, 'lines', 'items').map((x) => (typeof x === 'string' ? { line: x } : x));
};
function logView(lines) {
  return h('div.fn-log', lines.map((l) => h('div.ll' + (/error|warn/.test(l.level || '') ? '.' + l.level : ''),
    l.at && h('time', { title: fullTime(l.at) }, clock(l.at)), l.level && l.level !== 'log' && l.level !== 'info' && h('b', l.level), h('span', l.line ?? ''))));
}
function clock(ts) {
  const t = typeof ts === 'number' ? (ts > 1e12 ? ts : ts * 1000) : Date.parse(ts);
  return t ? new Date(t).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : '';
}

// A schedule like "*/5 * * * *" in words, for the common shapes.
export function cronWords(c) {
  const p = String(c || '').trim().split(/\s+/);
  if (p.length !== 5) return c;
  const [mi, hr, dom, mon, dow] = p;
  const any = (x) => x === '*';
  const at = () => (/^\d+$/.test(hr) && /^\d+$/.test(mi) ? ' at ' + hr.padStart(2, '0') + ':' + mi.padStart(2, '0') + ' UTC' : '');
  if (p.every(any)) return 'Every minute';
  let m;
  if ((m = mi.match(/^\*\/(\d+)$/)) && [hr, dom, mon, dow].every(any)) return 'Every ' + plural(+m[1], 'minute');
  if (/^\d+$/.test(mi) && any(hr) && [dom, mon, dow].every(any)) return 'Every hour at minute ' + mi;
  if ((m = hr.match(/^\*\/(\d+)$/)) && /^\d+$/.test(mi) && [dom, mon, dow].every(any)) return 'Every ' + plural(+m[1], 'hour');
  if ([dom, mon, dow].every(any) && at()) return 'Every day' + at();
  if (any(dom) && any(mon) && dow === '1-5' && at()) return 'Weekdays' + at();
  if (any(dom) && any(mon) && /^[0-6]$/.test(dow) && at()) return 'Every ' + ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'][+dow] + at();
  if (/^\d+$/.test(dom) && any(mon) && any(dow) && at()) return 'Monthly on day ' + dom + at();
  return c;
}

export function fnBadges(f) {
  return [
    f.elevated && h('span.badge.draft', { title: 'Runs with the app owner’s rights, not the caller’s' }, icon('shield', 12), 'Runs as owner'),
    f.schedule && h('span.badge.ok', { title: 'Runs on a schedule (UTC): ' + f.schedule }, icon('clock', 12), cronWords(f.schedule)),
    f.onWrite?.length > 0 && h('span.badge.ok', { title: 'Runs after changes to ' + f.onWrite.join(', ') }, icon('db', 12), 'On changes to ' + f.onWrite.join(', ')),
    f.timeoutMs > 0 && h('span.badge', { title: 'Time limit for each run' }, 'Up to ' + ms(f.timeoutMs)),
  ];
}

export default async function functions(panel, ws) {
  const wrap = h('div.fn-wrap', skeletons(3, '.line'));
  clear(panel, wrap);
  let d;
  try { d = await get(sp(ws.slug, '/functions' + (ws.hasDraft ? '?draft=1' : '')), { quiet: true }); }
  catch (e) {
    if (!ws.alive()) return;
    clear(wrap, e.missing || e.status === 404
      ? empty({ title: 'Server functions aren’t on this server yet', text: 'When your Bunker is updated, you can add code in an api/ folder that runs on the server, and test it here.', art: false, iconName: 'terminal' })
      : empty({ title: 'Couldn’t load the functions', text: e.message, art: false, iconName: 'terminal' }));
    return;
  }
  if (!ws.alive()) return;
  const fns = list(d, 'functions', 'items');
  let blocked = d.blocked || (d.enabled === false ? { scope: 'global' } : null);
  ws.setFnBlock?.(blocked);

  const offs = [];
  const statusEl = h('div');
  const adminEl = h('div');
  const listEl = h('section.fn-sec', { 'aria-labelledby': 'fn-h-list' });
  const testEl = h('section.fn-sec', { 'aria-labelledby': 'fn-h-test' });
  const runsEl = h('section.fn-sec', { 'aria-labelledby': 'fn-h-runs' });
  const liveEl = h('section.fn-sec', { 'aria-labelledby': 'fn-h-live' });
  const secretsEl = h('section.fn-sec', { 'aria-labelledby': 'fn-h-secrets' });
  clear(wrap, statusEl, adminEl, listEl, fns.length > 0 && [testEl, runsEl, liveEl], secretsEl);

  // ---------- blocked ----------
  const paintStatus = () => clear(statusEl, blocked && h('div.notice.warn', { role: 'status', 'data-testid': 'fn-blocked' }, icon('info', 18), h('div',
    h('b', 'Server code is turned off. '),
    scopeText(blocked.scope, state.me && ws.site.ownerId === state.me.id) + ' ',
    blocked.reason && h('span', 'Reason: “' + blocked.reason + '”. '),
    'Pages still open, but these functions, schedules and triggers don’t run. ',
    state.me?.isAdmin ? (blocked.scope === 'global' ? h('a', { href: '/admin/functions' }, 'Change it in Admin') : null) : 'If you need it back on, ask an admin.')));
  paintStatus();

  // ---------- admin: turn this app off / on ----------
  async function paintAdmin() {
    if (!state.me?.isAdmin) return;
    const blocks = await adminBlocks();
    if (!blocks || !ws.alive()) return;
    const b = blocks.get('site:' + ws.slug);
    const label = '“' + ws.site.title + '”';
    clear(adminEl, h('div.setting.fn-admin',
      h('div', h('b', icon('shield', 14), ' Admin: server code for this app'), h('span.d', b ? 'Turned off' + (b.by ? ' by ' + userName(b.by) : '') + ' ' + relTime(b.at) + (b.reason ? ': “' + b.reason + '”' : '') : 'On. Turn it off if this app misbehaves; its pages keep working.')),
      h('button.btn.sm' + (b ? '' : '.danger'), { 'data-testid': 'fn-admin-toggle', onclick: async () => {
        const ok = b ? await unblock({ kind: 'site', id: ws.slug, label }) : await blockDialog({ kind: 'site', id: ws.slug, label });
        if (ok) functions(panel, ws);
      } }, b ? 'Turn back on' : 'Turn off server functions')));
  }
  paintAdmin();

  // ---------- list ----------
  if (!fns.length) {
    clear(listEl,
      h('h2.h3#fn-h-list', 'Functions'),
      h('div.empty.fn-empty', h('div.art', icon('terminal', 40)),
        h('h3', 'No server functions yet'),
        h('p', 'Add a file called api/hello.js to run code on the server: save to the database as the owner, call another service with a secret key, or run something every night. Publish (or preview the draft), then call it from your page.'),
        h('div.fn-example', h('b.small', 'api/hello.js'), codeBlock(EXAMPLE), h('b.small', 'In your page'), codeBlock(PAGE_EXAMPLE)),
        h('div.row.wrap', h('button.btn.primary', { onclick: (e) => busy(e.currentTarget, async () => {
          await put(sp(ws.slug, '/files/api/hello.js'), EXAMPLE + '\n', { headers: { 'Content-Type': 'text/plain; charset=utf-8' } });
          ws.draftChanged();
          toast('Added api/hello.js to your draft. Check it in Files, then publish.', { type: 'ok' });
          functions(panel, ws);
        }) }, icon('plus', 16), 'Add api/hello.js to my draft'))));
  } else {
    clear(listEl, h('div.row', h('h2.h3#fn-h-list', 'Functions'), h('span.count.faint.small', fns.length), ws.hasDraft && h('span.badge.draft', { title: 'You have unpublished changes. Schedules and triggers use the live version until you publish.' }, 'From your draft'), h('span.spacer'),
      h('button.btn.sm.ghost', { onclick: () => functions(panel, ws) }, icon('refresh', 14), 'Refresh')),
      h('div.list', fns.map((f) => h('div.item.fn-item',
        h('span.site-icon.sm', icon('terminal', 16)),
        h('div.n', h('b', f.name), h('span.mono.tiny', f.path || 'api/' + f.name + '.js'),
          h('div.row.wrap.fn-badges', fnBadges(f)),
          f.error && h('div.fn-err', { role: 'note' }, h('b.small', icon('warn', 14), ' This function didn’t build'), h('pre', f.error))),
        h('button.btn.sm', { disabled: !!f.error, 'aria-label': 'Test ' + f.name, onclick: () => pick(f.name) }, icon('send', 14), 'Test')))));
  }

  // ---------- test run ----------
  const sel = h('select.input', { 'aria-label': 'Function', style: { maxWidth: '260px' } }, fns.filter((f) => !f.error).map((f) => h('option', { value: f.name }, f.name)));
  const input = h('textarea.input.mono.fn-json', { rows: 5, spellcheck: 'false', 'aria-label': 'Input (JSON)', placeholder: '{ "name": "Sam" }' }, '{}');
  const inputErr = h('span.hint.fn-bad.hidden', { role: 'alert' });
  const draftBox = h('input', { type: 'checkbox', checked: !!ws.hasDraft });
  const out = h('div', { 'aria-live': 'polite' });
  const runBtn = h('button.btn.primary', { type: 'submit', disabled: !!blocked }, icon('send', 16), 'Run');
  const pick = (name) => { sel.value = name; testEl.scrollIntoView({ block: 'start', behavior: 'smooth' }); input.focus(); };
  input.addEventListener('input', () => { input.removeAttribute('aria-invalid'); inputErr.classList.add('hidden'); });
  input.addEventListener('keydown', (e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); form.requestSubmit(); } });
  const form = h('form.stack-sm', { onsubmit: async (e) => {
    e.preventDefault();
    let val = null;
    const raw = input.value.trim();
    if (raw) {
      try { val = JSON.parse(raw); }
      catch (err) {
        input.setAttribute('aria-invalid', 'true');
        inputErr.textContent = 'That isn’t valid JSON yet: ' + err.message.replace(/^JSON\.parse: /, '');
        inputErr.classList.remove('hidden');
        return;
      }
    }
    await busy(runBtn, async () => {
      const t0 = performance.now();
      try {
        const r = await post(sp(ws.slug, '/functions/' + filePath(sel.value)), { input: val, draft: draftBox.checked || undefined }, { quiet: true });
        const dur = r?.durationMs ?? performance.now() - t0;
        clear(out, h('div.fn-result.ok', h('div.row', h('span.badge.ok.dot', 'Worked'), h('span.small.muted', 'in ' + ms(dur)), h('span.spacer')),
          h('pre', pretty(r && typeof r === 'object' && 'output' in r ? r.output : r) || 'null')));
      } catch (err) {
        const x = err.detail || {};
        const stack = x.stack || x.details?.stack || '';
        const title = { functions_off: 'Server code is turned off', busy: 'Too busy right now', timeout: 'It took too long', not_found: 'Function not found', function_error: 'The function threw an error' }[err.code] || 'It didn’t work';
        clear(out, h('div.fn-result.bad', { role: 'alert' }, h('div.row', h('span.badge.danger.dot', title), h('span.small.muted', ms(x.durationMs ?? performance.now() - t0))),
          h('p.small', err.message), stack && h('pre.stack', stack)));
      }
      loadRuns();
    });
  } },
    h('div.row.wrap', h('label.field', { style: { flex: '1 1 200px' } }, h('span.label', 'Function'), sel),
      ws.hasDraft && h('label.check.small', { style: { alignSelf: 'flex-end' } }, draftBox, h('span', 'Run the draft code'))),
    h('label.field', h('span.label', 'Input (JSON)'), input, inputErr, h('span.hint', 'Runs as you, like a call from the page would. ' + (navigator.platform.includes('Mac') ? '⌘' : 'Ctrl') + ' Enter runs it.')),
    h('div.row', h('span.spacer'), runBtn),
    out);
  clear(testEl, h('h2.h3#fn-h-test', 'Test run'), form);

  // ---------- recent runs ----------
  const runsList = h('div', skeletons(2, '.line'));
  const filter = h('select.mini', { 'aria-label': 'Show runs of', onchange: () => loadRuns() }, h('option', { value: '' }, 'All functions'), fns.map((f) => h('option', { value: f.name }, f.name)));
  clear(runsEl, h('div.row.wrap', h('h2.h3#fn-h-runs', 'Recent runs'), h('span.spacer'), filter, h('button.btn.sm.ghost', { onclick: () => loadRuns() }, icon('refresh', 14), 'Refresh')), runsList);
  let runSeq = 0;
  async function loadRuns() {
    const my = ++runSeq;
    const q = new URLSearchParams({ limit: '50' });
    if (filter.value) q.set('name', filter.value);
    const r = await maybe(sp(ws.slug, '/functions/_runs?' + q)).catch(() => null);
    if (!ws.alive() || my !== runSeq) return;
    const items = list(r, 'items', 'runs');
    if (!items.length) return clear(runsList, h('p.small.muted', r ? 'No runs yet. Test a function above, or open the page and use it.' : 'Run history isn’t available yet.'));
    clear(runsList, h('div.list.fn-runs', items.map((x) => {
      const lines = logLines(x.logs);
      const ok = x.status === 'ok';
      return h('details.item.fn-run',
        h('summary',
          h('span.badge.dot.' + (ok ? 'ok' : 'danger'), ok ? 'OK' : 'Error'),
          h('b.mono', x.name),
          h('span.small.muted', [trigName(x.trigger), userName(x.user), ms(x.durationMs)].filter(Boolean).join(' · ')),
          h('span.spacer'),
          h('time.small.faint', { title: fullTime(x.at) }, relTime(x.at)),
          icon('down', 14, 'chev')),
        h('div.fn-run-body',
          x.error && h('pre.stack', typeof x.error === 'object' ? (x.error.message || '') + (x.error.stack ? '\n' + x.error.stack : '') : x.error),
          lines.length ? logView(lines) : h('p.tiny.faint', 'Nothing was logged. Use console.log() in the function to see output here.')));
    })));
  }
  if (fns.length) loadRuns();

  // ---------- live logs ----------
  const liveLog = h('div.fn-log.live', { role: 'log', 'aria-label': 'Live logs', 'aria-live': 'off' });
  const liveState = h('span.small.muted');
  let es = null, paused = false, count = 0;
  const pauseBtn = h('button.btn.sm.ghost', { 'aria-pressed': 'false', onclick: () => { paused = !paused; pauseBtn.setAttribute('aria-pressed', String(paused)); pauseBtn.lastChild.textContent = paused ? 'Resume' : 'Pause'; } }, icon('clock', 14), h('span', 'Pause'));
  const clearBtn = h('button.btn.sm.ghost', { onclick: () => { liveLog.replaceChildren(); count = 0; empt(); } }, icon('x', 14), 'Clear');
  const empt = () => { if (!liveLog.firstChild) liveLog.append(h('p.tiny.faint.fn-wait', 'Waiting for logs. Lines from console.log() show up here while the function runs.')); };
  clear(liveEl, h('div.row.wrap', h('h2.h3#fn-h-live', 'Live logs'), liveState, h('span.spacer'), pauseBtn, clearBtn), liveLog);
  empt();
  function connect() {
    if (typeof EventSource === 'undefined') return;
    let fails = 0;
    es = new EventSource(sp(ws.slug, '/events?ch=logs'));
    es.onopen = () => { fails = 0; clear(liveState, h('span.badge.ok.dot', 'Live')); };
    es.onmessage = (e) => {
      if (!ws.alive() || !panel.contains(liveEl)) { es.close(); return; }
      let m;
      try { m = JSON.parse(e.data); } catch { return; }
      if (m && m.ch && m.ch !== 'logs') return;
      const l = m && m.ch ? m.data : m;
      if (!l || paused) return;
      liveLog.querySelector('.fn-wait')?.remove();
      const stick = liveLog.scrollTop + liveLog.clientHeight >= liveLog.scrollHeight - 8;
      liveLog.append(h('div.ll' + (/error|warn/.test(l.level || '') ? '.' + l.level : ''),
        h('time', clock(l.at || Date.now())), h('b.mono', l.name || ''), l.level && l.level !== 'log' && l.level !== 'info' && h('b', l.level), h('span', l.line ?? '')));
      if (++count > 500) { liveLog.firstChild.remove(); count--; }
      if (stick) liveLog.scrollTop = liveLog.scrollHeight;
      if (l.run && !l.line) loadRuns();
    };
    es.onerror = () => {
      if (++fails >= 3 || es.readyState === 2) { es.close(); clear(liveState, h('span.badge', 'Not connected')); }
      else clear(liveState, h('span.badge', 'Reconnecting…'));
    };
  }
  if (fns.length) connect();
  offs.push(() => es && es.close());

  // ---------- secrets ----------
  secrets(secretsEl, ws);

  return () => offs.forEach((f) => f());
}

// Secrets: write-only values the functions read with bunker.secrets.get(NAME).
async function secrets(el, ws) {
  const r = await maybe(sp(ws.slug, '/secrets')).catch(() => null);
  if (!ws.alive()) return;
  const items = list(r, 'items', 'secrets');
  const reload = () => secrets(el, ws);
  clear(el,
    h('div.row', h('h2.h3#fn-h-secrets', 'Secrets'), h('span.spacer'), r && h('button.btn.sm', { onclick: () => secretDialog(ws, null, reload) }, icon('plus', 14), 'Add secret')),
    h('p.small.muted', 'API keys and passwords your functions need. Read them with ', h('code', 'bunker.secrets.get("NAME")'), '. Values are stored encrypted and never shown again, not even to you. Pages can’t read them.'),
    !r ? h('p.small.muted', 'Secrets aren’t available on this server yet.')
      : items.length ? h('div.list', items.map((s) => h('div.item',
        h('span.site-icon.sm', icon('key', 16)),
        h('div.n', h('b.mono', s.name), h('span', 'Updated ' + (relTime(s.updatedAt) || '—') + (s.updatedBy ? ' by ' + userName(s.updatedBy) : '') + ' · value hidden')),
        h('button.btn.sm', { 'aria-label': 'Replace the value of ' + s.name, onclick: () => secretDialog(ws, s.name, reload) }, 'Replace'),
        h('button.btn.sm.ghost.danger', { 'aria-label': 'Delete secret ' + s.name, onclick: async () => {
          if (!(await confirmDialog({ title: 'Delete the secret ' + s.name + '?', message: 'Functions that use it get null from bunker.secrets.get("' + s.name + '") from now on. This can’t be undone.', confirm: 'Delete secret', danger: true }))) return;
          await del(sp(ws.slug, '/secrets/' + enc(s.name)));
          toast('Deleted ' + s.name + '.');
          reload();
        } }, icon('trash', 15)))))
        : h('div.empty.sm', h('p', 'No secrets yet.')));
}

function secretDialog(ws, name, done) {
  const nameIn = h('input.input.mono', { value: name || '', required: true, maxlength: 64, disabled: !!name, placeholder: 'STRIPE_KEY', autocomplete: 'off', spellcheck: 'false', autofocus: !name, 'aria-describedby': 'sec-name-hint' });
  const val = h('input.input.mono', { type: 'password', required: true, autocomplete: 'new-password', spellcheck: 'false', placeholder: name ? 'Paste the new value' : 'Paste the value', autofocus: !!name, 'aria-describedby': 'sec-val-hint' });
  const bad = h('span.hint.fn-bad.hidden', { role: 'alert' });
  nameIn.addEventListener('input', () => { nameIn.value = nameIn.value.toUpperCase().replace(/[^A-Z0-9_]/g, '_'); bad.classList.add('hidden'); });
  const go = h('button.btn.primary', { type: 'submit' }, name ? 'Replace value' : 'Save secret');
  const submit = async (e) => {
    e.preventDefault();
    const n = nameIn.value.trim();
    if (!SECRET_NAME.test(n)) { bad.textContent = 'Use capital letters, numbers and _, starting with a letter. Like STRIPE_KEY.'; bad.classList.remove('hidden'); nameIn.focus(); return; }
    if (!val.value) { val.focus(); return; }
    if (new Blob([val.value]).size > 16384) { bad.textContent = 'That value is too long. Secrets can be up to 16 KB.'; bad.classList.remove('hidden'); return; }
    await busy(go, async () => {
      await put(sp(ws.slug, '/secrets/' + enc(n)), { value: val.value });
      val.value = '';
      m.close();
      toast(name ? 'Replaced the value of ' + n + '.' : 'Saved ' + n + '. Your functions can use it now.', { type: 'ok' });
      done();
    });
  };
  const m = modal({ title: name ? 'Replace ' + name : 'Add a secret', body: h('form.stack', { onsubmit: submit },
    h('label.field', h('span.label', 'Name'), nameIn, h('span.hint#sec-name-hint', 'Capital letters, numbers and _. You use it in code: bunker.secrets.get("' + (name || 'NAME') + '").')),
    h('label.field', h('span.label', 'Value'), val, bad, h('span.hint#sec-val-hint', 'Stored encrypted. After you save, nobody can see it again; you can only replace it.')),
    h('button', { type: 'submit', hidden: true })),
    footer: [h('button.btn', { type: 'button', onclick: () => m.close() }, 'Cancel'), go] });
  go.addEventListener('click', submit);
}
