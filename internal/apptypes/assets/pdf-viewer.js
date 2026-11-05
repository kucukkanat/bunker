// Bunker PDF viewer on pdf.js (Chrome's built-in viewer can't run inside
// sandboxed frames). Pages render lazily as they scroll into view.
const pagesEl = document.getElementById('pages');
const fallback = document.getElementById('fallback');
const pgEl = document.getElementById('pg');
let scale = 1.3, doc = null, observer = null, token = 0;

function setDownload(src, name) {
  for (const id of ['dl', 'dl2']) { const a = document.getElementById(id); if (a) { a.href = src; a.setAttribute('download', name); } }
}

async function lib() {
  // Sandboxed (opaque-origin) pages can't start a Worker from the site's
  // URL, so pdf.js runs its "worker" on the main thread: loading the
  // worker module up front and exposing it as globalThis.pdfjsWorker.
  const [pdfjs, worker] = await Promise.all([import('./pdfjs/pdf.min.mjs'), import('./pdfjs/pdf.worker.min.mjs')]);
  globalThis.pdfjsWorker = worker;
  pdfjs.GlobalWorkerOptions.workerSrc = new URL('./pdfjs/pdf.worker.min.mjs', import.meta.url).href;
  return pdfjs;
}
const libP = lib();

async function open(src, name) {
  const my = ++token;
  setDownload(src, name);
  pagesEl.innerHTML = '<p class="muted">Loading…</p>';
  fallback.hidden = true; pagesEl.hidden = false;
  try {
    const pdfjs = await libP;
    const data = await (await fetch(src)).arrayBuffer();
    if (doc) { doc.destroy(); doc = null; }
    doc = await pdfjs.getDocument({ data, isEvalSupported: false }).promise;
    if (my !== token) return;
    await layout(my);
  } catch (e) {
    console.warn('PDF viewer:', e);
    if (my !== token) return;
    pagesEl.hidden = true; fallback.hidden = false;
  }
}

async function layout(my) {
  pagesEl.innerHTML = '';
  if (observer) observer.disconnect();
  observer = new IntersectionObserver((es) => {
    for (const e of es) if (e.isIntersecting && !e.target.dataset.done) draw(e.target, my);
  }, { rootMargin: '600px 0px' });
  const first = await doc.getPage(1);
  const vp = first.getViewport({ scale });
  for (let n = 1; n <= doc.numPages; n++) {
    const wrap = document.createElement('div');
    wrap.className = 'pdf-page'; wrap.dataset.n = n;
    wrap.style.width = Math.floor(vp.width) + 'px'; wrap.style.maxWidth = '100%';
    wrap.style.aspectRatio = vp.width + ' / ' + vp.height;
    wrap.setAttribute('aria-label', 'Page ' + n);
    pagesEl.appendChild(wrap); observer.observe(wrap);
  }
  pgEl.textContent = doc.numPages + ' page' + (doc.numPages === 1 ? '' : 's');
}

async function draw(wrap, my) {
  wrap.dataset.done = '1';
  const page = await doc.getPage(+wrap.dataset.n);
  if (my !== token) return;
  const ratio = window.devicePixelRatio || 1;
  const vp = page.getViewport({ scale: scale * ratio });
  const c = document.createElement('canvas');
  c.width = Math.floor(vp.width); c.height = Math.floor(vp.height);
  c.style.width = Math.floor(vp.width / ratio) + 'px';
  wrap.style.aspectRatio = ''; wrap.appendChild(c);
  await page.render({ canvasContext: c.getContext('2d'), viewport: vp }).promise;
}

function zoom(f) { if (!doc) return; scale = Math.min(4, Math.max(0.5, scale * f)); layout(token); }
document.getElementById('zin').onclick = () => zoom(1.2);
document.getElementById('zout').onclick = () => zoom(1 / 1.2);
const tabs = document.getElementById('tabs');
if (tabs) for (const b of tabs.querySelectorAll('button')) b.onclick = () => {
  for (const x of tabs.querySelectorAll('button')) x.setAttribute('aria-selected', x === b);
  open(b.dataset.src, b.dataset.name);
};
open(pagesEl.dataset.src, pagesEl.dataset.name);
