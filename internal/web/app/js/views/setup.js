// /setup: first-run wizard. Built-in accounts (default) or company sign-in.
import { h, icon, clear, copyBtn, busy, celebrate } from '../ui.js';
import { get, post, api } from '../api.js';

const PROVIDERS = {
  entra: { name: 'Microsoft Entra ID', logo: 'M', desc: 'For companies on Microsoft 365.', color: '#2f5543' },
  google: { name: 'Google Workspace', logo: 'G', desc: 'For companies on Google Workspace.', color: '#3e6a74' },
  oidc: { name: 'Other (OpenID Connect)', logo: 'ID', desc: 'Okta, Auth0, Keycloak, Authentik and others.', color: '#6b5a7a' },
};

const brand = () => h('a.brand', { href: '/setup', 'data-native': '' }, h('img', { src: '/_/icon.svg', alt: '' }), 'bunker');

function howto(provider, redirect) {
  const ru = h('div.copy-field', { style: { margin: '8px 0 4px' } }, h('span', { 'data-testid': 'redirect-uri' }, redirect), copyBtn(redirect));
  const b = (t) => h('b', t);
  if (provider === 'entra') return h('ol.howto',
    h('li', 'Open the ', h('a', { href: 'https://entra.microsoft.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade', target: '_blank', rel: 'noopener' }, 'Microsoft Entra admin center'), ' and go to ', b('App registrations'), ', then ', b('New registration'), '.'),
    h('li', 'Name it ', b('Bunker'), '. Under ', b('Supported account types'), ', choose ', b('Accounts in this organizational directory only (Single tenant)'), '.'),
    h('li', 'Under ', b('Redirect URI'), ', pick ', b('Web'), ' and paste this address:', ru),
    h('li', 'Press ', b('Register'), '. On the overview page, copy the ', b('Application (client) ID'), ' and the ', b('Directory (tenant) ID'), ' into the boxes below.'),
    h('li', 'Go to ', b('Certificates & secrets'), ', then ', b('New client secret'), '. Copy the ', b('Value'), ' (not the Secret ID).'),
    h('li', 'Go to ', b('API permissions'), '. Make sure Microsoft Graph has ', b('User.Read'), ', ', b('openid'), ', ', b('profile'), ' and ', b('email'), '. To share with groups, add ', b('GroupMember.Read.All'), ', then press ', b('Grant admin consent'), '.'));
  if (provider === 'google') return h('ol.howto',
    h('li', 'In the ', h('a', { href: 'https://console.cloud.google.com/apis/credentials', target: '_blank', rel: 'noopener' }, 'Google Cloud console'), ', set the ', b('OAuth consent screen'), ' to ', b('Internal'), '.'),
    h('li', 'Go to ', b('Credentials'), ', then ', b('Create credentials → OAuth client ID'), ', type ', b('Web application'), '.'),
    h('li', 'Under ', b('Authorized redirect URIs'), ', add:', ru),
    h('li', 'Press ', b('Create'), ' and copy the ', b('Client ID'), ' and ', b('Client secret'), ' below.'),
    h('li', 'Enter your company’s email domain so only your people can sign in.'));
  return h('ol.howto',
    h('li', 'In your sign-in provider, create a new ', b('web application'), ' (OpenID Connect, authorization code flow).'),
    h('li', 'Set its redirect (callback) URL to:', ru),
    h('li', 'Copy the ', b('issuer URL'), ', ', b('client ID'), ' and ', b('client secret'), ' below.'),
    h('li', 'Enter your company’s email domain so only your people can sign in.'));
}

const field = (label, input, hint) => {
  if (!input.hasAttribute('aria-label')) input.setAttribute('aria-label', label);
  return h('label.field', h('span.label', label), input, hint && h('span.hint', hint));
};
function codeField(ref) {
  const inp = h('input.input.mono', { placeholder: 'XXXX-XXXX', maxlength: 9, autocomplete: 'off', spellcheck: 'false', required: true, 'aria-label': 'Setup code', style: { letterSpacing: '.12em', textTransform: 'uppercase', maxWidth: '220px' } });
  inp.addEventListener('input', () => { let v = inp.value.toUpperCase().replace(/[^A-Z0-9]/g, ''); if (v.length > 4) v = v.slice(0, 4) + '-' + v.slice(4, 8); inp.value = v; });
  ref.value = () => inp.value;
  return h('div.stack-sm', h('div.field', h('span.label', 'Setup code'), inp),
    h('div.notice', icon('info', 18), h('div', 'This proves you installed Bunker. Find it in the server log (look for “Bunker is ready to set up”) or in ', h('code', 'setup-code.txt'), ' in Bunker’s data folder. You won’t need it when you open this page on the server itself.')));
}
const errorBox = () => h('div.notice.warn.hidden', { role: 'alert' });
function showErr(box, ex) { clear(box, icon('warn', 18), h('div', h('b', 'That didn’t work. '), ex.message)); box.classList.remove('hidden'); }

