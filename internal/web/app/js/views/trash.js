import { h, clear, skeletons, empty, icon, siteIcon, relTime, toast, busy } from '../ui.js';
import { get, post } from '../api.js';
import { state } from '../state.js';
import { navigate } from '../router.js';

export default async function trash(root, ctx) {
  const listEl = h('div', skeletons(3, '.line'));
  clear(root, h('div.page.narrow',
    h('div.page-head', h('div', h('h1', 'Trash'), h('p.sub', 'Deleted sites stay here for 30 days. Restore one to bring it back exactly as it was.'))),
    listEl));
  const d = await get('/api/v1/sites?filter=trash').catch(() => null);
  if (!ctx.alive()) return;
  const draw = (sites) => clear(listEl, sites.length ? h('div.list', sites.map((s) => {
    const name = s.slug.replace(/^trash-\d+-/, '');
    const btn = h('button.btn.sm', { onclick: () => busy(btn, async () => {
      const r = await post('/api/v1/trash/' + s.id + '/restore', {});
      state.sites = null;
      toast('Restored “' + s.title + '”.', { type: 'ok', action: { label: 'Open', run: () => navigate('/sites/' + r.slug) } });
      draw(sites.filter((x) => x.id !== s.id));
    }) }, icon('undo', 16), 'Restore');
    return h('div.item', siteIcon(s, 'sm'), h('div.n', h('b', s.title), h('span', '/s/' + name + ' · deleted ' + relTime(s.deletedAt))), btn);
  })) : empty({ title: 'The trash is empty', text: 'When you delete a site, it waits here for 30 days in case you change your mind.', iconName: 'trash', art: false }));
  draw(d?.sites || []);
}
