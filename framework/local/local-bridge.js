// local-bridge: the four bridges between framework/local's browser
// store and Go screens — the seed (a signal filled from a record), the
// mirror (a record a render reads at first paint, through a cookie),
// the upload (records that ride an RPC request) and the download
// (records a response writes back). Requires local-store, which owns
// the store, the caps and the migrations; this file owns the markers
// data-local-seed and data-local-send, the cookies, the
// request/response hooks on rpc.js's data-fui-rpc-with seam, and
// nothing else.
//
// The line between the two modules is a responsibility, not a byte
// count: local-store keeps records, local-bridge is every way those
// records reach a Go handler. A page whose store keeps its records to
// itself never loads this file; local-store asks for it when a
// declaration mirrors a collection or a logout is pending.
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

  // ─── the mirror bridge: a record a Go render reads at first paint ──

  // A mirrored collection keeps every record in a cookie too, so a Go
  // screen can read it at FIRST PAINT (the IndexedDB read lands after
  // hydration, by construction). The name is the framework's namespace
  // plus one component-encoded segment, the value the component-
  // encoded JSON text: neither can carry the cookie grammar. Same
  // shape as banner.js's dismissal cookie, which is the one channel a
  // browser-held value had to the server before this module.
  //
  // On https the cookie is Secure, like every cookie the Go side sets
  // (response.go follows the request scheme). Without it a single
  // plain-http request to the origin — a typo, a stripped link, a
  // captive portal — carries every mirrored record in clear text.
  // Spelled as whole literal writes per case rather than a
  // concatenated suffix: the cookie lint reads a document.cookie
  // assignment operand by operand and refuses any non-literal that is
  // not component-encoded, which is the rule that keeps a value from
  // planting its own cookie name and attributes.
  const SECURE = (() => {
    try { return window.location.protocol === 'https:'; } catch (_) { return false; }
  })();
  const cookieNamed = (name) => {
    try { return ('; ' + document.cookie).indexOf('; ' + name + '=') >= 0; } catch (_) { return false; }
  };

  // mirrorUsed is what this store's OTHER mirror cookies already cost
  // the Cookie header, which is the number the budget is about: a
  // record costs more encoded than stored, and a header block past the
  // 8-16 KiB most proxies allow is a 431 the user can only clear by
  // hand.
  const mirrorUsed = (app, seg) => {
    let all = '';
    try { all = document.cookie; } catch (_) { return 0; }
    let used = 0;
    for (const part of all.split('; ')) {
      const eq = part.indexOf('=');
      if (eq < 0) continue;
      const nm = part.slice(0, eq);
      if (nm.indexOf('gofastr.local.' + app + '.') !== 0 || nm === 'gofastr.local.' + seg) continue;
      used += part.length + 2;
    }
    return used;
  };

  // mirror writes the record's cookie, or clears it when text is ''
  // (a zero max-age is the deletion). Over budget the write is refused
  // and the page hears it; the record itself is kept, only its
  // shortcut to first paint is not.
  const mirror = (app, coll, key, text, budget) => {
    const seg = encodeURIComponent(app + '.' + coll + '.' + key);
    if (text) {
      const enc = encodeURIComponent(text);
      // The name, the '=', the encoded value and the '; ' a browser
      // puts between cookies.
      const cost = 'gofastr.local.'.length + seg.length + 1 + enc.length + 2;
      if (budget > 0 && mirrorUsed(app, seg) + cost > budget) {
        try {
          window.dispatchEvent(new CustomEvent('gofastr:local-error', {
            detail: { app: app, collection: coll, key: key, reason: 'mirror', size: cost, max: budget },
          }));
        } catch (_) { /* best-effort */ }
        return;
      }
    }
    try {
      if (text && SECURE) document.cookie = 'gofastr.local.' + encodeURIComponent(app + '.' + coll + '.' + key) + '=' + encodeURIComponent(text) + '; path=/; max-age=31536000; SameSite=Lax; Secure';
      else if (text) document.cookie = 'gofastr.local.' + encodeURIComponent(app + '.' + coll + '.' + key) + '=' + encodeURIComponent(text) + '; path=/; max-age=31536000; SameSite=Lax';
      else if (SECURE) document.cookie = 'gofastr.local.' + encodeURIComponent(app + '.' + coll + '.' + key) + '=; path=/; max-age=0; SameSite=Lax; Secure';
      else document.cookie = 'gofastr.local.' + encodeURIComponent(app + '.' + coll + '.' + key) + '=; path=/; max-age=0; SameSite=Lax';
    } catch (_) { /* best-effort */ }
  };
  // Take the seam over: local-store queued every mirror call made
  // before this file evaluated, and they run now, in order.
  NS._localMirror(mirror);

  // Clear-on-next-load: a full-navigation logout cannot ride an RPC
  // response header, so the Go side plants a bit in a cookie and this
  // module honours it once, then drops the cookie — but only once the
  // clear actually succeeded, or a logout that could not reach the
  // store would be forgotten.
  //
  // Honoured over every app the manifest declares, not only the ones a
  // marker on this page names: the page a logout redirects to need not
  // carry that app's marker at all.
  const honourClearBits = () => {
    const all = window.__gofastr_local;
    if (!all || typeof all !== 'object') return;
    for (const app of Object.keys(all)) {
      if (RESERVED.test(app) || !cookieNamed('gofastr.local.clear.' + encodeURIComponent(app))) continue;
      const store = openStore(app);
      if (!store) continue;
      store.clear().then((r) => {
        if (!r.ok) return;
        try {
          if (SECURE) document.cookie = 'gofastr.local.clear.' + encodeURIComponent(app) + '=; path=/; max-age=0; SameSite=Lax; Secure';
          else document.cookie = 'gofastr.local.clear.' + encodeURIComponent(app) + '=; path=/; max-age=0; SameSite=Lax';
        } catch (_) { /* best-effort */ }
      });
    }
  };

  // A mirrored collection re-stamps its cookies when this module loads,
  // so a cookie the browser dropped while the record survived comes
  // back.
  const restampMirrors = () => {
    const all = window.__gofastr_local;
    if (!all || typeof all !== 'object') return;
    for (const app of Object.keys(all)) {
      if (RESERVED.test(app)) continue;
      const store = openStore(app);
      if (!store) continue;
      const m = all[app];
      const cs = m && isObject(m.collections) ? m.collections : null;
      const budget = m && m.mirrorMax > 0 ? m.mirrorMax : 0;
      if (!cs) continue;
      for (const name of Object.keys(cs)) {
        if (!cs[name] || !cs[name].mirror || !store.collection(name)) continue;
        const prefix = 'local.' + app + '.' + name + ':';
        const coll = name;
        store.collection(name).count().then(() => NS.local.entries(prefix)).then((er) => {
          if (!er.ok) return;
          for (const e of er.entries) mirror(app, coll, e.key.slice(prefix.length), encode(e.value) || 'null', budget);
        });
      }
    }
  };

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

  honourClearBits();
  restampMirrors();
  scan(document);
  NS._moduleScanners = NS._moduleScanners || {};
  NS._moduleScanners[NAME] = scan;
})();
