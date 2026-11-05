// BK: a thin, forgiving wrapper over window.bunker. Outside Bunker (or
// in a preview without the SDK) it falls back to an in-memory demo so the
// template still works.
var BK = (function () {
  var sdk = window.bunker;
  var real = !!(sdk && sdk.db && typeof sdk.db.collection === 'function');
  var mem = {}, subs = {}, seq = 0;
  function list(x) { return Array.isArray(x) ? x : (x && (x.docs || x.items || x.data || x.results)) || []; }
  function emit(n) { (subs[n] || []).forEach(function (f) { try { f({ type: 'change' }); } catch (e) {} }); }
  function demoCol(n) {
    mem[n] = mem[n] || [];
    return {
      list: function () { return Promise.resolve(mem[n].slice()); },
      add: function (d) { var doc = Object.assign({}, d, { _id: 'd' + (++seq), _author: 0, _authorName: 'You', _created: Date.now() / 1000 | 0 }); mem[n].push(doc); emit(n); return Promise.resolve(doc); },
      update: function (id, p) { mem[n] = mem[n].map(function (d) { return d._id === id ? Object.assign({}, d, p) : d; }); emit(n); return Promise.resolve(); },
      remove: function (id) { mem[n] = mem[n].filter(function (d) { return d._id !== id; }); emit(n); return Promise.resolve(); },
      subscribe: function (f) { (subs[n] = subs[n] || []).push(f); return function () {}; }
    };
  }
  function realCol(n) {
    var c = sdk.db.collection(n);
    return {
      list: function (opts) { return Promise.resolve(c.list(opts)).then(list); },
      add: function (d) { return c.add(d); },
      update: function (id, p) { return c.update(id, p); },
      remove: function (id) { return (c.delete || c.remove).call(c, id); },
      subscribe: function (f) { try { return c.subscribe(f); } catch (e) { return function () {}; } }
    };
  }
  function toast(t) {
    if (real && sdk.toast) { try { sdk.toast(t); return; } catch (e) {} }
    var el = document.createElement('div'); el.className = 'toast'; el.setAttribute('role', 'status'); el.textContent = t;
    document.body.appendChild(el); setTimeout(function () { el.remove(); }, 2600);
  }
  function errText(e) { return (e && (e.message || (e.error && e.error.message))) || String(e || 'Something went wrong'); }
  return {
    demo: !real,
    me: function () { return real ? Promise.resolve(sdk.me()).then(function (u) { return u || {}; }) : Promise.resolve({ id: 0, name: 'You (demo)', email: 'you@example.com', groups: [] }); },
    col: function (n) { return real ? realCol(n) : demoCol(n); },
    toast: toast,
    err: function (e) { toast(errText(e)); console.warn(e); },
    // Returns the assistant's text, or null when AI isn't set up.
    ai: function (messages, opts) {
      if (!real || !sdk.ai || !sdk.ai.chat) return Promise.resolve(null);
      return Promise.resolve(sdk.ai.chat(messages, opts)).then(function (r) {
        if (typeof r === 'string') return r;
        return r && (r.text || r.content || (r.message && r.message.content) || '');
      });
    },
    room: function (n) {
      if (real && sdk.room) return sdk.room(n);
      var hs = [];
      return { send: function (d) { hs.forEach(function (h) { h(d); }); return Promise.resolve(); }, on: function (h) { hs.push(h); }, presence: function () { return Promise.resolve([]); } };
    },
    m365: real && sdk.m365 ? sdk.m365 : null,
    initials: function (name) { return String(name || '?').replace(/\([^)]*\)/g, '').split(/[\s@.]+/).filter(Boolean).slice(0, 2).map(function (s) { return s[0].toUpperCase(); }).join(''); },
    esc: function (s) { return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]; }); },
    when: function (t) { if (!t) return ''; var d = new Date(typeof t === 'number' && t < 1e12 ? t * 1000 : t); return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) + ' ' + d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' }); },
    banner: function () { if (real) return; var b = document.createElement('div'); b.className = 'demo'; b.textContent = 'Demo mode: open this page in Bunker to save data and share it with your team.'; var w = document.querySelector('.wrap'); if (w) w.prepend(b); }
  };
})();
