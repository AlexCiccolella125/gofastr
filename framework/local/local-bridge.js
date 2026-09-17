// local-bridge: the three bridges between framework/local's browser
// store and Go screens — the seed (a signal filled from a record), the
// upload (records that ride an RPC request) and the download (records
// a response writes back). Requires local-store, which owns the store,
// the caps, the migrations and the mirror; this file owns the markers
// data-local-seed and data-local-send, the request/response hooks on
// rpc.js's data-fui-rpc-with seam, and nothing else.
(() => {
  'use strict';
  const NAME = 'local-bridge';
  const NS = window.__gofastr = window.__gofastr || {};
  if (NS.loadedModules && Object.prototype.hasOwnProperty.call(NS.loadedModules, NAME)) return;
  // Flag first (see local-store.js).
  (NS.loadedModules = NS.loadedModules || {})[NAME] = true;

  const H = NS._localHelpers;
  const validKey = H.validKey;
  const encode = H.encode;
  const isObject = H.isObject;
  const RESERVED = H.RESERVED;
  const own = (o, k) => Object.prototype.hasOwnProperty.call(o, k);
  const openStore = (app) => NS.localStore(app);

  const MARKER = '[data-local-seed]';

  // ─── the seed bridge: a signal filled from a record ─────────────

  // signal name -> { echoing, last }. Per NAME, not per element, and
  // never torn down: the seeded slice is app-global (Seed implies
  // Global in Go), so the listener outlives any one page.
  const seeds = new Map();

  const wireSeed = (el, store) => {
    const spec = el.getAttribute('data-local-seed') || '';
    const colon = spec.indexOf(':');
    const name = el.getAttribute('data-fui-signal');
    if (colon <= 0 || !name || RESERVED.test(name)) return;
    const coll = spec.slice(0, colon);
    const key = spec.slice(colon + 1);
    const c = store.collection(coll);
    if (!c || !validKey(key)) return;
    let entry = seeds.get(name);
    const apply = (value) => {
      if (value === undefined) return;
      entry.last = encode(value);
      entry.echoing = true;
      try { NS.setSignal(name, value); } finally { entry.echoing = false; }
    };
    if (!entry) {
      entry = { echoing: false, last: null };
      seeds.set(name, entry);
      // The kernel's own reserved-key refusal, spelled here because
      // this module creates the signal slot (persist.js's idiom): a
      // planted data-fui-signal="__proto__" would otherwise re-parent
      // the shared signal store. Own-property read, computed.js's
      // idiom: a name like "constructor" resolves through the
      // prototype chain otherwise.
      if (name === '__proto__' || name === 'constructor' || name === 'prototype') return;
      if (!Object.prototype.hasOwnProperty.call(NS._signals, name) || !NS._signals[name]) NS._signals[name] = { value: undefined, listeners: [] };
      NS._signals[name].listeners.push((v) => {
        if (entry.echoing) return; // a restore or a mirror is not a new write
        const text = encode(v);
        if (text === null || text === entry.last) return;
        c.put(key, v).then((r) => { if (r.ok) entry.last = text; });
      });
      c.subscribe((ev) => {
        if (ev.key !== key || ev.source !== 'tab') return;
        c.get(key).then(apply);
      });
    }
    // On EVERY scan, including the one after a client navigation
    // whose DOM came back from the route cache: the record is what
    // this browser holds, so it wins over whatever the cached markup
    // painted. A restore never writes back (echoing).
    c.get(key).then(apply);
  };

  // ─── the upload bridge: a request hook on data-fui-rpc ──────────

  // The trigger carries data-local-send="<coll>[:<key>][,<coll>…]" and
  // data-fui-rpc-with="local-bridge", so rpc.js has this module loaded
  // and runs the hook before the fetch. Only what the attribute names
  // travels, as the reserved field __local: {"<coll>": [{k, v}, …]} in
  // a JSON body, the form field __local in a FormData body, and a
  // fresh JSON body when the trigger had none. A GET carries nothing.
  const gather = (store, sendSpec) => {
    // A Map, never a plain object: the collection name arrives from
    // an attribute, and a bracket write keyed by one is how __proto__
    // re-parents a store. Serialised as an object at the end.
    const out = new Map();
    const jobs = [];
    for (const raw of sendSpec.split(',')) {
      const part = raw.trim();
      if (!part) continue;
      const colon = part.indexOf(':');
      const coll = colon < 0 ? part : part.slice(0, colon);
      const key = colon < 0 ? '' : part.slice(colon + 1);
      const c = store.collection(coll);
      if (!c || RESERVED.test(coll)) continue;
      if (!out.has(coll)) out.set(coll, []);
      const rows = out.get(coll);
      if (key) {
        jobs.push(c.get(key).then((v) => { if (v !== undefined) rows.push({ k: key, v: v }); }));
      } else {
        jobs.push(c.list().then((list) => { for (const r of list) rows.push({ k: r.key, v: r.value }); }));
      }
    }
    return Promise.all(jobs).then(() => Object.fromEntries(out));
  };

  const requestHook = (node, req) => {
    const sendSpec = node.getAttribute('data-local-send');
    const app = node.getAttribute('data-local-store');
    if (!sendSpec || !app || RESERVED.test(app) || req.method === 'GET') return Promise.resolve();
    const store = openStore(app);
    if (!store) return Promise.resolve();
    return gather(store, sendSpec).then((payload) => {
      if (req.isFormData && req.body && typeof req.body.append === 'function') {
        req.body.append('__local', JSON.stringify(payload));
        return;
      }
      let obj = {};
      if (typeof req.body === 'string' && req.body !== '') {
        try { obj = JSON.parse(req.body); } catch (_) { obj = null; }
        if (!isObject(obj)) return; // not a JSON object body: nothing to ride on
      }
      obj.__local = payload;
      req.body = JSON.stringify(obj);
      req.headers['Content-Type'] = 'application/json';
    });
  };

  // ─── the download bridge: records written from a response ───────

  // X-Gofastr-Local: {"app":"<app>","ops":[{"c":"<coll>","k":"<key>","v":…}
  // | {"c":"<coll>","k":"<key>","d":true} | {"clear":true}]}. Every op
  // goes through the same put/delete as a page write, so the caps
  // hold and subscribers hear it; a refused op raises
  // gofastr:local-error like any other.
  const responseHook = (node, r) => {
    let header = '';
    try { header = r.headers.get('X-Gofastr-Local') || ''; } catch (_) { return; }
    if (!header) return;
    let msg = null;
    try { msg = JSON.parse(header); } catch (_) { return; }
    if (!isObject(msg) || typeof msg.app !== 'string' || RESERVED.test(msg.app) || !Array.isArray(msg.ops)) return;
    const store = openStore(msg.app);
    if (!store) return;
    for (const op of msg.ops) {
      if (!isObject(op)) continue;
      if (op.clear === true) { store.clear(); continue; }
      const c = typeof op.c === 'string' ? store.collection(op.c) : null;
      if (!c || !validKey(op.k)) continue;
      if (op.d === true) c.delete(op.k);
      else if (own(op, 'v')) c.put(op.k, op.v);
    }
  };

  NS._rpcHooks = NS._rpcHooks || { request: [], response: [] };
  NS._rpcHooks.request.push(requestHook);
  NS._rpcHooks.response.push(responseHook);

  // ─── scan ───────────────────────────────────────────────────────

  const wire = (el) => {
    const app = el.getAttribute('data-local-store');
    if (!app || RESERVED.test(app)) return;
    const store = openStore(app);
    if (store) wireSeed(el, store);
  };
  const scan = (root) => {
    const scope = root && root.querySelectorAll ? root : document;
    if (scope.matches && scope.matches(MARKER)) wire(scope);
    scope.querySelectorAll(MARKER).forEach(wire);
  };

  scan(document);
  NS._moduleScanners = NS._moduleScanners || {};
  NS._moduleScanners[NAME] = scan;
})();
