// Tiny DOM + UI kit: h(), icons, toasts, dialogs, helpers.

const PROPS = { value: 1, checked: 1, selected: 1, indeterminate: 1 };

export function h(tag, attrs, ...kids) {
  let id;
  const hi = tag.indexOf('#');
  if (hi >= 0) { id = tag.slice(hi + 1); tag = tag.slice(0, hi); }
  const [t, ...cls] = tag.split('.');
  const el = document.createElement(t || 'div');
  if (id) el.id = id;
  if (cls.length) el.className = cls.join(' ');
  if (attrs != null && (typeof attrs !== 'object' || attrs instanceof Node || Array.isArray(attrs))) {
    kids.unshift(attrs);
    attrs = null;
  }
  for (const k in attrs || {}) {
    const v = attrs[k];
    if (v == null || v === false) continue;
    if (k[0] === 'o' && k[1] === 'n' && typeof v === 'function') el.addEventListener(k.slice(2).toLowerCase(), v);
    else if (k === 'class') el.className += (el.className ? ' ' : '') + v;
    else if (k === 'style' && typeof v === 'object') for (const s in v) el.style.setProperty(s.startsWith('--') ? s : s.replace(/[A-Z]/g, (c) => '-' + c.toLowerCase()), v[s]);
    else if (k === 'html') el.innerHTML = v;
    else if (k === 'ref') v(el);
    else if (PROPS[k]) el[k] = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
  add(el, kids);
  return el;
}

export function add(el, kids) {
  for (const k of Array.isArray(kids) ? kids : [kids]) {
    if (k == null || k === false || k === true) continue;
    if (Array.isArray(k)) add(el, k);
    else el.append(k instanceof Node ? k : String(k));
  }
  return el;
}

export const $ = (s, r = document) => r.querySelector(s);
export const $$ = (s, r = document) => [...r.querySelectorAll(s)];
export function clear(el, ...kids) { el.replaceChildren(); return add(el, kids); }

// ---- icons (24px grid, stroke) ----
const P = {
  home: 'M3 10.5 12 3l9 7.5V20a1 1 0 0 1-1 1h-5v-6h-6v6H4a1 1 0 0 1-1-1z',
  plus: 'M12 5v14M5 12h14',
  grid: 'M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z',
  trash: 'M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3',
  gear: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z',
  shield: 'M12 3 4 6v6c0 5 3.5 8 8 9 4.5-1 8-4 8-9V6z',
  search: 'M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14zM20 20l-4-4',
  star: 'm12 3 2.8 5.7 6.2.9-4.5 4.4 1 6.2L12 17.3 6.5 20.2l1-6.2L3 9.6l6.2-.9z',
  upload: 'M12 16V4M7 9l5-5 5 5M4 16v3a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-3',
  sparkle: 'M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8zM19 16l.8 2.2L22 19l-2.2.8L19 22l-.8-2.2L16 19l2.2-.8z',
  folder: 'M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z',
  file: 'M14 3H6a1 1 0 0 0-1 1v16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8zM14 3v5h5',
  share: 'M16 8a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM6 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM16 22a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM8.6 13.5l4.8 2.9M13.4 6.6 8.6 9.5',
  users: 'M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM2 21v-1a6 6 0 0 1 12 0v1M16 3.1a4 4 0 0 1 0 7.8M22 21v-1a6 6 0 0 0-4-5.6',
  ext: 'M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5',
  eye: 'M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z',
  phone: 'M7 2h10a1 1 0 0 1 1 1v18a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1zM11 18h2',
  tablet: 'M5 3h14a1 1 0 0 1 1 1v16a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1zM11 18h2',
  desktop: 'M3 4h18a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1zM8 21h8M12 17v4',
  refresh: 'M20 11a8 8 0 1 0-2.3 5.7M20 4v7h-7',
  x: 'M6 6l12 12M18 6 6 18',
  check: 'm5 12.5 4.5 4.5L19 7',
  copy: 'M9 9h10a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H9a1 1 0 0 1-1-1V10a1 1 0 0 1 1-1zM5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1',
  clock: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 7v5l3 2',
  history: 'M3 12a9 9 0 1 0 3-6.7L3 8M3 3v5h5M12 7v5l3 2',
  code: 'm8 7-5 5 5 5M16 7l5 5-5 5M14 4l-4 16',
  db: 'M12 8c4.4 0 8-1.3 8-3s-3.6-3-8-3-8 1.3-8 3 3.6 3 8 3zM4 5v14c0 1.7 3.6 3 8 3s8-1.3 8-3V5M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3',
  chat: 'M4 5h16a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1h-9l-5 4v-4H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1z',
  sliders: 'M4 6h10M18 6h2M4 12h4M12 12h8M4 18h12M20 18h0M14 4v4M8 10v4M16 16v4',
  left: 'm15 6-6 6 6 6',
  down: 'm6 9 6 6 6-6',
  right: 'm9 6 6 6-6 6',
  user: 'M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM4 21a8 8 0 0 1 16 0',
  key: 'M15 9a4 4 0 1 1-8 0 4 4 0 0 1 8 0zM14 12l7 7M18 16l2-2M16 14l2-2',
  plug: 'M9 2v5M15 2v5M6 7h12v4a6 6 0 0 1-12 0zM12 17v5',
  logout: 'M15 4h4a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1h-4M10 16l-4-4 4-4M6 12h10',
  arrow: 'M5 12h14M13 6l6 6-6 6',
  undo: 'M9 14 4 9l5-5M4 9h10a6 6 0 0 1 0 12h-3',
  info: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 11v5M12 8h0',
  warn: 'M12 3 2 20h20zM12 10v4M12 17h0',
  menu: 'M4 7h16M4 12h16M4 17h16',
  globe: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM3 12h18M12 3c2.5 2.5 3.8 5.5 3.8 9s-1.3 6.5-3.8 9c-2.5-2.5-3.8-5.5-3.8-9S9.5 5.5 12 3z',
  lock: 'M6 11h12a1 1 0 0 1 1 1v8a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1v-8a1 1 0 0 1 1-1zM8 11V7a4 4 0 0 1 8 0v4',
  send: 'M4 12 20 4l-6 16-3-7z',
  bell: 'M6 9a6 6 0 0 1 12 0c0 6 2.5 7.5 2.5 7.5h-17S6 15 6 9zM10 20a2 2 0 0 0 4 0',
  chart: 'M4 20V10M10 20V4M16 20v-7M22 20H2',
  mail: 'M3 6h18v12H3zM3 6l9 7 9-7',
  terminal: 'M4 5h16a1 1 0 0 1 1 1v12a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zM7 9l3 3-3 3M12 15h5',
  keyboard: 'M3 6h18a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1zM6 10h0M10 10h0M14 10h0M18 10h0M7 14h10',
  dots: 'M5 12h0M12 12h0M19 12h0',
  book: 'M4 4h6a3 3 0 0 1 3 3v13a2 2 0 0 0-2-2H4zM20 4h-6a3 3 0 0 0-1 .2M20 4v14h-7',
  pencil: 'M4 20h4L19 9l-4-4L4 16zM14 6l4 4',
};
export function icon(name, size = 18, cls = '') {
  const ns = 'http://www.w3.org/2000/svg';
  const s = document.createElementNS(ns, 'svg');
  s.setAttribute('viewBox', '0 0 24 24');
  s.setAttribute('width', size);
  s.setAttribute('height', size);
  s.setAttribute('fill', 'none');
  s.setAttribute('stroke', 'currentColor');
  s.setAttribute('stroke-width', '1.8');
  s.setAttribute('stroke-linecap', 'round');
  s.setAttribute('stroke-linejoin', 'round');
  s.setAttribute('aria-hidden', 'true');
  s.setAttribute('class', 'i ' + cls);
  const p = document.createElementNS(ns, 'path');
  p.setAttribute('d', P[name] || P.info);
  s.append(p);
  return s;
}

// Friendly line drawing of the Bunker arch, used in empty states.
export function archArt(w = 92) {
  const d = document.createElement('div');
  d.className = 'art';
  d.innerHTML = `<svg width="${w}" height="${Math.round(w * .8)}" viewBox="0 0 92 74" fill="none" aria-hidden="true"><path d="M14 66V40a32 32 0 0 1 64 0v26" stroke="currentColor" stroke-width="3" stroke-linecap="round"/><path d="M36 66V52a10 10 0 0 1 20 0v14" stroke="currentColor" stroke-width="3" stroke-linecap="round"/><path d="M6 66h80" stroke="#c2683f" stroke-width="3.5" stroke-linecap="round"/><path d="M46 4v10M30 9l4 8M62 9l-4 8" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" opacity=".45"/></svg>`;
  return d;
}

// ---- formatting ----
// Times come as unix seconds, milliseconds or ISO strings (data rows).
const toMs = (ts) => (typeof ts === 'string' ? (/^\d+$/.test(ts) ? toMs(+ts) : Date.parse(ts)) : ts > 1e12 ? ts : ts * 1000);
export function relTime(ts) {
  if (!ts) return '';
  const ms = toMs(ts);
  if (!ms) return '';
  const s = Math.round((Date.now() - ms) / 1000);
  if (s < 45) return 'just now';
  const m = Math.round(s / 60);
  if (m < 60) return m === 1 ? 'a minute ago' : m + ' minutes ago';
  const hr = Math.round(m / 60);
  if (hr < 24) return hr === 1 ? 'an hour ago' : hr + ' hours ago';
  const d = Math.round(hr / 24);
  if (d === 1) return 'yesterday';
  if (d < 7) return d + ' days ago';
  const dt = new Date(ms);
  const opts = { month: 'short', day: 'numeric' };
  if (dt.getFullYear() !== new Date().getFullYear()) opts.year = 'numeric';
  return dt.toLocaleDateString(undefined, opts);
}
export function fullTime(ts) {
  if (!ts) return '';
  const ms = toMs(ts);
  return ms ? new Date(ms).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' }) : '';
}
export function bytes(n) {
  if (n == null) return '';
  if (n < 1024) return n + ' B';
  if (n < 1048576) return (n / 1024).toFixed(n < 10240 ? 1 : 0) + ' KB';
  if (n < 1073741824) return (n / 1048576).toFixed(1) + ' MB';
  return (n / 1073741824).toFixed(1) + ' GB';
}
export function plural(n, one, many) { return n + ' ' + (n === 1 ? one : many || one + 's'); }
export const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);

