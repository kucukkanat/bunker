// Data tab: browse and edit the site's collections (bunker.db).
import { h, icon, clear, empty, toast, confirmDialog, relTime, fullTime, skeletons, modal, copyBtn, codeBlock, busy, plural } from '../ui.js';
import { get, post, put, patch, del, site as sp, enc, list, maybe } from '../api.js';

const RULES = [
  ['own', 'Everyone adds; people edit only their own rows'],
  ['read', 'Everyone can read; only editors change'],
  ['append', 'Everyone can add; only editors change'],
  ['private', 'People see only their own rows'],
  ['open', 'Everyone can read and change everything'],
];
const SYS = ['_id', '_author', '_authorName', '_created', '_updated'];

// live keeps the Data tab up to date: new entries (from the site, another
// person or a webhook) show up without a reload.
let liveES = null;
function live(panel, wrap, ws, names, onChange) {
  liveES?.close();
  liveES = null;
  const chans = [...new Set(names)].filter((n) => /^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/.test(n)).slice(0, 30).map((n) => 'db:' + n);
  if (!chans.length || typeof EventSource === 'undefined') return;
  let t = null;
  const es = new EventSource(sp(ws.slug, '/events?ch=' + enc(chans.join(','))));
  liveES = es;
  es.onmessage = () => {
    if (!ws.alive() || !panel.contains(wrap)) { es.close(); return; }
    clearTimeout(t);
    t = setTimeout(() => { if (!wrap.querySelector('td input')) onChange(); }, 400);
  };
}

