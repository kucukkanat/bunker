// Bunker server-function runner. Loaded by qjs as /fn/__run.js; imports the
// user's bundled module from ./main.js, calls its default export and reports
// back to the host over stdout. Protocol: every message is one line,
// "\x01" + JSON; the host answers requests with one JSON line on stdin.
// Console output goes to stderr as "\x02" + level + "\t" + JSON string.
import * as std from "qjs:std";
import * as os from "qjs:os";

const MODE = scriptArgs[1] || "run";
const G = globalThis;
const stdout = std.out, stdin = std.in, stderr = std.err;
const exit = std.exit;
const loadFile = std.loadFile;
const osSetTimeout = os.setTimeout, osClearTimeout = os.clearTimeout;
const jsonStringify = JSON.stringify, jsonParse = JSON.parse;

// ---- protocol ----
function send(msg) {
  stdout.puts("\x01" + jsonStringify(msg) + "\n");
  stdout.flush();
}
function request(msg) {
  send(msg);
  const line = stdin.getline();
  if (line === null) exit(1); // the host stopped this run
  return jsonParse(line);
}

// ---- errors ----
class BunkerError extends Error {
  constructor(code, message, status, details) {
    super(message || code);
    this.name = "BunkerError";
    this.code = code || "error";
    this.status = status || 0;
    if (details) this.details = details;
  }
}
function replyError(r, fallback) {
  const er = (r && r.error) || {};
  return new BunkerError(er.code || "error", er.message || fallback || "Something went wrong", (r && r.status) || 0, r && r.error);
}

