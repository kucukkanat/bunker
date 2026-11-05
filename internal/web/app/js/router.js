// History-API router with lazy-loaded views.
const routes = [];
let onRender = () => {};
let cleanup = null;
let seq = 0;

// route(/^\/sites\/([^/]+)$/, () => import('./views/site.js'), 'workspace')
export function route(re, load, layout = 'app') { routes.push({ re, load, layout }); }
export function setRenderer(fn) { onRender = fn; }

export function navigate(url, opt = {}) {
  if (url === location.pathname + location.search && !opt.force) return;
  history[opt.replace ? 'replaceState' : 'pushState']({}, '', url);
  render();
}

// Update the query string without re-rendering the view.
export function setQuery(params) {
  const u = new URL(location.href);
  for (const k in params) {
    if (params[k] == null || params[k] === '') u.searchParams.delete(k);
    else u.searchParams.set(k, params[k]);
  }
  history.replaceState({}, '', u.pathname + u.search);
}

export async function render() {
  const my = ++seq;
  const path = location.pathname.replace(/\/+$/, '') || '/';
  let match, r;
  for (r of routes) if ((match = path.match(r.re))) break;
  if (!match) { r = routes[routes.length - 1]; match = [path]; }
  if (cleanup) { try { cleanup(); } catch {} cleanup = null; }
  const ctx = {
    params: match.slice(1).map(decodeURIComponent),
    query: new URLSearchParams(location.search),
    path,
    alive: () => my === seq,
  };
  const root = onRender(r.layout, path);
  let mod;
  try { mod = await r.load(); }
  catch (e) {
    console.warn(e);
    root.replaceChildren();
    const box = document.createElement('div');
    box.className = 'empty';
    box.style.cssText = 'max-width:560px;margin:48px auto';
    box.setAttribute('role', 'alert');
    box.innerHTML = '<h3>This page didn’t load</h3><p>Bunker isn’t answering right now, or it was just updated. Check your connection and try again.</p>';
    const b = document.createElement('a');
    b.className = 'btn primary';
    b.href = location.href;
    b.dataset.native = '';
    b.textContent = 'Try again';
    box.append(b);
    root.append(box);
    return;
  }
  if (my !== seq) return;
  const c = await mod.default(root, ctx);
  if (my !== seq) { if (typeof c === 'function') c(); return; }
  cleanup = typeof c === 'function' ? c : null;
}

export function start() {
  addEventListener('popstate', render);
  document.addEventListener('click', (e) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const a = e.target.closest('a[href]');
    if (!a || a.target || a.hasAttribute('download') || a.dataset.native != null) return;
    const u = new URL(a.href, location.href);
    if (u.origin !== location.origin) return;
    if (/^\/(s|auth|api|_|app|mcp)\//.test(u.pathname) || u.pathname === '/auth/login') return;
    e.preventDefault();
    navigate(u.pathname + u.search + u.hash);
  });
  render();
}
