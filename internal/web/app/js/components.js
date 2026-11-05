// Shared pieces: site cards, create flows.
import { h, icon, siteIcon, relTime, roleLabel, canEdit, toast, celebrate, bytes } from './ui.js';
import { post, stream, upload, ApiError } from './api.js';
import { state, setStar, hub } from './state.js';
import { navigate } from './router.js';
import { toForm, totalSize, describe } from './drop.js';

export function siteHref(s) { return canEdit(s) ? '/sites/' + s.slug : '/s/' + s.slug + '/'; }

export function starButton(site, onChange) {
  const b = h('button.star-btn', { type: 'button', 'aria-pressed': String(!!site.starred), 'aria-label': (site.starred ? 'Unstar ' : 'Star ') + site.title, onclick: async (e) => {
    e.preventDefault();
    e.stopPropagation();
    const on = !site.starred;
    b.setAttribute('aria-pressed', String(on));
    b.classList.remove('bump'); void b.offsetWidth; b.classList.add('bump');
    const ok = await setStar(site, on);
    if (!ok) b.setAttribute('aria-pressed', String(!on));
    else { b.setAttribute('aria-label', (on ? 'Unstar ' : 'Star ') + site.title); onChange && onChange(on); }
  } }, icon('star', 18));
  return b;
}

export function siteCard(s, i = 0, onStar) {
  const me = state.me;
  const role = roleLabel(s, me);
  const owner = s.ownerId === me?.id ? 'you' : s.owner?.name || s.owner?.email || 'someone';
  const when = s.extra?.visitedAt ? 'Opened ' + relTime(s.extra.visitedAt) : 'Updated ' + relTime(s.updatedAt);
  const href = siteHref(s);
  return h('a.site-card', { href, style: { animationDelay: Math.min(i, 12) * 30 + 'ms' }, 'data-native': href.startsWith('/s/') ? '' : null, 'aria-label': s.title + ', ' + role + ', ' + when.toLowerCase() },
    h('div.star', starButton(s, onStar)),
    h('div.top', siteIcon(s), h('div.t', h('h3', s.title), h('div.addr', '/s/' + s.slug))),
    s.description && h('p.desc', s.description),
    h('div.meta',
      role && h('span.badge' + (role === 'Owner' ? '.ok' : ''), role),
      !s.version && h('span.badge.draft', 'Not published'),
      h('span.by', when + (owner !== 'you' ? ' · ' + owner : ''))),
  );
}

// ---------- AI create ----------
export async function startAI(prompt, btn) {
  prompt = prompt.trim();
  if (!prompt) { toast('Describe what you’d like to make first.'); return; }
  const live = hub();
  let slug = null;
  const original = btn && btn.innerHTML;
  if (btn) { btn.disabled = true; btn.replaceChildren(h('span.spin'), 'Setting things up…'); }
  try {
    await stream('/api/v1/ai/create', { prompt }, (type, d) => {
      const s = d && typeof d === 'object' ? d.slug || d.site?.slug : null;
      if (type !== 'done' || slug) live.push(type, d);
      if (!slug && s) {
        slug = s;
        state.live[slug] = { prompt, hub: live };
        state.sites = null;
        navigate('/sites/' + slug + '?tab=build');
      }
      if (type === 'done' && slug && !live.done) live.push('done', d);
    }, { quiet: true });
    if (!slug) throw new ApiError(500, 'error', 'The builder didn’t start. Please try again.');
  } catch (e) {
    if (slug) { live.push('error', { message: e.message }); return; }
    if (btn) { btn.disabled = false; btn.innerHTML = original; }
    if (e.missing) toast('The AI builder isn’t switched on yet. Ask your Bunker admin, or upload files instead.', { type: 'error' });
    else toast(e.message, { type: 'error' });
  }
}

// ---------- drop to create ----------
export async function dropCreate(items, ui) {
  if (!items.length) return;
  const size = totalSize(items);
  ui.start('Uploading ' + describe(items) + ' (' + bytes(size) + ')…');
  try {
    const res = await upload('/api/v1/drop', toForm(items), (p) => ui.progress(p));
    ui.progress(1);
    state.sites = null;
    celebrate();
    toast('“' + (res.title || res.slug) + '” is live. Share it when you’re ready.', { type: 'ok' });
    navigate('/sites/' + res.slug);
  } catch {
    ui.reset();
  }
}

export async function htmlCreate(html, title) {
  const m = html.match(/<title[^>]*>([^<]{1,100})<\/title>/i);
  const res = await post('/api/v1/drop', { title: title || (m ? m[1].trim() : undefined), files: { 'index.html': html } });
  state.sites = null;
  celebrate();
  toast('Your page is live as “' + (res.title || res.slug) + '”.', { type: 'ok' });
  navigate('/sites/' + res.slug);
}

export const IDEAS = [
  ['📝', 'Leave requests', 'A form where people request time off, and a list for managers to approve them.'],
  ['💸', 'Expense tracker', 'Log expenses with category, amount and receipt note, with monthly totals.'],
  ['📇', 'Team directory', 'A searchable directory of our team with names, roles, and what each person helps with.'],
  ['📅', 'Event sign-up', 'A sign-up page for our team offsite with a headcount and dietary needs.'],
  ['📊', 'KPI dashboard', 'A simple dashboard with four headline numbers and a monthly chart.'],
  ['✅', 'Onboarding checklist', 'A checklist for new joiners that each person can tick off, saved per person.'],
];
