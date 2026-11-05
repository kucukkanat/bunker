// Bunker dashboard entry: shell, routes, palette, shortcuts, notifications.
import { route, start, setRenderer, navigate } from './router.js';
import { h, icon, avatar, clear, $, modal, toast, store, siteIcon, relTime } from './ui.js';
import { get, post, maybe } from './api.js';
import { state, loadSites } from './state.js';

const app = document.getElementById('app');
const V = (n) => () => import('./views/' + n + '.js');

route(/^\/$/, V('home'));
route(/^\/new$/, V('new'));
route(/^\/sites\/([^/]+)$/, V('site'), 'workspace');
route(/^\/templates$/, V('templates'));
route(/^\/trash$/, V('trash'));
route(/^\/settings(?:\/([a-z-]+))?$/, V('settings'));
route(/^\/admin(?:\/([a-z0-9-]+))?$/, V('admin'));
route(/^\/device$/, V('device'), 'bare');
route(/^\/setup$/, V('setup'), 'bare');
route(/^\/auth\/signed-out$/, V('signedout'), 'bare');
route(/^\/welcome$/, V('welcome'));
route(/^\/docs$/, V('docs'));
route(/.*/, V('notfound'));

const PUBLIC = ['/setup', '/auth/signed-out'];

// ---------- shell ----------
let shell, main, side, navEl;
function navLink(href, ic, label) {
  return h('a', { href }, icon(ic, 18), label);
}
function buildShell() {
  const me = state.me;
  navEl = h('nav.nav', { 'aria-label': 'Main' },
    navLink('/', 'home', 'Home'),
    navLink('/templates', 'grid', 'Templates'),
    navLink('/trash', 'trash', 'Trash'),
    navLink('/settings', 'gear', 'Settings'),
    navLink('/settings/connect', 'plug', 'Connect your tools'),
    me?.isAdmin && h('div.sec', 'Admin'),
    me?.isAdmin && navLink('/admin', 'shield', 'Admin'));
  side = h('aside.side', { id: 'sidebar' },
    h('div.row', h('a.brand', { href: '/' }, h('img', { src: '/_/icon.svg', alt: '' }), 'bunker'), h('span.spacer'), bell()),
    h('a.btn.primary.new', { href: '/new' }, icon('plus', 18), 'New site'),
    navEl,
    h('div.foot',
      h('button.palette-hint', { type: 'button', onclick: openPalette }, icon('search', 16), 'Jump to…', h('kbd', isMac ? '⌘K' : 'Ctrl K')),
      me?.dev && h('span.pill-dev', { title: 'Sign-in is simulated. Don’t use dev mode for real work.' }, icon('code', 14), 'Dev mode'),
      me && h('a.me-chip', { href: '/settings' }, avatar(me.name || me.email), h('span.who', h('b', me.name || me.email), h('span', me.email)))));
  main = h('main#main', { tabindex: '-1' });
  const top = h('div.topbar-m',
    h('button.btn.ghost.icon', { 'aria-label': 'Open menu', 'aria-controls': 'sidebar', onclick: openSide }, icon('menu', 20)),
    h('a.brand', { href: '/' }, h('img', { src: '/_/icon.svg', alt: '' }), 'bunker'),
    h('span.spacer'),
    bell(),
    h('a.btn.primary.sm', { href: '/new', 'aria-label': 'New site' }, icon('plus', 16), 'New'));
  shell = h('div.shell', side, h('div', top, main));
}
let scrim;
function openSide() {
  side.classList.add('open');
  scrim = h('div.side-scrim', { onclick: closeSide });
  document.body.append(scrim);
  side.querySelector('a')?.focus();
}
function closeSide() { side?.classList.remove('open'); scrim?.remove(); scrim = null; }

