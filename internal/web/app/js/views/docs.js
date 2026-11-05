import { h, clear } from '../ui.js';

const QA = [
  ['Who can see my site?', 'Only you, until you share it. Use the Share button to add people or groups as viewers or editors.'],
  ['Can my site reach the internet?', 'No. Pages run in a sealed box. They can save data and use AI through Bunker, but can’t send data anywhere else.'],
  ['I broke something. Can I undo it?', 'Yes. Every change makes a version. Open the site, go to Versions, and restore an earlier one.'],
  ['Can I use my own tools?', 'Yes. See Connect your tools for the command line and AI coding assistants.'],
];

export default function docs(root) {
  clear(root, h('div.page.narrow',
    h('div.page-head', h('div', h('h1', 'Help'), h('p.sub', 'Short answers to common questions.'))),
    h('div.card.pad.stack',
      QA.map(([q, a]) => h('div', h('h3', q), h('p.muted', { style: { marginTop: '4px' } }, a))),
      h('p', h('a', { href: '/settings/connect' }, 'Connect your tools'), ' · ', h('a', { href: '/llms.txt', 'data-native': '' }, 'Guide for AI agents')))));
}
