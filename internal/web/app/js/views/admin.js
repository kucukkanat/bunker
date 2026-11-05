// /admin: overview, people, sites, sign-in, AI, M365, connections, sharing, audit.
import { h, icon, clear, empty, avatar, toast, modal, copyBtn, confirmDialog, promptDialog, relTime, fullTime, bytes, busy, toggle, siteIcon, skeletons, debounce, plural, sparkline } from '../ui.js';
import { get, put, post, patch, del, maybe, enc, list } from '../api.js';
import { state } from '../state.js';
import { providerForm, entraAuto } from './setup.js';

const SECTIONS = [
  ['', 'Overview'], ['people', 'People'], ['sites', 'Sites'], ['sign-in', 'Sign-in'], ['ai', 'AI'],
  ['m365', 'Microsoft 365'], ['connections', 'Connections'], ['sharing', 'Sharing'], ['email', 'Email'], ['audit', 'Audit log'],
];

const notYet = (what) => empty({ title: what + ' isn’t installed yet', text: 'This part of Bunker is still being built. When your server is updated, its settings appear here.', art: false, iconName: 'info' });

export default async function admin(root, ctx) {
  const sub = ctx.params[0] || '';
  if (!state.me.isAdmin) {
    return clear(root, h('div.page.narrow', empty({ title: 'Admins only', text: 'Ask a Bunker admin if you need something changed.', actions: [h('a.btn', { href: '/' }, 'Go to Home')] })));
  }
  const body = h('div', skeletons(3, '.line'));
  const title = SECTIONS.find((s) => s[0] === sub)?.[1] || 'Admin';
  clear(root, h('div.page',
    h('div.page-head', h('div', h('h1', 'Admin'), h('p.sub', 'Keep Bunker safe and running for everyone.'))),
    h('nav.subnav', { 'aria-label': 'Admin sections' }, SECTIONS.map(([id, l]) => h('a', { href: '/admin' + (id ? '/' + id : ''), 'aria-current': id === sub ? 'page' : null }, l))),
    body));
  document.title = title + ' · Admin · Bunker';
  const fn = { '': overview, people, sites, 'sign-in': signin, ai, m365, connections, sharing, email, audit }[sub];
  if (!fn) return clear(body, empty({ title: 'Nothing here', text: 'Pick a section above.', art: false }));
  return fn(body, ctx);
}