setRenderer((layout, path) => {
  closeSide();
  document.querySelectorAll('.backdrop').forEach((b) => b.remove());
  if (layout === 'app') {
    if (!shell) buildShell();
    if (shell.parentNode !== app) clear(app, h('a.skip', { href: '#main' }, 'Skip to content'), shell);
    navEl.querySelectorAll('a').forEach((a) => {
      const hr = a.getAttribute('href');
      const on = hr === '/' ? path === '/' : path === hr || (path.startsWith(hr + '/') && !(hr === '/settings' && path.startsWith('/settings/connect')));
      if (on) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
    });
    clear(main);
    window.scrollTo(0, 0);
    return main;
  }
  const root = h('div', { id: layout === 'workspace' ? 'main' : null });
  clear(app, root);
  return root;
});

// ---------- notifications ----------
const notes = { list: [], unread: 0, ok: false, subs: new Set() };
const isMac = /Mac|iPhone|iPad/.test(navigator.platform);
function notify() { notes.subs.forEach((f) => f()); }
async function loadNotes() {
  const d = await maybe('/api/v1/notifications').catch(() => null);
  if (!d) return false;
  notes.ok = true;
  notes.list = d.notifications || [];
  notes.unread = d.unread ?? notes.list.filter((n) => !n.read).length;
  notify();
  return true;
}
function liveNotes() {
  let es;
  try { es = new EventSource('/api/v1/notifications/events'); } catch { return; }
  es.onmessage = (e) => {
    let d;
    try { d = JSON.parse(e.data); } catch { return; }
    if (!d || (d.type && d.type !== 'notification')) return;
    notes.list.unshift({ ...d, read: false, createdAt: Date.now() / 1000 });
    notes.unread++;
    notify();
    toast(d.title + (d.body ? ': ' + d.body : ''), d.link ? { action: { label: 'Open', run: () => navigate(d.link) } } : {});
  };
  es.addEventListener('notification', (e) => es.onmessage(e));
  // The stream drops when Bunker goes away: say so, and say when it's back.
  let lost = 0, banner;
  es.onerror = () => {
    if (lost) return;
    lost = setTimeout(() => {
      banner = h('div.conn-banner', { role: 'status' }, h('span.spin'), 'Can’t reach Bunker. Trying again…');
      document.body.append(banner);
    }, 2500);
  };
  es.onopen = () => {
    clearTimeout(lost);
    lost = 0;
    if (banner) { banner.remove(); banner = null; toast('You’re back online.', { type: 'ok', timeout: 2500 }); }
  };
}
export function bell() {
  const badge = h('span.bell-badge', { 'aria-hidden': 'true' });
  const btn = h('button.btn.ghost.icon.sm.bell', { type: 'button', 'aria-haspopup': 'true', 'aria-expanded': 'false', onclick: () => openNotes(btn) }, icon('bell', 18), badge);
  const upd = () => {
    btn.hidden = !notes.ok;
    badge.textContent = notes.unread > 9 ? '9+' : notes.unread || '';
    badge.hidden = !notes.unread;
    btn.setAttribute('aria-label', notes.unread ? `Notifications, ${notes.unread} unread` : 'Notifications');
  };
  notes.subs.add(upd);
  upd();
  return btn;
}
function openNotes(btn) {
  const close = () => { pop.remove(); btn.setAttribute('aria-expanded', 'false'); document.removeEventListener('mousedown', out, true); document.removeEventListener('keydown', esc, true); };
  const out = (e) => { if (!pop.contains(e.target) && !btn.contains(e.target)) close(); };
  const esc = (e) => { if (e.key === 'Escape') { close(); btn.focus(); } };
  const markAll = async () => {
    notes.list.forEach((n) => (n.read = true));
    notes.unread = 0;
    notify();
    render();
    post('/api/v1/notifications/read', { ids: [] }, { quiet: true }).catch(() => {});
  };
  const listEl = h('div.notes-list');
  const render = () => clear(listEl, notes.list.length ? notes.list.slice(0, 30).map((n) =>
    h('a.note' + (n.read ? '' : '.unread'), { href: n.link || '#', onclick: (e) => {
      if (!n.link) e.preventDefault();
      if (!n.read) { n.read = true; notes.unread = Math.max(0, notes.unread - 1); notify(); post('/api/v1/notifications/read', { ids: [n.id] }, { quiet: true }).catch(() => {}); }
      close();
    } }, h('span.nd'), h('span.nt', h('b', n.title), n.body && h('span', n.body), h('time', relTime(n.createdAt)))))
    : h('div.notes-empty', icon('bell', 28), h('b', 'You’re all caught up'), h('span', 'When someone shares a site with you or asks for access, it shows up here.')));
  render();
  const pop = h('div.popover.notes', { role: 'dialog', 'aria-label': 'Notifications' },
    h('div.notes-head', h('b', 'Notifications'), h('span.spacer'), notes.unread > 0 && h('button.link-btn.small', { onclick: markAll }, 'Mark all as read')),
    listEl);
  document.body.append(pop);
  const r = btn.getBoundingClientRect();
  const w = Math.min(360, innerWidth - 16);
  pop.style.width = w + 'px';
  pop.style.top = r.bottom + 8 + 'px';
  pop.style.left = Math.max(8, Math.min(r.left, innerWidth - w - 8)) + 'px';
  btn.setAttribute('aria-expanded', 'true');
  setTimeout(() => { document.addEventListener('mousedown', out, true); document.addEventListener('keydown', esc, true); pop.querySelector('a,button')?.focus(); });
}