const TONES = ['#2f5543', '#4f7a63', '#b5653f', '#7a6a3e', '#3e6a74', '#6b5a7a', '#8a4f4f', '#4f6b8a'];
function hash(s) { let x = 0; for (const c of String(s)) x = (x * 31 + c.codePointAt(0)) | 0; return Math.abs(x); }
export function tone(s) { return TONES[hash(s) % TONES.length]; }
export function initials(s) {
  const w = String(s || '?').replace(/@.*$/, '').replace(/\(.*?\)/g, '').replace(/[^\p{L}\p{N}\s._-]/gu, '').trim().split(/[\s._-]+/).filter(Boolean);
  if (!w.length) return '?';
  return ((w[0]?.[0] || '?') + (w.length > 1 ? w[w.length - 1][0] : w[0]?.[1] || '')).toUpperCase();
}
export function avatar(name, cls = '') {
  return h('span.avatar' + (cls ? '.' + cls : ''), { style: { background: tone(name) }, 'aria-hidden': 'true' }, initials(name));
}
export function siteIcon(site, size = '') {
  const c = size ? '.' + size : '';
  if (site.icon) return h('span.site-icon' + c, { 'aria-hidden': 'true' }, site.icon);
  return h('span.site-icon.initials' + c, { style: { background: tone(site.slug || site.title) }, 'aria-hidden': 'true' }, initials(site.title));
}
export function roleLabel(site, me) {
  if (me && site.ownerId === me.id) return 'Owner';
  return { owner: 'Owner', editor: 'Editor', viewer: 'Viewer', admin: 'Admin' }[site.role] || '';
}
export const canEdit = (site) => ['editor', 'owner', 'admin'].includes(site?.role);
export const isOwner = (site, me) => site?.role === 'owner' || site?.role === 'admin' || (me && site?.ownerId === me.id);

