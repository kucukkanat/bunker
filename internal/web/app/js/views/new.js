import { h, icon, clear, skeletons, siteIcon, promptDialog, toast, empty, esc } from '../ui.js';
import { get, post, put, site } from '../api.js';
import { state, aiStatus, aiReady } from '../state.js';
import { startAI, dropCreate, IDEAS } from '../components.js';
import { pickers, onWindowDrag, fromDataTransfer } from '../drop.js';
import { navigate } from '../router.js';

export function starterHTML(title) {
  return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${esc(title)}</title>
  <style>
    body { font: 17px/1.6 system-ui, sans-serif; margin: 0; background: #f7f4ec; color: #1c211e; }
    main { max-width: 640px; margin: 12vh auto; padding: 0 24px; }
    h1 { font-family: Georgia, serif; color: #1f3a2e; font-size: 2.4rem; margin: 0 0 12px; }
    p { color: #5a635d; }
  </style>
</head>
<body>
  <main>
    <h1>${esc(title)}</h1>
    <p>This is your new site. Edit this page in the Files tab, or ask the builder to change it.</p>
  </main>
</body>
</html>
`;
}

export async function createBlank(title) {
  title = title || (await promptDialog({ title: 'Start a blank site', label: 'What’s it called?', placeholder: 'For example: Team lunch orders', confirm: 'Create site' }));
  if (!title) return;
  const s = await post('/api/v1/sites', { title });
  await put(site(s.slug, '/files/index.html'), starterHTML(s.title), { headers: { 'Content-Type': 'text/html' } }).catch(() => {});
  state.sites = null;
  navigate('/sites/' + s.slug + '?tab=files');
}

export async function useTemplate(tpl, btn) {
  if (btn) btn.disabled = true;
  try {
    const s = await post('/api/v1/sites', { title: tpl.title, template: tpl.slug, icon: tpl.icon || undefined, description: tpl.description || undefined });
    state.sites = null;
    toast('Made a copy of “' + tpl.title + '”. It’s yours to change.', { type: 'ok' });
    navigate('/sites/' + s.slug);
  } finally { if (btn) btn.disabled = false; }
}

export function templateCard(t, i) {
  const btn = h('button.btn.primary.sm', { onclick: () => useTemplate(t, btn) }, 'Use this template');
  return h('div.tpl-card', { style: { animationDelay: i * 40 + 'ms' } },
    h('div.row', siteIcon(t), h('div', { style: { minWidth: 0 } }, h('h2.h3', t.title), h('div.faint.tiny', 'by ' + (t.owner?.name || 'a coworker')))),
    h('p.desc', t.description || 'A starting point you can change however you like.'),
    h('div.row', btn, t.version > 0 && h('a.btn.ghost.sm', { href: '/s/' + t.slug + '/', target: '_blank', rel: 'noopener' }, icon('eye', 16), 'Preview')));
}

export default async function newSite(root, ctx) {
  const prompt = h('textarea.input', { rows: 3, placeholder: 'Describe the app you want, in plain words…', 'aria-label': 'Describe the app you want' });
  const go = h('button.btn.primary', { type: 'submit' }, icon('sparkle', 16), 'Build it');
  const bar = h('div.progress.hidden', h('i'));
  const upMsg = h('p.small.muted', 'An HTML file, a folder, a .zip, a spreadsheet, Markdown, images or a PDF.');
  const pk = pickers((items) => dropCreate(items, upUi));
  const upUi = {
    start(m) { upMsg.textContent = m; bar.classList.remove('hidden'); },
    progress(p) { bar.firstChild.style.width = p * 100 + '%'; if (p >= 1) bar.classList.add('indeterminate'); },
    reset() { bar.classList.add('hidden'); bar.classList.remove('indeterminate'); upMsg.textContent = 'That didn’t work. Try again.'; },
  };
  const off = onWindowDrag({ enter() {}, leave() {}, drop: async (e) => dropCreate(await fromDataTransfer(e.dataTransfer), upUi) });
  const tplGrid = h('div.grid', skeletons(3));
  const aiNote = h('p.small.muted.hidden');

  clear(root, h('div.page',
    h('div.page-head', h('div', h('h1', 'Make a new site'), h('p.sub', 'Pick how you’d like to start. You can switch approaches later.'))),
    h('div.choices',
      h('form.choice.ai', { onsubmit: (e) => { e.preventDefault(); startAI(prompt.value, go); } },
        h('span.ic', icon('sparkle', 24)), h('h2', 'Describe it'), h('p', 'Tell the AI builder what you need. It writes the site and you watch it take shape.'),
        prompt,
        h('div.chips', IDEAS.slice(0, 3).map(([e, t, p]) => h('button', { type: 'button', onclick: () => { prompt.value = p; prompt.focus(); } }, e + ' ' + t))),
        aiNote, h('div.row', h('span.spacer'), go)),
      h('div.choice',
        h('span.ic', icon('upload', 24)), h('h2', 'Upload'), h('p', 'Already have HTML, maybe from ChatGPT or a colleague? Drop it here or anywhere on this page.'),
        upMsg, bar,
        h('div.row.wrap', pk.fileBtn('btn'), pk.folderBtn('btn')), pk.inputs),
      h('div.choice',
        h('span.ic', icon('file', 24)), h('h2', 'Start blank'), h('p', 'Begin with a simple page and edit the code yourself, or ask the builder later.'),
        h('div.spacer'), h('div.row', h('button.btn', { onclick: () => createBlank() }, icon('plus', 16), 'Blank site')))),
    h('section.section', h('div.section-head', h('h2', 'Or start from a template')), tplGrid)));
  prompt.addEventListener('keydown', (e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); prompt.form.requestSubmit(); } });

  aiStatus().then((s) => {
    if (aiReady(s) || !ctx.alive()) return;
    aiNote.classList.remove('hidden');
    aiNote.textContent = s === null ? 'The AI builder is coming soon.' : (state.me?.isAdmin ? 'The AI builder needs an API key: add one in Admin → AI.' : 'The AI builder isn’t switched on yet. Ask your Bunker admin.');
    go.disabled = true;
  });
  const d = await get('/api/v1/sites?filter=templates').catch(() => ({ sites: [] }));
  if (!ctx.alive()) return off;
  const tpls = d.sites || [];
  clear(tplGrid, tpls.length ? tpls.map(templateCard) : empty({ title: 'No templates yet', text: 'Any site can become a template. Open its Settings and turn on “Offer as a template”, and it will show up here for everyone.', art: false, iconName: 'grid' }));
  return off;
}
