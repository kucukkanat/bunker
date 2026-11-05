import { h, clear, skeletons, empty, icon } from '../ui.js';
import { get } from '../api.js';
import { templateCard, createBlank } from './new.js';

export default async function templates(root, ctx) {
  const grid = h('div.grid', skeletons(6));
  clear(root, h('div.page',
    h('div.page-head', h('div', h('h1', 'Templates'), h('p.sub', 'Ready-made sites to start from, by Bunker and your coworkers. Using one makes your own copy, so you can change anything.')),
      h('span.spacer'), h('button.btn', { onclick: () => createBlank() }, icon('plus', 16), 'Blank site')),
    grid));
  const d = await get('/api/v1/sites?filter=templates').catch(() => null);
  if (!ctx.alive()) return;
  const t = d?.sites || [];
  clear(grid, t.length ? t.map(templateCard) : empty({
    title: 'No templates yet',
    text: 'Made something others could reuse, like a sign-up form or a tracker? Open the site, go to Settings, and turn on “Offer as a template”.',
    actions: [h('a.btn.primary', { href: '/new' }, icon('plus', 16), 'Make a site')],
  }));
}
