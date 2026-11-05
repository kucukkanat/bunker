// /device?code=XXXX-XXXX: approve a CLI or agent sign-in.
import { h, icon, clear, busy } from '../ui.js';
import { get, post, enc } from '../api.js';
import { state } from '../state.js';
import { navigate } from '../router.js';

const SCOPES = {
  read: ['eye', 'See your sites', 'Read the sites you can open, their files and versions.'],
  write: ['pencil', 'Change and publish sites', 'Create sites, edit files and publish new versions as you.'],
  admin: ['shield', 'Administer Bunker', 'Change Bunker’s settings, like an admin can.'],
};
const CLIENTS = { cli: 'The Bunker command line', mcp: 'An AI assistant (MCP)', agent: 'An AI agent' };

const brand = () => h('a.brand', { href: '/' }, h('img', { src: '/_/icon.svg', alt: '' }), 'bunker');

export default async function device(root, ctx) {
  const card = h('div.bare-card.sm');
  clear(root, h('div.bare', brand(), card));
  const code = (ctx.query.get('code') || '').toUpperCase().trim();
  if (!code) return askCode(card);
  clear(card, h('div.skel.line', { style: { width: '70%', margin: '0 auto' } }), h('div.skel.line', { style: { width: '50%', margin: '14px auto 0' } }));
  let info;
  try { info = await get('/api/v1/auth/device/' + enc(code), { quiet: true }); }
  catch (e) {
    return clear(card, h('div.big-check', { style: { background: 'var(--accent-soft)', color: 'var(--accent)' } }, icon('warn', 32)),
      h('h1', 'That code didn’t work'), h('p.lead', 'It may have expired (codes last 10 minutes) or been used already. Start the sign-in again from your tool.'),
      h('p', { style: { marginTop: '20px' } }, h('button.btn', { onclick: () => askCode(card) }, 'Enter a different code')));
  }
  const who = CLIENTS[info.client] || info.client || 'A tool';
  const deny = h('button.btn', { onclick: () => decide(false, deny) }, 'Deny');
  const ok = h('button.btn.primary', { onclick: () => decide(true, ok), autofocus: true }, icon('check', 16), 'Approve');
  clear(card,
    h('span.site-icon.lg', { style: { margin: '0 auto 16px' } }, icon('terminal', 30)),
    h('h1', who + ' wants to sign in as you'),
    h('p.lead', 'Check that the code below matches the one on your screen.'),
    h('div.code-input', { style: { display: 'grid', placeItems: 'center', margin: '18px 0 0', borderRadius: '12px', background: 'var(--surface-2)' }, 'aria-label': 'Code ' + code.split('').join(' ') }, code),
    h('div.grant', h('b.small', 'It will be able to:'), (info.scopes || []).map((s) => SCOPES[s] && h('div', icon(SCOPES[s][0], 18), h('div', h('b', SCOPES[s][1]), h('div.small.muted', SCOPES[s][2]))))),
    h('p.small.muted', 'Signed in as ' + state.me.email + '. You can revoke it any time under Settings → Access tokens.'),
    h('div.row', { style: { justifyContent: 'center', marginTop: '20px' } }, deny, ok));

  async function decide(approve, btn) {
    await busy(btn, async () => {
      await post('/api/v1/auth/device/' + enc(code), { approve });
      clear(card, h('div.big-check', icon(approve ? 'check' : 'x', 36)),
        h('h1', approve ? 'You’re signed in' : 'Sign-in denied'),
        h('p.lead', approve ? 'Go back to ' + (CLIENTS[info.client] ? who.replace(/^The /, 'the ') : who) + '. You can close this tab.' : 'Nothing was given access. If this wasn’t you, you can safely ignore it.'),
        h('p', { style: { marginTop: '20px' } }, h('a.btn', { href: '/' }, 'Go to Home')));
    });
  }
}

function askCode(card) {
  const inp = h('input.input.code-input', { placeholder: 'XXXX-XXXX', maxlength: 9, autofocus: true, 'aria-label': 'Sign-in code', autocomplete: 'one-time-code', autocapitalize: 'characters' });
  inp.addEventListener('input', () => { let v = inp.value.toUpperCase().replace(/[^A-Z0-9]/g, ''); if (v.length > 4) v = v.slice(0, 4) + '-' + v.slice(4, 8); inp.value = v; });
  clear(card, h('h1', 'Connect a tool'), h('p.lead', 'Enter the code shown in your terminal or AI assistant.'),
    h('form', { style: { marginTop: '20px' }, onsubmit: (e) => { e.preventDefault(); if (inp.value.length >= 9) navigate('/device?code=' + inp.value); } }, inp,
      h('button.btn.primary.lg', { style: { width: '100%', marginTop: '12px' } }, 'Continue')));
  inp.focus();
}