// ---- bytes / text ----
function utf8Encode(s) {
  s = String(s);
  const out = [];
  for (let i = 0; i < s.length; i++) {
    let c = s.charCodeAt(i);
    if (c >= 0xd800 && c <= 0xdbff && i + 1 < s.length) {
      const d = s.charCodeAt(i + 1);
      if (d >= 0xdc00 && d <= 0xdfff) { c = 0x10000 + ((c - 0xd800) << 10) + (d - 0xdc00); i++; }
      else c = 0xfffd;
    } else if (c >= 0xd800 && c <= 0xdfff) c = 0xfffd;
    if (c < 0x80) out.push(c);
    else if (c < 0x800) out.push(0xc0 | (c >> 6), 0x80 | (c & 63));
    else if (c < 0x10000) out.push(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
    else out.push(0xf0 | (c >> 18), 0x80 | ((c >> 12) & 63), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
  }
  return new Uint8Array(out);
}
function utf8Decode(b) {
  b = toBytes(b);
  let s = "", chunk = [];
  const flush = () => { s += String.fromCharCode.apply(null, chunk); chunk = []; };
  for (let i = 0; i < b.length;) {
    const c = b[i];
    let cp = 0xfffd, n = 1;
    if (c < 0x80) cp = c;
    else if (c >= 0xc2 && c < 0xe0 && i + 1 < b.length && (b[i + 1] & 0xc0) === 0x80) { cp = ((c & 31) << 6) | (b[i + 1] & 63); n = 2; }
    else if (c >= 0xe0 && c < 0xf0 && i + 2 < b.length && (b[i + 1] & 0xc0) === 0x80 && (b[i + 2] & 0xc0) === 0x80) {
      cp = ((c & 15) << 12) | ((b[i + 1] & 63) << 6) | (b[i + 2] & 63); n = 3;
      if (cp < 0x800 || (cp >= 0xd800 && cp <= 0xdfff)) cp = 0xfffd;
    } else if (c >= 0xf0 && c < 0xf5 && i + 3 < b.length && (b[i + 1] & 0xc0) === 0x80 && (b[i + 2] & 0xc0) === 0x80 && (b[i + 3] & 0xc0) === 0x80) {
      cp = ((c & 7) << 18) | ((b[i + 1] & 63) << 12) | ((b[i + 2] & 63) << 6) | (b[i + 3] & 63); n = 4;
      if (cp < 0x10000 || cp > 0x10ffff) cp = 0xfffd;
    }
    i += n;
    if (cp >= 0x10000) { cp -= 0x10000; chunk.push(0xd800 + (cp >> 10), 0xdc00 + (cp & 1023)); }
    else chunk.push(cp);
    if (chunk.length > 8000) flush();
  }
  flush();
  return s;
}
function toBytes(x) {
  if (x instanceof Uint8Array) return x;
  if (x instanceof ArrayBuffer) return new Uint8Array(x);
  if (ArrayBuffer.isView(x)) return new Uint8Array(x.buffer, x.byteOffset, x.byteLength);
  return utf8Encode(String(x));
}
const atobRaw = G.atob, btoaRaw = G.btoa;
function b64Encode(bytes) {
  bytes = toBytes(bytes);
  let s = "";
  for (let i = 0; i < bytes.length; i += 8192) s += String.fromCharCode.apply(null, bytes.subarray(i, i + 8192));
  return btoaRaw(s);
}
function b64Decode(s) {
  const bin = atobRaw(String(s));
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}
class TextEncoder {
  get encoding() { return "utf-8"; }
  encode(s) { return utf8Encode(s === undefined ? "" : s); }
}
class TextDecoder {
  get encoding() { return "utf-8"; }
  decode(b) { return b === undefined ? "" : utf8Decode(b); }
}

// ---- console ----
function fmt(x) {
  if (typeof x === "string") return x;
  if (x instanceof Error) return x.stack ? x.name + ": " + x.message + "\n" + x.stack : x.name + ": " + x.message;
  if (x === undefined || typeof x === "function" || typeof x === "symbol" || typeof x === "bigint") return String(x);
  try { const j = jsonStringify(x); return j === undefined ? String(x) : j; } catch (_) { return String(x); }
}
function logger(level) {
  return function () {
    const parts = [];
    for (let i = 0; i < arguments.length; i++) parts.push(fmt(arguments[i]));
    stderr.puts("\x02" + level + "\t" + jsonStringify(parts.join(" ")) + "\n");
    stderr.flush();
  };
}
const console = {
  log: logger("log"), info: logger("info"), warn: logger("warn"), error: logger("error"), debug: logger("log")
};
console.trace = console.debug;

// ---- timers ----
const timers = new Map();
let timerSeq = 0;
function runTimer(fn, args) {
  try { fn.apply(undefined, args); } catch (e) { console.error("Uncaught error in timer:", e); }
}
function setTimeout(fn, ms, ...args) {
  if (typeof fn !== "function") throw new TypeError("setTimeout needs a function");
  const id = ++timerSeq;
  timers.set(id, osSetTimeout(() => { timers.delete(id); runTimer(fn, args); }, Math.max(0, +ms || 0)));
  return id;
}
function clearTimeout(id) {
  const h = timers.get(id);
  if (h !== undefined) { timers.delete(id); osClearTimeout(h); }
}
function setInterval(fn, ms, ...args) {
  if (typeof fn !== "function") throw new TypeError("setInterval needs a function");
  const id = ++timerSeq, every = Math.max(1, +ms || 0);
  const tick = () => { timers.set(id, osSetTimeout(tick, every)); runTimer(fn, args); };
  timers.set(id, osSetTimeout(tick, every));
  return id;
}

// ---- host calls ----
function call(method, path, body) {
  return new Promise((resolve, reject) => {
    const r = request({ t: "api", method, path, body: body === undefined ? null : body });
    if (r && r.ok) resolve(r.data === undefined ? null : r.data);
    else reject(replyError(r));
  });
}
function enc(s) { return encodeURIComponent(String(s)); }
function qs(o) {
  const parts = [];
  Object.keys(o || {}).forEach((k) => {
    let v = o[k];
    if (v === undefined || v === null || v === "") return;
    if (typeof v === "object") v = jsonStringify(v);
    parts.push(enc(k) + "=" + enc(v));
  });
  return parts.length ? "?" + parts.join("&") : "";
}
function unsupported(what) {
  return () => Promise.reject(new BunkerError("unsupported", what + " is not available in server functions"));
}

// ---- db ----
function doc(d) {
  if (d && typeof d === "object" && d._id !== undefined && !Object.prototype.hasOwnProperty.call(d, "id")) {
    Object.defineProperty(d, "id", { value: d._id, enumerable: false, configurable: true, writable: true });
  }
  return d;
}
function docs(res) {
  const items = (res && res.items) || [];
  items.forEach(doc);
  return items;
}
function page(items, next) {
  Object.defineProperty(items, "items", { value: items, enumerable: false, configurable: true });
  Object.defineProperty(items, "next", { value: next || null, enumerable: false, configurable: true, writable: true });
  return items;
}
function ruleNote() {
  return Promise.reject(new BunkerError("forbidden", "Data rules are changed by editors in the site settings."));
}
function collection(name) {
  if (!name || typeof name !== "string") throw new BunkerError("invalid", "collection(name) needs a name");
  const base = "/db/" + enc(name);
  const listArgs = (o) => {
    o = o || {};
    return { where: o.where, order: o.order || o.orderBy || o.sort, limit: o.limit, after: o.after || o.cursor };
  };
  const c = {
    name,
    add: (data) => call("POST", base, data || {}).then(doc),
    get: (id) => call("GET", base + "/" + enc(id)).then(doc, (e) => { if (e.code === "not_found") return null; throw e; }),
    set: (id, data) => call("PUT", base + "/" + enc(id), data || {}).then(doc),
    update: (id, patch) => call("PATCH", base + "/" + enc(id), patch || {}).then(doc),
    delete: (id) => call("DELETE", base + "/" + enc(id)).then(() => true),
    list: (o) => call("GET", base + qs(listArgs(o))).then((res) => ({ items: docs(res), next: (res && res.next) || null })),
    query: (o) => {
      if (o && !o.where && !o.order && !o.orderBy && !o.limit && !o.after && Object.keys(o).length && !o.sort) o = { where: o };
      return call("POST", base + "/_query", listArgs(o)).then((res) => page(docs(res), res && res.next));
    },
    all: async (o) => {
      o = Object.assign({}, o || {});
      const max = o.max || 10000, out = [];
      o.limit = 500;
      for (;;) {
        const p = await c.query(o);
        out.push.apply(out, p);
        if (!p.next || out.length >= max) return out.slice(0, max);
        o.after = p.next;
      }
    },
    count: (where) => call("GET", base + "/_count" + qs({ where })).then((r) => r.count),
    export: (format) => call("GET", base + "/_export" + qs({ format: format || "json" })),
    subscribe: unsupported("subscribe()"),
    rule: ruleNote
  };
  c.remove = c.delete;
  c.onChange = c.subscribe;
  return c;
}
const db = { collection, list: () => call("GET", "/db").then((r) => r.collections) };

// ---- files ----
// Content replies are a string (text), or {bodyB64[, type]} for bytes.
function contentBytes(b) {
  if (b && typeof b === "object" && typeof b.bodyB64 === "string") return b64Decode(b.bodyB64);
  if (typeof b === "string") return utf8Encode(b);
  return utf8Encode(b == null ? "" : jsonStringify(b));
}
const fileId = (id) => enc(id && typeof id === "object" ? id.id : id);
const files = {
  // upload(name, string | Uint8Array | ArrayBuffer, {type, folder})
  upload: (name, data, o) => {
    o = o || {};
    if (!name) return Promise.reject(new BunkerError("invalid", "upload(name, data) needs a file name"));
    const isText = typeof data === "string";
    const file = {
      name: String(name),
      type: o.type || (isText ? "text/plain" : "application/octet-stream"),
      bodyB64: b64Encode(isText ? utf8Encode(data) : toBytes(data == null ? "" : data))
    };
    const body = { __multipart: true, file };
    if (o.folder) body.folder = String(o.folder);
    return call("POST", "/uploads", body).then((r) => (r && r.items && r.items.length === 1 ? r.items[0] : r && r.items ? r.items : r));
  },
  list: (o) => {
    o = o || {};
    return call("GET", "/uploads" + qs({ folder: o.folder, mine: o.mine ? 1 : "", limit: o.limit, before: o.before || o.after }))
      .then((r) => page((r && r.items) || [], r && r.next));
  },
  get: (id) => call("GET", "/uploads/" + fileId(id)),
  delete: (id) => call("DELETE", "/uploads/" + fileId(id)).then(() => true),
  url: (f) => (f && typeof f === "object" ? f.url || null : null),
  // read(id, 'text' | 'json' | 'bytes') — bytes (Uint8Array) by default.
  read: (id, as) => call("GET", "/uploads/" + fileId(id) + "/content").then((b) => {
    if (as === "text") return typeof b === "string" ? b : utf8Decode(contentBytes(b));
    if (as === "json") return typeof b === "string" ? jsonParse(b) : (b && typeof b.bodyB64 === "string" ? jsonParse(utf8Decode(contentBytes(b))) : b);
    return contentBytes(b);
  }),
  rule: ruleNote
};
files.remove = files.delete;

// ---- storage ----
const storage = {
  get: (k) => call("GET", "/kv/" + enc(k)).then((r) => (r && r.exists ? r.value : null)),
  set: (k, v) => call("PUT", "/kv/" + enc(k), { value: v === undefined ? null : v }).then(() => true),
  remove: (k) => call("DELETE", "/kv/" + enc(k)).then(() => true),
  keys: (prefix) => call("GET", "/kv" + qs({ prefix })).then((r) => ((r && r.keys) || []).filter((k) => prefix || k.indexOf("ls:") !== 0)),
  clear: () => call("DELETE", "/kv").then(() => true)
};
storage.delete = storage.remove;

// ---- rooms ----
function rooms(name) {
  name = String(name);
  const base = "/rooms/" + enc(name);
  return {
    name,
    send: (data) => call("POST", base, { data: data === undefined ? null : data }),
    members: () => call("GET", base + "/presence").then((res) => res.members),
    subscribe: unsupported("rooms().subscribe()")
  };
}

// ---- AI ----
function msgs(m) { return typeof m === "string" ? [{ role: "user", content: m }] : m; }
function textOf(d) {
  if (d == null) return "";
  if (typeof d === "string") return d;
  if (typeof d.text === "string") return d.text;
  if (typeof d.content === "string") return d.content;
  if (d.choices && d.choices[0]) return (d.choices[0].message && d.choices[0].message.content) || "";
  return "";
}
const ai = {
  chat: (messages, o) => call("POST", "/ai/chat", Object.assign({}, o || {}, { messages: msgs(messages) })).then((r) => {
    if (r && typeof r === "object" && r.text === undefined) r.text = textOf(r);
    return r;
  }),
  json: (messages, o) => call("POST", "/ai/chat", Object.assign({}, o || {}, { messages: msgs(messages), json: true })).then((r) => {
    if (r && r.json === null && r.jsonError) throw new BunkerError("invalid", r.jsonError);
    if (r && r.json !== undefined) return r.json;
    if (r && r.data !== undefined) return r.data;
    const t = textOf(r).trim().replace(/^```(?:json)?\s*|\s*```$/g, "");
    try { return jsonParse(t); } catch (_) { throw new BunkerError("invalid", "The AI answer was not valid JSON"); }
  }),
  stream: () => { throw new BunkerError("unsupported", "ai.stream() is not available in server functions; use ai.chat()"); }
};

// ---- Microsoft 365 / connections ----
const m365 = {
  graph: (path, o) => call("GET", "/m365/graph" + qs(Object.assign({}, o || {}, { path }))),
  lists: { items: (siteUrl, list, o) => call("GET", "/m365/lists/items" + qs(Object.assign({}, o || {}, { site: siteUrl, list }))) },
  excel: { range: (fileUrl, range) => call("GET", "/m365/excel/range" + qs({ file: fileUrl, range })) },
  files: { list: (folderUrl, o) => call("GET", "/m365/files" + qs(Object.assign({}, o || {}, { folder: folderUrl }))) }
};
m365.lists.get = m365.lists.items;
const connections = {
  fetch: (name, o) => {
    o = o || {};
    return call("POST", "/connections/" + enc(name) + "/fetch", { method: o.method || "GET", path: o.path || "/", query: o.query, body: o.body, headers: o.headers });
  }
};

// ---- me / notify / secrets ----
function me() { return call("GET", "/me"); }
me.inGroup = (g) => call("GET", "/me/groups/" + enc(g)).then((r) => !!r.member);
function notify(o) {
  o = o || {};
  const to = Array.isArray(o.to) ? o.to : o.to ? [o.to] : [];
  return call("POST", "/notify", { to, title: o.title, body: o.body, path: o.path });
}
const secrets = {
  get: (name) => new Promise((resolve, reject) => {
    const r = request({ t: "secret", name: String(name) });
    if (r && r.ok) resolve(typeof r.data === "string" ? r.data : null);
    else reject(replyError(r));
  })
};

// ---- fetch ----
function headerMap(h) {
  const out = {};
  if (!h) return out;
  if (typeof h.forEach === "function" && !Array.isArray(h)) h.forEach((v, k) => { out[String(k).toLowerCase()] = String(v); });
  else if (Array.isArray(h)) h.forEach((p) => { out[String(p[0]).toLowerCase()] = String(p[1]); });
  else Object.keys(h).forEach((k) => { out[k.toLowerCase()] = String(h[k]); });
  return out;
}
class Headers {
  constructor(init) { this._h = headerMap(init); }
  get(n) { n = String(n).toLowerCase(); return Object.prototype.hasOwnProperty.call(this._h, n) ? this._h[n] : null; }
  has(n) { return Object.prototype.hasOwnProperty.call(this._h, String(n).toLowerCase()); }
  set(n, v) { this._h[String(n).toLowerCase()] = String(v); }
  delete(n) { delete this._h[String(n).toLowerCase()]; }
  forEach(fn) { Object.keys(this._h).forEach((k) => fn(this._h[k], k, this)); }
  keys() { return Object.keys(this._h)[Symbol.iterator](); }
  entries() { return Object.keys(this._h).map((k) => [k, this._h[k]])[Symbol.iterator](); }
  [Symbol.iterator]() { return this.entries(); }
}
function responseHeaders(h) {
  const flat = {};
  Object.keys(h || {}).forEach((k) => { const v = h[k]; flat[k] = Array.isArray(v) ? v.join(", ") : String(v); });
  return new Headers(flat);
}
function fetch(url, init) {
  return new Promise((resolve, reject) => {
    init = init || {};
    if (url && typeof url === "object" && url.url) url = url.url;
    const msg = { t: "fetch", url: String(url), method: String(init.method || "GET").toUpperCase(), headers: headerMap(init.headers) };
    const b = init.body;
    if (b !== undefined && b !== null) {
      if (typeof b === "string") msg.body = b;
      else if (b instanceof ArrayBuffer || ArrayBuffer.isView(b)) msg.bodyB64 = b64Encode(b);
      else msg.body = String(b);
    }
    const r = request(msg);
    if (!r || !r.ok) { reject(replyError(r, "fetch failed")); return; }
    const d = r.data || {};
    let bytes = null;
    const raw = () => {
      if (bytes === null) bytes = typeof d.bodyB64 === "string" ? b64Decode(d.bodyB64) : utf8Encode(d.body == null ? "" : d.body);
      return bytes;
    };
    let used = false;
    const consume = () => {
      if (used) throw new TypeError("Body has already been consumed");
      used = true;
    };
    const status = d.status | 0;
    resolve({
      ok: status >= 200 && status < 300,
      status,
      statusText: d.statusText || "",
      url: String(url),
      redirected: false,
      headers: responseHeaders(d.headers),
      get bodyUsed() { return used; },
      text: () => new Promise((res) => { consume(); res(typeof d.bodyB64 === "string" ? utf8Decode(raw()) : String(d.body == null ? "" : d.body)); }),
      json: () => new Promise((res) => { consume(); res(jsonParse(typeof d.bodyB64 === "string" ? utf8Decode(raw()) : String(d.body == null ? "" : d.body))); }),
      arrayBuffer: () => new Promise((res) => { consume(); const u = raw(); res(u.buffer.slice(u.byteOffset, u.byteOffset + u.byteLength)); }),
      bytes: () => new Promise((res) => { consume(); res(raw().slice()); })
    });
  });
}

// ---- run ----
function errorMessage(e) {
  if (e instanceof Error) return { name: e.name || "Error", message: String(e.message), stack: String(e.stack || ""), code: e.code };
  let message;
  try { message = typeof e === "string" ? e : jsonStringify(e); } catch (_) { message = String(e); }
  return { name: "Error", message: "Uncaught " + message, stack: "" };
}
function fail(phase, e) {
  const m = errorMessage(e);
  send({ t: "error", phase, name: m.name, message: m.message, stack: m.stack, code: m.code });
  exit(0);
}
function finish(value) {
  let json;
  try { json = jsonStringify(value === undefined ? null : value); } catch (e) { fail("run", e); return; }
  stdout.puts("\x01{\"t\":\"return\",\"value\":" + (json === undefined ? "null" : json) + "}\n");
  stdout.flush();
  exit(0);
}

const input = jsonParse(loadFile("/fn/input.json"));
const ctx = jsonParse(loadFile("/fn/context.json"));
const site = Object.freeze(Object.assign({}, (ctx && ctx.site) || {}));

const bunker = Object.freeze({
  version: "1",
  site,
  me,
  db: Object.freeze(db),
  files: Object.freeze(files),
  storage: Object.freeze(storage),
  rooms,
  room: rooms,
  ai: Object.freeze(ai),
  m365,
  connections: Object.freeze(connections),
  notify,
  secrets: Object.freeze(secrets),
  BunkerError
});

function define(name, value) {
  Object.defineProperty(G, name, { value, writable: true, configurable: true, enumerable: false });
}
["std", "os", "bjson", "scriptArgs", "execArgv", "argv0", "gc", "print", "navigator"].forEach((k) => { delete G[k]; });
Object.defineProperty(G, "bunker", { value: bunker, writable: false, configurable: false, enumerable: true });
define("BunkerError", BunkerError);
define("console", console);
define("print", console.log);
define("fetch", fetch);
define("Headers", Headers);
define("TextEncoder", TextEncoder);
define("TextDecoder", TextDecoder);
define("setTimeout", setTimeout);
define("clearTimeout", clearTimeout);
define("setInterval", setInterval);
define("clearInterval", clearTimeout);

// No top-level await here: qjs never exits while the entry module's
// evaluation is pending, so a promise that never settles would hang until
// the timeout instead of ending the run.
async function main() {
  let mod;
  try {
    mod = await import("./main.js");
  } catch (e) {
    fail("load", e);
    return;
  }
  if (MODE === "inspect") {
    let config = null;
    try { config = mod.config === undefined ? null : jsonParse(jsonStringify(mod.config)); } catch (e) { fail("load", e); return; }
    finish({ config, hasDefault: typeof mod.default === "function" });
    return;
  }
  if (typeof mod.default !== "function") {
    fail("load", new Error("the module has no default export function"));
    return;
  }
  let value;
  try {
    value = await mod.default(input, ctx);
  } catch (e) {
    fail("run", e);
    return;
  }
  finish(value);
}
main();
