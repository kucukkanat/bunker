// Site workspace: tabs on the left, live preview on the right.
import { h, icon, clear, siteIcon, toast, confirmDialog, celebrate, busy, empty, archArt, canEdit, isOwner, promptDialog } from '../ui.js';
import { get, post, del, site as sp, upload, ApiError } from '../api.js';
import { state } from '../state.js';
import { navigate, setQuery } from '../router.js';
import { fromDataTransfer, onWindowDrag, toForm, describe, pickers } from '../drop.js';
import { bell } from '../main.js';
import { warnToast } from '../components.js';
import { fnStatus, siteBlock, fnOffBadge } from '../functions.js';

const TABS = [
  ['build', 'Build', 'chat'],
  ['files', 'Files', 'code'],
  ['data', 'Data', 'db'],
  ['functions', 'Functions', 'terminal'],
  ['versions', 'Versions', 'history'],
  ['settings', 'Settings', 'sliders'],
];
const LOAD = {
  build: () => import('./ws-build.js'),
  files: () => import('./ws-files.js'),
  data: () => import('./ws-data.js'),
  functions: () => import('./ws-functions.js'),
  versions: () => import('./ws-versions.js'),
  settings: () => import('./ws-settings.js'),
};

export function menu(btn, items) {
  const close = () => { pop.remove(); btn.setAttribute('aria-expanded', 'false'); document.removeEventListener('mousedown', out, true); document.removeEventListener('keydown', key, true); };
  const out = (e) => { if (!pop.contains(e.target) && !btn.contains(e.target)) close(); };
  const key = (e) => {
    const its = [...pop.querySelectorAll('button')];
    const i = its.indexOf(document.activeElement);
    if (e.key === 'Escape') { close(); btn.focus(); }
    else if (e.key === 'ArrowDown') { e.preventDefault(); its[(i + 1) % its.length].focus(); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); its[(i - 1 + its.length) % its.length].focus(); }
  };
  const pop = h('div.popover.menu', { role: 'menu' }, items.filter(Boolean).map(([label, ic, run, danger]) =>
    h('button.mi' + (danger ? '.danger' : ''), { role: 'menuitem', onclick: () => { close(); run(); } }, icon(ic, 16), label)));
  document.body.append(pop);
  const r = btn.getBoundingClientRect();
  pop.style.top = r.bottom + 6 + 'px';
  pop.style.right = Math.max(8, innerWidth - r.right) + 'px';
  btn.setAttribute('aria-expanded', 'true');
  setTimeout(() => { document.addEventListener('mousedown', out, true); document.addEventListener('keydown', key, true); pop.querySelector('button')?.focus(); });
}

