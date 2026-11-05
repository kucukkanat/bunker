// /settings: profile, access tokens, connect your tools.
import { h, icon, clear, avatar, toast, confirmDialog, relTime, busy, codeBlock, copyBtn, empty, skeletons } from '../ui.js';
import { get, post, del, maybe } from '../api.js';
import { state } from '../state.js';
import { signOut } from '../main.js';

const TABS = [['', 'Profile'], ['tokens', 'Access tokens'], ['connect', 'Connect your tools']];

export default async function settings(root, ctx) {
  const sub = ctx.params[0] || '';
  const body = h('div');
  clear(root, h('div.page.narrow',
    h('div.page-head', h('div', h('h1', sub === 'connect' ? 'Connect your tools' : 'Settings'), h('p.sub', sub === 'connect' ? 'Use Bunker from the command line, or let an AI coding assistant build and publish for you.' : 'Your account and how other tools sign in as you.'))),
    h('nav.subnav', { 'aria-label': 'Settings sections' }, TABS.map(([id, l]) => h('a', { href: '/settings' + (id ? '/' + id : ''), 'aria-current': id === sub ? 'page' : null }, l))),
    body));
  if (sub === 'tokens') return tokens(body, ctx);
  if (sub === 'connect') return connect(body);
  return profile(body);
}

function passwordCard() {
  const cur = h('input.input', { type: 'password', required: true, autocomplete: 'current-password' });
  const nw = h('input.input', { type: 'password', required: true, minlength: 10, autocomplete: 'new-password' });
  const nw2 = h('input.input', { type: 'password', required: true, minlength: 10, autocomplete: 'new-password' });
  nw2.addEventListener('input', () => nw2.removeAttribute('aria-invalid'));
  const btn = h('button.btn.primary', { type: 'submit' }, 'Change password');
  const f = (l, i, hint) => h('label.field', h('span.label', l), i, hint && h('span.hint', hint));
  return h('form.card.pad.stack', { style: { marginTop: '16px' }, onsubmit: async (e) => {
    e.preventDefault();
    if (nw.value !== nw2.value) { nw2.setAttribute('aria-invalid', 'true'); nw2.focus(); return toast('The new passwords don’t match.', { type: 'error' }); }
    await busy(btn, async () => {
      await post('/api/v1/me/password', { current: cur.value, new: nw.value });
      cur.value = nw.value = nw2.value = '';
      toast('Password changed. Use the new one next time you sign in.', { type: 'ok' });
    });
  } },
    h('h3', 'Password'),
    h('p.small.muted', 'For signing in with your email. If you use your company account, you don’t need one.'),
    h('div', { style: { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: '12px' } },
      f('Current password', cur), f('New password', nw, 'At least 10 characters.'), f('New password again', nw2)),
    h('div.row', h('span.spacer'), btn));
}

function profile(el) {
  const me = state.me;
  const pwSlot = h('div');
  maybe('/api/v1/auth/methods').then((m) => { if (m?.local && !me.dev) pwSlot.replaceWith(passwordCard()); }).catch(() => {});
  clear(el,
    h('div.card.pad', h('div.row', { style: { gap: '16px' } }, avatar(me.name || me.email, 'lg'),
      h('div', h('h2', me.name || me.email), h('p.muted', me.email), h('div.row.wrap', { style: { marginTop: '8px' } },
        me.isAdmin && h('span.badge.ok', icon('shield', 12), 'Admin'), me.dev && h('span.pill-dev', 'Dev mode'))))),
    h('div.card.pad', { style: { marginTop: '16px' } },
      h('h3', 'Groups'),
      h('p.small.muted', { style: { margin: '4px 0 12px' } }, 'Sites shared with these groups show up for you automatically. Groups come from your company sign-in.'),
      me.groups.length ? h('div.chips', me.groups.map((g) => h('span.chip', icon('users', 14), g.name || g.id))) : h('p.small.faint', 'You’re not in any groups yet.')),
    pwSlot,
    h('div.card.pad', { style: { marginTop: '16px' } },
      h('h3', 'Sign out'),
      h('div.setting', h('div', h('b', 'This browser'), h('span.d', 'You’ll need to sign in again here.')), h('button.btn', { onclick: () => signOut() }, icon('logout', 16), 'Sign out')),
      h('div.setting', h('div', h('b', 'Everywhere'), h('span.d', 'Signs you out on every computer and phone. Access tokens keep working until you revoke them.')),
        h('button.btn.danger', { onclick: async () => { if (await confirmDialog({ title: 'Sign out everywhere?', message: 'Every browser where you’re signed in will need to sign in again, including this one.', confirm: 'Sign out everywhere', danger: true })) signOut(true); } }, 'Sign out everywhere'))));
}

