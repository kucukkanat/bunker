import { h, clear, archArt } from '../ui.js';

export default function signedOut(root) {
  document.title = 'Signed out · Bunker';
  clear(root, h('div.bare', h('a.brand', { href: '/', 'data-native': '' }, h('img', { src: '/_/icon.svg', alt: '' }), 'bunker'),
    h('div.bare-card.sm',
      h('div', { style: { display: 'flex', justifyContent: 'center', color: 'var(--moss)', marginBottom: '8px' } }, archArt(96)),
      h('h1', 'You’re signed out'),
      h('p.lead', 'Your sites are safe and waiting for you. See you soon.'),
      h('p', { style: { marginTop: '24px' } }, h('a.btn.primary.lg', { href: '/auth/login?next=/', 'data-native': '' }, 'Sign in again')))));
}
