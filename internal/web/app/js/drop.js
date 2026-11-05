// Collect dropped files and folders (with relative paths) and build uploads.
import { h, icon } from './ui.js';

const SKIP = /(^|\/)(\.git|\.DS_Store|node_modules|Thumbs\.db|__MACOSX)(\/|$)/;

function readAll(reader) {
  return new Promise((resolve) => {
    const out = [];
    const next = () => reader.readEntries((batch) => {
      if (!batch.length) return resolve(out);
      out.push(...batch);
      next();
    }, () => resolve(out));
    next();
  });
}
async function walk(entry, prefix, out) {
  if (!entry || SKIP.test(prefix + entry.name)) return;
  if (entry.isFile) {
    const f = await new Promise((res) => entry.file(res, () => res(null)));
    if (f) out.push({ path: prefix + entry.name, file: f });
  } else if (entry.isDirectory) {
    const kids = await readAll(entry.createReader());
    for (const k of kids) await walk(k, prefix + entry.name + '/', out);
  }
}

// From a DataTransfer (drop event): [{path, file}]
export async function fromDataTransfer(dt) {
  const out = [];
  const entries = [];
  for (const it of dt.items || []) {
    if (it.kind !== 'file') continue;
    const e = it.webkitGetAsEntry && it.webkitGetAsEntry();
    if (e) entries.push(e);
  }
  if (entries.length) for (const e of entries) await walk(e, '', out);
  else for (const f of dt.files || []) out.push({ path: f.webkitRelativePath || f.name, file: f });
  return out;
}

// From an <input type=file> (multiple or webkitdirectory)
export function fromInput(input) {
  return [...input.files].map((f) => ({ path: f.webkitRelativePath || f.name, file: f })).filter((x) => !SKIP.test(x.path));
}

// Folder drops arrive as "my-site/index.html": the server strips a common
// top folder, but we keep the name to guess a title.
export function toForm(items, fields = {}) {
  const fd = new FormData();
  for (const k in fields) if (fields[k] != null) fd.append(k, String(fields[k]));
  for (const { path, file } of items) fd.append('file:' + path, file, file.name);
  return fd;
}
export function totalSize(items) { return items.reduce((n, x) => n + x.file.size, 0); }
export function describe(items) {
  if (items.length === 1) return items[0].path;
  const tops = new Set(items.map((x) => x.path.split('/')[0]));
  return tops.size === 1 && items[0].path.includes('/') ? 'the folder “' + [...tops][0] + '”' : items.length + ' files';
}

// Hidden pickers for files and folders.
export function pickers(onPick) {
  const files = h('input', { type: 'file', multiple: true, hidden: true, 'aria-hidden': 'true', tabindex: '-1', 'data-testid': 'pick-files', onchange: (e) => { const it = fromInput(e.target); e.target.value = ''; if (it.length) onPick(it); } });
  const folder = h('input', { type: 'file', multiple: true, webkitdirectory: true, hidden: true, 'aria-hidden': 'true', tabindex: '-1', 'data-testid': 'pick-folder', onchange: (e) => { const it = fromInput(e.target); e.target.value = ''; if (it.length) onPick(it); } });
  return {
    inputs: [files, folder],
    fileBtn: (cls = 'btn') => h('button.' + cls, { type: 'button', onclick: (e) => { e.stopPropagation(); files.click(); } }, icon('file', 16), 'Choose files'),
    folderBtn: (cls = 'btn') => h('button.' + cls, { type: 'button', onclick: (e) => { e.stopPropagation(); folder.click(); } }, icon('folder', 16), 'Choose a folder'),
  };
}

// Track window-level drags so the page can show an overlay.
export function onWindowDrag({ enter, leave, drop }) {
  let depth = 0;
  const has = (e) => [...(e.dataTransfer?.types || [])].includes('Files');
  const de = (e) => { if (!has(e)) return; e.preventDefault(); if (depth++ === 0) enter(e); };
  const dl = (e) => { if (!has(e)) return; if (--depth <= 0) { depth = 0; leave(e); } };
  const dov = (e) => { if (has(e)) { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; } };
  const dd = (e) => { if (!has(e)) return; e.preventDefault(); depth = 0; leave(e); drop(e); };
  addEventListener('dragenter', de);
  addEventListener('dragleave', dl);
  addEventListener('dragover', dov);
  addEventListener('drop', dd);
  return () => { removeEventListener('dragenter', de); removeEventListener('dragleave', dl); removeEventListener('dragover', dov); removeEventListener('drop', dd); };
}
