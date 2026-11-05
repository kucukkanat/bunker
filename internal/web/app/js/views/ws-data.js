// Data tab: browse and edit the site's collections (bunker.db).
import { h, icon, clear, empty, toast, confirmDialog, relTime, skeletons } from '../ui.js';
import { get, put, patch, del, site as sp, enc, list } from '../api.js';

const RULES = [
  ['own', 'Everyone adds; people edit only their own rows'],
  ['read', 'Everyone can read; only editors change'],
  ['append', 'Everyone can add; only editors change'],
  ['private', 'People see only their own rows'],
  ['open', 'Everyone can read and change everything'],
];
const SYS = ['_id', '_author', '_authorName', '_created', '_updated'];

export default async function data(panel, ws) {
  const wrap = h('div.data-wrap', skeletons(3, '.line'));
  clear(panel, wrap);
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
  if (!colls.length) {
    clear(wrap, empty({ title: 'No data yet', text: 'When people use this site to save things, like form entries or sign-ups, the entries appear here. You can edit them and export to a spreadsheet.', art: false, iconName: 'db' }));
    return;
  }
  colls = colls.map((c) => (typeof c === 'string' ? { name: c } : c));
  let cur = colls[0].name;
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
      const td = h('td.edit', { tabindex: '0', title: 'Click to edit' }, fmt(v));
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
        inp.addEventListener('keydown', (e) => { if (e.key === 'Enter') commit(true); else if (e.key === 'Escape') { commit(false); td.focus(); } });
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
        h('td.sys', r._authorName || r._author || ''), h('td.sys', { title: r._created ? new Date((r._created > 1e12 ? r._created : r._created * 1000)).toLocaleString() : '' }, relTime(r._created)),
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
