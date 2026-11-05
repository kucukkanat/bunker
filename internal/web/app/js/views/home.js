import { h, icon, clear, archArt, skeletons, empty, toast, debounce, store, add } from '../ui.js';
import { get } from '../api.js';
import { state, aiStatus, aiReady } from '../state.js';
import { siteCard, startAI, dropCreate, htmlCreate, githubCreate, IDEAS } from '../components.js';
import { fromDataTransfer, pickers, onWindowDrag } from '../drop.js';
import { showWelcome } from './welcome.js';
import { fnStatus, blockedBanner } from '../functions.js';

function greeting(name) {
  const hr = new Date().getHours();
  const part = hr < 5 ? 'Good evening' : hr < 12 ? 'Good morning' : hr < 18 ? 'Good afternoon' : 'Good evening';
  const first = (name || '').split(/[\s(]/)[0];
  return part + (first && first.toLowerCase() !== 'you' ? ', ' + first : '');
}

export default async function home(root, ctx) {
  const me = state.me;
  const search = h('input.input', { type: 'search', placeholder: 'Search your sites', 'aria-label': 'Search your sites', 'data-search': '' });

  // ---- describe card ----
  const prompt = h('textarea', { placeholder: 'For example: “A form where my team can book the meeting room, with a calendar of who booked what.”', 'aria-label': 'Describe the app you want', rows: 3 });
  const buildBtn = h('button.btn.primary', { type: 'submit' }, icon('sparkle', 16), 'Build it');
  const aiNote = h('p.note.hidden');
  const describeCard = h('form.describe', { onsubmit: (e) => { e.preventDefault(); startAI(prompt.value, buildBtn); } },
    h('h2', icon('sparkle', 22), 'Describe an app'),
    h('p.lead', 'Say what you need in plain words. Bunker builds it and shows you a preview.'),
    prompt,
    h('div.chips', { role: 'group', 'aria-label': 'Ideas' }, IDEAS.slice(0, 4).map(([e, t, p]) => h('button', { type: 'button', onclick: () => { prompt.value = p; prompt.focus(); } }, e + ' ' + t))),
    h('div.row', aiNote, h('span.spacer'), buildBtn));
  prompt.addEventListener('keydown', (e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); describeCard.requestSubmit(); } });

  // ---- drop zone ----
  const bar = h('div.progress', h('i'));
  const status = h('p', { 'aria-live': 'polite' }, 'An HTML file, a folder, a .zip, a spreadsheet, Markdown… or paste a GitHub link. It’s private until you share it.');
  const title = h('h2#drop-title', 'Drop a file or folder to make it a site');
  const pk = pickers((items) => dropCreate(items, ui));
  const btnRow = h('div.row.wrap', { style: { justifyContent: 'center' } }, pk.fileBtn('btn'), pk.folderBtn('btn'));
  // A labelled region; its "Choose files" / "Choose folder" buttons are the
  // keyboard way in (a click anywhere else on it is a mouse shortcut).
  const drop = h('section.drop', { 'aria-labelledby': 'drop-title', 'data-testid': 'dropzone',
    onclick: (e) => !drop.classList.contains('busy') && !e.target.closest('button,input,a') && pk.inputs[0].click(),
    ondragover: (e) => { e.preventDefault(); drop.classList.add('over'); },
    ondragleave: (e) => { if (!drop.contains(e.relatedTarget)) drop.classList.remove('over'); },
  }, archArt(92), title, status, btnRow, pk.inputs);
  const ui = {
    start(msg) { drop.classList.remove('failed'); status.removeAttribute('role'); drop.classList.add('busy'); title.textContent = 'Making your site…'; status.textContent = msg; btnRow.replaceWith(bar); bar.firstChild.style.width = '2%'; },
    progress(p) { bar.firstChild.style.width = Math.max(2, p * 100) + '%'; if (p >= 1) { bar.classList.add('indeterminate'); status.textContent = 'Almost there. Checking and publishing…'; } },
    reset(e) {
      drop.classList.remove('busy');
      drop.classList.add('failed');
      title.textContent = e?.code === 'secret_detected' ? 'We found a password or key in your files' : e?.code === 'blocked_type' ? 'That upload has a program in it' : e?.status === 413 ? 'That’s too big for one site' : e?.code === 'network' ? 'Bunker isn’t answering' : 'That didn’t work';
      status.textContent = (e?.message && (e.status || e.code === 'network') ? e.message : 'Please try again, or pick different files.');
      status.setAttribute('role', 'alert');
      bar.classList.remove('indeterminate'); bar.replaceWith(btnRow);
    },
  };
  let overlay;
  const offDrag = onWindowDrag({
    enter: () => { drop.classList.add('over'); overlay = h('div.drop-overlay', h('div.inner', 'Drop anywhere to make a new site')); document.body.append(overlay); },
    leave: () => { drop.classList.remove('over'); overlay?.remove(); },
    drop: async (e) => { if (drop.classList.contains('busy')) return; const items = await fromDataTransfer(e.dataTransfer); dropCreate(items, ui); },
  });

  // ---- paste HTML to create ----
  const onPaste = (e) => {
    const t = e.target;
    const html = e.clipboardData?.getData('text/plain') || '';
    const gh = html.trim().match(/^https?:\/\/(www\.)?github\.com\/[\w.-]+\/[\w.-]+(\/tree\/[^\s]+)?\/?$/);
    // A GitHub link pasted into the search box is almost surely meant for import.
    if (t.isContentEditable || (/^(INPUT|TEXTAREA)$/.test(t.tagName) && !(gh && t === search))) return;
    if (gh) {
      e.preventDefault();
      toast(gh[2] ? 'Make a site from this GitHub folder?' : 'Make a site from this GitHub repository?', { action: { label: 'Import', run: () => githubCreate(html.trim()).catch(() => {}) }, timeout: 10000 });
      return;
    }
    if (!/<(html|body|div|head|!doctype|section|main|table|style|script|h1|p)[\s>]/i.test(html)) return;
    e.preventDefault();
    toast('Make a new site from the HTML you pasted?', { action: { label: 'Create site', run: () => htmlCreate(html).catch(() => {}) }, timeout: 10000 });
  };
  document.addEventListener('paste', onPaste);

  const lists = h('div');
  const fnSlot = h('div.fn-slot');
  clear(root, h('div.page',
    fnSlot,
    h('div.home-head',
      h('div', h('h1', greeting(me?.name)), h('p.sub', 'What would you like to make today?')),
      h('label.search', icon('search', 18), search, h('kbd', '/'))),
    h('div.hero', describeCard, drop),
    lists));
  add(lists, h('div.section', h('div.section-head', h('h2', 'Your sites')), h('div.grid', skeletons(4))));

  aiStatus().then((s) => {
    if (!ctx.alive() || aiReady(s)) return;
    aiNote.classList.remove('hidden');
    if (state.me?.isAdmin && s !== null) {
      aiNote.replaceChildren(icon('info', 16), h('span', 'The AI builder needs an API key. ', h('a', { href: '/admin/ai' }, 'Set it up in Admin → AI'), ' (Claude, OpenAI, Azure or a local model).'));
    } else {
      aiNote.replaceChildren(icon('info', 16), s === null ? 'The AI builder is coming soon. Upload files or start from a template meanwhile.' : 'The AI builder isn’t switched on yet. Ask your Bunker admin to turn it on.');
    }
    buildBtn.disabled = true;
    describeCard.parentNode?.classList.add('ai-off');
  });

  let all = [], recent = [];
  try {
    const [a, r] = await Promise.all([get('/api/v1/sites'), get('/api/v1/sites?filter=recent').catch(() => ({ sites: [] })), fnStatus().then(() => ctx.alive() && clear(fnSlot, blockedBanner()))]);
    all = a.sites || [];
    recent = (r.sites || []).sort((x, y) => (y.extra?.visitedAt || 0) - (x.extra?.visitedAt || 0));
  } catch { clear(lists, empty({ title: 'Couldn’t load your sites', text: 'Check your connection, then refresh the page.' })); return cleanup; }
  if (!ctx.alive()) return cleanup;
  state.sites = all;

  const draw = () => {
    const q = search.value.trim().toLowerCase();
    if (q) {
      const hits = all.filter((s) => (s.title + ' ' + s.slug + ' ' + (s.description || '')).toLowerCase().includes(q));
      return clear(lists, section('Results', hits, hits.length ? null : empty({ title: 'No sites match “' + search.value + '”', text: 'Try a different word, or clear the search.', art: false, iconName: 'search', actions: [h('button.btn', { onclick: () => { search.value = ''; draw(); } }, 'Clear search')] })));
    }
    if (!all.length) {
      return clear(lists, h('div.section', empty({
        title: 'Your sites will live here',
        text: 'Make your first one by describing it above, dropping a file, or starting from a template. Only you can see it until you share it.',
        actions: [h('a.btn.primary', { href: '/new' }, icon('plus', 16), 'New site'), h('a.btn', { href: '/templates' }, 'Browse templates')],
      }), h('div.ideas', { style: { margin: '16px auto 0' } }, IDEAS.slice(0, 3).map(([e, t, p]) => h('div', h('b', e + ' ' + t), p)))));
    }
    const mine = all.filter((s) => s.ownerId === me.id);
    const shared = all.filter((s) => s.ownerId !== me.id);
    const starred = all.filter((s) => s.starred);
    clear(lists,
      recent.length > 0 && section('Recent', recent.slice(0, 4)),
      starred.length > 0 && section('Starred', starred),
      section('My sites', mine, !mine.length && empty({ title: 'You haven’t made a site yet', text: 'Drop a file above or describe an app to get started.', art: false, iconName: 'sparkle' })),
      shared.length > 0 && section('Shared with me', shared));
  };
  const section = (name, sites, fallback) => h('section.section', { 'aria-label': name },
    h('div.section-head', h('h2', name), sites.length > 0 && h('span.count', sites.length)),
    fallback || h('div.grid', sites.map((s, i) => siteCard(s, i, () => draw()))));
  search.addEventListener('input', debounce(draw, 120));
  draw();

  if (!all.some((s) => s.ownerId === me.id) && !store('welcomed')) showWelcome();

  function cleanup() { offDrag(); overlay?.remove(); document.removeEventListener('paste', onPaste); }
  return cleanup;
}