async function tokens(el, ctx) {
  const listEl = h('div', skeletons(2, '.line'));
  const name = h('input.input', { placeholder: 'What’s it for? e.g. “Laptop CLI”', required: true, maxlength: 60, 'aria-label': 'Token name' });
  const days = h('select.input', { 'aria-label': 'Expires after', style: { width: 'auto' } }, [7, 30, 90].map((d) => h('option', { value: d, selected: d === 30 }, d + ' days')));
  const scopes = { read: true, write: true, admin: false };
  const scopeBox = (k, label, desc) => h('label.check', h('input', { type: 'checkbox', checked: scopes[k], onchange: (e) => (scopes[k] = e.target.checked) }), h('div', h('b', label), h('span', desc)));
  const once = h('div');
  const create = h('button.btn.primary', { type: 'submit' }, icon('plus', 16), 'Create token');
  clear(el,
    h('div.notice', icon('info', 18), h('div', 'Tokens let scripts and tools act as you. Treat them like a password: never paste them into a web page or share them. For most tools, ', h('a', { href: '/settings/connect' }, 'signing in from the tool'), ' is easier.')),
    h('form.card.pad.stack', { style: { marginTop: '16px' }, onsubmit: async (e) => {
      e.preventDefault();
      const sc = Object.keys(scopes).filter((k) => scopes[k]);
      if (!sc.length) return toast('Pick at least one thing the token can do.');
      await busy(create, async () => {
        const r = await post('/api/v1/tokens', { name: name.value.trim(), scopes: sc, days: +days.value });
        clear(once, h('div.token-once', h('b', 'Here’s your new token. Copy it now: it won’t be shown again.'),
          h('div.copy-field', { style: { marginTop: '10px', background: 'var(--surface)' } }, h('span', { 'data-testid': 'new-token' }, r.token), copyBtn(r.token))));
        name.value = '';
        load();
      });
    } },
      h('h3', 'New token'),
      h('div.row.wrap', { style: { alignItems: 'flex-start' } }, h('div', { style: { flex: '1 1 240px' } }, name), days),
      h('div', scopeBox('read', 'See', 'Read your sites and their files.'), scopeBox('write', 'Change', 'Create, edit and publish sites.'), state.me.isAdmin && scopeBox('admin', 'Administer', 'Change Bunker’s settings. Only for admin scripts.')),
      h('div.row', h('span.spacer'), create), once),
    h('h3', { style: { margin: '28px 0 12px' } }, 'Your tokens'),
    listEl);
  async function load() {
    const d = await get('/api/v1/tokens').catch(() => null);
    if (!d || !ctx.alive()) return;
    const ts = d.tokens || [];
    clear(listEl, ts.length ? h('div.list', ts.map((t) => {
      const rv = h('button.btn.sm.danger', { onclick: async () => {
        if (!(await confirmDialog({ title: 'Revoke “' + t.name + '”?', message: 'Anything using this token stops working right away.', confirm: 'Revoke token', danger: true }))) return;
        await busy(rv, () => del('/api/v1/tokens/' + t.id));
        toast('Token revoked.');
        load();
      } }, 'Revoke');
      return h('div.item', h('span.site-icon.sm', icon(t.client && t.client !== 'manual' ? 'terminal' : 'key', 16)),
        h('div.n', h('b', t.name), h('span', h('code', t.prefix + '…'), ' · ' + t.scopes.join(', ') + ' · created ' + relTime(t.createdAt) + ' · ' + (t.lastUsed ? 'last used ' + relTime(t.lastUsed) : 'never used') + (t.expiresAt ? ' · expires ' + new Date(t.expiresAt * 1000).toLocaleDateString() : ''))),
        rv);
    })) : empty({ title: 'No tokens yet', text: 'When you sign in from the command line or an AI assistant, its token shows up here so you can revoke it.', art: false, iconName: 'key' }));
  }
  load();
}

function connect(el) {
  const o = location.origin;
  const mcpJson = JSON.stringify({ mcpServers: { bunker: { url: o + '/mcp' } } }, null, 2);
  const localJson = JSON.stringify({ mcpServers: { bunker: { command: 'bunker', args: ['mcp'] } } }, null, 2);
  const agent = `You can publish internal web apps to our Bunker server at ${o}.
Use the "bunker" MCP tools (or the \`bunker\` CLI) to create a site, write its files as a draft, and publish when I say so.
Rules for pages:
- Plain HTML, CSS and JavaScript. index.html is the start page.
- Pages run in a sandbox with no internet access: don't load scripts, fonts or APIs from other websites. Inline or bundle everything.
- To save data, use the built-in SDK (window.bunker): bunker.db.collection('name').add({...}), .list(), .subscribe(cb); bunker.me() for the viewer's name and email; bunker.files for uploads; bunker.ai.chat() for AI.
- Every publish creates a version, so it's safe to iterate. Save drafts first and show me the preview link.
Full guide: ${o}/llms.txt`;
  const card = (ic, title, text, ...kids) => h('div.card.pad.stack', { style: { marginBottom: '16px' } }, h('div.row', h('span.site-icon.sm', icon(ic, 16)), h('h3', title)), text && h('p.muted.small', text), kids);
  clear(el,
    card('terminal', 'Command line', 'Deploy a folder from your computer. The first command opens your browser so you can approve the sign-in.',
      codeBlock(`bunker login ${o}\nbunker deploy ./my-site`)),
    card('sparkle', 'Claude Code', 'Add Bunker as a tool, then ask Claude to “build a leave-request form and publish it to Bunker”.',
      codeBlock(`claude mcp add --transport http bunker ${o}/mcp`)),
    card('code', 'Cursor, VS Code and other assistants', 'Paste this into your MCP settings (for example .cursor/mcp.json). You’ll be asked to sign in the first time.',
      codeBlock(mcpJson),
      h('details', h('summary.small', { style: { cursor: 'pointer', color: 'var(--muted)' } }, 'Prefer to run it locally with the bunker CLI?'), h('div', { style: { marginTop: '10px' } }, codeBlock(localJson)))),
    card('chat', 'Agent prompt', 'Give this to any AI assistant so it knows how to build for Bunker.', codeBlock(agent)),
    h('p.small.muted', 'Signed-in tools appear under ', h('a', { href: '/settings/tokens' }, 'Access tokens'), ', where you can revoke them at any time.'));
}
