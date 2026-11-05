/* Bunker page SDK (window.bunker). Runs inside the sandboxed site frame and
 * talks to the Bunker shell (the parent page) over a MessageChannel that the
 * shell hands over on every page load. Plain ES2017, no dependencies. */
(function () {
  'use strict';
  if (window.bunker && window.bunker.__sdk) return;
  var W = window, D = document, P = W.parent;
  var inShell = P && P !== W;
  // ~r = inside the Bunker shell, ~e = embedded by an MCP Apps host.
  var m = location.pathname.match(/^\/s\/([^/]+)\/~([re])\/([^/]+)\//);
  if (m) m = [m[0], m[1], m[3], m[2]];
  var BASE = m ? '/s/' + m[1] + '/~' + m[3] + '/' + m[2] + '/' : '/';
  var site = { slug: m ? decodeURIComponent(m[1]) : '', id: 0, title: D.title || '', role: '', version: 0, draft: false, icon: '' };
  var filesBase = m ? '/s/' + m[1] + '/~f/' + m[2] + '/' : '';

  // ---- errors ----
  class BunkerError extends Error {
    constructor(code, message, status, details) {
      super(message || code);
      this.name = 'BunkerError';
      this.code = code || 'error';
      this.status = status || 0;
      if (details) this.details = details;
    }
  }

  // ---- transport ----
  var port = null, queue = [], seq = 0, pending = new Map(), listeners = new Map();
  var nonce = Math.random().toString(36).slice(2);
  var readyResolve, initResolve;
  var initDone = new Promise(function (r) { initResolve = r; });

  function send(msg) {
    msg.bk = 1;
    if (port) port.postMessage(msg); else queue.push(msg);
  }

  function onPort(e) {
    var d = e.data;
    if (!d || d.bk !== 1) return;
    if (d.type === 'event') {
      var set = listeners.get(d.channel);
      if (set) set.forEach(function (fn) { try { fn(d.data); } catch (err) { setTimeout(function () { throw err; }); } });
      return;
    }
    var p = pending.get(d.id);
    if (!p) return;
    if (d.type === 'chunk') { if (p.onChunk) p.onChunk(d); return; }
    pending.delete(d.id);
    if (d.ok) p.resolve(d.data);
    else {
      var er = d.error || {};
      p.reject(new BunkerError(er.code || 'error', er.message || 'Something went wrong', d.status, er));
    }
  }

  W.addEventListener('message', function (e) {
    var d = e.data;
    if (e.source !== P || !d || d.bk !== 1 || d.type !== 'init' || !e.ports || !e.ports[0]) return;
    if (d.nonce && d.nonce !== nonce) return;
    var first = !port;
    if (port) try { port.close(); } catch (_) {}
    port = e.ports[0];
    port.onmessage = onPort;
    if (d.site) Object.assign(site, d.site);
    if (d.filesBase) filesBase = d.filesBase;
    if (first) {
      var q = queue; queue = [];
      q.forEach(function (msg) { port.postMessage(msg); });
    } else {
      // A replacement channel: renew live subscriptions on it.
      listeners.forEach(function (_, ch) { port.postMessage({ bk: 1, type: 'subscribe', channel: ch }); });
    }
    initResolve();
    reportLocation();
    reportTitle();
  });

  if (inShell) {
    // Ask the shell for a channel; repeat in case it wasn't listening yet.
    var tries = 0;
    (function hello() {
      if (port || tries++ > 40) return;
      try { P.postMessage({ bk: 1, type: 'hello', nonce: nonce }, '*'); } catch (_) {}
      setTimeout(hello, tries < 5 ? 100 : 500);
    })();
  }

  function call(method, path, body, opts) {
    opts = opts || {};
    if (!inShell) return Promise.reject(new BunkerError('unavailable', 'Open this page through Bunker to use bunker features.'));
    return new Promise(function (resolve, reject) {
      var id = ++seq;
      pending.set(id, { resolve: resolve, reject: reject, onChunk: opts.onChunk });
      if (opts.idOut) opts.idOut(id);
      send({ type: 'api', id: id, method: method, path: path, body: body === undefined ? null : body, stream: !!opts.stream });
    });
  }

  function enc(s) { return encodeURIComponent(String(s)); }
  function qs(o) {
    var parts = [];
    Object.keys(o || {}).forEach(function (k) {
      var v = o[k];
      if (v === undefined || v === null || v === '') return;
      if (typeof v === 'object') v = JSON.stringify(v);
      parts.push(enc(k) + '=' + enc(v));
    });
    return parts.length ? '?' + parts.join('&') : '';
  }

  function on(channel, fn) {
    var set = listeners.get(channel);
    if (!set) {
      set = new Set();
      listeners.set(channel, set);
      send({ type: 'subscribe', channel: channel });
    }
    set.add(fn);
    return function off() {
      var s = listeners.get(channel);
      if (!s || !s.delete(fn) || s.size) return;
      listeners.delete(channel);
      send({ type: 'unsubscribe', channel: channel });
    };
  }

  // ---- docs ----
  // Docs carry _id; add a hidden `id` alias so `const { id } = await add()` works.
  function doc(d) {
    if (d && typeof d === 'object' && d._id !== undefined && !Object.prototype.hasOwnProperty.call(d, 'id')) {
      Object.defineProperty(d, 'id', { value: d._id, enumerable: false, configurable: true, writable: true });
    }
    return d;
  }
  function docs(res) {
    var items = (res && res.items) || [];
    items.forEach(doc);
    return items;
  }
  // Arrays that also look like a page ({ items, next }), so both styles work.
  function page(items, next) {
    Object.defineProperty(items, 'items', { value: items, enumerable: false, configurable: true });
    Object.defineProperty(items, 'next', { value: next || null, enumerable: false, configurable: true, writable: true });
    return items;
  }
  function ruleNote() {
    return Promise.reject(new BunkerError('forbidden', 'Data rules are changed by editors in the site settings, not from the page.'));
  }

  function collection(name) {
    if (!name || typeof name !== 'string') throw new BunkerError('invalid', 'collection(name) needs a name');
    var base = '/db/' + enc(name);
    function listArgs(o) {
      o = o || {};
      return { where: o.where, order: o.order || o.orderBy || o.sort, limit: o.limit, after: o.after || o.cursor };
    }
    var c = {
      name: name,
      add: function (data) { return call('POST', base, data || {}).then(doc); },
      get: function (id) {
        return call('GET', base + '/' + enc(id)).then(doc, function (e) { if (e.code === 'not_found') return null; throw e; });
      },
      set: function (id, data) { return call('PUT', base + '/' + enc(id), data || {}).then(doc); },
      update: function (id, patch) { return call('PATCH', base + '/' + enc(id), patch || {}).then(doc); },
      delete: function (id) { return call('DELETE', base + '/' + enc(id)).then(function () { return true; }); },
      list: function (o) {
        return call('GET', base + qs(listArgs(o))).then(function (res) { return { items: docs(res), next: res.next || null }; });
      },
      query: function (o) {
        if (o && !o.where && !o.order && !o.orderBy && !o.limit && !o.after && Object.keys(o).length && !o.sort) o = { where: o };
        return call('POST', base + '/_query', listArgs(o)).then(function (res) { return page(docs(res), res.next); });
      },
      // all() pages through everything (up to `max`, default 10,000).
      all: async function (o) {
        o = Object.assign({}, o || {});
        var max = o.max || 10000, out = [];
        o.limit = 500;
        for (;;) {
          var page = await c.query(o);
          out.push.apply(out, page);
          if (!page.next || out.length >= max) return out.slice(0, max);
          o.after = page.next;
        }
      },
      count: function (where) {
        return call('GET', base + '/_count' + qs({ where: where })).then(function (r) { return r.count; });
      },
      export: function (format) { return call('GET', base + '/_export' + qs({ format: format || 'json' })); },
      subscribe: function (cb) {
        return on('db:' + name, function (ev) { if (ev && ev.doc) doc(ev.doc); cb(ev); });
      },
      rule: ruleNote
    };
    c.remove = c.delete;
    c.onChange = c.subscribe;
    return c;
  }

  // ---- files ----
  var extFor = { 'image/png': '.png', 'image/jpeg': '.jpg', 'image/gif': '.gif', 'image/webp': '.webp', 'image/svg+xml': '.svg',
    'application/pdf': '.pdf', 'text/plain': '.txt', 'text/csv': '.csv', 'application/json': '.json', 'video/mp4': '.mp4',
    'video/webm': '.webm', 'audio/mpeg': '.mp3', 'audio/webm': '.weba', 'audio/wav': '.wav', 'application/zip': '.zip' };
  function asFile(f, name) {
    if (typeof f === 'string') f = new Blob([f], { type: 'text/plain' });
    if (!(f instanceof Blob)) throw new BunkerError('invalid', 'upload() needs a File or Blob');
    var n = name || f.name;
    if (!n) n = 'upload' + (extFor[(f.type || '').split(';')[0]] || '.bin');
    if (f.name === n) return f;
    try { return new File([f], n, { type: f.type }); } catch (_) { f.name = n; return f; }
  }
  var files = {
    upload: function (file, o) {
      o = o || {};
      var body = { __multipart: true, file: Array.isArray(file) ? file.map(function (x) { return asFile(x); }) : asFile(file, o.name) };
      if (o.folder) body.folder = String(o.folder);
      return call('POST', '/uploads', body).then(function (r) { return r && r.items ? r.items : r; });
    },
    list: function (o) {
      o = o || {};
      return call('GET', '/uploads' + qs({ folder: o.folder, mine: o.mine ? 1 : '', limit: o.limit, before: o.before || o.after })).then(function (r) {
        return page(r.items || [], r.next);
      });
    },
    get: function (id) { return call('GET', '/uploads/' + enc(id)); },
    delete: function (id) { return call('DELETE', '/uploads/' + enc(typeof id === 'object' ? id.id : id)).then(function () { return true; }); },
    url: function (f) {
      if (f && typeof f === 'object') return f.url;
      return filesBase + enc(f) + '/file';
    },
    // read(id, 'text' | 'json' | 'blob') fetches a file's content.
    read: function (id, as) {
      return call('GET', '/uploads/' + enc(typeof id === 'object' ? id.id : id) + '/content').then(function (b) {
        if (!(b instanceof Blob)) b = new Blob([typeof b === 'string' ? b : JSON.stringify(b)]);
        if (as === 'text') return b.text();
        if (as === 'json') return b.text().then(JSON.parse);
        return b;
      });
    },
    rule: ruleNote
  };
  files.remove = files.delete;

  // ---- storage ----
  var storage = {
    get: function (k) { return call('GET', '/kv/' + enc(k)).then(function (r) { return r && r.exists ? r.value : null; }); },
    set: function (k, v) { return call('PUT', '/kv/' + enc(k), { value: v === undefined ? null : v }).then(function () { return true; }); },
    remove: function (k) { return call('DELETE', '/kv/' + enc(k)).then(function () { return true; }); },
    keys: function (prefix) {
      return call('GET', '/kv' + qs({ prefix: prefix })).then(function (r) {
        return (r.keys || []).filter(function (k) { return prefix || k.indexOf('ls:') !== 0; });
      });
    },
    clear: function () { return call('DELETE', '/kv').then(function () { local.__reset(); return true; }); }
  };
  storage.delete = storage.remove;

  // ---- rooms ----
  var rooms = {};
  function room(name) {
    name = String(name);
    if (rooms[name]) return rooms[name];
    var base = '/rooms/' + enc(name), beat = null, last = null;
    var r = {
      name: name,
      send: function (data) { return call('POST', base, { data: data === undefined ? null : data }); },
      // cb receives { data, from: { id, name }, ts }
      subscribe: function (cb) { return on('room:' + name, function (msg) { cb(msg); }); },
      presence: function (state) {
        last = state === undefined ? null : state;
        if (!beat) beat = setInterval(function () { call('POST', base + '/presence', { state: last }).catch(function () {}); }, 15000);
        return call('POST', base + '/presence', { state: last }).then(function (res) { return res.members; });
      },
      members: function () { return call('GET', base + '/presence').then(function (res) { return res.members; }); },
      onPresence: function (cb) { return on('presence:' + name, function (ev) { cb(ev.members || [], ev); }); },
      leave: function () {
        if (beat) clearInterval(beat);
        beat = null;
        return call('DELETE', base + '/presence').catch(function () {});
      }
    };
    r.on = r.subscribe;
    rooms[name] = r;
    return r;
  }
  W.addEventListener('pagehide', function () {
    Object.keys(rooms).forEach(function (n) { if (rooms[n]) rooms[n].leave(); });
  });

  // ---- AI ----
  function msgs(m) { return typeof m === 'string' ? [{ role: 'user', content: m }] : m; }
  function textOf(d) {
    if (d == null) return '';
    if (typeof d === 'string') return d;
    if (typeof d.text === 'string') return d.text;
    if (typeof d.delta === 'string') return d.delta;
    if (d.delta && typeof d.delta.text === 'string') return d.delta.text;
    if (typeof d.content === 'string') return d.content;
    if (d.choices && d.choices[0]) return (d.choices[0].delta && d.choices[0].delta.content) || (d.choices[0].message && d.choices[0].message.content) || '';
    return '';
  }
  var ai = {
    chat: function (messages, o) {
      return call('POST', '/ai/chat', Object.assign({}, o || {}, { messages: msgs(messages) })).then(function (r) {
        if (r && typeof r === 'object' && r.text === undefined) r.text = textOf(r);
        return r;
      });
    },
    json: function (messages, o) {
      return call('POST', '/ai/chat', Object.assign({}, o || {}, { messages: msgs(messages), json: true })).then(function (r) {
        if (r && r.json === null && r.jsonError) throw new BunkerError('invalid', r.jsonError);
        if (r && r.json !== undefined) return r.json;
        if (r && r.data !== undefined) return r.data;
        var t = textOf(r).trim().replace(/^```(?:json)?\s*|\s*```$/g, '');
        try { return JSON.parse(t); } catch (_) { throw new BunkerError('invalid', 'The AI answer was not valid JSON'); }
      });
    },
    // for await (const chunk of bunker.ai.stream(messages)) ...
    stream: function (messages, o) {
      var buf = [], wake = null, done = false, err = null, id = 0;
      function poke() { if (wake) { var w = wake; wake = null; w(); } }
      call('POST', '/ai/chat', Object.assign({}, o || {}, { messages: msgs(messages), stream: true }), {
        stream: true,
        idOut: function (i) { id = i; },
        onChunk: function (c) {
          var d = c.data;
          try { d = JSON.parse(d); } catch (_) {}
          var typ = (d && typeof d === 'object' && d.type) || c.event || '';
          if (typ === 'error' || (d && typeof d === 'object' && d.error)) {
            var er = (d && typeof d.error === 'object' && d.error) || d || {};
            err = new BunkerError(er.code || 'error', er.message || 'The AI request failed', 0);
          } else if (typ === 'done') {
            done = true;
          } else if (typ === '' || typ === 'text' || typ === 'message' || typ === 'delta') {
            var t = textOf(d);
            if (t) buf.push(t);
          }
          poke();
        }
      }).then(function () { done = true; poke(); }, function (e) { err = e; poke(); });
      var it = {
        next: function () {
          if (buf.length) return Promise.resolve({ value: buf.shift(), done: false });
          if (err) { var e = err; err = null; done = true; return Promise.reject(e); }
          if (done) return Promise.resolve({ value: undefined, done: true });
          return new Promise(function (r) { wake = r; }).then(function () { return it.next(); });
        },
        return: function () {
          if (!done && id) send({ type: 'abort', id: id });
          done = true;
          buf = [];
          return Promise.resolve({ value: undefined, done: true });
        }
      };
      it[Symbol.asyncIterator] = function () { return it; };
      return it;
    }
  };

  // ---- Microsoft 365 / connections ----
  var m365 = {
    graph: function (path, o) { return call('GET', '/m365/graph' + qs(Object.assign({}, o || {}, { path: path }))); },
    lists: {
      items: function (siteUrl, list, o) { return call('GET', '/m365/lists/items' + qs(Object.assign({}, o || {}, { site: siteUrl, list: list }))); }
    },
    excel: {
      range: function (fileUrl, range) { return call('GET', '/m365/excel/range' + qs({ file: fileUrl, range: range })); }
    },
    files: {
      list: function (folderUrl, o) { return call('GET', '/m365/files' + qs(Object.assign({}, o || {}, { folder: folderUrl }))); }
    }
  };
  m365.lists.get = m365.lists.items;
  var connections = {
    fetch: function (name, o) {
      o = o || {};
      return call('POST', '/connections/' + enc(name) + '/fetch', { method: o.method || 'GET', path: o.path || '/', query: o.query, body: o.body, headers: o.headers });
    }
  };

  // ---- me ----
  var meP = null;
  function me() {
    if (!meP) meP = call('GET', '/me').catch(function (e) { meP = null; throw e; });
    return meP;
  }
  me.inGroup = function (g) { return call('GET', '/me/groups/' + enc(g)).then(function (r) { return !!r.member; }); };

  // ---- UI helpers ----
  function toast(text) { send({ type: 'toast', text: String(text).slice(0, 300) }); }
  function open(url) {
    var u;
    try { u = new URL(String(url), location.href); } catch (_) { return false; }
    if (!/^(https?|mailto|tel):$/.test(u.protocol)) return false;
    send({ type: 'open', url: u.href });
    return true;
  }
  function download(filename, data, type) {
    var blob = data instanceof Blob ? data : new Blob([typeof data === 'string' ? data : JSON.stringify(data, null, 2)], { type: type || (typeof data === 'string' ? 'text/plain' : 'application/json') });
    var a = D.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = filename || 'download';
    a.style.display = 'none';
    (D.body || D.documentElement).appendChild(a);
    a.click();
    setTimeout(function () { URL.revokeObjectURL(a.href); a.remove(); }, 1000);
  }
  function notify(o) {
    o = o || {};
    var to = Array.isArray(o.to) ? o.to : (o.to ? [o.to] : []);
    return call('POST', '/notify', { to: to, title: o.title, body: o.body, path: o.path });
  }

  // ---- location and title sync ----
  function reportLocation() {
    if (!port) return;
    var p = location.pathname;
    var rel = p.indexOf(BASE) === 0 ? p.slice(BASE.length) : '';
    send({ type: 'navigate', path: rel + location.search + location.hash });
  }
  var lastTitle = null;
  function reportTitle() {
    if (port && D.title !== lastTitle) { lastTitle = D.title; send({ type: 'title', title: D.title }); }
  }
  ['pushState', 'replaceState'].forEach(function (k) {
    var orig = history[k];
    if (typeof orig !== 'function') return;
    history[k] = function () { var r = orig.apply(this, arguments); setTimeout(reportLocation); return r; };
  });
  W.addEventListener('popstate', reportLocation);
  W.addEventListener('hashchange', reportLocation);
  D.addEventListener('DOMContentLoaded', function () {
    reportTitle();
    var t = D.querySelector('title');
    if (t && W.MutationObserver) new MutationObserver(reportTitle).observe(t, { childList: true, characterData: true, subtree: true });
    else setInterval(reportTitle, 2000);
  });

  // ---- external links and popups (the frame has no allow-popups) ----
  function onClick(e) {
    if (e.defaultPrevented || (e.button !== 0 && e.button !== 1)) return;
    var path = e.composedPath ? e.composedPath() : [];
    var a = null;
    for (var i = 0; i < path.length; i++) if (path[i].tagName === 'A' && path[i].href) { a = path[i]; break; }
    if (!a && e.target && e.target.closest) a = e.target.closest('a[href]');
    if (!a || a.hasAttribute('download')) return;
    var u;
    try { u = new URL(a.href, location.href); } catch (_) { return; }
    if (!/^(https?|mailto|tel):$/.test(u.protocol)) return;
    var here = new URL(location.href);
    var external = u.origin !== here.origin;
    var t = (a.getAttribute('target') || '').toLowerCase();
    var newTab = (t && t !== '_self') || e.ctrlKey || e.metaKey || e.shiftKey || e.button === 1;
    if (!external && !newTab) return; // ordinary navigation inside the site
    e.preventDefault();
    open(u.href);
  }
  W.addEventListener('click', onClick);
  W.addEventListener('auxclick', onClick);
  try {
    Object.defineProperty(W, 'open', { configurable: true, writable: true, value: function (url) { if (url) open(url); return null; } });
  } catch (_) { W.open = function (url) { if (url) open(url); return null; }; }

  // ---- WebRTC off (best effort; Connection-Allowlist is the real guard) ----
  ['RTCPeerConnection', 'webkitRTCPeerConnection', 'mozRTCPeerConnection', 'RTCDataChannel', 'RTCIceCandidate', 'RTCSessionDescription', 'RTCRtpSender', 'RTCRtpReceiver'].forEach(function (n) {
    try { Object.defineProperty(W, n, { value: undefined, writable: false, configurable: false }); } catch (_) {}
  });

  // ---- localStorage / sessionStorage / cookie shims ----
  // Opaque-origin frames throw on storage access, which breaks libraries
  // at import time. These are synchronous in-memory stores; localStorage
  // is hydrated from bunker.storage ("ls:" keys) and written behind.
  function makeStore(persist) {
    var map = new Map(), dirty = new Set(), timer = null, cleared = false;
    function flush() {
      timer = null;
      if (!inShell) return;
      if (cleared) { cleared = false; call('DELETE', '/kv' + qs({ prefix: 'ls:' })).catch(warn); }
      dirty.forEach(function (k) {
        var p = map.has(k) ? call('PUT', '/kv/' + enc('ls:' + k), { value: map.get(k) }) : call('DELETE', '/kv/' + enc('ls:' + k));
        p.catch(warn);
      });
      dirty.clear();
    }
    function touch(k) {
      if (!persist) return;
      dirty.add(k);
      if (!timer) timer = setTimeout(flush, 250);
    }
    var api = {
      getItem: function (k) { k = String(k); return map.has(k) ? map.get(k) : null; },
      setItem: function (k, v) { k = String(k); map.set(k, String(v)); touch(k); },
      removeItem: function (k) { k = String(k); if (map.delete(k)) touch(k); },
      clear: function () { map.clear(); if (persist) { dirty.clear(); cleared = true; if (!timer) timer = setTimeout(flush, 250); } },
      key: function (i) { var ks = Array.from(map.keys()); return i >= 0 && i < ks.length ? ks[i] : null; },
      get length() { return map.size; },
      __hydrate: function (items) {
        Object.keys(items || {}).forEach(function (k) {
          var key = k.slice(3), v = items[k];
          if (!map.has(key) && !dirty.has(key)) map.set(key, typeof v === 'string' ? v : JSON.stringify(v));
        });
      },
      __flush: function () { if (timer) { clearTimeout(timer); flush(); } },
      __reset: function () { map.clear(); dirty.clear(); }
    };
    if (typeof Proxy !== 'function') return api;
    return new Proxy(api, {
      get: function (t, k) { if (typeof k === 'symbol' || k in t) return t[k]; return map.has(k) ? map.get(k) : undefined; },
      set: function (t, k, v) { if (typeof k === 'symbol' || k in t) return false; api.setItem(k, v); return true; },
      deleteProperty: function (t, k) { api.removeItem(k); return true; },
      has: function (t, k) { return k in t || map.has(k); },
      ownKeys: function () { return Array.from(map.keys()); },
      getOwnPropertyDescriptor: function (t, k) {
        if (map.has(k)) return { value: map.get(k), enumerable: true, configurable: true, writable: true };
        return undefined;
      }
    });
  }
  var warned = false;
  function warn(e) { if (!warned) { warned = true; console.warn('[bunker] could not save localStorage:', e && e.message); } }
  function blocked(name) {
    try { var s = W[name]; s.getItem('__bk'); return false; } catch (_) { return true; }
  }
  var local = makeStore(true), session = makeStore(false), shimmed = false;
  if (blocked('localStorage')) {
    try { Object.defineProperty(W, 'localStorage', { configurable: true, enumerable: true, get: function () { return local; } }); shimmed = true; } catch (_) {}
  }
  if (blocked('sessionStorage')) {
    try { Object.defineProperty(W, 'sessionStorage', { configurable: true, enumerable: true, get: function () { return session; } }); } catch (_) {}
  }
  try { void D.cookie; } catch (_) {
    var jar = new Map();
    try {
      Object.defineProperty(D, 'cookie', {
        configurable: true,
        get: function () { return Array.from(jar).map(function (kv) { return kv[0] + '=' + kv[1]; }).join('; '); },
        set: function (v) {
          var first = String(v).split(';')[0], i = first.indexOf('=');
          if (i < 0) return;
          var k = first.slice(0, i).trim(), val = first.slice(i + 1).trim();
          if (/;\s*(max-age=0|expires=thu, 01 jan 1970)/i.test(v) || val === '') jar.delete(k); else jar.set(k, val);
        }
      });
    } catch (_) {}
  }
  W.addEventListener('pagehide', function () { local.__flush(); });
  D.addEventListener('visibilitychange', function () { if (D.visibilityState === 'hidden') local.__flush(); });

  var ready = new Promise(function (r) { readyResolve = r; });
  if (!inShell) readyResolve();
  else {
    var timeout = setTimeout(readyResolve, 4000);
    initDone.then(function () {
      var hydrate = shimmed ? call('GET', '/kv' + qs({ prefix: 'ls:', values: 1 })).then(function (r) { local.__hydrate(r.items); }) : Promise.resolve();
      return hydrate.catch(function () {});
    }).then(function () { clearTimeout(timeout); readyResolve(); });
  }
  ready.then(function () { try { W.dispatchEvent(new Event('bunker:ready')); } catch (_) {} });

  var bunker = {
    __sdk: 1,
    version: '1',
    ready: ready,
    site: site,
    me: me,
    db: { collection: collection, list: function () { return call('GET', '/db').then(function (r) { return r.collections; }); } },
    files: files,
    storage: storage,
    room: room,
    ai: ai,
    m365: m365,
    connections: connections,
    notify: notify,
    toast: toast,
    open: open,
    download: download,
    on: on,
    BunkerError: BunkerError
  };
  W.BunkerError = BunkerError;
  try { Object.defineProperty(W, 'bunker', { value: Object.freeze(bunker), writable: false, configurable: false, enumerable: true }); } catch (_) { W.bunker = bunker; }
})();
