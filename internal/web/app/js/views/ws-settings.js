// Site settings tab.
import { h, icon, clear, toast, confirmDialog, busy, toggle, isOwner, sparkline, plural, relTime } from '../ui.js';
import { get, patch, post, site as sp, maybe } from '../api.js';
import { state } from '../state.js';
import { navigate } from '../router.js';

const EMOJI = '📝 📋 ✅ 📅 📊 📈 💸 💼 🧾 📇 👋 🎉 🏖️ ✈️ 🍕 ☕ 🏢 🛠️ 💡 🚀 🌱 🌍 🔒 📦 🎓 🏆 ❤️ ⭐ 🧭 📣 🗂️ 🗳️ 🧮 🩺 🎨 🎯 🤝 📚 🔔 🧪'.split(' ');

export function emojiPicker(btn, onPick) {
  const close = () => { pop.remove(); document.removeEventListener('mousedown', out, true); document.removeEventListener('keydown', key, true); btn.setAttribute('aria-expanded', 'false'); };
  const out = (e) => { if (!pop.contains(e.target) && e.target !== btn) close(); };
  const key = (e) => {
    const b = [...pop.querySelectorAll('button')];
    const i = b.indexOf(document.activeElement);
    const mv = { ArrowRight: 1, ArrowLeft: -1, ArrowDown: 8, ArrowUp: -8 }[e.key];
    if (e.key === 'Escape') { e.stopPropagation(); close(); btn.focus(); }
    else if (mv && i >= 0) { e.preventDefault(); b[Math.max(0, Math.min(b.length - 1, i + mv))].focus(); }
  };
  const pop = h('div.popover', { role: 'dialog', 'aria-label': 'Pick an icon', style: { position: 'fixed' } },
    h('div.emoji-grid', EMOJI.map((e) => h('button', { type: 'button', 'aria-label': e, onclick: () => { onPick(e); close(); btn.focus(); } }, e))),
    h('div', { style: { padding: '0 10px 10px' } }, h('button.btn.sm.ghost', { type: 'button', onclick: () => { onPick(''); close(); btn.focus(); } }, 'Use initials instead')));
  document.body.append(pop);
  const r = btn.getBoundingClientRect();
  pop.style.top = Math.min(r.bottom + 6, innerHeight - 330) + 'px';
  pop.style.left = Math.min(r.left, innerWidth - 330) + 'px';
  btn.setAttribute('aria-expanded', 'true');
  setTimeout(() => { document.addEventListener('mousedown', out, true); document.addEventListener('keydown', key, true); pop.querySelector('button').focus(); });
}