export default async function data(panel, ws, keep) {
  const wrap = h('div.data-wrap', skeletons(3, '.line'));
  const hooksEl = h('section.hooks', { 'aria-label': 'Webhooks' });
  clear(panel, wrap, hooksEl);
  const hookList = hooks(hooksEl, ws, () => data(panel, ws, keep));
  let colls;
  try {
    colls = list(await get(sp(ws.slug, '/db'), { quiet: true }), 'collections', 'items');
  } catch (e) {
    if (!ws.alive()) return;
    clear(wrap, empty({
      title: e.missing ? 'Data will show up here' : 'Couldn’t load the data',
      text: e.missing ? 'When this site saves information, like form entries or a to-do list, you’ll be able to see and edit it here.' : e.message,
      art: false, iconName: 'db',
    }));
    return;
  }
  if (!ws.alive()) return;
  colls = colls.map((c) => (typeof c === 'string' ? { name: c } : c));
  const hookColls = ((await hookList.catch(() => null)) || []).map((x) => x.collection);
  let cur = colls.some((c) => c.name === keep) ? keep : colls[0]?.name;
  live(panel, wrap, ws, [...colls.map((c) => c.name), ...hookColls], () => data(panel, ws, cur));
  if (!colls.length) {
    clear(wrap, empty({ title: 'No data yet', text: 'When people use this site to save things, like form entries or sign-ups, the entries appear here. You can edit them and export to a spreadsheet.', art: false, iconName: 'db' }));
    return;
  }
  const tabs = h('div.coll-tabs', { role: 'group', 'aria-label': 'Collections' });
  const tools = h('div.row.wrap');
  const table = h('div');
  clear(wrap, tabs, tools, table);

  function drawTabs() {
    clear(tabs, colls.map((c) => h('button', { 'aria-pressed': String(c.name === cur), onclick: () => { cur = c.name; drawTabs(); loadColl(); } },
      icon('db', 14), c.name, c.count != null && h('span.n', c.count))));
  }

  async function loadColl() {
    const c = colls.find((x) => x.name === cur);
    const rule = h('select.mini', { 'aria-label': 'Who can do what', onchange: async (e) => {
      const old = c.rule;
      c.rule = e.target.value;
      try { await put(sp(ws.slug, '/db/' + enc(cur) + '/_rule'), { rule: c.rule }); toast('Updated who can change “' + cur + '”.', { type: 'ok', timeout: 2400 }); }
      catch { c.rule = old; e.target.value = old || 'own'; }
    } }, RULES.map(([v, l]) => h('option', { value: v, selected: (c.rule || 'own') === v }, l)),
      c.rule && c.rule.startsWith('group:') && h('option', { value: c.rule, selected: true }, 'Only the group ' + c.rule.slice(6)));
    clear(tools, h('label.row.small.muted', icon('lock', 14), 'Access', rule), h('span.spacer'),
      h('a.btn.sm', { href: sp(ws.slug, '/db/' + enc(cur) + '/_export?format=csv'), download: cur + '.csv', 'data-native': '' }, icon('down', 16), 'Export CSV'),
      h('button.btn.sm.ghost.danger', { onclick: drop }, icon('trash', 16), 'Delete collection'));
    clear(table, skeletons(3, '.line'));
    const d = await get(sp(ws.slug, '/db/' + enc(cur) + '?limit=200'), { quiet: true }).catch(() => null);
    if (!ws.alive() || c.name !== cur) return;
    const rows = list(d, 'docs', 'items', 'rows', 'data', 'results');
    if (!rows.length) return clear(table, empty({ title: 'This collection is empty', text: 'Entries appear here as people use the site.', art: false, iconName: 'db' }));
    const cols = [...new Set(rows.flatMap((r) => Object.keys(r)))].filter((k) => !SYS.includes(k));
    const cell = (r, k) => {
      const v = r[k];
      const td = h('td.edit', { tabindex: '0', title: 'Click or press Enter to edit' }, fmt(v));
      const edit = () => {
        if (td.querySelector('input')) return;
        const raw = v == null ? '' : typeof v === 'object' ? JSON.stringify(v) : String(v);
        const inp = h('input', { value: raw, 'aria-label': 'Edit ' + k });
        let done = false;
        const commit = async (ok) => {
          if (done) return;
          done = true;
          if (!ok || inp.value === raw) return clear(td, fmt(r[k]));
          let nv = inp.value;
          if (typeof v === 'number' && nv.trim() !== '' && !isNaN(+nv)) nv = +nv;
          else if (typeof v === 'boolean') nv = nv === 'true';
          else if (typeof v === 'object' && v) { try { nv = JSON.parse(nv); } catch {} }
          const prev = r[k];
          r[k] = nv;
          clear(td, fmt(nv));
          try { await patch(sp(ws.slug, '/db/' + enc(cur) + '/' + enc(r._id)), { [k]: nv }); }
          catch { r[k] = prev; clear(td, fmt(prev)); }
        };
        inp.addEventListener('keydown', (e) => { if (e.key === 'Enter') { commit(true); td.focus(); } else if (e.key === 'Escape') { commit(false); td.focus(); } });
        inp.addEventListener('blur', () => commit(true));
        clear(td, inp);
        inp.focus();
        inp.select();
      };
      td.addEventListener('click', edit);
      td.addEventListener('keydown', (e) => { if (e.key === 'Enter' && e.target === td) edit(); });
      return td;
    };
    clear(table, h('div.table-wrap', h('table.t',
      h('thead', h('tr', cols.map((k) => h('th', { scope: 'col' }, k)), h('th', { scope: 'col' }, 'Added by'), h('th', { scope: 'col' }, 'When'), h('th', h('span.sr-only', 'Actions')))),
      h('tbody', rows.map((r) => h('tr', cols.map((k) => cell(r, k)),
        h('td.sys', r._authorName || r._author || ''), h('td.sys', { title: fullTime(r._created) }, relTime(r._created)),
        h('td', h('button.btn.ghost.icon.sm', { 'aria-label': 'Delete row', onclick: async (e) => {
          if (!(await confirmDialog({ title: 'Delete this entry?', message: 'It’s removed for everyone. This can’t be undone.', confirm: 'Delete', danger: true }))) return;
          await del(sp(ws.slug, '/db/' + enc(cur) + '/' + enc(r._id)));
          e.target.closest('tr').remove();
          if (c.count) { c.count--; drawTabs(); }
        } }, icon('trash', 15)))))))),
      h('p.tiny.faint', { style: { marginTop: '8px' } }, 'Click a cell to edit it. Showing up to 200 entries; export to see everything.'));
  }

  async function drop() {
    if (!(await confirmDialog({ title: 'Delete the collection “' + cur + '”?', message: 'All of its entries are deleted for everyone. Export a CSV first if you might need them.', confirm: 'Delete collection', danger: true }))) return;
    await del(sp(ws.slug, '/db/' + enc(cur)));
    colls = colls.filter((c) => c.name !== cur);
    toast('Deleted “' + cur + '”.');
    if (!colls.length) return data(panel, ws);
    cur = colls[0].name;
    drawTabs();
    loadColl();
  }

  drawTabs();
  loadColl();
}

