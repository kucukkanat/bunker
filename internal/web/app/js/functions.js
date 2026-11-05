// Server functions: shared status (who's blocked), badges, banner, block dialog.
import { h, icon, modal, busy, fullTime, relTime, toast } from './ui.js';
import { maybe, put, del, enc } from './api.js';
import { state } from './state.js';

// GET /api/v1/functions/status, loaded once on dashboard load.
// Resolves to null when the server doesn't have server functions yet.
let pending = null;
export function fnStatus(force) {
  if (force || !pending) {
    pending = maybe('/api/v1/functions/status', { noRedirect: true })
      .catch(() => null)
      .then((d) => {
        state.fn = d ? { enabled: d.enabled !== false, user: d.user && d.user.blocked ? d.user : null, sites: d.sites || {} } : null;
        return state.fn;
      });
  }
  return pending;
}

// The block for one of the caller's own apps: {scope, reason} or null.
export function siteBlock(slug) {
  return state.fn?.sites?.[slug] || null;
}

const who = (by) => (by && typeof by === 'object' ? by.name || by.email : by) || '';

// What a scope means, in plain words.
export function scopeText(scope, mine = true) {
  if (scope === 'global') return 'An admin turned off server code for all of Bunker.';
  if (scope === 'user') return mine ? 'An admin turned off server code for everything you own.' : 'An admin turned off server code for everything this app’s owner has.';
  return 'An admin turned off server code for this app.';
}

export function fnOffBadge(b) {
  if (!b) return null;
  const why = (b.reason ? 'Reason: ' + b.reason + '. ' : '') + scopeText(b.scope);
  return h('span.badge.danger', { title: why, 'data-testid': 'fn-off' }, icon('code', 12), 'Server functions off', h('span.sr-only', '. ' + why));
}

// Calm banner for someone whose server code an admin turned off.
export function blockedBanner() {
  const u = state.fn?.user;
  if (!u) return null;
  const by = who(u.by);
  return h('div.notice.fn-banner', { role: 'status', 'data-testid': 'fn-banner' }, icon('info', 18),
    h('div',
      h('b', 'Server code is turned off for your apps. '),
      'Your pages still open and work; only the code in their api/ folder doesn’t run. ',
      u.reason && h('span.why', 'Reason: “' + u.reason + '”. '),
      (by || u.at) && h('span.muted', 'Turned off' + (by ? ' by ' + by : '') + (u.at ? ' ' + relTime(u.at) : '') + '. ', u.at && h('time.sr-only', fullTime(u.at))),
      'If you need it back on, ask an admin.'));
}

// Ask an admin for a reason, then turn server code off for a person or an app.
// kind: 'user' | 'site'; id: user id or site slug. Resolves true when done.
export function blockDialog({ kind, id, label }) {
  return new Promise((resolve) => {
    let ok = false;
    const reason = h('textarea.input', { rows: 3, maxlength: 500, required: true, autofocus: true, placeholder: kind === 'user' ? 'For example: a scheduled job is sending too many requests' : 'For example: this app calls an outside service too often' });
    const go = h('button.btn.danger.solid', { type: 'submit' }, 'Turn off server code');
    const submit = async (e) => {
      e.preventDefault();
      const r = reason.value.trim();
      if (!r) { reason.setAttribute('aria-invalid', 'true'); reason.focus(); return; }
      await busy(go, async () => {
        try { await put('/api/v1/admin/functions/blocks/' + kind + '/' + enc(id), { reason: r }, { quiet: true }); }
        catch (err) { toast(err.missing ? 'Server functions aren’t installed on this server yet.' : err.message, { type: 'error' }); return; }
        ok = true;
        toast('Server code is off for ' + label + '.', { type: 'ok' });
        fnStatus(true);
        m.close();
      });
    };
    const m = modal({
      title: 'Turn off server code for ' + label + '?',
      body: h('form.stack', { onsubmit: submit },
        h('p.muted', kind === 'user'
          ? 'Every app ' + label + ' owns stops running its server functions, schedules and triggers. Their pages still load. They see your reason on their Home page.'
          : 'This app stops running its server functions, schedules and triggers. Its pages still load. The owner sees your reason.'),
        h('label.field', h('span.label', 'Reason'), reason, h('span.hint', 'Plain words help them fix it. They can see this.'))),
      footer: [h('button.btn', { type: 'button', onclick: () => m.close() }, 'Cancel'), go],
      onClose: () => resolve(ok),
    });
    go.addEventListener('click', submit);
  });
}

export async function unblock({ kind, id, label }) {
  try { await del('/api/v1/admin/functions/blocks/' + kind + '/' + enc(id), { quiet: true }); }
  catch (err) { toast(err.missing ? 'Server functions aren’t installed on this server yet.' : err.message, { type: 'error' }); return false; }
  toast('Server code is back on for ' + label + '.', { type: 'ok' });
  fnStatus(true);
  return true;
}

// Admin view: blocks keyed "user:<id>" / "site:<slug>". Null when not installed.
export async function adminBlocks() {
  const d = await maybe('/api/v1/admin/functions/blocks').catch(() => null);
  if (!d) return null;
  const map = new Map();
  for (const b of d.items || []) map.set(b.kind + ':' + b.id, b);
  return map;
}
