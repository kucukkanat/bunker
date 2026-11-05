// Build tab: chat with the AI builder (SSE over fetch).
import { h, icon, clear, empty, toast } from '../ui.js';
import { get, stream, site as sp, list } from '../api.js';
import { state, aiStatus, aiReady } from '../state.js';

const SUGGEST = ['Make it mobile-friendly', 'Add a dark mode', 'Add a chart', 'Save entries to a table', 'Make it look more polished'];

const textOf = (m) => {
  const c = m.text ?? m.content ?? m.message ?? '';
  if (Array.isArray(c)) return c.map((b) => (typeof b === 'string' ? b : b.text || '')).join('');
  return typeof c === 'string' ? c : '';
};
const fileOf = (d) => (typeof d === 'string' ? d : d?.path || d?.file || d?.name || '');

export default async function build(panel, ws) {
  const log = h('div.chat-log', { role: 'log', 'aria-live': 'polite', 'aria-label': 'Conversation with the builder' });
  const input = h('textarea', { rows: 2, placeholder: 'Ask for a change, like “add a field for the department”', 'aria-label': 'Message to the builder' });
  const send = h('button.btn.primary.sm', { type: 'submit', 'aria-label': 'Send' }, icon('send', 16), 'Send');
  const chips = h('div.chips', SUGGEST.map((t) => h('button', { type: 'button', onclick: () => { input.value = t; submit(); } }, t)));
  const form = h('form.composer', { onsubmit: (e) => { e.preventDefault(); submit(); } },
    chips, h('div.box', input, h('div.bar', h('span.tiny.faint', 'Changes go to a draft. Nothing is live until you publish.'), h('span.spacer'), send)));
  const wrap = h('div.chat', log, form);
  clear(panel, wrap);
  let busy = false;
  let abort = null;

  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); submit(); }
  });
  input.addEventListener('input', () => { input.style.height = 'auto'; input.style.height = Math.min(200, input.scrollHeight) + 'px'; });

  const scroll = () => { panel.scrollTop = panel.scrollHeight; };
  const bubble = (who, text, files) => {
    const el = h('div.msg.' + who, text);
    if (files && files.length) el.append(h('div.files', files.map((f) => h('span.file-chip', icon('file', 12), f))));
    log.append(el);
    scroll();
    return el;
  };
  const intro = () => h('div.chat-intro',
    h('h2.h3', 'What should we change?'),
    h('p.muted.small', 'Tell the builder what you want in plain words. It edits the draft and the preview updates on the right. You can always go back to an earlier version.'));

  // Render an event stream into the log.
  function attach(onDone) {
    let ai = null, statusEl = null, files = [], filesEl = null;
    const setStatus = (t) => {
      if (!statusEl) { statusEl = h('div.status-line', h('span.dots', h('i'), h('i'), h('i')), h('span')); log.append(statusEl); }
      statusEl.lastChild.textContent = t || 'Working…';
      scroll();
    };
    setStatus('Thinking…');
    // The reply streams in token by token: screen readers wait for it to
    // finish (aria-busy) and then read it once.
    const end = () => { statusEl?.remove(); statusEl = null; ai?.removeAttribute('aria-busy'); };
    return (type, d) => {
      if (type === 'status') setStatus(typeof d === 'string' ? d : d?.text || d?.message || d?.status);
      else if (type === 'text' || type === 'delta' || type === 'message') {
        const t = typeof d === 'string' ? d : d?.text ?? d?.delta ?? d?.content ?? '';
        if (!t) return;
        if (!ai) { ai = bubble('ai', ''); ai.setAttribute('aria-busy', 'true'); if (statusEl) log.append(statusEl); }
        ai.firstChild ? (ai.firstChild.textContent += t) : ai.prepend(t);
        scroll();
      } else if (type === 'file') {
        const f = fileOf(d);
        if (!f || files.includes(f)) return;
        files.push(f);
        if (!ai) { ai = bubble('ai', ''); if (statusEl) log.append(statusEl); }
        if (!filesEl) { filesEl = h('div.files'); ai.append(filesEl); }
        filesEl.append(h('span.file-chip', icon(d?.action === 'delete' ? 'trash' : 'pencil', 12), f));
        setStatus('Updating ' + f + '…');
        ws.draftChanged();
      } else if (type === 'done') {
        end();
        const t = d && typeof d === 'object' ? d.summary || d.text : '';
        if (t && !ai) bubble('ai', t);
        if (!ai && !files.length && !t) bubble('ai', 'Done. Take a look at the preview.');
        ws.refresh();
        onDone && onDone(true);
      } else if (type === 'error') {
        end();
        bubble('err', (d && (d.message || d.error?.message || d.error)) || 'The builder ran into a problem. Please try again.');
        onDone && onDone(false);
      }
    };
  }

  // Auto-fix: after a build, look at errors the draft preview reported and
  // offer one click to send them to the builder (at most 2 rounds per message).
  const MAX_FIXES = 2;
  let fixRound = 0, fixEl = null;
  async function checkPreview() {
    await ws.previewSettled(2000);
    if (!ws.alive() || busy) return;
    const errs = ws.previewErrors();
    fixEl?.remove();
    fixEl = null;
    if (!errs.length) return;
    const n = errs.length;
    const list = h('ul.fix-list.small', errs.slice(-5).map((e) => h('li', h('code', e.message), e.source ? h('span.faint', ' · ' + e.source + (e.line ? ':' + e.line : '')) : null)));
    if (fixRound >= MAX_FIXES) {
      fixEl = bubble('ai', 'The preview still shows ' + (n === 1 ? 'an error' : n + ' errors') + '. Tell me what you see, or what should happen, and I’ll take another look.');
      fixEl.append(list);
      return;
    }
    fixEl = bubble('ai', 'The preview shows ' + (n === 1 ? 'an error' : n + ' errors') + '.');
    fixEl.classList.add('fix');
    fixEl.append(list, h('div', h('button.btn.sm.primary', { type: 'button', onclick: () => { fixRound++; fixEl?.remove(); fixEl = null; submit('Fix the errors the preview shows.', errs); } }, icon('sparkle', 14), 'Fix these errors')));
    scroll();
  }

  async function submit(auto, errors) {
    const msg = typeof auto === 'string' ? auto : input.value.trim();
    if (!msg || busy) return;
    log.querySelector('.chat-intro')?.remove();
    if (typeof auto !== 'string') { input.value = ''; input.style.height = ''; fixRound = 0; fixEl?.remove(); fixEl = null; }
    bubble('me', msg);
    setBusy(true);
    abort = new AbortController();
    const on = attach((ok) => { setBusy(false); if (ok) checkPreview(); });
    const body = { message: msg };
    if (errors && errors.length) body.errors = errors;
    try {
      await stream(sp(ws.slug, '/builder'), body, on, { quiet: true, signal: abort.signal });
    } catch (e) {
      if (e.name === 'AbortError') return;
      on('error', { message: e.missing ? 'The AI builder isn’t switched on yet. Ask your Bunker admin, or edit the files yourself.' : e.message });
    } finally { setBusy(false); }
  }
  function setBusy(b) { busy = b; send.disabled = b; chips.querySelectorAll('button').forEach((x) => (x.disabled = b)); }

  // History, or a stream handed over from Home ("Describe an app").
  const live = state.live[ws.slug];
  const [st, hist] = await Promise.all([aiStatus(), live ? null : get(sp(ws.slug, '/builder/history'), { quiet: true }).catch((e) => (e.missing ? 'missing' : null))]);
  if (!ws.alive()) return;
  if (!live && (hist === 'missing' || !aiReady(st))) {
    clear(log, empty({
      title: st === null || hist === 'missing' ? 'The AI builder is coming soon' : 'The AI builder is switched off',
      text: 'You can still change this site: edit its files, or drop a new version anywhere on this page.',
      art: false, iconName: 'sparkle',
      actions: [h('button.btn.primary', { onclick: () => ws.showTab('files') }, icon('code', 16), 'Edit files'), h('button.btn', { onclick: () => ws.upload() }, icon('upload', 16), 'Upload a folder')],
    }));
    form.classList.add('hidden');
    return () => abort?.abort();
  }
  const msgs = list(hist, 'messages', 'history', 'turns', 'items');
  for (const m of msgs) {
    const role = m.role === 'user' ? 'me' : 'ai';
    const t = textOf(m);
    if (t || m.files) bubble(role, t, (m.files || m.changes || []).map(fileOf));
  }
  if (live) {
    delete state.live[ws.slug];
    bubble('me', live.prompt);
    setBusy(true);
    const on = attach(() => setBusy(false));
    const off = live.hub.sub((t, d) => { if (t !== 'created' && t !== 'site') on(t, d); });
    if (live.hub.done) setBusy(false);
    return () => { off(); abort?.abort(); };
  }
  if (hist && hist.building) log.append(h('div.status-line', h('span.dots', h('i'), h('i'), h('i')), h('span', 'The builder is still working on the last request. The preview updates as it goes.')));
  if (!msgs.length) log.append(intro());
  if (matchMedia('(pointer: fine)').matches) setTimeout(() => input.focus(), 50);
  return () => abort?.abort();
}

export { toast };