async function overview(el, ctx) {
  const [stats, users, sites] = await Promise.all([maybe('/api/v1/admin/stats').catch(() => null), get('/api/v1/admin/users', { quiet: true }).catch(() => null), get('/api/v1/sites?filter=everything', { quiet: true }).catch(() => null)]);
  if (!ctx.alive()) return;
  const s = stats || {};
  const n = (v) => (v == null ? '—' : typeof v === 'number' ? v.toLocaleString() : v);
  const u = users?.users || [];
  const weekAgo = Date.now() / 1000 - 7 * 86400;
  const tiles = [
    [s.users ?? u.length, 'people have signed in'],
    [s.activeUsers ?? s.active ?? u.filter((x) => (x.lastSeenAt || x.lastSeen) > weekAgo).length, 'active this week'],
    [s.sites ?? sites?.sites?.length, 'sites'],
    [s.storage != null || s.bytes != null ? bytes(s.storage ?? s.bytes) : null, 'storage used'],
    [s.versions, 'versions kept'],
    [s.aiCalls ?? s.ai?.calls, 'AI requests this month'],
  ].filter(([v]) => v != null);
  const recent = (sites?.sites || []).filter((x) => !x.isTemplate || x.owner?.email !== 'templates@bunker.local').slice(0, 5);
  const upd = h('div');
  maybe('/api/v1/admin/version').then((v) => {
    if (!v?.updateAvailable || !ctx.alive()) return;
    clear(upd, h('div.notice', { style: { marginBottom: '16px' } }, icon('sparkle', 18), h('div', h('b', 'Bunker ' + v.latest + ' is available. '), 'You have ' + v.current + '. ' + v.how + ' ', h('a', { href: v.url, target: '_blank', rel: 'noopener' }, 'What’s new'))));
  }).catch(() => {});
  clear(el,
    upd,
    h('div.stats', tiles.map(([v, l]) => h('div.stat', h('b', n(v)), h('span', l)))),
    h('div', { style: { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(300px, 1fr))', gap: '16px', marginTop: '24px' } },
      h('div.card.pad', h('h3', 'Recently changed sites'), h('div', { style: { marginTop: '10px' } }, recent.length ? recent.map((x) => h('div.person', siteIcon(x, 'sm'), h('div.n', h('b', x.title), h('span', (x.owner?.name || '') + ' · ' + relTime(x.updatedAt))), h('a.btn.sm.ghost', { href: '/s/' + x.slug + '/', target: '_blank', rel: 'noopener' }, 'Open'))) : h('p.small.muted', 'No sites yet.'))),
      h('div.card.pad', h('h3', 'Quick links'), h('div.stack-sm', { style: { marginTop: '10px' } },
        [['sign-in', 'key', 'Change how people sign in'], ['ai', 'sparkle', 'Set up the AI builder'], ['people', 'users', 'Manage admins and accounts'], ['audit', 'history', 'Review the audit log']].map(([id, ic, l]) =>
          h('a.row', { href: '/admin/' + id, style: { textDecoration: 'none', padding: '6px 0' } }, icon(ic, 16), l))))));
}

async function people(el, ctx) {
  const q = h('input.input', { type: 'search', placeholder: 'Search people', 'aria-label': 'Search people', style: { maxWidth: '320px' } });
  const listEl = h('div', skeletons(3, '.line'));
  const transferBtn = h('button.btn', { onclick: () => transferSites() }, icon('arrow', 16), 'Transfer sites');
  const methods = await maybe('/api/v1/auth/methods').catch(() => null);
  const inviteBtn = methods?.local && h('button.btn.primary', { onclick: () => invite() }, icon('mail', 16), 'Invite people');
  const invitesEl = h('div');
  clear(el, h('div.row.wrap', { style: { marginBottom: '16px' } }, q, h('span.spacer'), transferBtn, inviteBtn), invitesEl, listEl);
  let users = [];
  const linkDialog = (title, text, link) => {
    link = new URL(link, location.origin).href; // a full address people can open
    const m = modal({ title, body: [h('p.muted', text), h('div.copy-field', { style: { marginTop: '14px' } }, h('span', { 'data-testid': 'invite-link' }, link), copyBtn(link))], footer: [h('button.btn.primary', { onclick: () => m.close() }, 'Done')] });
  };
  function invite() {
    const email = h('input.input', { type: 'email', required: true, placeholder: 'name@example.com', autofocus: true });
    const name = h('input.input', { placeholder: 'Optional' });
    const groups = h('input.input', { placeholder: 'Optional, like Finance, HR' });
    let adm = false;
    const go = h('button.btn.primary', { type: 'submit' }, 'Create invite link');
    const m = modal({ title: 'Invite someone', body: h('form.stack', { id: 'inv-form', onsubmit: async (e) => {
      e.preventDefault();
      await busy(go, async () => {
        const r = await post('/api/v1/admin/invites', { email: email.value.trim(), name: name.value.trim(), isAdmin: adm, groups: groups.value.split(',').map((x) => x.trim()).filter(Boolean) });
        m.close();
        linkDialog(r.emailed ? 'We emailed ' + r.email : 'Send this link to ' + r.email, (r.emailed ? 'You can also send the link yourself. ' : '') + 'It lets them create their password and sign in. It works once and expires in 7 days.', r.link);
        loadInvites();
      });
    } },
      h('label.field', h('span.label', 'Email'), email),
      h('label.field', h('span.label', 'Name'), name),
      h('label.field', h('span.label', 'Groups'), groups, h('span.hint', 'Sites shared with these groups show up for them.')),
      h('div.setting', h('div', h('b', 'Make them an admin'), h('span.d', 'Admins can change Bunker’s settings and see every site.')), toggle({ checked: false, label: 'Make them an admin', onchange: (v) => (adm = v) })),
      h('div.row', h('span.spacer'), go)) });
  }
  async function loadInvites() {
    if (!methods?.local) return;
    const d = await maybe('/api/v1/admin/invites').catch(() => null);
    const inv = (d?.invites || []).filter((x) => x.kind !== 'reset');
    clear(invitesEl, inv.length > 0 && h('div', { style: { marginBottom: '20px' } }, h('h3', { style: { marginBottom: '8px' } }, 'Waiting to join'),
      h('div.list', inv.map((x) => h('div.item', h('span.avatar', { style: { background: 'var(--sand)', color: 'var(--muted)' } }, icon('mail', 16)),
        h('div.n', h('b', x.name || x.email), h('span', x.email + ' · invited ' + relTime(x.createdAt) + ' · link works for ' + plural(Math.max(1, Math.round((x.expiresAt - Date.now() / 1000) / 86400)), 'more day'))),
        x.isAdmin && h('span.badge.ok', 'Admin'),
        h('button.btn.sm', { type: 'button', title: 'Make a fresh link (the old one stops working)', onclick: async (ev) => busy(ev.currentTarget, async () => {
          const r = await post('/api/v1/admin/invites', { email: x.email, name: x.name, isAdmin: x.isAdmin, groups: x.groups || [] });
          linkDialog('New link for ' + (x.name || x.email), 'The previous link no longer works. This one works once and expires in 7 days.', r.link);
          loadInvites();
        }) }, 'New link'))))));
  }
  loadInvites();
  async function resetLink(u) {
    try {
      const r = await post('/api/v1/admin/users/' + u.id + '/reset-password', {}, { quiet: true });
      linkDialog('Password reset link for ' + (u.name || u.email), 'Send it to them. It lets them choose a new password, works once, and expires in 2 days.', r.link);
    } catch (e) { toast(e.status === 404 ? (u.name || u.email) + ' signs in with the company account, so there’s no password to reset.' : e.message, { type: 'error' }); }
  }
  const draw = () => {
    const f = q.value.trim().toLowerCase();
    const shown = users.filter((u) => !f || (u.name + ' ' + u.email).toLowerCase().includes(f));
    clear(listEl, shown.length ? h('div.list', shown.map((u) => h('div.item', avatar(u.name || u.email),
      h('div.n', h('b', u.name || u.email, u.id === state.me.id && !/\byou\b/i.test(u.name) ? ' (you)' : ''), h('span', u.email + ' · last seen ' + (relTime(u.lastSeenAt || u.lastSeen) || 'never') + (u.groups?.length ? ' · ' + plural(u.groups.length, 'group') : '') + (u.sites ? ' · owns ' + plural(u.sites, 'site') : ''))),
      u.disabled && h('span.badge.danger', 'Disabled'),
      h('label.row.small', 'Admin', toggle({ checked: u.isAdmin, label: 'Admin: ' + (u.name || u.email), disabled: u.id === state.me.id, onchange: async (v, t) => {
        try { await patch('/api/v1/admin/users/' + u.id, { isAdmin: v }); u.isAdmin = v; toast((u.name || u.email) + (v ? ' is now an admin.' : ' is no longer an admin.'), { type: 'ok' }); }
        catch { t.checked = !v; }
      } })),
      u.id !== state.me.id && h('button.btn.sm' + (u.disabled ? '' : '.danger'), { onclick: async () => {
        const who = u.name || u.email;
        const body = { disabled: !u.disabled };
        if (!u.disabled && u.sites > 0) {
          // Owner leaves: offer to move their sites in the same step.
          const to = await promptDialog({ title: 'Disable ' + who + '?', label: 'Move their ' + plural(u.sites, 'site') + ' to… (email)', type: 'email', placeholder: 'new.owner@company.com', hint: 'They’re signed out everywhere and their tokens stop working. The new owner needs to have signed in once. Leave empty to keep the sites with ' + who + '.', confirm: 'Disable account' });
          if (to === null) return;
          if (to) body.transferTo = to;
        } else if (!u.disabled && !(await confirmDialog({ title: 'Disable ' + who + '?', message: 'They’re signed out everywhere and their tokens stop working.', confirm: 'Disable account', danger: true }))) return;
        let r;
        try { r = await patch('/api/v1/admin/users/' + u.id, body, { quiet: true }); }
        catch (e) { toast(e.message, { type: 'error' }); return; }
        u.disabled = !u.disabled;
        if (body.transferTo) u.sites = 0;
        toast(u.disabled ? 'Account disabled.' + (body.transferTo ? ' Moved ' + plural(r?.moved ?? 0, 'site') + ' to ' + body.transferTo + '.' : '') : 'Account enabled again.', { type: 'ok' });
        draw();
      } }, u.disabled ? 'Enable' : 'Disable'),
      methods?.local && h('button.btn.sm.ghost', { 'aria-label': 'Password reset link for ' + u.email, title: 'Password reset link', onclick: () => resetLink(u) }, icon('key', 16)),
      h('button.btn.sm.ghost', { 'aria-label': 'Transfer sites owned by ' + u.email, title: 'Transfer their sites', onclick: () => transferSites(u.email) }, icon('arrow', 16)))))
      : empty({ title: 'Nobody matches', art: false, iconName: 'users' }));
  };
  async function transferSites(from) {
    from = from || (await promptDialog({ title: 'Transfer someone’s sites', label: 'From (their email)', type: 'email', confirm: 'Next' }));
    if (!from) return;
    const to = await promptDialog({ title: 'Who should own ' + from + '’s sites?', label: 'New owner’s email', type: 'email', hint: 'They need to have signed in once. ' + from + ' keeps no access unless shared again.', confirm: 'Transfer all sites' });
    if (!to) return;
    try {
      const r = await post('/api/v1/admin/transfer-sites', { from, to }, { quiet: true });
      toast('Moved ' + plural(r.moved ?? 0, 'site') + ' to ' + to + '.', { type: 'ok' });
    } catch (e) { toast(e.missing ? 'Transferring sites isn’t available on this server yet.' : e.message, { type: 'error' }); }
  }
  q.addEventListener('input', debounce(draw, 120));
  const d = await get('/api/v1/admin/users').catch(() => null);
  if (!ctx.alive() || !d) return;
  users = d.users || [];
  draw();
}

async function sites(el, ctx) {
  const q = h('input.input', { type: 'search', placeholder: 'Search all sites', 'aria-label': 'Search all sites', style: { maxWidth: '320px' } });
  const tbl = h('div', skeletons(3, '.line'));
  clear(el, h('div.row', { style: { marginBottom: '16px' } }, q), tbl);
  const d = await get('/api/v1/sites?filter=everything').catch(() => null);
  if (!ctx.alive() || !d) return;
  const all = d.sites || [];
  const draw = () => {
    const f = q.value.trim().toLowerCase();
    const shown = all.filter((s) => !f || (s.title + ' ' + s.slug + ' ' + (s.owner?.email || '')).toLowerCase().includes(f));
    clear(tbl, shown.length ? h('div.table-wrap', h('table.t',
      h('thead', h('tr', ['Site', 'Owner', 'Visibility', 'Version', 'Updated', ''].map((c) => h('th', { scope: 'col' }, c)))),
      h('tbody', shown.map((s) => h('tr',
        h('td', h('div.row', siteIcon(s, 'sm'), h('div', h('b', s.title), h('div.tiny.faint.mono', '/s/' + s.slug)))),
        h('td', s.owner?.name || ''), h('td', s.orgView ? h('span.badge.ok', 'Whole organization') : h('span.badge', 'Shared people')),
        h('td', s.version ? 'v' + s.version : h('span.faint', 'Not published')), h('td', { title: fullTime(s.updatedAt) }, relTime(s.updatedAt)),
        h('td', h('div.row', h('a.btn.sm.ghost', { href: '/sites/' + s.slug }, 'Manage'), h('a.btn.sm.ghost', { href: '/s/' + s.slug + '/', target: '_blank', rel: 'noopener' }, 'Open')))))))) : empty({ title: f ? 'No sites match' : 'No sites yet', art: false, iconName: 'grid' }));
  };
  q.addEventListener('input', debounce(draw, 120));
  draw();
}

async function signin(el, ctx) {
  const [st, methods] = await Promise.all([get('/api/v1/setup', { quiet: true }).catch(() => null), maybe('/api/v1/auth/methods').catch(() => null)]);
  if (!ctx.alive() || !st) return;
  const NAMES = { entra: 'Microsoft Entra ID', google: 'Google Workspace', oidc: 'OpenID Connect' };
  const sso = methods ? methods.sso : !!st.provider;
  let provider = (methods?.ssoProvider || st.provider) in NAMES ? methods?.ssoProvider || st.provider : 'entra';
  const formSlot = h('div');
  const pick = h('select.input', { 'aria-label': 'Provider', style: { maxWidth: '320px' }, onchange: () => { provider = pick.value; drawForm(); } },
    Object.entries(NAMES).map(([k, v]) => h('option', { value: k, selected: k === provider }, v)));
  const drawForm = () => clear(formSlot, st.dev && h('div.notice.warn', { style: { marginBottom: '16px' } }, icon('warn', 18), h('div', 'Bunker is in dev mode, so sign-in is simulated and these settings can’t be saved here.')),
    provider === 'entra' && !st.dev && [entraAuto({ codeNeeded: false, onDone: () => setTimeout(() => signin(el, ctx), 2500) }), h('p.small.muted', { style: { margin: '16px 0 8px' } }, 'Or enter the values by hand:')],
    providerForm({ provider, admin: true, onSaved: () => { toast('Company sign-in saved. New sign-ins use it right away.', { type: 'ok' }); signin(el, ctx); } }));
  const local = methods && h('div.setting',
    h('div', h('b', 'Built-in accounts'), h('span.d', sso ? 'Let people sign in with an email and password you invite them to. Turn off to require company sign-in.' : 'People sign in with an email and password. Set up company sign-in below before you can turn this off.')),
    toggle({ checked: methods.local, disabled: !sso && methods.local, label: 'Built-in accounts', onchange: async (v, t) => {
      try { await put('/api/v1/admin/auth/local', { enabled: v }); toast(v ? 'Built-in accounts are on.' : 'Built-in accounts are off. Everyone signs in with the company account.', { type: 'ok' }); }
      catch { t.checked = !v; }
    } }));
  clear(el, h('div.card.pad.stack',
    h('dl.kv',
      h('dt', 'People sign in with'), h('dd', [sso && NAMES[provider], methods?.local && 'Built-in accounts', st.dev && 'Dev mode (simulated)'].filter(Boolean).join(' and ') || 'Not set up'),
      h('dt', 'Web address'), h('dd', st.url || h('span.badge.draft', 'Not set: use BUNKER_URL')),
      h('dt', 'Redirect URI'), h('dd.mono', st.urlKnown ? st.redirectUri : location.origin + '/auth/callback'),
      st.secretExpires && [h('dt', 'Client secret valid until'), h('dd', expiry(st.secretExpires))]),
    local,
    h('hr.divider'),
    h('h3', sso ? 'Change company sign-in' : 'Set up company sign-in'),
    h('p.small.muted', 'Let people use their Microsoft, Google or other work account. Bunker tests the settings before saving, so a typo won’t lock anyone out.'),
    h('label.field', h('span.label', 'Provider'), pick),
    formSlot));
  drawForm();
}

function expiry(iso) {
  const t = Date.parse(iso);
  if (!t) return iso;
  const days = Math.round((t - Date.now()) / 86400000);
  const when = new Date(t).toLocaleDateString();
  if (days < 0) return h('span.badge.danger', 'Expired on ' + when + ': sign-in stops working. Make a new secret.');
  if (days < 30) return h('span.badge.draft', when + ' (in ' + plural(days, 'day') + '). Make a new secret soon.');
  return when;
}

// Generic settings form for config objects of any shape.
function configForm(cfg, { onSave, hints = {}, selects = {} }) {
  const values = { ...cfg };
  const SECRET = /key|secret|password|token/i;
  const skip = /^(usage|stats|ok|updatedAt|configured)$/;
  const human = (k) => hints[k]?.label || k.replace(/([a-z])([A-Z])/g, '$1 $2').replace(/[._]/g, ' ').replace(/^./, (c) => c.toUpperCase());
  const fields = Object.keys(cfg).filter((k) => !skip.test(k) && (cfg[k] == null || typeof cfg[k] !== 'object' || Array.isArray(cfg[k])));
  const save = h('button.btn.primary', { type: 'submit' }, 'Save');
  const form = h('form.stack', { onsubmit: async (e) => { e.preventDefault(); await busy(save, () => onSave(values)); } },
    fields.map((k) => {
      const v = cfg[k];
      const hint = hints[k]?.hint;
      if (typeof v === 'boolean') return h('div.setting', h('div', h('b', human(k)), hint && h('span.d', hint)), toggle({ checked: v, label: human(k), onchange: (x) => (values[k] = x) }));
      let inp;
      if (selects[k]) inp = h('select.input', { onchange: (e) => (values[k] = e.target.value) }, selects[k].map(([o, l]) => h('option', { value: o, selected: o === v }, l)));
      else if (Array.isArray(v)) inp = h('textarea.input', { rows: 2, oninput: (e) => (values[k] = e.target.value.split(/[\n,]/).map((x) => x.trim()).filter(Boolean)) }, v.join(', '));
      else if (typeof v === 'number') inp = h('input.input', { type: 'number', value: v, oninput: (e) => (values[k] = +e.target.value) });
      else if (SECRET.test(k)) { inp = h('input.input', { type: 'password', autocomplete: 'off', placeholder: v ? 'Saved. Type to replace it.' : 'Not set', oninput: (e) => (values[k] = e.target.value) }); values[k] = undefined; }
      else inp = h('input.input', { value: v ?? '', oninput: (e) => (values[k] = e.target.value) });
      return h('label.field', h('span.label', human(k)), inp, hint && h('span.hint', hint));
    }),
    h('div.row', h('span.spacer'), save));
  return form;
}

async function ai(el, ctx) {
  let cfg;
  try { cfg = await get('/api/v1/admin/ai', { quiet: true }); }
  catch (e) { if (ctx.alive()) clear(el, e.missing || e.status === 404 ? notYet('The AI builder') : empty({ title: 'Couldn’t load AI settings', text: e.message, art: false })); return; }
  if (!ctx.alive()) return;
  const providers = cfg.providers?.length ? cfg.providers : [{ id: 'none', label: 'Off' }, { id: 'anthropic', label: 'Anthropic (Claude)', needsKey: true }, { id: 'openai', label: 'OpenAI', needsKey: true }];
  const pinned = new Set(cfg.pinnedByEnv || []);
  const q = cfg.quotas || {};
  const v = { provider: cfg.provider || 'none' };
  const inp = (k, val, extra = {}) => h('input.input', { value: val ?? '', oninput: (e) => (v[k] = extra.type === 'number' ? +e.target.value : e.target.value), ...extra });
  const key = h('input.input', { type: 'password', autocomplete: 'off', placeholder: cfg.apiKeySet ? 'Saved (' + (cfg.apiKey || '••••') + '). Type to replace it.' : 'Paste the API key', oninput: (e) => (v.apiKey = e.target.value) });
  const endpoint = inp('endpoint', cfg.endpoint, { placeholder: 'https://…' });
  const apiVersion = inp('apiVersion', cfg.apiVersion);
  const builderModel = inp('builderModel', cfg.builderModel);
  const pageModel = inp('pageModel', cfg.pageModel);
  const userQ = inp('userDailyTokens', q.userDailyTokens, { type: 'number', min: 0 });
  const siteQ = inp('siteDailyTokens', q.siteDailyTokens, { type: 'number', min: 0 });
  const fieldWrap = (label, input, hint) => h('label.field', h('span.label', label), input, hint && h('span.hint', hint));
  const fEndpoint = fieldWrap('Endpoint', endpoint, 'Your Azure resource URL, or the address of an Ollama, LM Studio or vLLM server.');
  const fVersion = fieldWrap('API version', apiVersion);
  const fKey = fieldWrap('API key', key, 'Stored encrypted on the server. Pages and people never see it.');
  const sel = h('select.input', { 'aria-label': 'AI provider', onchange: () => { v.provider = sel.value; const p = providers.find((x) => x.id === v.provider) || {}; if (p.builderModel && !builderModel.value) { builderModel.value = v.builderModel = p.builderModel; } if (p.pageModel && !pageModel.value) { pageModel.value = v.pageModel = p.pageModel; } if (p.endpoint && !endpoint.value) { endpoint.value = v.endpoint = p.endpoint; } shape(); } },
    providers.map((p) => h('option', { value: p.id, selected: p.id === v.provider }, p.id === 'none' ? 'Off' : p.label)));
  const models = h('div', { style: { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: '16px' } }, fieldWrap('Model for the builder', builderModel, 'Azure: the deployment name.'), fieldWrap('Model for pages (bunker.ai)', pageModel, 'A smaller, cheaper model works well here.'));
  const limits = h('div', { style: { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: '16px' } }, fieldWrap('Tokens per person per day', userQ, '0 means no limit.'), fieldWrap('Tokens per site per day', siteQ, '0 means no limit.'));
  const shape = () => {
    const p = providers.find((x) => x.id === v.provider) || {};
    const off = v.provider === 'none';
    fKey.classList.toggle('hidden', off || p.needsKey === false && v.provider !== 'openai-compatible');
    fEndpoint.classList.toggle('hidden', !/azure|compatible/.test(v.provider));
    fVersion.classList.toggle('hidden', !/azure/.test(v.provider));
    models.classList.toggle('hidden', off);
    limits.classList.toggle('hidden', off);
    // Hints that fit the chosen provider.
    const az = /azure/.test(v.provider), local = v.provider === 'openai-compatible';
    const hint = (f, t) => { const el = f.querySelector('.hint'); if (el) el.textContent = t; };
    hint(fEndpoint, az ? 'Your Azure OpenAI resource, like https://my-team.openai.azure.com' : 'The address of your Ollama, LM Studio or vLLM server, like http://localhost:11434/v1');
    hint(fKey, local ? 'Leave empty if your server doesn’t need one. Stored encrypted; pages never see it.' : 'Stored encrypted on the server. Pages and people never see it.');
    hint(models.firstChild, az ? 'The deployment name in Azure.' : local ? 'The model name on your server, like llama3.1 or qwen2.5-coder.' : 'Leave as is unless you want a different model.');
    builderModel.placeholder = local ? 'llama3.1' : '';
  };
  const status = h('div');
  const paintStatus = (c) => clear(status, c.configured ? h('div.notice.ok', icon('check', 18), h('div', h('b', 'AI is on. '), 'Using ' + (c.providerLabel || c.provider) + '.'))
    : h('div.notice' + (c.provider && c.provider !== 'none' ? '.warn' : ''), icon(c.provider && c.provider !== 'none' ? 'warn' : 'info', 18), h('div', h('b', c.provider && c.provider !== 'none' ? 'Not working yet. ' : 'AI is off. '), (c.provider && c.provider !== 'none' && c.problem) || 'Pick a provider and add its API key to turn on the AI builder.')));
  paintStatus(cfg);
  // One-click options: keys already in the environment, local model servers.
  const quick = h('div');
  maybe('/api/v1/admin/ai/detect').then((d) => {
    const opts = (d?.options || []).filter((o) => !(cfg.configured && o.provider === cfg.provider));
    if (!opts.length || !ctx.alive()) return;
    clear(quick, h('div.notice', icon('sparkle', 18), h('div', h('b', 'Found on this machine: '),
      opts.map((o) => h('button.btn.sm', { type: 'button', style: { margin: '4px 8px 0 0' }, onclick: (ev) => busy(ev.currentTarget, async () => {
        const body = { provider: o.provider };
        if (o.endpoint) body.endpoint = o.endpoint;
        if (o.models?.length) { const best = o.models.find((m) => /coder|code/i.test(m)) || o.models[0]; body.builderModel = best; body.pageModel = best; }
        const n = await put('/api/v1/admin/ai', body);
        state.ai = undefined;
        toast('AI is on: ' + o.name + '.', { type: 'ok' });
        if (n) paintStatus(n);
        ai(el, ctx);
      }) }, 'Use ' + o.name + (o.models?.length ? ' (' + (o.models.find((m) => /coder|code/i.test(m)) || o.models[0]) + ')' : ''))))));
  }).catch(() => {});
  const result = h('div');
  const test = h('button.btn', { type: 'button', onclick: () => busy(test, async () => {
    try {
      const r = await post('/api/v1/admin/ai/test', {}, { quiet: true });
      clear(result, r && r.ok !== false
        ? h('div.notice.ok', icon('check', 18), h('div', h('b', 'It works. '), (r.model ? r.model + ' replied' : 'The provider replied') + (r.latencyMs ? ' in ' + (r.latencyMs / 1000).toFixed(1) + 's' : '') + (r.reply ? ': “' + r.reply + '”' : '.')))
        : h('div.notice.warn', icon('warn', 18), h('div', h('b', 'The test failed. '), r?.error || r?.detail || 'The provider didn’t answer.')));
    } catch (e) { clear(result, h('div.notice.warn', icon('warn', 18), h('div', h('b', 'The test failed. '), e.message))); }
  }) }, icon('sparkle', 16), 'Send a test message');
  const save = h('button.btn.primary', { type: 'submit' }, 'Save');
  const usageEl = h('div');
  clear(el, h('form.card.pad.stack', { onsubmit: async (e) => {
    e.preventDefault();
    await busy(save, async () => {
      const n = await put('/api/v1/admin/ai', v);
      state.ai = undefined;
      delete v.apiKey;
      key.value = '';
      if (n && typeof n === 'object') { paintStatus(n); if (n.apiKeySet) key.placeholder = 'Saved (' + (n.apiKey || '••••') + '). Type to replace it.'; }
      toast('AI settings saved.', { type: 'ok' });
    });
  } },
    h('p.muted', 'Bunker uses this AI service for the builder and for sites that use AI. Keys stay on the server and pages never see them.'),
    status,
    quick,
    pinned.size > 0 && h('p.small.muted', 'Some settings are fixed by the server’s environment and can’t be changed here: ' + [...pinned].join(', ') + '.'),
    fieldWrap('Provider', sel), fKey, fEndpoint, fVersion, models, limits,
    h('div.row.wrap', test, h('span.spacer'), save), result),
    h('div.card.pad', { style: { marginTop: '16px' } }, h('h3', 'Usage in the last 30 days'), usageEl));
  shape();
  const u = await maybe('/api/v1/admin/ai/usage?days=30').catch(() => null);
  if (!ctx.alive()) return;
  if (!u) return clear(usageEl, h('p.small.muted', { style: { marginTop: '8px' } }, 'No usage yet.'));
  const t = u.totals || {};
  const top = (u.byUser || []).slice(0, 5);
  clear(usageEl,
    h('div.stats', { style: { marginTop: '12px' } }, h('div.stat', h('b', (t.calls || 0).toLocaleString()), h('span', 'requests')), h('div.stat', h('b', (t.tokens || 0).toLocaleString()), h('span', 'tokens'))),
    (u.byDay || []).length > 1 && h('div', { style: { color: 'var(--moss)', marginTop: '12px' } }, sparkline(u.byDay.map((d) => d.tokens || 0), 300, 48)),
    top.length > 0 && h('div', { style: { marginTop: '12px' } }, h('b.small', 'Top people'), top.map((x) => h('div.person', avatar(x.name || x.email), h('div.n', h('b', x.name || x.email), h('span', (x.tokens || 0).toLocaleString() + ' tokens · ' + plural(x.calls || 0, 'request')))))));
}

async function m365(el, ctx) {
  let cfg;
  try { cfg = await get('/api/v1/admin/m365', { quiet: true }); }
  catch (e) { if (ctx.alive()) clear(el, notYet('Microsoft 365')); return; }
  if (!ctx.alive()) return;
  clear(el, h('div.card.pad.stack',
    h('p.muted', 'Let sites read SharePoint lists, Excel files and OneDrive with each viewer’s own permissions. A page only sees what the person looking at it can already see.'),
    configForm(cfg, { hints: { scopes: { hint: 'Microsoft Graph permissions pages may ask for, like Sites.Read.All, Files.Read. They need admin consent in Entra.' } }, onSave: async (v) => { await put('/api/v1/admin/m365', v); toast('Microsoft 365 settings saved.', { type: 'ok' }); } })));
}

async function connections(el, ctx) {
  let d;
  try { d = await get('/api/v1/admin/connections', { quiet: true }); }
  catch { if (ctx.alive()) clear(el, notYet('Connections')); return; }
  if (!ctx.alive()) return;
  const items = list(d, 'connections', 'items');
  const name = h('input.input', { placeholder: 'crm', required: true, pattern: '[a-z0-9-]+', 'aria-label': 'Name' });
  const base = h('input.input', { placeholder: 'https://api.example.com', type: 'url', required: true, 'aria-label': 'Base URL' });
  const hdr = h('input.input', { placeholder: 'Authorization: Bearer …', 'aria-label': 'Secret header', autocomplete: 'off' });
  const desc = h('input.input', { placeholder: 'What it’s for', 'aria-label': 'Description' });
  const add = h('button.btn.primary', { type: 'submit' }, icon('plus', 16), 'Add connection');
  clear(el,
    h('p.muted', { style: { marginBottom: '16px' } }, 'Connections let sites call an internal system (like a CRM or HR tool) through Bunker. Secrets stay on the server; pages only use the name.'),
    items.length ? h('div.list', items.map((c) => h('div.item', h('span.site-icon.sm', icon('plug', 16)), h('div.n', h('b', c.name), h('span', (c.baseUrl || c.url || '') + (c.description ? ' · ' + c.description : ''))),
      h('button.btn.sm.danger', { onclick: async () => {
        if (!(await confirmDialog({ title: 'Remove the connection “' + c.name + '”?', message: 'Sites that use it stop being able to reach that system.', confirm: 'Remove', danger: true }))) return;
        await del('/api/v1/admin/connections/' + enc(c.name || c.id));
        toast('Connection removed.');
        connections(el, ctx);
      } }, 'Remove')))) : empty({ title: 'No connections yet', text: 'Add one below to let sites reach an internal system safely.', art: false, iconName: 'plug' }),
    h('form.card.pad.stack', { style: { marginTop: '16px' }, onsubmit: async (e) => {
      e.preventDefault();
      await busy(add, async () => {
        const [hk, ...hv] = hdr.value.split(':');
        await post('/api/v1/admin/connections', { name: name.value, baseUrl: base.value, description: desc.value, headers: hdr.value ? { [hk.trim()]: hv.join(':').trim() } : {} });
        toast('Connection added.', { type: 'ok' });
        connections(el, ctx);
      });
    } }, h('h3', 'New connection'),
      h('div', { style: { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: '12px' } },
        h('label.field', h('span.label', 'Name'), name), h('label.field', h('span.label', 'Base URL'), base), h('label.field', h('span.label', 'Secret header (optional)'), hdr), h('label.field', h('span.label', 'Description'), desc)),
      h('div.row', h('span.spacer'), add)));
}

async function sharing(el, ctx) {
  const d = await maybe('/api/v1/admin/policy').catch(() => null);
  if (!ctx.alive()) return;
  if (!d) return clear(el, notYet('Sharing policy'));
  const p = d.policy || {}, pinned = d.pinned || {};
  const save = async (body, undo) => {
    try { await put('/api/v1/admin/policy', body, { quiet: true }); toast('Saved.', { type: 'ok', timeout: 2000 }); return true; }
    catch (e) { toast(e.message, { type: 'error' }); undo && undo(); return false; }
  };
  const lock = (k) => pinned[k] && h('span.badge', { title: 'Set by the server’s environment, so it can’t be changed here.' }, icon('lock', 12), ' Set by the server');
  const sw = (k, title, desc) => h('div.setting', h('div', h('b', title, ' ', lock(k)), h('span.d', desc)),
    toggle({ checked: !!p[k], disabled: pinned[k], label: title, onchange: (x, t) => save({ [k]: x }, () => (t.checked = !x)) }));
  const num = (k, title, desc, unit) => {
    const inp = h('input.input', { type: 'number', min: 1, value: p[k], disabled: pinned[k], 'aria-label': title, style: { width: '110px' }, onchange: async () => {
      const v = Math.round(+inp.value);
      if (!(v >= (k === 'maxSitesPerUser' ? 0 : 1))) { inp.value = p[k]; return; }
      if (await save({ [k]: v }, () => (inp.value = p[k]))) p[k] = v;
    } });
    return h('div.setting', h('div', h('b', title, ' ', lock(k)), h('span.d', desc)), h('label.row.small', inp, unit));
  };
  clear(el, h('div.card.pad',
    h('p.muted', { style: { marginBottom: '8px' } }, 'Sites are private until their owner shares them, and can never be shared outside this Bunker.'),
    sw('orgWideSharing', 'Allow sharing with everyone', 'Owners can let everyone who signs in to Bunker view a site.'),
    sw('editorsCanShare', 'Editors can share', 'When off, only a site’s owner can add people.'),
    'publicLinks' in p && sw('publicLinks', 'Allow public links', 'Owners can let anyone with the link view a site, without signing in. Turning this off closes every public link at once.'),
    num('siteMaxMB', 'Largest site', 'The most a single site can hold, files included.', 'MB'),
    num('uploadMaxMB', 'Largest upload', 'The most someone can upload at once.', 'MB'),
    num('maxSitesPerUser', 'Sites per person', 'How many sites each person can own.', 'sites')));
}

async function email(el, ctx) {
  const d = await maybe('/api/v1/admin/mail').catch(() => null);
  if (!ctx.alive()) return;
  if (!d) return clear(el, notYet('Email'));
  const url = h('input.input', { value: d.smtpUrl || '', placeholder: 'smtp://user:password@smtp.example.com:587', disabled: d.pinned, autocomplete: 'off', spellcheck: false });
  const from = h('input.input', { value: d.from || '', placeholder: 'Bunker <bunker@example.com>' });
  const status = h('p', d.configured ? h('span.badge.ok', icon('check', 12), ' Email is on') : h('span.badge', 'Email is off'), d.problem ? h('span.muted', ' ' + d.problem) : null);
  const save = h('button.btn.primary', { type: 'submit' }, 'Save');
  const test = h('button.btn', { type: 'button', disabled: !d.configured, onclick: async () => busy(test, async () => {
    const r = await post('/api/v1/admin/mail/test', {});
    r.ok ? toast('Test email sent to ' + state.me.email + '.', { type: 'ok' }) : toast('Couldn’t send: ' + r.error, { type: 'error', timeout: 9000 });
  }) }, 'Send me a test email');
  clear(el, h('div.card.pad', h('form.stack', { onsubmit: async (e) => { e.preventDefault(); await busy(save, async () => {
    await put('/api/v1/admin/mail', { smtpUrl: url.value.trim(), from: from.value.trim() });
    toast('Saved.', { type: 'ok', timeout: 2000 });
    email(el, ctx);
  }); } },
    h('p.muted', 'Optional. With an email server, Bunker emails invite links, password resets and notifications (people can turn notifications off in Settings). Without one, you copy links yourself and notifications stay in the bell.'),
    status,
    h('label.field', h('span.label', 'SMTP server'), url, h('span.hint', d.pinned ? 'Set by the server’s environment (BUNKER_SMTP_URL).' : 'Use smtp:// for port 587 (STARTTLS) or smtps:// for port 465. Microsoft 365: smtp://you%40corp.com:password@smtp.office365.com:587. Leave empty to turn email off.')),
    h('label.field', h('span.label', 'From'), from),
    h('div.row', test, h('span.spacer'), save))));
}

async function audit(el, ctx) {
  const q = h('input.input', { type: 'search', placeholder: 'Search by person, site or event', 'aria-label': 'Search the audit log', style: { maxWidth: '320px' } });
  const kind = h('select.input', { 'aria-label': 'Event type', style: { width: 'auto' } }, [['', 'All events'], ['site.', 'Sites'], ['site.share', 'Sharing'], ['auth.', 'Sign-ins'], ['token.', 'Tokens'], ['admin.', 'Admin changes'], ['ai.', 'AI'], ['m365.', 'Microsoft 365'], ['upload.blocked', 'Blocked uploads']].map(([v, l]) => h('option', { value: v }, l)));
  const since = h('select.input', { 'aria-label': 'Time range', style: { width: 'auto' } }, [['1', 'Last 24 hours'], ['7', 'Last 7 days'], ['30', 'Last 30 days'], ['', 'All time']].map(([v, l]) => h('option', { value: v, selected: v === '7' }, l)));
  const exp = h('a.btn', { 'data-native': '', download: 'bunker-audit.csv' }, icon('down', 16), 'Export CSV');
  const tbl = h('div', skeletons(4, '.line'));
  clear(el, h('div.row.wrap', { style: { marginBottom: '16px' } }, q, kind, since, h('span.spacer'), exp), tbl);
  const params = () => {
    const p = new URLSearchParams();
    if (q.value.trim()) p.set('q', q.value.trim());
    if (kind.value) p.set('event', kind.value);
    if (since.value) p.set('since', String(Math.floor(Date.now() / 1000 - +since.value * 86400)));
    p.set('limit', '200');
    return p;
  };
  const load = async () => {
    const p = params();
    const ex = new URLSearchParams(p);
    ex.set('format', 'csv');
    ex.delete('limit');
    exp.href = '/api/v1/admin/audit/export?' + ex;
    let d;
    try { d = await get('/api/v1/admin/audit?' + p, { quiet: true }); }
    catch (e) { if (ctx.alive()) { clear(tbl, notYet('The audit log viewer')); exp.classList.add('hidden'); } return; }
    if (!ctx.alive()) return;
    const ev = list(d, 'events', 'entries', 'items');
    clear(tbl, ev.length ? h('div.table-wrap', h('table.t',
      h('thead', h('tr', ['When', 'Who', 'What', 'Site', 'Details'].map((c) => h('th', { scope: 'col' }, c)))),
      h('tbody', ev.map((x) => h('tr',
        h('td', { title: fullTime(x.at || x.createdAt || x.time) }, relTime(x.at || x.createdAt || x.time)),
        h('td', x.userName || x.user?.name || x.email || x.userEmail || x.actor || '—'),
        h('td', h('code', x.event || x.action)),
        h('td', x.siteSlug || x.site || x.siteId || ''),
        h('td.sys', [x.target, x.ip, x.meta && typeof x.meta === 'object' ? JSON.stringify(x.meta) : x.meta].filter(Boolean).join(' · ')))))))
      : empty({ title: 'Nothing in this range', text: 'Try a longer time range or clear the search.', art: false, iconName: 'history' }));
  };
  q.addEventListener('input', debounce(load, 300));
  kind.addEventListener('change', load);
  since.addEventListener('change', load);
  load();
}
