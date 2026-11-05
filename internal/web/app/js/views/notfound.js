import { h, clear, empty, icon } from '../ui.js';

export default function notFound(root) {
  clear(root, h('div.page.narrow', empty({ title: 'There’s nothing at this address', text: 'The link may be old, or the page has moved.', actions: [h('a.btn.primary', { href: '/' }, icon('home', 16), 'Go to Home')] })));
}
