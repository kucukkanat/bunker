// API client: same-origin fetch, X-Bunker CSRF header, friendly errors.
import { toast } from './ui.js';

export class ApiError extends Error {
  constructor(status, code, message) {
    super(message);
    this.status = status;
    this.code = code;
  }
  // The endpoint isn't there (another part of Bunker not installed yet) or is switched off.
  get missing() { return (this.status === 404 && this.code === 'missing') || this.status === 405 || this.status === 501 || this.status === 503; }
}

function friendly(msg) {
  msg = String(msg || '').trim();
  if (!msg) return 'Something went wrong. Please try again.';
  // Don't capitalize a leading file name ("index.html is…").
  if (!/^\S*[./]\S/.test(msg)) msg = msg[0].toUpperCase() + msg.slice(1);
  return /[.!?]$/.test(msg) ? msg : msg + '.';
}

export function loginRedirect() {
  location.href = '/auth/login?next=' + encodeURIComponent(location.pathname + location.search);
}

async function toError(res) {
  let data = null;
  const ct = res.headers.get('content-type') || '';
  try { data = ct.includes('json') ? await res.json() : await res.text(); } catch {}
  const e = data && data.error;
  if (e && typeof e === 'object') {
    const err = new ApiError(res.status, e.code || 'error', friendly(e.message));
    err.detail = e; // the whole error object (e.g. a server function's stack)
    return err;
  }
  if (res.status === 404) return new ApiError(404, 'missing', 'This part of Bunker isn’t available yet.');
  if (res.status === 413) return new ApiError(413, 'too_large', 'That’s too big to upload. Try fewer or smaller files.');
  if (res.status >= 500) return new ApiError(res.status, 'server', 'Bunker had a problem. Please try again in a moment.');
  return new ApiError(res.status, 'error', friendly(typeof data === 'string' ? data.slice(0, 200) : ''));
}

// api(path, {method, body, quiet, raw, headers, signal})
// quiet: don't toast on error (caller handles it). raw: return the Response.
export async function api(path, opt = {}) {
  const headers = { 'X-Bunker': '1', Accept: 'application/json', ...(opt.headers || {}) };
  const init = { method: opt.method || 'GET', credentials: 'same-origin', headers, signal: opt.signal };
  const b = opt.body;
  if (b !== undefined) {
    if (b instanceof FormData || b instanceof Blob || typeof b === 'string') init.body = b;
    else { init.body = JSON.stringify(b); headers['Content-Type'] = 'application/json'; }
  }
  let res;
  try {
    res = await fetch(path, init);
  } catch (err) {
    if (err.name === 'AbortError') throw err;
    const e = new ApiError(0, 'network', 'We couldn’t reach Bunker. Check your connection and try again.');
    if (!opt.quiet) toast(e.message, { type: 'error' });
    throw e;
  }
  if (res.status === 401 && !opt.noRedirect) {
    loginRedirect();
    throw new ApiError(401, 'unauthorized', 'Please sign in.');
  }
  if (!res.ok) {
    const e = await toError(res);
    if (!opt.quiet && !(opt.quietMissing && e.missing)) toast(e.message, { type: 'error' });
    throw e;
  }
  if (opt.raw) return res;
  if (res.status === 204) return null;
  const ct = res.headers.get('content-type') || '';
  return ct.includes('json') ? res.json() : res.text();
}

export const get = (p, o) => api(p, o);
export const post = (p, body, o) => api(p, { ...o, method: 'POST', body: body ?? {} });
export const patch = (p, body, o) => api(p, { ...o, method: 'PATCH', body });
export const put = (p, body, o) => api(p, { ...o, method: 'PUT', body });
export const del = (p, o) => api(p, { ...o, method: 'DELETE' });

// Try an optional endpoint. Resolves to null when it isn't installed.
export async function maybe(p, o = {}) {
  try { return await api(p, { quiet: true, ...o }); }
  catch (e) { if (e.missing || e.status === 404 || e.status === 403) return null; throw e; }
}

export const enc = (s) => encodeURIComponent(s);
export const site = (slug, rest = '') => '/api/v1/sites/' + enc(slug) + rest;
export const filePath = (p) => p.split('/').map(enc).join('/');

// Pull the first array out of a response of unknown shape.
export function list(d, ...keys) {
  if (Array.isArray(d)) return d;
  if (!d || typeof d !== 'object') return [];
  for (const k of keys) if (Array.isArray(d[k])) return d[k];
  for (const k in d) if (Array.isArray(d[k])) return d[k];
  return [];
}

// Multipart upload with progress (fetch has no upload progress).
// opt.quiet: the caller shows the error itself.
export function upload(path, form, onProgress, opt = {}) {
  return new Promise((resolve, reject) => {
    const x = new XMLHttpRequest();
    x.open('POST', path);
    x.withCredentials = true;
    x.setRequestHeader('X-Bunker', '1');
    x.setRequestHeader('Accept', 'application/json');
    x.upload.onprogress = (e) => e.lengthComputable && onProgress && onProgress(e.loaded / e.total);
    x.onload = () => {
      let data = null;
      try { data = JSON.parse(x.responseText); } catch {}
      if (x.status === 401) { loginRedirect(); return reject(new ApiError(401, 'unauthorized', 'Please sign in.')); }
      if (x.status >= 200 && x.status < 300) return resolve(data);
      const e = data && data.error;
      const err = e ? new ApiError(x.status, e.code, friendly(e.message))
        : new ApiError(x.status, x.status === 404 ? 'missing' : 'error', x.status === 413 ? 'That’s too big to upload. Try fewer or smaller files.' : 'The upload didn’t go through. Please try again.');
      if (!opt.quiet) toast(err.message, { type: 'error' });
      reject(err);
    };
    x.onerror = () => {
      const err = new ApiError(0, 'network', 'The upload was interrupted. Check your connection and try again.');
      if (!opt.quiet) toast(err.message, { type: 'error' });
      reject(err);
    };
    x.send(form);
  });
}

// POST that answers with text/event-stream (EventSource can't POST).
// onEvent(type, data). Resolves when the stream ends. JSON answers are
// delivered as a single 'done' event.
export async function stream(path, body, onEvent, opt = {}) {
  const res = await api(path, { method: 'POST', body, raw: true, headers: { Accept: 'text/event-stream' }, ...opt });
  const ct = res.headers.get('content-type') || '';
  if (!ct.includes('event-stream')) {
    const d = ct.includes('json') ? await res.json().catch(() => null) : await res.text();
    onEvent('done', d);
    return;
  }
  await readSSE(res.body, onEvent);
}

export async function readSSE(bodyStream, onEvent) {
  const reader = bodyStream.pipeThrough(new TextDecoderStream()).getReader();
  let buf = '', ev = '', data = [];
  const flush = () => {
    if (!data.length) { ev = ''; return; }
    const raw = data.join('\n');
    let d = raw;
    try { d = JSON.parse(raw); } catch {}
    let type = ev || (d && typeof d === 'object' && d.type) || 'message';
    onEvent(type, d);
    ev = '';
    data = [];
  };
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += value;
    let i;
    while ((i = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, i).replace(/\r$/, '');
      buf = buf.slice(i + 1);
      if (line === '') flush();
      else if (line[0] === ':') continue;
      else {
        const c = line.indexOf(':');
        const f = c < 0 ? line : line.slice(0, c);
        let v = c < 0 ? '' : line.slice(c + 1);
        if (v[0] === ' ') v = v.slice(1);
        if (f === 'event') ev = v;
        else if (f === 'data') data.push(v);
      }
    }
  }
  flush();
}
