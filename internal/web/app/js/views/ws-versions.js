// Versions tab: history, preview, restore, what changed.
import { h, icon, clear, empty, toast, confirmDialog, relTime, fullTime, bytes, busy, skeletons, plural } from '../ui.js';
import { get, post, site as sp } from '../api.js';

const SRC = { drop: 'Uploaded', upload: 'Uploaded', draft: 'Published draft', restore: 'Restored', template: 'From template', cli: 'From the command line', api: 'From a script', ai: 'AI builder', builder: 'AI builder', mcp: 'From an AI assistant' };

export default async function versions(panel, ws) {
  const wrap = h('div.vlist', skeletons(4, '.line'));
  clear(panel, wrap);

  async function load() {
    const d = await get(sp(ws.slug, '/versions')).catch(() => null);
    if (!d || !ws.alive()) return;
    const vs = d.versions || [];
    if (!vs.length) {
      return clear(wrap, empty({ title: 'No versions yet', text: 'Each time you publish, Bunker keeps a copy here. If something breaks, you can go back in one click.', art: false, iconName: 'history' }));
    }
    clear(wrap,
      ws.hasDraft && h('div.notice.warn', { style: { margin: '6px 6px 12px' } }, icon('pencil', 16), h('div', h('b', 'You have unpublished changes. '), 'They’ll become version ' + (vs[0].n + 1) + ' when you publish.')),
      vs.map((v) => row(v)));
  }

  function row(v) {
    const diffBox = h('div');
    const viewing = ws.viewVersion === v.n;
    const restore = h('button.btn.sm', { onclick: async () => {
      if (!(await confirmDialog({ title: 'Make version ' + v.n + ' live again?', message: 'Bunker publishes a copy of version ' + v.n + ' as a new version. Nothing is lost: today’s version stays in the history.', confirm: 'Restore version ' + v.n }))) return;
      await busy(restore, async () => {
        const r = await post(sp(ws.slug, '/versions/' + v.n + '/restore'), {});
        toast('Version ' + v.n + ' is live again (saved as version ' + r.version + ').', { type: 'ok' });
        ws.previewVersion(null);
        await ws.refresh();
        ws.reloadPreview();
        load();
      });
    } }, icon('undo', 15), 'Restore');
    const diffBtn = h('button.btn.sm.ghost', { 'aria-expanded': 'false', onclick: async () => {
      if (diffBox.firstChild) { clear(diffBox); diffBtn.setAttribute('aria-expanded', 'false'); return; }
      diffBtn.setAttribute('aria-expanded', 'true');
      const d = await get(sp(ws.slug, '/versions/' + v.n + '/diff')).catch(() => null);
      if (!d) return;
      const lines = [...d.added.map((p) => ['a', '+ ' + p]), ...d.changed.map((p) => ['c', '~ ' + p]), ...d.removed.map((p) => ['r', '− ' + p])];
      clear(diffBox, h('div.diff', lines.length ? lines.map(([c, t]) => h('div.' + c, t)) : h('div.faint', 'No files changed.'),
        h('div.faint', { style: { marginTop: '4px' } }, [d.added.length && plural(d.added.length, 'added'), d.changed.length && plural(d.changed.length, 'changed'), d.removed.length && plural(d.removed.length, 'removed')].filter(Boolean).join(' · ') || ' ')));
    } }, 'What changed');
    return h('div.ver' + (v.live ? '.live' : '') + (viewing ? '.viewing' : ''),
      h('span.dot', { 'aria-hidden': 'true' }, v.n),
      h('div',
        h('div.row.wrap', h('span.msgt', v.message || SRC[v.source] || 'Version ' + v.n), v.live && h('span.badge.ok', 'Live')),
        h('div.small.muted', { title: fullTime(v.createdAt) }, 'Version ' + v.n + ' · ' + relTime(v.createdAt) + (v.author ? ' · ' + v.author : '') + ' · ' + plural(v.files, 'file') + ', ' + bytes(v.size)),
        h('div.acts',
          h('button.btn.sm' + (viewing ? '.primary' : ''), { 'aria-pressed': String(viewing), onclick: () => { ws.previewVersion(viewing ? null : v.n); load(); } }, icon('eye', 15), viewing ? 'Previewing' : 'Preview'),
          !v.live && restore,
          v.n > 1 && diffBtn),
        diffBox));
  }

  const onChange = () => load();
  ws.filesSubs.add(onChange);
  await load();
  return () => { ws.filesSubs.delete(onChange); if (ws.viewVersion) ws.previewVersion(null); };
}