// The company sign-in form, shared with Admin → Sign-in.
export function providerForm({ provider, admin, codeNeeded, onSaved }) {
  const code = {};
  const tenant = h('input.input', { value: '', placeholder: 'xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx', required: provider === 'entra', spellcheck: 'false' });
  const issuer = h('input.input', { placeholder: 'https://login.example.com', type: 'url', required: provider === 'oidc' });
  const cid = h('input.input', { required: true, spellcheck: 'false' });
  const secret = h('input.input', { type: 'password', required: !admin, autocomplete: 'off', placeholder: admin ? 'Leave empty to keep the current secret' : '' });
  const domains = h('input.input', { placeholder: 'example.com', required: provider !== 'entra' });
  const admins = h('input.input', { placeholder: 'you@example.com, it-team@example.com' });
  const groups = h('input.input', { placeholder: 'Leave empty to let everyone in your company sign in' });
  const err = errorBox();
  const save = h('button.btn.primary.lg', { type: 'submit' }, icon('check', 18), 'Test & save');
  return h('form.stack', { onsubmit: async (e) => {
    e.preventDefault();
    err.classList.add('hidden');
    const body = { provider, code: code.value ? code.value() : undefined, tenantId: tenant.value, issuer: issuer.value, clientId: cid.value, clientSecret: secret.value, allowedDomains: domains.value, admins: admins.value, allowedGroups: groups.value };
    await busy(save, async () => {
      try {
        save.lastChild.textContent = 'Checking…';
        await post('/api/v1/setup', { ...body, dryRun: true }, { quiet: true });
        save.lastChild.textContent = 'Saving…';
        await post('/api/v1/setup', body, { quiet: true });
        onSaved && onSaved();
      } catch (ex) { showErr(err, ex); }
      finally { save.lastChild.textContent = 'Test & save'; }
    });
  } },
    codeNeeded && codeField(code),
    provider === 'entra' && field('Directory (tenant) ID', tenant),
    provider === 'oidc' && field('Issuer URL', issuer, 'The address of your sign-in provider. Bunker checks it before saving.'),
    field(provider === 'entra' ? 'Application (client) ID' : 'Client ID', cid),
    field('Client secret', secret, 'Stored encrypted. Nobody can read it back, not even admins.'),
    provider !== 'entra' && field('Allowed email domains', domains, 'Comma-separated. Only people with these email addresses can sign in.'),
    field('Admins', admins, 'Emails of people who should manage Bunker. You can add more later.'),
    h('details', h('summary.small', { style: { cursor: 'pointer', color: 'var(--muted)' } }, 'More options'),
      h('div', { style: { marginTop: '12px' } }, field('Only these groups can sign in', groups, 'Group names or IDs, comma-separated.'))),
    err,
    h('div.row', h('span.spacer'), save));
}

function localForm({ codeNeeded, onSaved }) {
  const code = {};
  const email = h('input.input', { type: 'email', required: true, autocomplete: 'email', placeholder: 'you@example.com' });
  const name = h('input.input', { required: true, autocomplete: 'name', placeholder: 'Ada Lovelace' });
  const pw = h('input.input', { type: 'password', required: true, minlength: 10, autocomplete: 'new-password' });
  const pw2 = h('input.input', { type: 'password', required: true, minlength: 10, autocomplete: 'new-password' });
  const tip = 'At least 10 characters. A short sentence works well.';
  const meter = h('p.hint.small.muted', { 'aria-live': 'polite', style: { marginTop: '6px' } }, tip);
  pw.addEventListener('input', () => {
    const n = pw.value.length;
    meter.textContent = n === 0 ? tip : n < 10 ? (10 - n) + ' more to go' : 'Looks good';
    meter.style.color = n >= 10 ? 'var(--ok)' : '';
  });
  const err = errorBox();
  const save = h('button.btn.primary.lg', { type: 'submit' }, 'Create my account', icon('arrow', 18));
  return h('form.stack', { onsubmit: async (e) => {
    e.preventDefault();
    err.classList.add('hidden');
    if (pw.value !== pw2.value) return showErr(err, { message: 'The two passwords don’t match.' });
    await busy(save, async () => {
      try {
        await post('/api/v1/setup', { provider: 'local', email: email.value.trim(), name: name.value.trim(), password: pw.value, code: code.value ? code.value() : undefined }, { quiet: true });
        onSaved(email.value.trim(), pw.value);
      } catch (ex) { showErr(err, ex); }
    });
  } },
    codeNeeded && codeField(code),
    field('Your email', email),
    field('Your name', name, 'Shown to people you share sites with.'),
    field('Password', pw), meter,
    field('Type the password again', pw2),
    err,
    h('div.row', h('span.spacer'), save));
}

