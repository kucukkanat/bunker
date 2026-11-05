// Files tab: file tree + lightweight code editor working on the draft.
import { h, icon, clear, toast, confirmDialog, promptDialog, bytes, busy, empty } from '../ui.js';
import { get, put, del, api, site as sp, filePath } from '../api.js';

const TEXT = /\.(html?|css|js|mjs|json|md|txt|svg|xml|csv|ts|jsx|tsx|yml|yaml|webmanifest)$/i;
const IMG = /\.(png|jpe?g|gif|webp|avif|ico|svg)$/i;

export default async function files(panel, ws) {
  const tree = h('nav.tree', { 'aria-label': 'Files' });
  const pane = h('div.editor-pane');
  clear(panel, h('div.files-wrap', tree, pane));
  let list = [], current = null, original = '', dirty = false, blobUrl = null, isDraft = false;

  const ta = h('textarea', { spellcheck: 'false', autocapitalize: 'off', autocomplete: 'off', wrap: 'off', 'aria-label': 'File contents' });
  const gutter = h('div.gutter', { 'aria-hidden': 'true' });
  let lines = 0;
  const paintGutter = () => {
    const n = ta.value.split('\n').length;
    if (n !== lines) { lines = n; gutter.textContent = Array.from({ length: n }, (_, i) => i + 1).join('\n'); }
  };
  ta.addEventListener('scroll', () => { gutter.scrollTop = ta.scrollTop; });
  ta.addEventListener('input', () => { paintGutter(); setDirty(ta.value !== original); });
  ta.addEventListener('keydown', (e) => {
    if (e.key === 'Tab' && !e.shiftKey && !e.ctrlKey && !e.metaKey) {
      // Indent; Esc then Tab still leaves the editor (see hint).
      if (ta.dataset.escaped) { delete ta.dataset.escaped; return; }
      e.preventDefault();
      document.execCommand('insertText', false, '  ') || ta.setRangeText('  ', ta.selectionStart, ta.selectionEnd, 'end');
    } else if (e.key === 'Escape') ta.dataset.escaped = '1';
    else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') { e.preventDefault(); save(); }
    else if (e.key === 'Enter' && !e.shiftKey) {
      // Keep the indentation of the current line.
      const before = ta.value.slice(0, ta.selectionStart);
      const indent = before.slice(before.lastIndexOf('\n') + 1).match(/^\s*/)[0];
      if (indent) { e.preventDefault(); document.execCommand('insertText', false, '\n' + indent) || ta.setRangeText('\n' + indent, ta.selectionStart, ta.selectionEnd, 'end'); paintGutter(); }
    }
  });
  const saveBtn = h('button.btn.primary.sm', { onclick: () => save(), title: 'Save (Ctrl+S)' }, icon('check', 16), 'Save');
  const nameEl = h('span.name');
  const dot = h('span.dirty-dot.hidden', { title: 'Unsaved changes' });
  const delBtn = h('button.btn.ghost.icon.sm', { 'aria-label': 'Delete this file', title: 'Delete file', onclick: () => remove() }, icon('trash', 16));
  const bar = h('div.editor-bar', icon('file', 16), nameEl, dot, h('span.spacer'), delBtn, saveBtn);
  const body = h('div');

  function setDirty(d) { dirty = d; dot.classList.toggle('hidden', !d); saveBtn.disabled = !d; }

  async function load(sel) {
    const d = await get(sp(ws.slug, '/files')).catch(() => null);
    if (!d || !ws.alive()) return;
    list = d.files;
    isDraft = d.draft;
    drawTree();
    const pick = sel || current || (list.find((f) => f.path === 'index.html') || list.find((f) => TEXT.test(f.path)) || list[0])?.path;
    if (pick && list.some((f) => f.path === pick)) open(pick, true);
    else showEmpty();
  }

  function drawTree() {
    const dirs = {};
    for (const f of list) { const i = f.path.lastIndexOf('/'); (dirs[i < 0 ? '' : f.path.slice(0, i)] ||= []).push(f); }
    clear(tree,
      h('div.head', 'Files', h('span.spacer'),
        h('button.btn.ghost.icon.sm', { 'aria-label': 'New file', title: 'New file', onclick: () => create() }, icon('plus', 16)),
        h('button.btn.ghost.icon.sm', { 'aria-label': 'Upload files', title: 'Upload files', onclick: () => ws.upload() }, icon('upload', 16))),
      Object.keys(dirs).sort().map((d) => [
        d && h('div.dir', icon('folder', 14), d),
        dirs[d].map((f) => h('button.f', { 'aria-current': String(f.path === current), title: f.path + ' · ' + bytes(f.size), style: { paddingLeft: (d ? 22 : 8) + 'px' }, onclick: () => open(f.path) },
          icon(IMG.test(f.path) && !/\.svg$/.test(f.path) ? 'eye' : 'file', 14), h('span', f.path.slice(d ? d.length + 1 : 0)))),
      ]),
      !list.length && h('p.tiny.faint', { style: { padding: '8px' } }, 'No files yet.'));
  }

  function showEmpty() {
    current = null;
    clear(pane, h('div.editor-empty', empty({ title: 'This site has no files yet', text: 'Start with a page, or upload files you already have.', art: false, iconName: 'file',
      actions: [h('button.btn.primary', { onclick: () => create('index.html') }, icon('plus', 16), 'Create index.html'), h('button.btn', { onclick: () => ws.upload() }, icon('upload', 16), 'Upload')] })));
  }

  async function open(path, quietly) {
    if (dirty && path !== current && !quietly && !(await confirmDialog({ title: 'Leave without saving?', message: 'You have unsaved changes in ' + current + '.', confirm: 'Discard my changes', danger: true }))) return;
    current = path;
    drawTree();
    if (blobUrl) { URL.revokeObjectURL(blobUrl); blobUrl = null; }
    const f = list.find((x) => x.path === path);
    nameEl.textContent = path;
    setDirty(false);
    if (TEXT.test(path)) {
      const text = await api(sp(ws.slug, '/files/' + filePath(path)), { headers: { Accept: 'text/plain' } }).catch(() => null);
      if (text == null || current !== path) return;
      original = ta.value = typeof text === 'string' ? text : JSON.stringify(text, null, 2);
      lines = 0;
      paintGutter();
      saveBtn.classList.remove('hidden');
      clear(body);
      body.className = 'editor';
      body.append(gutter, ta);
      clear(pane, bar, body);
      ta.scrollTop = 0;
      gutter.scrollTop = 0;
    } else {
      saveBtn.classList.add('hidden');
      body.className = 'img-preview';
      clear(body, h('p.muted.small', 'This file can’t be edited here.'), h('p.tiny.faint', f ? bytes(f.size) + ' · ' + f.type : ''));
      clear(pane, bar, body);
      if (IMG.test(path)) {
        const res = await api(sp(ws.slug, '/files/' + filePath(path)), { raw: true }).catch(() => null);
        if (res && current === path) {
          blobUrl = URL.createObjectURL(await res.blob());
          body.prepend(h('img', { src: blobUrl, alt: 'Preview of ' + path }));
        }
      }
    }
  }

  async function save() {
    if (!current || !dirty) return;
    const val = ta.value;
    await busy(saveBtn, async () => {
      await put(sp(ws.slug, '/files/' + filePath(current)), val, { headers: { 'Content-Type': 'text/plain; charset=utf-8' } });
      original = val;
      setDirty(ta.value !== original);
      const wasDraft = isDraft;
      isDraft = true;
      ws.draftChanged();
      toast(wasDraft ? 'Saved to the draft.' : 'Saved. This started a draft, so publish when you’re ready.', { type: 'ok', timeout: 2600 });
    });
  }

  async function create(name) {
    name = name || (await promptDialog({ title: 'New file', label: 'File name', placeholder: 'about.html', hint: 'Use folders with a slash, like css/style.css.', confirm: 'Create file' }));
    if (!name) return;
    name = name.replace(/^\/+/, '');
    if (list.some((f) => f.path === name)) return open(name);
    const starter = /\.html?$/.test(name) ? '<!doctype html>\n<html lang="en">\n<head>\n  <meta charset="utf-8">\n  <meta name="viewport" content="width=device-width, initial-scale=1">\n  <title>New page</title>\n</head>\n<body>\n  <h1>Hello</h1>\n</body>\n</html>\n' : '';
    await put(sp(ws.slug, '/files/' + filePath(name)), starter, { headers: { 'Content-Type': 'text/plain; charset=utf-8' } });
    ws.draftChanged();
    dirty = false;
    await load(name);
    toast('Created ' + name + '.', { type: 'ok', timeout: 2000 });
  }

  async function remove() {
    if (!current) return;
    if (!(await confirmDialog({ title: 'Delete ' + current + '?', message: 'It’s removed from the draft. The live site keeps it until you publish.', confirm: 'Delete file', danger: true }))) return;
    await del(sp(ws.slug, '/files/' + filePath(current)));
    dirty = false;
    const gone = current;
    current = null;
    ws.draftChanged();
    toast('Deleted ' + gone + '.');
    load();
  }

  const onChange = () => { if (!dirty) load(); };
  ws.filesSubs.add(onChange);
  const warn = (e) => { if (dirty) { e.preventDefault(); e.returnValue = ''; } };
  addEventListener('beforeunload', warn);
  await load();
  return () => { ws.filesSubs.delete(onChange); removeEventListener('beforeunload', warn); if (blobUrl) URL.revokeObjectURL(blobUrl); };
}