// ---------- command palette ----------
const ACTIONS = [
  ['New site', 'plus', '/new', 'N'],
  ['Home', 'home', '/', 'G H'],
  ['Templates', 'grid', '/templates', 'G T'],
  ['Trash', 'trash', '/trash'],
  ['Settings', 'gear', '/settings', 'G S'],
  ['Access tokens', 'key', '/settings/tokens'],
  ['Connect your tools (CLI, Claude Code, Cursor)', 'plug', '/settings/connect'],
  ['Admin', 'shield', '/admin', 'G A', true],
  ['Audit log', 'history', '/admin/audit', '', true],
  ['People', 'users', '/admin/people', '', true],
  ['Keyboard shortcuts', 'keyboard', () => showShortcuts(), '?'],
  ['Sign out', 'logout', () => signOut()],
];
export async function openPalette() {
  if (document.querySelector('.palette')) return;
  const input = h('input', { type: 'text', placeholder: 'Search sites and actions…', 'aria-label': 'Search sites and actions', role: 'combobox', 'aria-expanded': 'true', 'aria-controls': 'pal-list', autocomplete: 'off', autofocus: true });
  const listEl = h('div.list#pal-list', { role: 'listbox' });
  let items = [], sel = 0, sites = state.sites || [];
  const go = (it) => { m.close(); typeof it.to === 'function' ? it.to() : navigate(it.to); };
  const draw = () => {
    const q = input.value.trim().toLowerCase();
    const acts = ACTIONS.filter((a) => !a[4] || state.me?.isAdmin).map(([label, ic, to, k]) => ({ label, ic, to, k }));
    const ss = sites.map((s) => ({ label: s.title, site: s, to: ['viewer'].includes(s.role) ? null : '/sites/' + s.slug, k: '/s/' + s.slug }));
    const f = (x) => !q || x.label.toLowerCase().includes(q) || (x.k || '').toLowerCase().includes(q);
    const fs = ss.filter(f).slice(0, 8), fa = acts.filter(f);
    items = [...fs, ...fa];
    sel = Math.min(sel, Math.max(0, items.length - 1));
    const opt = (it, i) => h('div.option', { role: 'option', id: 'pal-' + i, 'aria-selected': String(i === sel), onmousemove: () => { if (sel !== i) { sel = i; draw(); } }, onclick: () => go(it) },
      it.site ? siteIcon(it.site, 'sm') : h('span.site-icon.sm', icon(it.ic, 16)), h('span.n', h('b', it.label)), it.k && h('span.k', it.k));
    clear(listEl,
      fs.length && h('div.grp', 'Sites'), fs.map((it, i) => opt(it, i)),
      fa.length && h('div.grp', 'Actions'), fa.map((it, i) => opt(it, i + fs.length)),
      !items.length && h('div.none', 'Nothing matches “' + input.value + '”.'));
    input.setAttribute('aria-activedescendant', items.length ? 'pal-' + sel : '');
    listEl.querySelector('[aria-selected=true]')?.scrollIntoView({ block: 'nearest' });
  };
  input.addEventListener('input', () => { sel = 0; draw(); });
  input.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); sel = (sel + 1) % Math.max(1, items.length); draw(); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); sel = (sel - 1 + items.length) % Math.max(1, items.length); draw(); }
    else if (e.key === 'Enter' && items[sel]) {
      e.preventDefault();
      const it = items[sel];
      if (it.site && !it.to) { m.close(); location.href = '/s/' + it.site.slug + '/'; } else go(it);
    }
  });
  const m = modal({ bare: true, cls: 'palette', top: true, label: 'Command palette', body: [h('div.q', icon('search', 20), input, h('kbd', 'Esc')), listEl] });
  draw();
  sites = await loadSites();
  draw();
}