function fmt(v) {
  if (v == null || v === '') return h('span.faint', '—');
  if (typeof v === 'boolean') return v ? 'Yes' : 'No';
  if (typeof v === 'object') return JSON.stringify(v);
  return String(v);
}

// Webhooks: a secret URL that adds the JSON it receives to a collection.
async function hooks(el, ws, reload = () => hooks(el, ws)) {
  const d = await maybe(sp(ws.slug, '/hooks')).catch(() => null);
  if (!ws.alive() || !d) return null;
  const items = d.hooks || [];
  const add = h('button.btn.sm', { onclick: () => create(ws, reload) }, icon('plus', 14), 'New webhook');
  clear(el, h('div.row', h('h3', 'Webhooks'), h('span.spacer'), add),
    h('p.small.muted', 'Send data in from Power Automate, Zapier or a script. Each webhook adds what it receives to a collection.'),
    items.length > 0 && h('div.list', items.map((x) => h('div.item', h('span.avatar', { style: { background: 'var(--sand)', color: 'var(--muted)' } }, icon('plug', 16)),
      h('div.n', h('b', x.name), h('span', 'Into “' + x.collection + '” · ' + (x.calls ? plural(x.calls, 'call') + ', last ' + relTime(x.lastUsedAt) : 'not used yet') + ' · ' + x.prefix + '…')),
      h('button.btn.sm.ghost.danger', { 'aria-label': 'Delete webhook ' + x.name, onclick: async () => {
        if (!(await confirmDialog({ title: 'Delete “' + x.name + '”?', message: 'Its URL stops working right away. Data it already sent stays.', confirm: 'Delete webhook', danger: true }))) return;
        await del(sp(ws.slug, '/hooks/' + x.id));
        toast('Webhook deleted.');
        reload();
      } }, icon('trash', 16))))));
  return items;
}

function create(ws, done) {
  const name = h('input.input', { required: true, maxlength: 100, placeholder: 'Power Automate: new hires', autofocus: true });
  const coll = h('input.input', { required: true, maxlength: 64, value: 'entries', pattern: '[A-Za-z0-9_\\-]+', title: 'Letters, numbers, - and _' });
  const go = h('button.btn.primary', { type: 'submit' }, 'Create webhook');
  const m = modal({ title: 'New webhook', body: h('form.stack', { onsubmit: async (e) => {
    e.preventDefault();
    await busy(go, async () => {
      const r = await post(sp(ws.slug, '/hooks'), { name: name.value.trim(), collection: coll.value.trim() });
      m.close();
      const url = new URL(r.url, location.origin).href;
      const ex = (r.example || '').replace(r.url, url);
      const m2 = modal({ title: 'Copy your webhook URL', body: [
        h('div.notice.warn', icon('warn', 18), h('div', h('b', 'You’ll only see it once. '), 'Anyone who has it can add data, so keep it secret.')),
        h('div.copy-field', { style: { marginTop: '14px' } }, h('span', { 'data-testid': 'hook-url' }, url), copyBtn(url)),
        h('p.small.muted', { style: { margin: '14px 0 6px' } }, 'Send JSON to it with a POST. For example:'),
        codeBlock(ex)],
        footer: [h('button.btn.primary', { onclick: () => m2.close() }, 'Done')] });
      done();
    });
  } },
    h('label.field', h('span.label', 'Name'), name, h('span.hint', 'What sends to it, so you recognise it later.')),
    h('label.field', h('span.label', 'Collection'), coll, h('span.hint', 'Where the data goes. It’s created if it doesn’t exist yet.')),
    h('div.row', h('span.spacer'), go)) });
}