export default async function settings(panel, ws) {
  const s = ws.site;
  const owner = isOwner(s, state.me);
  let icon_ = s.icon || '';
  const titleIn = h('input.input', { value: s.title, maxlength: 120, required: true, id: 'set-title' });
  const slugIn = h('input.input', { value: s.slug, maxlength: 48, pattern: '[a-z0-9][a-z0-9\\-]{1,46}[a-z0-9]', disabled: !owner, id: 'set-slug', 'aria-describedby': 'slug-hint', spellcheck: 'false' });
  const descIn = h('textarea.input', { rows: 2, maxlength: 500, id: 'set-desc', placeholder: 'A sentence about what this site is for' }, s.description || '');
  const emojiBtn = h('button.emoji-btn', { type: 'button', 'aria-label': 'Change icon', 'aria-haspopup': 'dialog', onclick: () => emojiPicker(emojiBtn, (e) => { icon_ = e; emojiBtn.textContent = e || '∅'; }) }, icon_ || '∅');
  const set = { ...s.settings };
  const saveBtn = h('button.btn.primary', { type: 'submit' }, 'Save changes');
  slugIn.addEventListener('input', () => { slugIn.value = slugIn.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'); });

  const form = h('form.form-pad', { onsubmit: async (e) => {
    e.preventDefault();
    await busy(saveBtn, async () => {
      const body = { title: titleIn.value.trim(), description: descIn.value, icon: icon_, settings: set };
      if (owner && slugIn.value !== ws.slug) body.slug = slugIn.value;
      const n = await patch(sp(ws.slug), body);
      ws.setSite(n);
      state.sites = null;
      toast(body.slug ? 'Saved. The new address is /s/' + n.slug + '. Old links stop working.' : 'Saved.', { type: 'ok' });
    });
  } },
    h('div.insights-slot'),
    h('h3', 'Basics'),
    h('div.row', { style: { alignItems: 'flex-end', gap: '14px' } },
      h('div.field', h('span.label', 'Icon'), emojiBtn),
      h('label.field', { style: { flex: '1' } }, h('span.label', 'Name'), titleIn)),
    h('label.field', h('span.label', 'Address'),
      h('div.input-group', h('span.addon', location.host + '/s/'), slugIn),
      h('span.hint#slug-hint', owner ? 'Lowercase letters, numbers and dashes. If you change it, old links stop working.' : 'Only the owner can change the address.')),
    h('label.field', h('span.label', 'Description'), descIn, h('span.hint', 'Shown on Home and in templates.')),
    h('h3', { style: { marginTop: '28px' } }, 'Behaviour'),
    h('div',
      opt('Single-page app', 'Send every address to index.html. Turn on if your site does its own page switching.', set.spa, (v) => (set.spa = v)),
      opt('Hide the Bunker badge', 'Viewers won’t see the small Bunker button in the corner. Editors always do.', set.hideBadge, (v) => (set.hideBadge = v)),
      opt('Offer as a template', 'Anyone can start their own copy from Templates. Your data isn’t copied.', s.isTemplate, async (v, el) => {
        try { const n = await patch(sp(ws.slug), { isTemplate: v }); ws.setSite(n); toast(v ? 'Now offered as a template.' : 'No longer a template.', { type: 'ok', timeout: 2400 }); }
        catch { el.checked = !v; }
      })),
    h('div.row', h('span.spacer'), saveBtn),
    h('hr.divider'),
    h('h3', 'Export'),
    h('div.setting', h('div', h('b', 'Download as zip'), h('span.d', 'Get every file of the current version, to keep a copy or move it elsewhere.')),
      h('a.btn.sm', { href: sp(ws.slug, '/export.zip'), 'data-native': '', download: ws.slug + '.zip' }, icon('down', 16), 'Download')),
    owner && h('div.danger-zone', { style: { marginTop: '24px' } },
      h('h3', 'Careful'),
      h('div.setting', h('div', h('b', 'Give this site to someone else'), h('span.d', 'They become the owner. You keep edit access.')),
        h('button.btn.sm', { type: 'button', onclick: transfer }, icon('users', 16), 'Transfer')),
      h('div.setting', h('div', h('b', 'Move to trash'), h('span.d', 'People lose access right away. You can restore it for 30 days.')),
        h('button.btn.sm.danger', { type: 'button', onclick: () => ws.trash() }, icon('trash', 16), 'Move to trash'))));
  clear(panel, form);

  async function transfer() {
    const { promptDialog } = await import('../ui.js');
    const email = await promptDialog({ title: 'Give “' + ws.site.title + '” to someone else', label: 'Their work email', type: 'email', placeholder: 'name@company.com', hint: 'They need to have signed in to Bunker at least once.', confirm: 'Continue' });
    if (!email) return;
    if (!(await confirmDialog({ title: 'Transfer to ' + email + '?', message: 'They’ll own the site and can remove your access. You’ll stay on as an editor.', confirm: 'Transfer ownership', danger: true }))) return;
    const r = await post(sp(ws.slug, '/transfer'), { email });
    toast('“' + ws.site.title + '” now belongs to ' + (r.owner || email) + '.', { type: 'ok' });
    state.sites = null;
    await ws.refresh();
    if (!['owner', 'admin'].includes(ws.site.role)) navigate('/sites/' + ws.slug, { force: true });
  }

  // Insights (views over 30 days); hidden if the stats API isn't there.
  const st = await maybe(sp(ws.slug, '/stats?days=30')).catch(() => null);
  if (!st || !ws.alive()) return;
  const daily = (st.daily || []).map((d) => d.views || 0);
  const top = (st.topVisitors || []).slice(0, 3);
  form.querySelector('.insights-slot').replaceWith(h('div',
    h('h3', { style: { marginBottom: '10px' } }, 'Last 30 days'),
    h('div.insights',
      h('div.num', h('b', st.views ?? 0), h('span', 'views')),
      h('div.num', h('b', st.visitors ?? 0), h('span', plural(st.visitors ?? 0, 'person', 'people').replace(/^\d+ /, '') + ' visited')),
      daily.length > 1 && sparkline(daily, 150, 40)),
    top.length > 0 && h('p.small.muted', { style: { marginTop: '8px' } }, 'Most visits: ' + top.map((t) => t.email.split('@')[0] + ' (' + t.views + ')').join(', ') + (top[0].lastSeen ? ' · last seen ' + relTime(top[0].lastSeen) : ''))));
}

function opt(title, desc, val, onchange) {
  return h('div.setting', h('div', h('b', title), h('span.d', desc)), toggle({ checked: val, label: title, onchange }));
}
