/* Bunker shell: the trusted parent page around a site's sandboxed frame.
 * It hands the frame a MessageChannel on every page load, forwards SDK calls
 * to the site's own API with the viewer's session, multiplexes live updates
 * over one EventSource, and draws the Bunker badge. Plain ES2017. */
(function () {
  'use strict';
  var frame = document.getElementById('bk-frame');
  var root = document.getElementById('bk-shell');
  if (!frame || !root) return;
  var cfg = root.dataset;
  var SLUG = cfg.site;
  var API = '/api/v1/sites/' + encodeURIComponent(SLUG);
  var version = parseInt(cfg.version, 10) || 0;
  var isDraft = cfg.draft === 'true';
  var role = cfg.role || 'viewer';
  var canEdit = role === 'editor' || role === 'owner' || role === 'admin';
  // A visitor on a public link, not signed in: the page can be viewed, but
  // its data, files and AI need an account, so answer those here instead of
  // sending requests the server will refuse.
  var guest = cfg.guest === 'true';
  var GUEST_ME = { id: 0, name: 'Guest', email: '', groups: [], role: 'guest', canEdit: false, guest: true };
  var frameBase = frame.getAttribute('data-base') || '';
  var METHODS = { GET: 1, POST: 1, PUT: 1, PATCH: 1, DELETE: 1 };
  var CHANNEL = /^(deploy|(db|room|presence):[A-Za-z0-9][A-Za-z0-9_.-]{0,63})$/;

  // ---------- channel to the frame ----------
  var port = null, lastNonce = null, helloSinceLoad = false;
  var ports = new Set();

  function connect(nonce) {
    ports.forEach(function (p) { dropPort(p); });
    var ch = new MessageChannel();
    var p = ch.port1;
    p.onmessage = function (e) { onPortMessage(p, e.data); };
    port = p;
    ports.add(p);
    lastNonce = nonce;
    var w = frame.contentWindow;
    if (!w) return;
    w.postMessage({
      bk: 1, type: 'init', nonce: nonce || null,
      site: { slug: SLUG, id: parseInt(cfg.siteId, 10) || 0, title: cfg.title || '', role: role, version: version, live: parseInt(cfg.live, 10) || 0, draft: isDraft, icon: cfg.icon || '' },
      filesBase: frameBase.replace('/~r/', '/~f/')
    }, '*', [ch.port2]);
  }

  function dropPort(p) {
    ports.delete(p);
    unsubscribeAll(p);
    inflightFor(p).forEach(function (c) { try { c.abort(); } catch (_) {} });
    try { p.close(); } catch (_) {}
    if (port === p) port = null;
  }

  window.addEventListener('message', function (e) {
    if (e.source !== frame.contentWindow) return;
    var d = e.data;
    if (!d || d.bk !== 1 || d.type !== 'hello') return;
    helloSinceLoad = true;
    var n = typeof d.nonce === 'string' ? d.nonce.slice(0, 64) : null;
    if (port && n && n === lastNonce) return; // already connected to this document
    if (collectErrors && n) { // drop errors from earlier documents
      var keep = window.__bunkerErrors.filter(function (x) { return x.doc === n; });
      window.__bunkerErrors.length = 0;
      keep.forEach(function (x) { window.__bunkerErrors.push(x); });
    }
    connect(n);
  });

  // ---------- page errors (draft previews by editors only) ----------
  // The workspace preview iframe is same-origin with this page, so the
  // dashboard reads window.__bunkerErrors (and also gets a postMessage) to
  // offer the builder's "Fix these errors".
  var collectErrors = isDraft && canEdit;
  if (collectErrors) window.__bunkerErrors = [];
  function pageError(d) {
    if (!collectErrors || typeof d.message !== 'string' || !d.message) return;
    var e = { message: d.message.slice(0, 600), source: typeof d.source === 'string' ? d.source.slice(0, 300) : '', line: d.line | 0, at: Date.now(), doc: typeof d.doc === 'string' ? d.doc.slice(0, 64) : '' };
    var buf = window.__bunkerErrors;
    buf.push(e);
    if (buf.length > 20) buf.splice(0, buf.length - 20);
    if (window.parent !== window) {
      try { window.parent.postMessage({ bkShell: 1, type: 'preview-error', error: e }, location.origin); } catch (_) {}
    }
  }
  frame.addEventListener('load', function () {
    if (!helloSinceLoad) connect(null); // pages without the SDK, or a missed hello
    helloSinceLoad = false;
  });

  function reply(p, msg) {
    msg.bk = 1;
    try { p.postMessage(msg); } catch (_) {}
  }

  function onPortMessage(p, d) {
    if (!d || d.bk !== 1 || !ports.has(p)) return;
    switch (d.type) {
      case 'api': return enqueue(p, d);
      case 'abort': return abortReq(p, d.id);
      case 'subscribe': return subscribe(p, d.channel);
      case 'unsubscribe': return unsubscribe(p, d.channel);
      case 'navigate': return navigate(d.path);
      case 'title': if (typeof d.title === 'string') document.title = d.title.slice(0, 200) || cfg.title || 'Bunker'; return;
      case 'toast': if (typeof d.text === 'string' && d.text) toast(d.text.slice(0, 300)); return;
      case 'open': return openExternal(d.url);
      case 'consent': return consentPrompt(Array.isArray(d.scopes) ? d.scopes.join(' ') : '').then(function () {});
      case 'error': return pageError(d);
    }
  }

  // ---------- API forwarding ----------
  var MAX_INFLIGHT = 16, inflight = 0, waiting = [];
  var controllers = new Map(); // port -> Map(id -> AbortController)

  function inflightFor(p) {
    var m = controllers.get(p);
    return m ? Array.from(m.values()) : [];
  }
  function abortReq(p, id) {
    var m = controllers.get(p);
    if (m && m.get(id)) m.get(id).abort();
  }
  function enqueue(p, d) {
    if (inflight >= MAX_INFLIGHT) { waiting.push([p, d]); return; }
    inflight++;
    handleApi(p, d, false).then(done, done);
    function done() {
      inflight--;
      var next = waiting.shift();
      if (next) enqueue(next[0], next[1]);
    }
  }

  function apiURL(path) {
    if (typeof path !== 'string' || path.charAt(0) !== '/' || path.length > 8192) return null;
    var u;
    try { u = new URL(API + path, location.origin); } catch (_) { return null; }
    // Dot segments (also %2e) are resolved by the URL parser; the result
    // must still be inside this site's API.
    if (u.origin !== location.origin) return null;
    if (u.pathname !== API && u.pathname.indexOf(API + '/') !== 0) return null;
    return u;
  }

  function buildBody(body, headers) {
    if (body === null || body === undefined) return undefined;
    if (body instanceof Blob) {
      if (body.type) headers['Content-Type'] = body.type;
      return body;
    }
    if (typeof body === 'object' && body.__multipart) {
      // Plain fields first: the server streams files and reads fields
      // (like folder) that come before them.
      var fd = new FormData(), blobs = [];
      Object.keys(body).forEach(function (k) {
        if (k === '__multipart') return;
        var v = body[k];
        (Array.isArray(v) ? v : [v]).forEach(function (x) {
          if (x instanceof Blob) blobs.push([k, x]);
          else if (x !== undefined && x !== null) fd.append(k, typeof x === 'object' ? JSON.stringify(x) : String(x));
        });
      });
      blobs.forEach(function (kv) { fd.append(kv[0], kv[1], kv[1].name || 'file'); });
      return fd;
    }
    headers['Content-Type'] = 'application/json';
    return JSON.stringify(body);
  }

  function errorOf(status, data) {
    var e = data && data.error;
    if (e && typeof e === 'object') return { code: e.code || 'error', message: e.message || 'Something went wrong', consentUrl: e.consentUrl || e.url, scopes: e.scopes, stack: typeof e.stack === 'string' ? e.stack.slice(0, 8000) : undefined };
    var code = status === 401 ? 'unauthorized' : status === 403 ? 'forbidden' : status === 404 ? 'not_found' : status === 413 ? 'too_large' : status === 429 ? 'quota_exceeded' : status >= 500 ? 'unavailable' : 'error';
    return { code: code, message: status === 401 ? 'Your Bunker session ended. Reload the page to sign in again.' : 'The request failed (' + status + ').' };
  }

  async function handleApi(p, d, retried) {
    var id = d.id;
    var method = String(d.method || 'GET').toUpperCase();
    if (guest) {
      var path = String(d.path || '').split('?')[0];
      if (method === 'GET' && path === '/me') return reply(p, { id: id, ok: true, status: 200, data: GUEST_ME });
      if (method === 'GET' && path === '/kv') return reply(p, { id: id, ok: true, status: 200, data: { items: [], keys: [] } });
      return reply(p, { id: id, ok: false, status: 401, error: { code: 'unauthorized', message: 'This is a public page: you can look, but saving or loading its data needs a Bunker account.' } });
    }
    var url = apiURL(d.path);
    if (!METHODS[method] || !url) {
      return reply(p, { id: id, ok: false, status: 400, error: { code: 'forbidden', message: 'Pages can only call their own site\'s API.' } });
    }
    var headers = { 'X-Bunker': '1', 'X-Bunker-Page': '1', 'Accept': d.stream ? 'text/event-stream' : 'application/json' };
    var body;
    try { body = method === 'GET' || method === 'DELETE' ? undefined : buildBody(d.body, headers); } catch (err) {
      return reply(p, { id: id, ok: false, status: 400, error: { code: 'invalid', message: 'That data can\'t be sent: ' + err.message } });
    }
    var ctrl = new AbortController();
    var map = controllers.get(p) || new Map();
    controllers.set(p, map);
    map.set(id, ctrl);
    var timer = setTimeout(function () { ctrl.abort(); }, d.stream ? 600000 : 120000);
    try {
      var res = await fetch(url.href, { method: method, headers: headers, body: body, credentials: 'same-origin', redirect: 'error', signal: ctrl.signal, cache: 'no-store' });
      var ct = res.headers.get('Content-Type') || '';
      if (res.ok && ct.indexOf('text/event-stream') === 0 && res.body) {
        await pumpSSE(res.body, function (event, data) { reply(p, { id: id, type: 'chunk', event: event, data: data }); });
        return reply(p, { id: id, ok: true, status: res.status, data: { done: true } });
      }
      var data = null;
      if (res.status !== 204) {
        if (ct.indexOf('application/json') === 0) data = await res.json().catch(function () { return null; });
        else if (ct.indexOf('text/') === 0) data = await res.text();
        else data = await res.blob();
      }
      if (res.ok) return reply(p, { id: id, ok: true, status: res.status, data: data });
      var err = errorOf(res.status, data);
      if (err.code === 'consent_required' && !retried) {
        if (await consentPrompt(err.scopes || '', err.consentUrl)) return handleApi(p, d, true);
      }
      return reply(p, { id: id, ok: false, status: res.status, error: err.stack ? { code: err.code, message: err.message, stack: err.stack } : { code: err.code, message: err.message } });
    } catch (e) {
      var aborted = e && e.name === 'AbortError';
      return reply(p, { id: id, ok: false, status: 0, error: { code: 'unavailable', message: aborted ? 'The request took too long or was cancelled.' : 'Bunker can\'t be reached. Check your connection and try again.' } });
    } finally {
      clearTimeout(timer);
      map.delete(id);
    }
  }

  // Reads a text/event-stream body and calls fn(event, data) per message.
  async function pumpSSE(stream, fn) {
    var reader = stream.getReader(), dec = new TextDecoder(), buf = '';
    for (;;) {
      var r = await reader.read();
      if (r.done) break;
      buf += dec.decode(r.value, { stream: true });
      var i;
      while ((i = buf.search(/\r?\n\r?\n/)) >= 0) {
        var block = buf.slice(0, i);
        buf = buf.slice(i).replace(/^\r?\n\r?\n/, '');
        var ev = 'message', data = [];
        block.split(/\r?\n/).forEach(function (line) {
          if (line.indexOf('data:') === 0) data.push(line.slice(5).replace(/^ /, ''));
          else if (line.indexOf('event:') === 0) ev = line.slice(6).trim();
        });
        if (data.length) fn(ev, data.join('\n'));
      }
    }
  }

  // ---------- live updates: one EventSource for every subscription ----------
  var subs = new Map(); // channel -> Set(port)
  var es = null, esKey = '', esTimer = null, backoff = 1000;

  function subscribe(p, ch) {
    if (typeof ch !== 'string' || !CHANNEL.test(ch)) return;
    var s = subs.get(ch);
    if (!s) { s = new Set(); subs.set(ch, s); }
    s.add(p);
    scheduleES();
  }
  function unsubscribe(p, ch) {
    var s = subs.get(ch);
    if (!s) return;
    s.delete(p);
    if (!s.size) subs.delete(ch);
    scheduleES();
  }
  function unsubscribeAll(p) {
    subs.forEach(function (s, ch) { s.delete(p); if (!s.size) subs.delete(ch); });
    scheduleES();
  }
  function scheduleES() {
    clearTimeout(esTimer);
    esTimer = setTimeout(openES, 40);
  }
  function openES(force) {
    if (guest) return; // live updates need an account
    var chans = Array.from(subs.keys()).filter(function (c) { return c !== 'deploy'; }).sort().slice(0, 31);
    chans.unshift('deploy'); // always on: powers the "new version" notice
    var key = chans.join(',');
    if (es && key === esKey && !force && es.readyState !== 2) return;
    if (es) es.close();
    esKey = key;
    es = new EventSource(API + '/events?ch=' + encodeURIComponent(key));
    es.onopen = function () { backoff = 1000; };
    es.onmessage = function (e) {
      var m;
      try { m = JSON.parse(e.data); } catch (_) { return; }
      if (!m || typeof m.ch !== 'string') return;
      if (m.ch === 'deploy') onDeploy(m.data || {});
      var s = subs.get(m.ch);
      if (s) s.forEach(function (p) { reply(p, { type: 'event', channel: m.ch, data: m.data }); });
    };
    es.onerror = function () {
      if (es.readyState === 2) { // closed for good (e.g. 429): retry with backoff
        var self = es;
        setTimeout(function () { if (es === self) openES(true); }, backoff);
        backoff = Math.min(backoff * 2, 30000);
      }
    };
  }
  openES();

  function onDeploy(d) {
    if (d.type === 'deploy' && !isDraft && d.version > version) {
      toast('A new version of this app was published.', { action: 'Reload', onAction: function () { location.reload(); }, sticky: true });
    } else if (d.type === 'draft' && isDraft) {
      toast('The draft was updated.', { action: 'Reload', onAction: function () { frame.src = frame.src; }, sticky: true, key: 'draft' });
    }
  }

  // ---------- URL bar sync ----------
  function navigate(path) {
    if (typeof path !== 'string' || path.length > 2048 || /[\u0000-\u001f]/.test(path)) return;
    path = path.replace(/^\/+/, '');
    var u;
    try { u = new URL('/s/' + encodeURIComponent(SLUG) + '/' + path, location.origin); } catch (_) { return; }
    if (u.origin !== location.origin || u.pathname.indexOf('/s/' + encodeURIComponent(SLUG) + '/') !== 0) return;
    var cur = new URLSearchParams(location.search);
    ['draft', 'v'].forEach(function (k) { if (cur.has(k)) u.searchParams.set(k, cur.get(k)); });
    var next = u.pathname + u.search + u.hash;
    if (next !== location.pathname + location.search + location.hash) {
      try { history.replaceState(null, '', next); } catch (_) {}
    }
  }

  // ---------- external links ----------
  function openExternal(raw) {
    var u;
    try { u = new URL(String(raw)); } catch (_) { return; }
    if (!/^(https?|mailto|tel):$/.test(u.protocol)) return;
    var base = frameBase ? new URL(frameBase, location.origin) : null;
    var ours = u.origin === location.origin || (base && u.origin === base.origin);
    if (ours) {
      // A link to one of this site's own pages: open the shell, not raw content.
      var m = u.pathname.match(/^\/s\/([^/]+)\/~r\/[^/]+\/(.*)$/);
      if (m) u = new URL('/s/' + m[1] + '/' + m[2] + u.search + u.hash, location.origin);
      return window.open(u.href, '_blank', 'noopener,noreferrer');
    }
    if (u.protocol === 'mailto:' || u.protocol === 'tel:') { location.href = u.href; return; }
    dialog({
      title: 'Leave Bunker?',
      text: 'This link opens ' + u.host + ' in a new tab.',
      detail: u.href.length > 140 ? u.href.slice(0, 140) + '…' : u.href,
      ok: 'Open link', cancel: 'Stay here'
    }).then(function (yes) { if (yes) window.open(u.href, '_blank', 'noopener,noreferrer'); });
  }

  // ---------- Microsoft 365 consent ----------
  var consentBusy = null;
  function consentPrompt(scopes, url) {
    if (consentBusy) return consentBusy;
    var target = url || ('/auth/login?next=' + encodeURIComponent('/?bk_consented=1') + '&scopes=' + encodeURIComponent(scopes || ''));
    consentBusy = dialog({
      title: 'Allow Microsoft 365 access?',
      text: (cfg.title || 'This app') + ' wants to read Microsoft 365 data as you. It only sees what you can already see.',
      ok: 'Continue', cancel: 'Not now'
    }).then(function (yes) {
      if (!yes) return false;
      var w = window.open(target, 'bk-consent', 'popup,width=520,height=680');
      if (!w) { toast('Allow pop-ups for Bunker, then try again.'); return false; }
      return new Promise(function (resolve) {
        var started = Date.now();
        var t = setInterval(function () {
          var closed = true;
          try { closed = w.closed; } catch (_) {}
          // Back on Bunker after signing in: close the window ourselves.
          try { if (!closed && w.location.origin === location.origin && w.location.search.indexOf('bk_consented=1') >= 0) { w.close(); closed = true; } } catch (_) {}
          if (closed || Date.now() - started > 300000) { clearInterval(t); resolve(true); }
        }, 500);
      });
    }).finally(function () { consentBusy = null; });
    return consentBusy;
  }

  // ---------- UI: toasts, dialog, badge ----------
  function el(tag, attrs, kids) {
    var n = document.createElement(tag);
    Object.keys(attrs || {}).forEach(function (k) {
      if (k === 'text') n.textContent = attrs[k];
      else if (k === 'onclick') n.addEventListener('click', attrs[k]);
      else n.setAttribute(k, attrs[k]);
    });
    (kids || []).forEach(function (c) { if (c) n.appendChild(c); });
    return n;
  }
  var toasts = el('div', { class: 'bk-toasts', role: 'status', 'aria-live': 'polite' });
  document.body.appendChild(toasts);
  var toastKeys = {};
  function toast(text, o) {
    o = o || {};
    if (o.key && toastKeys[o.key]) return;
    var t = el('div', { class: 'bk-toast' }, [el('span', { text: text })]);
    function close() { t.classList.add('bk-out'); setTimeout(function () { t.remove(); }, 200); if (o.key) delete toastKeys[o.key]; }
    if (o.action) t.appendChild(el('button', { type: 'button', class: 'bk-toast-act', text: o.action, onclick: function () { close(); o.onAction(); } }));
    if (o.sticky) t.appendChild(el('button', { type: 'button', class: 'bk-toast-x', 'aria-label': 'Dismiss', text: '×', onclick: close }));
    toasts.appendChild(t);
    if (o.key) toastKeys[o.key] = t;
    while (toasts.children.length > 4) toasts.firstChild.remove();
    if (!o.sticky) setTimeout(close, 4000);
  }

  function dialog(o) {
    return new Promise(function (resolve) {
      var prev = document.activeElement;
      var okB = el('button', { type: 'button', class: 'bk-btn-primary', text: o.ok || 'OK' });
      var noB = el('button', { type: 'button', class: 'bk-btn-quiet', text: o.cancel || 'Cancel' });
      var box = el('div', { class: 'bk-dialog', role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': 'bk-dlg-t' }, [
        el('h2', { id: 'bk-dlg-t', text: o.title }),
        el('p', { text: o.text }),
        o.detail ? el('p', { class: 'bk-dialog-detail', text: o.detail }) : null,
        el('div', { class: 'bk-dialog-actions' }, [noB, okB])
      ]);
      var wrap = el('div', { class: 'bk-overlay' }, [box]);
      function done(v) { wrap.remove(); document.removeEventListener('keydown', key, true); if (prev && prev.focus) prev.focus(); resolve(v); }
      function key(e) {
        if (e.key === 'Escape') { e.preventDefault(); done(false); }
        if (e.key === 'Tab') { e.preventDefault(); (document.activeElement === okB ? noB : okB).focus(); }
      }
      okB.addEventListener('click', function () { done(true); });
      noB.addEventListener('click', function () { done(false); });
      wrap.addEventListener('click', function (e) { if (e.target === wrap) done(false); });
      document.addEventListener('keydown', key, true);
      document.body.appendChild(wrap);
      okB.focus();
    });
  }

  if (isDraft) {
    document.body.appendChild(el('div', { class: 'bk-ribbon', role: 'note', text: 'Draft preview' }));
  }

  var MARK = '<svg viewBox="0 0 32 32" width="18" height="18" aria-hidden="true"><path fill="currentColor" d="M16 3 4 9v4h24V9L16 3Zm-9 12v10H5v3h22v-3h-2V15h-3v10h-4V15h-4v10h-4V15H7Z"/></svg>';
  if (cfg.badge !== 'false') {
    var panelId = 'bk-badge-panel';
    var btn = el('button', { type: 'button', class: 'bk-badge-btn', 'aria-expanded': 'false', 'aria-controls': panelId, 'aria-label': 'Bunker: ' + (cfg.title || 'site') + ' menu', title: 'Bunker' });
    btn.innerHTML = MARK;
    var links = [];
    if (canEdit) links.push(el('a', { href: '/sites/' + encodeURIComponent(SLUG), text: 'Edit app' }));
    if (canEdit) links.push(el('a', { href: '/sites/' + encodeURIComponent(SLUG) + '?share=1', text: 'Share' }));
    links.push(guest ? el('a', { href: '/auth/login', text: 'Sign in to Bunker' }) : el('a', { href: '/', text: 'Back to Bunker' }));
    var head = el('div', { class: 'bk-panel-head' }, [
      cfg.icon ? el('span', { class: 'bk-panel-icon', 'aria-hidden': 'true', text: cfg.icon }) : null,
      el('div', {}, [el('strong', { text: cfg.title || SLUG }), el('small', { text: (isDraft ? 'Draft · ' : version ? 'Version ' + version + ' · ' : '') + (guest ? 'Public page · not signed in' : cfg.user ? 'Signed in as ' + cfg.user : '') })])
    ]);
    var panel = el('div', { id: panelId, class: 'bk-panel', hidden: '' }, [head, el('nav', { 'aria-label': 'Bunker' }, links)]);
    var badge = el('div', { class: 'bk-badge' }, [panel, btn]);
    document.body.appendChild(badge);
    var open = false;
    function setOpen(v, focusBtn) {
      open = v;
      btn.setAttribute('aria-expanded', String(v));
      if (v) { panel.removeAttribute('hidden'); badge.classList.add('bk-open'); (links[0] || btn).focus(); }
      else { panel.setAttribute('hidden', ''); badge.classList.remove('bk-open'); if (focusBtn) btn.focus(); }
    }
    btn.addEventListener('click', function () { setOpen(!open, true); });
    document.addEventListener('keydown', function (e) { if (open && e.key === 'Escape') setOpen(false, true); });
    document.addEventListener('mousedown', function (e) { if (open && !badge.contains(e.target)) setOpen(false); });
    window.addEventListener('blur', function () { if (open && document.activeElement === frame) setOpen(false); });
  }
})();