export function showShortcuts() {
  const k = (...keys) => h('span', keys.map((x) => h('kbd', x)));
  const mod = isMac ? '⌘' : 'Ctrl';
  modal({ title: 'Keyboard shortcuts', body: h('div.shortcuts',
    'Jump to a site or action', k(mod, 'K'),
    'Search your sites', k('/'),
    'New site', k('N'),
    'Go to Home', k('G', 'H'),
    'Go to Templates', k('G', 'T'),
    'Go to Settings', k('G', 'S'),
    'Save the file you’re editing', k(mod, 'S'),
    'Send a message to the builder', k(mod, 'Enter'),
    'Close a dialog', k('Esc'),
    'Show this list', k('?')) });
}

export async function signOut(everywhere) {
  if (everywhere) await post('/api/v1/me/logout-everywhere', {});
  await fetch('/auth/logout', { method: 'POST', credentials: 'same-origin', headers: { 'X-Bunker': '1', Accept: 'application/json' } }).catch(() => {});
  location.href = '/auth/signed-out';
}

// ---------- keyboard ----------
let gPending = 0;
document.addEventListener('keydown', (e) => {
  const t = e.target;
  const typing = t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName);
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
    if (!state.me) return;
    e.preventDefault();
    openPalette();
    return;
  }
  if (typing || e.metaKey || e.ctrlKey || e.altKey || document.querySelector('.backdrop') || !state.me) return;
  if (e.key === '?') { e.preventDefault(); showShortcuts(); return; }
  if (e.key === '/') {
    const s = document.querySelector('[data-search]');
    if (s) { e.preventDefault(); s.focus(); s.select(); }
    return;
  }
  if (Date.now() - gPending < 1200) {
    gPending = 0;
    const to = { h: '/', t: '/templates', s: '/settings', a: state.me.isAdmin ? '/admin' : null }[e.key.toLowerCase()];
    if (to) { e.preventDefault(); navigate(to); }
    return;
  }
  if (e.key === 'g') { gPending = Date.now(); return; }
  if (e.key === 'n' && !location.pathname.startsWith('/sites/')) { e.preventDefault(); navigate('/new'); }
});

// ---------- boot ----------
(async function boot() {
  const path = location.pathname.replace(/\/+$/, '') || '/';
  if (!PUBLIC.includes(path)) {
    try {
      state.me = await get('/api/v1/me', { quiet: true });
    } catch (e) {
      if (e.status === 401) return;
      clear(app, h('div.bare', h('div.bare-card.sm', h('h1', 'Bunker isn’t answering'), h('p.lead', e.message), h('p', { style: { marginTop: '20px' } }, h('a.btn.primary', { href: location.href, 'data-native': '' }, 'Try again')))));
      return;
    }
    if (await loadNotes()) liveNotes();
  }
  start();
})();

export { $ };