export default async function workspace(root, ctx) {
  let slug = ctx.params[0];
  let s;
  try {
    s = await get(sp(slug), { quiet: true });
  } catch (e) {
    if (!ctx.alive()) return;
    clear(root, h('div.bare', h('a.brand', { href: '/' }, h('img', { src: '/_/icon.svg', alt: '' }), 'bunker'),
      h('div.bare-card.sm', archArt(80), h('h1', e.status === 404 ? 'We can’t find that site' : 'Couldn’t open this site'),
        h('p.lead', e.status === 404 ? 'It may have been deleted or renamed, or it hasn’t been shared with you.' : e.message),
        h('p', { style: { marginTop: '20px' } }, h('a.btn.primary', { href: '/' }, 'Go to Home')))));
    return;
  }
  if (!ctx.alive()) return;
  if (!canEdit(s)) { location.replace('/s/' + s.slug + '/'); return; }
  document.title = s.title + ' · Bunker';

  const timers = [];
  const offs = [];
  let tab = TABS.some((t) => t[0] === ctx.query.get('tab')) ? ctx.query.get('tab') : (s.version || s.extra?.hasDraft ? 'build' : 'build');
  let viewVersion = null; // previewing an old version
  let device = 'desktop';
  let sig = '';
  let openLink;

  // ---------- top bar ----------
  const titleEl = h('h1');
  const addrEl = h('div.addr');
  const iconSlot = h('span');
  const statusEl = h('span.badge');
  const fnEl = h('span.fn-top');
  const publicEl = h('span.badge.draft.hidden', { title: 'Anyone with the link can view it, without signing in' }, icon('globe', 12), 'Public');
  const publishBtn = h('button.btn.primary.publish', { 'aria-label': 'Publish', onclick: () => publish() }, icon('upload', 16), h('span.label', 'Publish'));
  const discardBtn = h('button.btn.ghost', { onclick: () => discard() }, h('span.label', 'Discard'), h('span.sr-only', ' changes'));
  const shareBtn = h('button.btn', { 'aria-label': 'Share', onclick: () => openShare() }, icon('share', 16), h('span.label', 'Share'));
  const moreBtn = h('button.btn.icon', { 'aria-label': 'More actions', 'aria-haspopup': 'menu', 'aria-expanded': 'false', onclick: () => menu(moreBtn, [
    ['Open in a new tab', 'ext', () => open('/s/' + s.slug + '/' + (ws.hasDraft ? '?draft=1' : ''), '_blank', 'noopener')],
    ['Upload files as a draft', 'upload', () => pk.inputs[0].click()],
    ['Upload a folder as a draft', 'folder', () => pk.inputs[1].click()],
    ['Duplicate', 'copy', duplicate],
    ['Download as zip', 'down', () => { location.href = sp(s.slug, '/export.zip'); }],
    isOwner(s, state.me) && ['Move to trash', 'trash', () => ws.trash(), true],
  ]) }, icon('dots', 20));
  const viewSwitch = h('div.seg.mobile-switch', { role: 'group', 'aria-label': 'Show' },
    h('button', { 'aria-pressed': 'true', onclick: () => setView('edit') }, 'Edit'),
    h('button', { 'aria-pressed': 'false', onclick: () => setView('preview') }, 'Preview'));
  const top = h('header.ws-top',
    h('a.btn.ghost.icon', { href: '/', 'aria-label': 'Back to Home' }, icon('left', 20)),
    h('div.title', iconSlot, h('div', titleEl, addrEl)),
    statusEl, publicEl, fnEl,
    h('div.acts', viewSwitch, bell(), discardBtn, moreBtn, shareBtn, publishBtn));

  // ---------- left: tabs ----------
  const tabBtns = TABS.map(([id, label, ic]) => h('button.tab', { role: 'tab', id: 'tab-' + id, 'aria-controls': 'panel', 'aria-selected': 'false', tabindex: '-1', onclick: () => showTab(id) }, icon(ic, 16), label));
  const tabList = h('div.tabs', { role: 'tablist', 'aria-label': 'Workspace', onkeydown: (e) => {
    const i = TABS.findIndex((t) => t[0] === tab);
    const n = e.key === 'ArrowRight' ? i + 1 : e.key === 'ArrowLeft' ? i - 1 : null;
    if (n == null) return;
    e.preventDefault();
    const id = TABS[(n + TABS.length) % TABS.length][0];
    showTab(id);
    tabBtns[TABS.findIndex((t) => t[0] === id)].focus();
  } }, tabBtns);
  const panel = h('div.tab-panel#panel', { role: 'tabpanel', tabindex: '0' });
  const left = h('section.ws-left', { 'aria-label': 'Edit' }, tabList, panel);

  // ---------- right: preview ----------
  const frame = h('iframe', { title: 'Preview of ' + s.title, onload: () => loading.classList.add('hidden') });
  const frameBox = h('div.pv-frame', frame);
  const loading = h('div.pv-loading', h('i'));
  const banner = h('div.pv-banner.hidden');
  const pvEmpty = h('div.pv-empty.hidden', archArt(84), h('h3', 'Nothing to preview yet'),
    h('p.small', 'Describe what you want in Build, add files, or drop a folder anywhere on this page.'));
  const stage = h('div.pv-stage', loading, banner, frameBox, pvEmpty);
  const urlEl = h('span');
  const devBtns = [['phone', 'Phone'], ['tablet', 'Tablet'], ['desktop', 'Desktop']].map(([d, l]) =>
    h('button', { 'aria-pressed': String(d === device), 'aria-label': l + ' size', title: l, onclick: () => setDevice(d) }, icon(d, 16)));
  const right = h('section.ws-right', { 'aria-label': 'Preview' },
    h('div.pv-bar',
      h('div.seg', { role: 'group', 'aria-label': 'Preview size' }, devBtns),
      h('div.pv-url', icon('lock', 14), urlEl),
      h('button.btn.ghost.icon.sm', { 'aria-label': 'Reload preview', title: 'Reload', onclick: () => reloadPreview() }, icon('refresh', 16)),
      h('a.btn.ghost.icon.sm', { 'aria-label': 'Open in a new tab', title: 'Open in a new tab', target: '_blank', rel: 'noopener', ref: (a) => (openLink = a) }, icon('ext', 16))),
    stage);
  const body = h('div.ws-body', { 'data-view': 'edit' }, left, right);
  const pk = pickers((items) => deploy(items, false));
  clear(root, h('div.ws', top, body, pk.inputs));

  // ---------- shared workspace API for tabs ----------
  const ws = {
    get site() { return s; },
    get slug() { return s.slug; },
    hasDraft: !!s.extra?.hasDraft,
    me: state.me,
    alive: ctx.alive,
    reloadPreview: () => reloadPreview(),
    async refresh() {
      const n = await get(sp(s.slug), { quiet: true }).catch(() => null);
      if (n) { s = n; ws.hasDraft = !!n.extra?.hasDraft; paintTop(); updatePreview(); }
      return s;
    },
    setSite(n) {
      const moved = n.slug !== s.slug;
      s = { ...s, ...n, extra: s.extra };
      if (moved) setQuery({});
      if (moved) history.replaceState({}, '', '/sites/' + s.slug + location.search);
      paintTop();
      updatePreview();
    },
    previewVersion(n) { viewVersion = n; updatePreview(); },
    get viewVersion() { return viewVersion; },
    // Errors the draft preview reported (shell.js keeps the last 20).
    previewErrors() {
      try {
        const b = frame.contentWindow && frame.contentWindow.__bunkerErrors;
        return Array.isArray(b) ? b.map(({ message, source, line }) => ({ message, source, line })) : [];
      } catch { return []; }
    },
    // Resolves `ms` after the preview has finished loading (max ~8s wait).
    async previewSettled(ms = 2000) {
      for (let i = 0; i < 80 && !loading.classList.contains('hidden'); i++) await new Promise((r) => setTimeout(r, 100));
      await new Promise((r) => setTimeout(r, ms));
    },
    draftChanged() { ws.hasDraft = true; paintTop(); updatePreview(true); sig = ''; },
    filesSubs: new Set(),
    // Server functions turned off by an admin ({scope, reason} or null).
    setFnBlock(b) { clear(fnEl, b && b.scope !== 'global' ? fnOffBadge(b) : null); },
    upload: () => pk.inputs[1].click(),
    showTab: (t) => showTab(t),
    async trash() {
      if (!(await confirmDialog({ title: 'Move “' + s.title + '” to the trash?', message: 'People you shared it with lose access right away. You can restore it from the trash for 30 days.', confirm: 'Move to trash', danger: true }))) return;
      await del(sp(s.slug));
      state.sites = null;
      const id = s.id;
      navigate('/');
      toast('Moved “' + s.title + '” to the trash.', { action: { label: 'Undo', run: async () => { const r = await post('/api/v1/trash/' + id + '/restore', {}); state.sites = null; navigate('/sites/' + r.slug); } } });
    },
  };

  function paintTop() {
    clear(iconSlot, siteIcon(s, 'sm'));
    titleEl.textContent = s.title;
    addrEl.textContent = location.host + '/s/' + s.slug;
    document.title = s.title + ' · Bunker';
    const draft = ws.hasDraft;
    statusEl.className = 'badge dot ' + (draft ? 'draft' : s.version ? 'ok' : '');
    statusEl.textContent = draft ? 'Draft — not published yet' : s.version ? 'Live · version ' + s.version : 'Not published yet';
    if (s.archivedAt) { statusEl.className = 'badge dot'; statusEl.textContent = 'Archived — open the preview to restore it'; }
    publishBtn.disabled = !draft;
    publishBtn.title = draft ? 'Make your changes live for everyone you’ve shared with' : 'Nothing new to publish';
    discardBtn.classList.toggle('hidden', !draft);
    publicEl.classList.toggle('hidden', !s.public);
  }

  function previewSrc() {
    if (viewVersion) return '/s/' + s.slug + '/?v=' + viewVersion;
    if (ws.hasDraft) return '/s/' + s.slug + '/?draft=1';
    return s.version ? '/s/' + s.slug + '/' : '';
  }
  let lastSrc = '';
  function updatePreview(force) {
    const src = previewSrc();
    urlEl.textContent = src ? location.host + src.replace('?draft=1', '') + (ws.hasDraft && !viewVersion ? '  (draft)' : '') : location.host + '/s/' + s.slug + '/';
    openLink.href = src || '/s/' + s.slug + '/';
    pvEmpty.classList.toggle('hidden', !!src);
    frameBox.classList.toggle('hidden', !src);
    if (viewVersion) {
      clear(banner, icon('eye', 16), 'Previewing version ' + viewVersion, h('button.btn.sm', { onclick: () => ws.previewVersion(null) }, 'Back to current'));
      banner.classList.remove('hidden');
    } else banner.classList.add('hidden');
    if (!src) { lastSrc = ''; loading.classList.add('hidden'); return; }
    if (src !== lastSrc || force) { lastSrc = src; loading.classList.remove('hidden'); frame.src = src; }
  }
  function reloadPreview() { updatePreview(true); }
  function setDevice(d) {
    device = d;
    devBtns.forEach((b, i) => b.setAttribute('aria-pressed', String(['phone', 'tablet', 'desktop'][i] === d)));
    frameBox.className = 'pv-frame ' + (d === 'desktop' ? '' : d) + (frameBox.classList.contains('hidden') ? ' hidden' : '');
  }
  function setView(v) {
    body.dataset.view = v;
    viewSwitch.querySelectorAll('button').forEach((b, i) => b.setAttribute('aria-pressed', String((i === 0) === (v === 'edit'))));
  }

  let tabCleanup = null, tabSeq = 0;
  async function showTab(id) {
    tab = id;
    setQuery({ tab: id === 'build' ? null : id, share: null });
    tabBtns.forEach((b, i) => { const on = TABS[i][0] === id; b.setAttribute('aria-selected', String(on)); b.tabIndex = on ? 0 : -1; });
    panel.setAttribute('aria-labelledby', 'tab-' + id);
    body.classList.toggle('wide', id === 'files' || id === 'data' || id === 'functions');
    if (tabCleanup) { try { tabCleanup(); } catch {} tabCleanup = null; }
    const my = ++tabSeq;
    clear(panel, h('div', { style: { padding: '20px' } }, h('div.skel.line', { style: { width: '60%' } }), h('div.skel.line', { style: { width: '40%', marginTop: '12px' } })));
    const mod = await LOAD[id]();
    if (my !== tabSeq || !ctx.alive()) return;
    clear(panel);
    panel.scrollTop = 0;
    const c = await mod.default(panel, ws);
    if (my !== tabSeq) { if (typeof c === 'function') c(); return; }
    tabCleanup = typeof c === 'function' ? c : null;
  }

  async function publish() {
    await busy(publishBtn, async () => {
      const r = await post(sp(s.slug, '/draft/publish'), { message: 'Published changes' });
      ws.hasDraft = false;
      s.version = r.version;
      celebrate();
      toast('Published! Version ' + r.version + ' is now live.', { type: 'ok', action: { label: 'Open', run: () => open('/s/' + s.slug + '/', '_blank', 'noopener') } });
      viewVersion = null;
      sig = '';
      ws.filesSubs.forEach((f) => f('published'));
    });
    paintTop();
    updatePreview(true);
  }
  async function discard() {
    if (!(await confirmDialog({ title: 'Throw away your changes?', message: s.version ? 'The draft is deleted and the site goes back to the live version ' + s.version + '. This can’t be undone.' : 'The draft is deleted. This can’t be undone.', confirm: 'Discard changes', danger: true }))) return;
    await del(sp(s.slug, '/draft'));
    ws.hasDraft = false;
    sig = '';
    toast('Changes discarded.');
    paintTop();
    updatePreview(true);
    ws.filesSubs.forEach((f) => f('discarded'));
  }
  async function duplicate() {
    const title = await promptDialog({ title: 'Duplicate this site', label: 'Name for the copy', value: s.title + ' (copy)', confirm: 'Duplicate' });
    if (!title) return;
    try {
      const n = await post(sp(s.slug, '/duplicate'), { title }, { quiet: true });
      state.sites = null;
      toast('Made a copy called “' + n.title + '”.', { type: 'ok' });
      navigate('/sites/' + n.slug);
    } catch (e) {
      toast(e.missing ? 'Duplicating isn’t available on this server yet.' : e.message, { type: 'error' });
    }
  }
  async function openShare() {
    const m = await import('./share.js');
    m.openShare(ws, () => paintTop());
  }

  // ---------- drop a new version ----------
  let overlay;
  async function deploy(items, publishNow) {
    if (!items.length) return;
    const t = toast('Uploading ' + describe(items) + '…', { timeout: 0 });
    try {
      const r = await upload(sp(s.slug, '/deploy'), toForm(items, { publish: String(publishNow), message: 'Uploaded ' + describe(items) }));
      t();
      if (publishNow) { s.version = r.version; ws.hasDraft = false; celebrate(); toast('Uploaded and published as version ' + r.version + '.', { type: 'ok' }); }
      else { ws.hasDraft = true; toast('Uploaded as a draft. Check the preview, then press Publish.', { type: 'ok' }); }
      warnToast(r.warnings);
      sig = '';
      paintTop();
      updatePreview(true);
      ws.filesSubs.forEach((f) => f('uploaded'));
    } catch { t(); }
  }
  offs.push(onWindowDrag({
    enter() {
      const zone = (mode, title, text, ic) => h('div', { 'data-mode': mode, ondragover: (e) => { e.preventDefault(); e.currentTarget.classList.add('over'); }, ondragleave: (e) => e.currentTarget.classList.remove('over') }, icon(ic, 40), h('h2', title), h('p', text));
      overlay = h('div.ws-drop', zone('draft', 'Save as a draft', 'Replace the files, check the preview, then publish.', 'pencil'), zone('publish', 'Publish right away', 'Replace the files and make them live now.', 'upload'));
      document.body.append(overlay);
    },
    leave() { overlay?.remove(); },
    async drop(e) {
      const z = e.target.closest && e.target.closest('[data-mode]');
      overlay?.remove();
      if (!z) return;
      deploy(await fromDataTransfer(e.dataTransfer), z.dataset.mode === 'publish');
    },
  }));

  // ---------- watch for changes (builder, other editors, agents) ----------
  async function poll() {
    if (document.hidden || !ctx.alive()) return;
    const d = await get(sp(s.slug, '/files'), { quiet: true, noRedirect: true }).catch(() => null);
    if (!d) return;
    const n = (d.draft ? 'd:' : 'l:') + d.files.map((f) => f.path + f.hash).join('|');
    if (sig && n !== sig) {
      if (d.draft !== ws.hasDraft) await ws.refresh();
      else updatePreview(true);
      ws.filesSubs.forEach((f) => f('remote'));
    }
    sig = n;
  }
  timers.push(setInterval(poll, 4000));
  poll();

  paintTop();
  updatePreview();
  fnStatus().then(() => { if (ctx.alive() && siteBlock(s.slug)) ws.setFnBlock(siteBlock(s.slug)); });
  showTab(tab);
  if (ctx.query.get('share') === '1') openShare();

  return () => {
    timers.forEach(clearInterval);
    offs.forEach((f) => f());
    overlay?.remove();
    if (tabCleanup) tabCleanup();
    document.querySelectorAll('.popover.menu').forEach((p) => p.remove());
  };
}

export { ApiError, empty };