export function debounce(fn, ms = 200) {
  let t;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}
export function store(k, v) {
  try {
    if (v === undefined) return localStorage.getItem('bk.' + k);
    if (v === null) localStorage.removeItem('bk.' + k); else localStorage.setItem('bk.' + k, v);
  } catch { return null; }
}

// ---- toasts ----
let toastsEl;
export function toast(msg, opt = {}) {
  if (!toastsEl) {
    toastsEl = h('div.toasts', { role: 'status', 'aria-live': 'polite' });
    document.body.append(toastsEl);
  }
  const type = opt.type || 'info';
  // The same message again: drop the old copy instead of stacking.
  for (const t of toastsEl.children) if (t.dataset.msg === String(msg)) t.remove();
  let timer;
  const close = () => {
    clearTimeout(timer);
    el.classList.add('out');
    setTimeout(() => el.remove(), 200);
  };
  const el = h('div.toast.' + type,
    icon(type === 'error' || type === 'warn' ? 'warn' : type === 'ok' ? 'check' : 'info', 18, 'lead'),
    h('div.msg-t', msg),
    opt.action && h('button.btn.sm', { onclick: () => { close(); opt.action.run(); } }, opt.action.label),
    h('button.x', { 'aria-label': 'Dismiss', onclick: close }, icon('x', 16)));
  el.dataset.msg = String(msg);
  toastsEl.append(el);
  while (toastsEl.children.length > 3) toastsEl.firstChild.remove();
  const ms = opt.timeout ?? (opt.action ? 7000 : type === 'error' ? 6500 : 4000);
  if (ms) timer = setTimeout(close, ms);
  return close;
}