export default async function setup(root) {
  document.title = 'Set up Bunker';
  const card = h('div.bare-card');
  clear(root, h('div.bare', brand(), card));
  let st;
  try { st = await get('/api/v1/setup', { quiet: true, noRedirect: true }); }
  catch (e) { return clear(card, h('h1', 'Bunker isn’t answering'), h('p.lead', e.message)); }
  const redirect = st.urlKnown ? st.redirectUri : location.origin + '/auth/callback';
  const codeNeeded = st.codeNeeded !== false;
  let step = 1, mode = '', provider = 'entra', email = '';
  const center = { textAlign: 'center' };
  // Offered only when the server advertises it (integrations workstream).
  const autoOk = !(st.entraAuto || st.autoEntra) ? Promise.resolve(false) : fetch('/api/v1/setup/entra/auto', { headers: { 'X-Bunker': '1' } }).then((r) => r.status === 405).catch(() => false);

  if (st.dev) {
    return clear(card, h('div.big-check', icon('code', 32)), h('h1', { style: center }, 'Bunker is in dev mode'),
      h('p.lead', { style: center }, 'Sign-in is simulated, so there’s nothing to set up. Everyone is signed in as a test admin. Don’t use dev mode for real work.'),
      h('p', { style: { ...center, marginTop: '24px' } }, h('a.btn.primary.lg', { href: '/', 'data-native': '' }, 'Go to Bunker')));
  }
  if (st.configured) {
    return clear(card, h('div.big-check', icon('check', 36)), h('h1', { style: center }, 'Bunker is already set up'),
      h('p.lead', { style: center }, (st.sso ? 'People sign in with ' + (PROVIDERS[st.provider]?.name || 'their company account') + '.' : 'People sign in with their Bunker email and password.') + ' Admins can change this under Admin → Sign-in.'),
      h('p', { style: { ...center, marginTop: '24px' } }, h('a.btn.primary.lg', { href: '/auth/login?next=/', 'data-native': '' }, 'Sign in')));
  }
  const names = () => (mode === 'local' ? ['Sign-in', 'Your account', 'Done'] : ['Sign-in', 'Provider', 'Connect', 'Done']);
  const stepsBar = () => h('div.steps-bar', { 'aria-label': 'Step ' + step + ' of ' + names().length },
    names().map((t, i) => h('div' + (i + 1 < step ? '.done' : i + 1 === step ? '.on' : ''), { 'aria-current': i + 1 === step ? 'step' : null }, t)));
  const urlWarn = () => !st.urlKnown && h('div.notice.warn', { style: { marginBottom: '20px' } }, icon('warn', 18), h('div',
    h('b', 'Bunker doesn’t know its web address yet. '), 'Company sign-in needs the exact address people will use. Set ', h('code', 'BUNKER_URL'), ' (for example ', h('code', 'BUNKER_URL=https://bunker.yourcompany.com'), ') and restart Bunker. Until then we’ll use ', h('code', location.origin), '.'));
  const back = (to) => h('div.row', { style: { marginTop: '16px' } }, h('button.btn.ghost', { type: 'button', onclick: () => { step = to; if (to === 1) mode = ''; draw(); } }, icon('left', 16), 'Back'));
  const done = (title, text, signedIn) => {
    celebrate();
    clear(card, stepsBar(), h('div.big-check', icon('check', 36)), h('h1', { style: center }, title), h('p.lead', { style: center }, text),
      h('p', { style: { ...center, marginTop: '24px' } }, signedIn
        ? h('a.btn.primary.lg', { href: '/welcome', 'data-native': '' }, 'Open Bunker', icon('arrow', 18))
        : h('a.btn.primary.lg', { href: '/auth/login?next=/welcome&login_hint=' + encodeURIComponent(email), 'data-native': '' }, 'Sign in', icon('arrow', 18))));
    card.querySelector('.btn.primary')?.focus();
  };
  // Sign the new admin in right away, so they don't type the password twice.
  async function signIn(e, pw) {
    let ok = false;
    try {
      const r = await fetch('/auth/password', { method: 'POST', credentials: 'same-origin', redirect: 'manual', body: new URLSearchParams({ email: e, password: pw, next: '/welcome' }) });
      ok = r.type === 'opaqueredirect' || r.redirected || r.status === 303;
    } catch {}
    signedIn = ok;
    draw();
  }
  let signedIn = false;
  const choice = (onclick, pressed, logo, color, title, desc, badge) => h('button.provider', { type: 'button', 'aria-pressed': String(pressed), onclick },
    h('span.logo', { style: { background: color, color: '#fff' } }, logo), h('div', { style: { flex: '1' } }, h('div.row.wrap', { style: { gap: '4px 8px' } }, h('b', title), badge && h('span.badge.ok', badge)), h('span', desc)), icon('right', 18));

  function draw() {
    if (step === 1) {
      clear(card, stepsBar(),
        h('h1', 'Welcome! Let’s set up Bunker'), h('p.lead', 'First, choose how people will sign in. You can add company sign-in later.'),
        h('div.providers', { style: { marginTop: '24px' }, role: 'group', 'aria-label': 'How people sign in' },
          choice(() => { mode = 'local'; step = 2; draw(); }, mode === 'local', icon('user', 20), '#4f7a63', 'Just me or my team', 'Built-in accounts with email and password. You invite people with a link. Ready in a minute.', 'Recommended for personal use'),
          choice(() => { mode = 'sso'; step = 2; draw(); }, mode === 'sso', icon('shield', 20), '#2f5543', 'Company sign-in', 'People use their work account: Microsoft Entra ID, Google Workspace or another OpenID Connect provider.')));
    } else if (mode === 'local' && step === 2) {
      clear(card, stepsBar(),
        h('h1', 'Create your admin account'), h('p.lead', 'You’ll be Bunker’s first admin. Later you can invite others from Admin → People.'),
        h('div', { style: { marginTop: '24px' } }, localForm({ codeNeeded, onSaved: (e, pw) => { email = e; step = 3; signIn(e, pw); } })),
        back(1));
    } else if (mode === 'local') {
      done('You’re all set', signedIn ? 'Your account is ready and you’re signed in. Next: make your first site, then invite your team from Admin → People.' : 'Your account is set up. Sign in with ' + email + ' and the password you just chose.', signedIn);
    } else if (step === 2) {
      const auto = h('div');
      clear(card, stepsBar(),
        h('h1', 'Which company sign-in?'), h('p.lead', 'People will use their normal work account. Nobody gets a new password.'),
        h('div.providers', { style: { marginTop: '24px' }, role: 'group', 'aria-label': 'Sign-in provider' }, Object.entries(PROVIDERS).map(([k, p]) =>
          choice(() => { provider = k; step = 3; draw(); }, k === provider, p.logo, p.color, p.name, p.desc, k === 'entra' && 'Recommended'))),
        auto, back(1));
      autoOk.then((ok) => {
        if (!ok || step !== 2) return;
        const b = h('button.btn.lg', { style: { width: '100%' }, onclick: () => busy(b, async () => {
          const r = await api('/api/v1/setup/entra/auto', { method: 'POST', body: {} });
          const to = r?.url || r?.redirect || r?.authorizeUrl;
          if (to) location.href = to;
          else if (r?.ok || r?.configured) { step = 4; draw(); }
        }) }, icon('sparkle', 18), 'Set up automatically with Microsoft');
        clear(auto, h('hr.divider'), h('p.small.muted', { style: { marginBottom: '10px' } }, 'Are you a Microsoft 365 admin? Bunker can register itself for you.'), b);
      });
    } else if (step === 3) {
      const p = PROVIDERS[provider];
      clear(card, stepsBar(), urlWarn(),
        h('h1', 'Connect ' + p.name), h('p.lead', 'Follow these steps in another tab, then paste the values here.'),
        h('div', { style: { margin: '24px 0' } }, howto(provider, redirect)),
        h('hr.divider'),
        providerForm({ provider, codeNeeded, onSaved: () => { step = 4; draw(); } }),
        back(2));
    } else {
      done('You’re ready', 'Company sign-in is connected. Sign in with your work account to start building. The admins you listed can manage Bunker from the Admin page.');
    }
    card.querySelector('h1')?.setAttribute('tabindex', '-1');
    const f = card.querySelector('input:not([type=hidden])');
    (f || card.querySelector('h1'))?.focus({ preventScroll: true });
    scrollTo(0, 0);
  }
  draw();
}
