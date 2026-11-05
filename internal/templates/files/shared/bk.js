// BK: a thin, forgiving wrapper over window.bunker. Outside Bunker (or
// in a preview without the SDK) it falls back to an in-memory demo so the
// template still works.
var BK = (function () {
  var sdk = window.bunker;
  var real = !!(sdk && sdk.db && typeof sdk.db.collection === 'function');
  var mem = {}, subs = {}, seq = 0, guest = false;
  function list(x) { return Array.isArray(x) ? x : (x && (x.docs || x.items || x.data || x.results)) || []; }
  function emit(n) { (subs[n] || []).forEach(function (f) { try { f({ type: 'change' }); } catch (e) {} }); }
  function demoCol(n) {
    mem[n] = mem[n] || [];
    return {
      list: function () { return Promise.resolve(mem[n].slice()); },
      all: function () { return Promise.resolve(mem[n].slice()); },
      get: function (id) { return Promise.resolve(mem[n].filter(function (d) { return d._id === id; })[0] || null); },
      add: function (d) { var doc = Object.assign({}, d, { _id: 'd' + (++seq), _author: 0, _authorName: 'You', _created: Date.now() / 1000 | 0 }); mem[n].push(doc); emit(n); return Promise.resolve(doc); },
      update: function (id, p) { mem[n] = mem[n].map(function (d) { return d._id === id ? Object.assign({}, d, p) : d; }); emit(n); return Promise.resolve(); },
      remove: function (id) { mem[n] = mem[n].filter(function (d) { return d._id !== id; }); emit(n); return Promise.resolve(); },
      subscribe: function (f) { (subs[n] = subs[n] || []).push(f); return function () {}; }
    };
  }
  function realCol(n) {
    var c = sdk.db.collection(n);
    return {
      // A public-link visitor (not signed in) can't read data: show the page empty.
      list: function (opts) { return Promise.resolve(c.list(opts)).then(list, noData); },
      // all(): every document (pages through big collections).
      all: function (opts) { return (c.all ? Promise.resolve(c.all(opts)) : Promise.resolve(c.list(opts)).then(list)).catch(noData); },
      get: function (id) { return c.get(id); },
      add: function (d) { return c.add(d); },
      update: function (id, p) { return c.update(id, p); },
      remove: function (id) { return (c.delete || c.remove).call(c, id); },
      subscribe: function (f) { try { return c.subscribe(f); } catch (e) { return function () {}; } }
    };
  }
  function noData(e) { if (guest) return []; throw e; }
  function toast(t) {
    if (real && sdk.toast) { try { sdk.toast(t); return; } catch (e) {} }
    var el = document.createElement('div'); el.className = 'toast'; el.setAttribute('role', 'status'); el.textContent = t;
    document.body.appendChild(el); setTimeout(function () { el.remove(); }, 2600);
  }
  function errText(e) { return (e && (e.message || (e.error && e.error.message))) || String(e || 'Something went wrong'); }
  return {
    demo: !real,
    me: function () { return real ? Promise.resolve(sdk.me()).then(function (u) { guest = !!(u && u.guest); return u || {}; }) : Promise.resolve({ id: 0, name: 'You (demo)', email: 'you@example.com', groups: [], role: 'owner', canEdit: true }); },
    // canEdit(me): may this person manage the app (editors and owners)?
    canEdit: function (u) { return !!(u && (u.canEdit || /^(editor|owner|admin)$/.test(u.role || ''))); },
    // ms(t): a document timestamp (ISO string or unix seconds) in milliseconds.
    ms: function (t) { if (!t) return 0; if (typeof t === 'number') return t < 1e12 ? t * 1000 : t; var v = Date.parse(t); return isNaN(v) ? 0 : v; },
    // notify({to, title, body, path}): in-app notification (no-op in demo mode).
    notify: function (o) { if (!real || !sdk.notify) return Promise.resolve(false); return Promise.resolve(sdk.notify(o)).then(function () { return true; }, function (e) { console.warn(e); return false; }); },
    download: function (name, text, type) {
      if (real && sdk.download) return sdk.download(name, text, type);
      var a = document.createElement('a'); a.href = URL.createObjectURL(new Blob([text], { type: type || 'text/plain' })); a.download = name;
      document.body.appendChild(a); a.click(); setTimeout(function () { URL.revokeObjectURL(a.href); a.remove(); }, 1000);
    },
    // csv(rows): rows (arrays of cells) as CSV text; text that could run as a spreadsheet formula is defused.
    csv: function (rows) {
      return rows.map(function (r) {
        return r.map(function (v) {
          if (typeof v === 'number') return String(v);
          v = v == null ? '' : String(v);
          if (/^[=+\-@\t]/.test(v)) v = "'" + v;
          return /[",\n\r]/.test(v) ? '"' + v.replace(/"/g, '""') + '"' : v;
        }).join(',');
      }).join('\r\n');
    },
    // inGroup(name): is the viewer in this group? (false in demo mode)
    inGroup: function (g) { return real && sdk.me.inGroup ? Promise.resolve(sdk.me.inGroup(g)).catch(function () { return false; }) : Promise.resolve(false); },
    col: function (n) { return real ? realCol(n) : demoCol(n); },
    // fn(name, input, demo): call the server function api/<name>.js; in demo
    // mode (or on an older Bunker without functions) demo(input) runs instead.
    fn: function (name, input, demo) {
      if (real && typeof sdk.fn === 'function') return Promise.resolve(sdk.fn(name, input));
      return demo ? Promise.resolve().then(function () { return demo(input); }) : Promise.reject(new Error('This needs Bunker server functions.'));
    },
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
    banner: function () { if (real && !guest) return; var b = document.createElement('div'); b.className = 'demo'; b.textContent = guest ? 'You’re viewing a public page. Sign in to Bunker to take part and see what others added.' : 'Demo mode: open this page in Bunker to save data and share it with your team.'; var w = document.querySelector('.wrap'); if (w) w.prepend(b); }
  };
})();
