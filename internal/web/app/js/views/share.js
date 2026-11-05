// Share dialog (Google Docs style).
import { h, icon, clear, modal, avatar, toast, copyText, debounce, toggle, isOwner, relTime, busy, confirmDialog } from '../ui.js';
import { get, post, del, patch, site as sp, enc, maybe } from '../api.js';
import { state } from '../state.js';
import { setQuery } from '../router.js';

export function openShare(ws, onChange) {
  const s = ws.site;
  const owner = isOwner(s, state.me);
  const link = location.origin + '/s/' + s.slug + '/';
  const input = h('input.input', { type: 'text', placeholder: 'Add people or groups by name or email', 'aria-label': 'Add people or groups', role: 'combobox', 'aria-autocomplete': 'list', 'aria-expanded': 'false', 'aria-controls': 'share-ta', autocomplete: 'off', autofocus: true });
  const roleSel = h('select.input', { 'aria-label': 'Access for new people', style: { width: 'auto' } }, h('option', { value: 'viewer' }, 'Can view'), h('option', { value: 'editor' }, 'Can edit'));
  const addBtn = h('button.btn.primary', { type: 'submit' }, 'Share');
  const ta = h('div.popover.typeahead.hidden#share-ta', { role: 'listbox' });
  const reqs = h('div');
  const people = h('div.people');
  const orgSlot = h('div');
  let picked = null, results = [], sel = -1, members = [], ownerP = s.owner, orgView = s.orgView;

  const m = modal({
    title: 'Share “' + s.title + '”', cls: 'lg',
    body: [
      reqs,
      h('form.share-add', { onsubmit: (e) => { e.preventDefault(); addPicked(); } }, h('div.ta', input, ta), roleSel, addBtn),
      people, orgSlot,
    ],
    footer: [h('button.btn', { onclick: () => copyText(link, 'Link copied. Only people with access can open it.') }, icon('copy', 16), 'Copy link'), h('span.spacer'), h('button.btn.primary', { onclick: () => m.close() }, 'Done')],
    onClose: () => { setQuery({ share: null }); onChange && onChange(); },
  });
  m.el.querySelector('footer').classList.add('split');

  const search = debounce(async (q) => {
    if (q.length < 1) return hideTa();
    const d = await get('/api/v1/directory?q=' + enc(q), { quiet: true }).catch(() => ({ results: [] }));
    if (input.value.trim() !== q) return;
    results = (d.results || []).filter((r) => !members.some((x) => x.type === r.type && x.id.toLowerCase() === String(r.type === 'user' ? r.email || r.id : r.id).toLowerCase()));
    if (/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(q) && !results.some((r) => (r.email || r.id).toLowerCase() === q.toLowerCase())) results.push({ type: 'user', id: q, email: q, name: q, typed: true });
    sel = results.length ? 0 : -1;
    drawTa();
  }, 180);
  function drawTa() {
    if (!results.length) return hideTa();
    ta.classList.remove('hidden');
    input.setAttribute('aria-expanded', 'true');
    clear(ta, results.map((r, i) => h('div.option', { role: 'option', id: 'ta-' + i, 'aria-selected': String(i === sel), onmousedown: (e) => { e.preventDefault(); pick(r); } },
      r.type === 'group' ? h('span.avatar', { style: { background: 'var(--moss)' } }, icon('users', 16)) : avatar(r.name || r.email),
      h('span.n', h('b', r.name || r.email || r.id), h('span', r.type === 'group' ? 'Group' + (r.members ? ' · ' + r.members + ' people' : '') : r.typed ? 'Invite by email' : r.email || r.id)))));
    input.setAttribute('aria-activedescendant', sel >= 0 ? 'ta-' + sel : '');
  }
  function hideTa() { ta.classList.add('hidden'); input.setAttribute('aria-expanded', 'false'); results = []; }
  function pick(r) { picked = r; input.value = r.name && r.name !== r.email ? r.name + (r.email ? ' <' + r.email + '>' : '') : r.email || r.id; hideTa(); addPicked(); }
  input.addEventListener('input', () => { picked = null; search(input.value.trim()); });
  input.addEventListener('keydown', (e) => {
    if (ta.classList.contains('hidden')) return;
    if (e.key === 'ArrowDown') { e.preventDefault(); sel = (sel + 1) % results.length; drawTa(); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); sel = (sel - 1 + results.length) % results.length; drawTa(); }
    else if (e.key === 'Enter' && sel >= 0) { e.preventDefault(); pick(results[sel]); }
    else if (e.key === 'Escape') { e.stopPropagation(); e.preventDefault(); hideTa(); }
  });
  input.addEventListener('blur', () => setTimeout(hideTa, 120));

  async function addPicked() {
    let r = picked;
    const q = input.value.trim();
    if (!r) {
      if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(q)) { if (q) toast('Pick someone from the list, or type their full email address.'); input.focus(); return; }
      r = { type: 'user', id: q, email: q };
    }
    await busy(addBtn, async () => {
      const d = await post(sp(ws.slug, '/members'), { type: r.type, id: r.type === 'user' ? r.email || r.id : r.id, name: r.typed ? '' : r.name || '', role: roleSel.value });
      members = d.members || members;
      toast('Shared with ' + (r.name || r.email || r.id) + '. ' + (r.type === 'group' ? 'Everyone in the group' : 'They') + ' can ' + (roleSel.value === 'editor' ? 'edit' : 'view') + ' it now.', { type: 'ok' });
      input.value = '';
      picked = null;
      drawPeople();
    });
    input.focus();
  }

  function drawPeople() {
    const me = state.me;
    clear(people, h('h3', 'People with access'),
      ownerP && h('div.person', avatar(ownerP.name || ownerP.email), h('div.n', h('b', ownerP.name + (ownerP.id === me.id && !/\byou\b/i.test(ownerP.name) ? ' (you)' : '')), h('span', ownerP.email)), h('span.badge.ok', 'Owner')),
      members.map((x) => {
        const label = x.name || x.id;
        const sel = h('select.mini', { 'aria-label': 'Access for ' + label, onchange: async (e) => {
          const old = x.role;
          x.role = e.target.value;
          try { const d = await post(sp(ws.slug, '/members'), { type: x.type, id: x.id, name: x.name, role: x.role }); members = d.members; toast(label + ' can now ' + (x.role === 'editor' ? 'edit.' : 'view.'), { timeout: 2200 }); }
          catch { x.role = old; e.target.value = old; }
        } }, h('option', { value: 'viewer', selected: x.role === 'viewer' }, 'Can view'), h('option', { value: 'editor', selected: x.role === 'editor' }, 'Can edit'));
        return h('div.person',
          x.type === 'group' ? h('span.avatar', { style: { background: 'var(--moss)' } }, icon('users', 16)) : avatar(label),
          h('div.n', h('b', label), h('span', x.type === 'group' ? 'Group' : x.id)),
          sel,
          h('button.btn.ghost.icon.sm', { 'aria-label': 'Remove ' + label, title: 'Remove access', onclick: async () => {
            const d = await del(sp(ws.slug, '/members/' + x.type + '/' + enc(x.id)));
            members = d.members;
            drawPeople();
            toast(label + ' no longer has access.', { action: { label: 'Undo', run: async () => { const d2 = await post(sp(ws.slug, '/members'), { type: x.type, id: x.id, name: x.name, role: x.role }); members = d2.members; drawPeople(); } } });
          } }, icon('x', 16)));
      }),
      !members.length && h('p.small.muted', { style: { padding: '6px 0' } }, 'Only you can open this site right now.'));
  }

  function drawOrg() {
    const orgOff = state.me?.policy?.orgWideSharing === false;
    const pub = !!ws.site.public;
    const pubRow = (state.me?.policy?.publicLinks || pub) && h('div.org-row' + (pub ? '.public' : ''), { style: { marginTop: '10px' } },
      h('span.ic', icon(pub ? 'globe' : 'lock', 18)),
      h('div', { style: { flex: '1' } }, h('b', 'Anyone with the link (no sign-in)'),
        h('div.small' + (pub ? '.warn-text' : '.muted'), !owner ? 'Only the owner can change this.' : pub ? 'Anyone on the internet who has the link can view this site. They can’t sign in or change data.' : 'For things you’d put on a public web page. Off by default.')),
      toggle({ checked: pub, disabled: !owner || (!pub && !state.me?.policy?.publicLinks), label: 'Anyone with the link can view, without signing in', onchange: async (v, el) => {
        if (v && !(await confirmDialog({ title: 'Make “' + s.title + '” public?', message: 'Anyone on the internet who has the link can view this site, without signing in. They can’t sign in or change data. You can turn this off any time.', confirm: 'Make it public' }))) { el.checked = false; return; }
        try { await patch(sp(ws.slug), { public: v }); ws.site.public = v; drawOrg(); toast(v ? 'Anyone with the link can view it now.' : 'The public link is off. Only people with access can open it.', { type: 'ok' }); }
        catch (e) { el.checked = !v; if (e.status === 403) toast('Your admin has turned off public links.', { type: 'error' }); }
      } }));
    clear(orgSlot, h('div.org-row',
      h('span.ic', icon(orgView ? 'globe' : 'lock', 18)),
      h('div', { style: { flex: '1' } }, h('b', 'Everyone in the organization can view'),
        h('div.small.muted', owner ? (orgView ? 'Anyone who signs in to Bunker can open it.' : orgOff ? 'Your admin has turned off organization-wide sharing.' : 'Turn on for things like a team directory or handbook.') : 'Only the owner can change this.')),
      toggle({ checked: orgView, disabled: !owner || (!orgView && orgOff), label: 'Everyone in the organization can view', onchange: async (v, el) => {
        try { await patch(sp(ws.slug), { orgView: v }); orgView = v; ws.site.orgView = v; drawOrg(); toast(v ? 'Everyone in your organization can view it now.' : 'Only people you’ve added can open it now.', { type: 'ok' }); }
        catch { el.checked = !v; }
      } })), pubRow);
  }

  async function loadRequests() {
    const d = await maybe(sp(ws.slug, '/access-requests')).catch(() => null);
    const list = d?.requests || [];
    if (!list.length) return clear(reqs);
    clear(reqs, h('div.people', { style: { marginTop: 0, marginBottom: '16px' } }, h('h3', list.length === 1 ? '1 person is asking for access' : list.length + ' people are asking for access'),
      list.map((r) => {
        const role = h('select.mini', { 'aria-label': 'Access for ' + (r.name || r.email) }, h('option', { value: 'viewer', selected: r.role !== 'editor' }, 'Can view'), h('option', { value: 'editor', selected: r.role === 'editor' }, 'Can edit'));
        const decide = async (approve, btn) => busy(btn, async () => {
          await post(sp(ws.slug, '/access-requests/' + r.id), { approve, role: role.value });
          toast(approve ? (r.name || r.email) + ' can open it now. We’ve let them know.' : 'Request declined.', { type: approve ? 'ok' : 'info' });
          loadRequests();
          if (approve) { const d2 = await get(sp(ws.slug, '/members'), { quiet: true }).catch(() => null); if (d2) { members = d2.members; drawPeople(); } }
        });
        const yes = h('button.btn.primary.sm', { onclick: () => decide(true, yes) }, 'Approve');
        const no = h('button.btn.sm', { onclick: () => decide(false, no) }, 'Decline');
        return h('div.req', avatar(r.name || r.email), h('div.n', h('b', r.name || r.email), h('span', r.email + ' · ' + relTime(r.createdAt)), r.message && h('q', r.message)), role, no, yes);
      })));
  }

  members = s.extra?.members || [];
  drawPeople();
  drawOrg();
  loadRequests();
  get(sp(ws.slug, '/members'), { quiet: true }).then((d) => { members = d.members || []; ownerP = d.owner || ownerP; orgView = d.orgView; if (d.public != null) ws.site.public = d.public; drawPeople(); drawOrg(); }).catch(() => {});
  return m;
}
