import { h, icon, modal, store, clear, archArt } from '../ui.js';
import { state } from '../state.js';
import { navigate } from '../router.js';

const STEPS = [
  ['Make something', 'Describe an app in plain words, or drop in a file or folder you already have.'],
  ['Share it like a document', 'Pick people or groups and choose whether they can view or edit. It’s private until you do.'],
  ['Change it any time', 'Ask for changes, edit the files, or go back to an earlier version in one click.'],
];
const stepsEl = () => h('div.welcome-steps', STEPS.map(([t, d], i) => h('div', h('span.n', i + 1), h('div', h('b', t), h('span', d)))));
const first = () => (state.me?.name || '').split(/[\s(]/)[0];

export function showWelcome() {
  store('welcomed', '1');
  const m = modal({
    title: 'Welcome to Bunker' + (first() && first().toLowerCase() !== 'you' ? ', ' + first() : ''),
    body: [h('p.muted', 'Bunker is a safe place to build small apps for your team: forms, trackers, dashboards and pages. Here’s how it works.'), stepsEl()],
    footer: [h('button.btn', { onclick: () => m.close() }, 'Look around first'), h('button.btn.primary', { autofocus: true, onclick: () => { m.close(); navigate('/new'); } }, icon('plus', 16), 'Make my first site')],
  });
}

export default function welcome(root) {
  store('welcomed', '1');
  clear(root, h('div.page.narrow', h('div.card.pad', { style: { textAlign: 'center', padding: '40px clamp(16px, 5vw, 32px)' } },
    h('div', { style: { display: 'flex', justifyContent: 'center', color: 'var(--moss)', marginBottom: '12px' } }, archArt(96)),
    h('h1', 'Welcome to Bunker' + (first() && first().toLowerCase() !== 'you' ? ', ' + first() : '')),
    h('p.muted', { style: { marginTop: '8px', fontSize: '1.05rem' } }, 'A safe place to build small apps for your team.'),
    h('div', { style: { textAlign: 'left', maxWidth: '520px', margin: '20px auto' } }, stepsEl()),
    h('div.row.wrap', { style: { justifyContent: 'center', marginTop: '20px' } }, h('a.btn.primary.lg', { href: '/new' }, icon('plus', 18), 'Make my first site'), h('a.btn.lg', { href: '/' }, 'Go to Home')))));
}