// ---- dialogs ----
const FOCUSABLE = 'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])';
export function modal({ title, body, footer, cls = '', label, onClose, top = false, bare = false }) {
  const prev = document.activeElement;
  const titleId = 'dlg' + Math.random().toString(36).slice(2, 8);
  const dlg = h('div.dialog' + (cls ? '.' + cls : '') + (top ? '.top' : ''), { role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': title ? titleId : null, 'aria-label': title ? null : label });
  if (bare) add(dlg, [body]);
  else add(dlg, [
    h('header', h('h2', { id: titleId }, title), h('button.btn.ghost.icon.sm', { 'aria-label': 'Close', onclick: () => close() }, icon('x'))),
    h('div.body', body),
    footer && h('footer', footer)]);
  const bd = h('div.backdrop', { onmousedown: (e) => { if (e.target === bd) close(); } }, dlg);
  let closed = false;
  function close(v) {
    if (closed) return;
    closed = true;
    document.removeEventListener('keydown', onKey, true);
    bd.classList.add('closing');
    setTimeout(() => bd.remove(), 150);
    if (prev && prev.focus) prev.focus();
    onClose && onClose(v);
  }
  function onKey(e) {
    if (e.key === 'Escape') { e.stopPropagation(); e.preventDefault(); close(); }
    else if (e.key === 'Tab') {
      const f = $$(FOCUSABLE, dlg).filter((x) => x.offsetParent !== null);
      if (!f.length) return;
      if (e.shiftKey && document.activeElement === f[0]) { e.preventDefault(); f[f.length - 1].focus(); }
      else if (!e.shiftKey && document.activeElement === f[f.length - 1]) { e.preventDefault(); f[0].focus(); }
    }
  }
  document.addEventListener('keydown', onKey, true);
  document.body.append(bd);
  requestAnimationFrame(() => {
    const auto = dlg.querySelector('[autofocus]') || $$(FOCUSABLE, dlg.querySelector('.body') || dlg)[0];
    (auto || dlg).focus();
  });
  return { el: dlg, close };
}

export function confirmDialog({ title, message, confirm = 'Continue', danger = false, cancel = 'Cancel' }) {
  return new Promise((resolve) => {
    let ok = false;
    const m = modal({
      title, body: h('p.muted', message),
      footer: [h('button.btn', { onclick: () => m.close() }, cancel),
        h('button.btn' + (danger ? '.danger.solid' : '.primary'), { autofocus: true, onclick: () => { ok = true; m.close(); } }, confirm)],
      onClose: () => resolve(ok),
    });
  });
}

export function promptDialog({ title, label, value = '', placeholder = '', confirm = 'Save', hint, type = 'text' }) {
  return new Promise((resolve) => {
    let out = null;
    const input = h('input.input', { type, value, placeholder, autofocus: true, 'aria-label': label });
    const submit = (e) => { e.preventDefault(); out = input.value.trim(); m.close(); };
    const m = modal({
      title,
      body: h('form', { onsubmit: submit }, h('label.field', h('span.label', label), input, hint && h('span.hint', hint))),
      footer: [h('button.btn', { onclick: () => m.close() }, 'Cancel'), h('button.btn.primary', { onclick: submit }, confirm)],
      onClose: () => resolve(out),
    });
    setTimeout(() => input.select(), 30);
  });
}

export async function copyText(text, what = 'Copied') {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const t = h('textarea', { style: { position: 'fixed', opacity: '0' } }, text);
    document.body.append(t);
    t.select();
    try { document.execCommand('copy'); } catch {}
    t.remove();
  }
  toast(what, { type: 'ok', timeout: 2200 });
}
export function copyBtn(text, label = 'Copy', cls = 'btn.sm') {
  const b = h('button.' + cls, { type: 'button', onclick: () => {
    copyText(typeof text === 'function' ? text() : text, 'Copied to clipboard');
    b.replaceChildren(icon('check', 16), label && 'Copied');
    setTimeout(() => b.replaceChildren(icon('copy', 16), label), 1600);
  } }, icon('copy', 16), label);
  if (!label) b.setAttribute('aria-label', 'Copy');
  return b;
}
export function codeBlock(text) {
  return h('div.code', text, copyBtn(text, '', 'btn.sm.icon.copy'));
}

export function spinner() { return h('span.spin', { 'aria-hidden': 'true' }); }
export async function busy(btn, fn) {
  if (btn.disabled) return;
  const old = [...btn.childNodes];
  btn.disabled = true;
  btn.setAttribute('aria-busy', 'true');
  btn.replaceChildren(spinner(), ...old.filter((n) => n.nodeType === 3));
  try { return await fn(); }
  finally { btn.disabled = false; btn.removeAttribute('aria-busy'); btn.replaceChildren(...old); }
}

export function empty({ title, text, actions, art = true, iconName }) {
  return h('div.empty', art && (iconName ? h('div.art', icon(iconName, 40)) : archArt(76)), h('h3', title), text && h('p', text), actions && h('div.row.wrap', actions));
}
export function skeletons(n = 4, cls = '') { return Array.from({ length: n }, (_, i) => h('div.skel' + cls, { style: { animationDelay: i * 80 + 'ms' } })); }

export function toggle({ checked, onchange, label, disabled }) {
  return h('label.switch', h('input', { type: 'checkbox', role: 'switch', checked: !!checked, disabled, 'aria-label': label, onchange: (e) => onchange(e.target.checked, e.target) }), h('i'));
}

export function celebrate() {
  if (matchMedia('(prefers-reduced-motion: reduce)').matches) return;
  const c = h('div.confetti', { 'aria-hidden': 'true' });
  const colors = ['#c2683f', '#4f7a63', '#1f3a2e', '#e9c46a', '#8fbf9f', '#e08a5f'];
  for (let i = 0; i < 70; i++) {
    c.append(h('i', { style: { left: Math.random() * 100 + 'vw', background: colors[i % colors.length], animationDelay: Math.random() * .35 + 's', '--dx': (Math.random() * 160 - 80) + 'px', '--rot': (Math.random() * 720 - 360) + 'deg' } }));
  }
  document.body.append(c);
  setTimeout(() => c.remove(), 2400);
}

// A tiny inline SVG sparkline.
export function sparkline(values, w = 160, hgt = 36) {
  const max = Math.max(1, ...values);
  const n = values.length;
  const pts = values.map((v, i) => [n < 2 ? w : (i / (n - 1)) * w, hgt - 3 - (v / max) * (hgt - 6)]);
  const line = pts.map((p, i) => (i ? 'L' : 'M') + p[0].toFixed(1) + ' ' + p[1].toFixed(1)).join('');
  const d = document.createElement('div');
  d.className = 'spark';
  d.innerHTML = `<svg width="${w}" height="${hgt}" viewBox="0 0 ${w} ${hgt}" preserveAspectRatio="none" aria-hidden="true"><path d="${line}L${w} ${hgt}L0 ${hgt}Z" fill="currentColor" opacity=".12"/><path d="${line}" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/></svg>`;
  return d;
}
