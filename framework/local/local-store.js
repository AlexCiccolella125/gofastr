// local-store: the runtime half of framework/local, a local-first
// state API for GoFastr apps — declared in Go, persisted in the
// browser, with a documented contract.
//
// This module is the OPINION; the storage is not here. Records live in
// the kernel's `local` primitive (core-ui/runtime/src/local.js:
// IndexedDB, a tiny-value localStorage fallback, one namespace, every
// call best-effort), which this file's registration Requires. What
// this file adds is what a store needs and a key-value primitive must
// not carry: named collections with a schema version and migrations,
// a size cap per record and per collection, a key field, a filtered
// list, a change feed that includes this tab's own writes, and the
// three bridges to Go screens — a signal seeded from a record, an
// upload declared per request, and records written from a response.
//
// The declaration arrives from Go, not from the DOM: Store.ScriptJS
// serves `window.__gofastr_local[<app>]` on the host's extra-script
// rail (uihost.WithExtraScripts), the same rail computed reducers use,
// so a marker planted in an island response cannot redefine a
// collection's caps or migrations. A marker names a collection; the
// manifest says what it is.
//
// Keys. A record is the primitive entry 'local.<app>.<collection>:<key>'
// and a collection's version is the entry 'local.<app>.<collection>'
// (no colon, so it never collides with a record: app and collection
// names are [a-z0-9-] by the Go validator). The primitive stores every
// entry under 'gofastr.state.' plus the component encoding, so nothing
// here can name storage outside that namespace, and one prefix
// enumerates one collection.
//
// Contract, the same promises the primitive keeps: every call settles,
// nothing throws, a refusal says why ({ok:false, reason}), and the page
// hears gofastr:local-error for the refusals an app should tell the
// user about (key, size, full, quota, encode, unavailable, migration).
// Truth still lives on the server. This is not offline sync: no queue,
// no conflict resolution, no reconciliation.
(() => {
  'use strict';
  const NAME = 'local-store';
  const NS = window.__gofastr = window.__gofastr || {};
  // The kernel fetches a module once per page, but anything that
  // evaluates this file twice must bind nothing twice.
  if (NS.loadedModules && Object.prototype.hasOwnProperty.call(NS.loadedModules, NAME)) return;

  const MARKER = '[data-local-store]';
  const KEY_MAX = 256;
  // Mirror the Go defaults in framework/local/collection.go; used only
  // when a manifest entry is missing a cap, which the Go side never
  // emits.
  const DEFAULT_MAX_RECORD = 65536;
  const DEFAULT_MAX_RECORDS = 1000;
  const DEFAULT_MAX_BYTES = 1048576;
  const RESERVED = /^(__proto__|constructor|prototype)$/;

  // app -> store API. A Map: the app id arrives from a DOM attribute on
  // the seed and send markers, and a bracket write keyed by one is how
  // __proto__ re-parents a registry.
  const stores = new Map();

  const emit = (type, detail) => {
    try { window.dispatchEvent(new CustomEvent(type, { detail: detail })); } catch (_) { /* best-effort */ }
  };
  const fail = (app, coll, key, reason, size, max) => {
    emit('gofastr:local-error', { app: app, collection: coll, key: key, reason: reason, size: size || 0, max: max || 0 });
    return { ok: false, reason: reason };
  };
  const encode = (value) => {
    let text;
    try { text = JSON.stringify(value); } catch (_) { return null; }
    return typeof text === 'string' ? text : null;
  };
  const bytesOf = (text) => {
    try { return new TextEncoder().encode(text).length; } catch (_) { return text.length; }
  };
  const validKey = (key) => typeof key === 'string' && key !== '' && key.length <= KEY_MAX && !RESERVED.test(key);
  const isObject = (v) => v !== null && typeof v === 'object' && !Array.isArray(v);
  const own = (o, k) => Object.prototype.hasOwnProperty.call(o, k);

  // The manifest the Go declaration served. Read at open time, never
  // cached before the rail ran: extra scripts are parser-blocking and
  // the kernel's module scan runs after them, but a module reached
  // through loadModule from a script that itself sits on the rail
  // could evaluate first.
  const manifestFor = (app) => {
    const all = window.__gofastr_local;
    if (!all || typeof all !== 'object' || !own(all, app)) return null;
    const m = all[app];
    return m && typeof m === 'object' && isObject(m.collections) ? m : null;
  };

  // ─── cookies (the mirror, and the clear-on-next-load bit) ────────

  // A mirrored collection keeps every record in a cookie too, so a Go
  // screen can read it at FIRST PAINT (the IndexedDB read lands after
  // hydration, by construction). The name is the framework's namespace
  // plus one component-encoded segment, the value the component-
  // encoded JSON text: neither can carry the cookie grammar. Same
  // shape as banner.js's dismissal cookie, which is the one channel a
  // browser-held value had to the server before this module.
  const mirrorWrite = (app, coll, key, text) => {
    try {
      document.cookie = 'gofastr.local.' + encodeURIComponent(app + '.' + coll + '.' + key) + '=' + encodeURIComponent(text) + '; path=/; max-age=31536000; SameSite=Lax';
    } catch (_) { /* best-effort */ }
  };
  const mirrorClear = (app, coll, key) => {
    try {
      document.cookie = 'gofastr.local.' + encodeURIComponent(app + '.' + coll + '.' + key) + '=; path=/; max-age=0; SameSite=Lax';
    } catch (_) { /* best-effort */ }
  };
  const cookieNamed = (name) => {
    let all = '';
    try { all = document.cookie || ''; } catch (_) { return false; }
    for (const part of all.split(';')) {
      const eq = part.indexOf('=');
      if ((eq < 0 ? part : part.slice(0, eq)).trim() === name) return true;
    }
    return false;
  };
  const clearBit = (app) => 'gofastr.local.clear.' + encodeURIComponent(app);

  // ─── migrations ─────────────────────────────────────────────────

  // applyStep runs one declared step over one record. Steps are data
  // transforms declared in Go (rename, default, remove) plus `func`,
  // a host-registered function on window.__gofastr._localMigrations
  // — a real function loaded from the script rail, never text, so the
  // page stays CSP-clean. A rename onto an existing field, or of a
  // missing one, is a no-op, which is what lets two tabs migrate the
  // same collection without a lock: every declared step is
  // idempotent; a func step is asked to be.
  const applyStep = (step, record, key) => {
    if (!isObject(record) || !step) return record;
    switch (step.op) {
      case 'rename':
        if (own(record, step.from) && !own(record, step.to)) {
          record[step.to] = record[step.from];
          delete record[step.from];
        }
        return record;
      case 'default':
        if (!own(record, step.field)) record[step.field] = step.value;
        return record;
      case 'remove':
        delete record[step.field];
        return record;
      case 'func': {
        const fns = NS._localMigrations;
        const fn = fns && own(fns, step.name) ? fns[step.name] : null;
        if (typeof fn !== 'function') throw new Error('migration function not registered: ' + step.name);
        const out = fn(record, key);
        return out === undefined ? record : out;
      }
      default:
        return record;
    }
  };

  // ─── one store ──────────────────────────────────────────────────

  const openStore = (app) => {
    if (stores.has(app)) return stores.get(app);
    const manifest = manifestFor(app);
    if (!manifest) return null;
    const P = NS.local;
    const prefixOf = (coll) => 'local.' + app + '.' + coll + ':';
    const metaKey = (coll) => 'local.' + app + '.' + coll;
    const recordKey = (coll, key) => prefixOf(coll) + key;
    // collection -> Set<fn>, this tab's subscribers.
    const subs = new Map();
    // collection -> Promise, the once-per-page readiness (migration).
    const ready = new Map();
    const collections = Object.create(null);

    const notify = (coll, key, source) => {
      const fns = subs.get(coll);
      if (!fns) return;
      for (const fn of fns) {
        try { fn({ app: app, collection: coll, key: key, source: source }); } catch (_) { /* a throwing subscriber is its own problem */ }
      }
    };

    // Another tab's write of anything under the store: route it to the
    // collection's subscribers with source 'tab'. One watcher per store.
    P.watch('local.' + app + '.', (k) => {
      const rest = k.slice(('local.' + app + '.').length);
      const colon = rest.indexOf(':');
      if (colon < 0) return; // a version entry, not a record
      notify(rest.slice(0, colon), rest.slice(colon + 1), 'tab');
    });

    const specOf = (coll) => {
      const c = manifest.collections;
      return own(c, coll) && isObject(c[coll]) ? c[coll] : null;
    };

    // migrate brings one collection to its declared version, once per
    // page, under the Web Locks API when the browser has it so two
    // tabs do not race the same rewrite. A collection nobody wrote yet
    // is stamped at the declared version and nothing runs. A func step
    // whose function is not registered leaves the records and the
    // version untouched and raises gofastr:local-error with reason
    // 'migration': the data is worth more than the schema.
    const migrate = (coll, spec) => {
      const target = spec.v > 0 ? spec.v : 1;
      const run = () => P.get(metaKey(coll)).then((meta) => {
        const have = meta && typeof meta.v === 'number' ? meta.v : 0;
        if (have >= target) return true;
        return P.entries(prefixOf(coll)).then((entries) => {
          if (have === 0 && entries.length === 0) return P.set(metaKey(coll), { v: target }).then(() => true);
          const from = have === 0 ? 1 : have;
          const steps = [];
          for (const m of spec.migrations || []) {
            if (m && m.v > from && m.v <= target) steps.push(m);
          }
          steps.sort((a, b) => a.v - b.v);
          const writes = [];
          for (const e of entries) {
            let rec = e.value;
            const key = e.key.slice(prefixOf(coll).length);
            for (const m of steps) for (const st of m.steps || []) rec = applyStep(st, rec, key);
            writes.push(P.set(e.key, rec));
          }
          return Promise.all(writes).then(() => P.set(metaKey(coll), { v: target })).then(() => {
            emit('gofastr:local-migrated', { app: app, collection: coll, from: from, to: target });
            if (spec.mirror) for (const e of entries) mirrorWrite(app, coll, e.key.slice(prefixOf(coll).length), encode(e.value) || 'null');
            return true;
          });
        });
      }).catch(() => {
        fail(app, coll, '', 'migration');
        return false;
      });
      const locks = window.navigator && window.navigator.locks;
      if (locks && typeof locks.request === 'function') {
        try { return locks.request('gofastr.local.' + app + '.' + coll, run); } catch (_) { return run(); }
      }
      return run();
    };
    const whenReady = (coll) => {
      const spec = specOf(coll);
      if (!spec) return Promise.resolve(false);
      if (!ready.has(coll)) ready.set(coll, migrate(coll, spec));
      return ready.get(coll);
    };

    const collectionAPI = (coll) => {
      const spec = specOf(coll);
      const maxRecord = spec.maxRecord > 0 ? spec.maxRecord : DEFAULT_MAX_RECORD;
      const maxRecords = spec.maxRecords > 0 ? spec.maxRecords : DEFAULT_MAX_RECORDS;
      const maxBytes = spec.maxBytes > 0 ? spec.maxBytes : DEFAULT_MAX_BYTES;

      const api = {
        name: coll,
        // available resolves what the engine underneath offers, after
        // the collection's migration settled.
        available() { return whenReady(coll).then(() => P.available()); },
        get(key) {
          if (!validKey(key)) return Promise.resolve(undefined);
          return whenReady(coll).then(() => P.get(recordKey(coll, key)));
        },
        // put(key, value), or put(value) when the collection declares a
        // key field: the key is then value[keyField], a non-empty string.
        // Resolves {ok, reason}: 'key', 'encode', 'size' (over the
        // per-record cap), 'full' (over the collection's record or byte
        // cap), 'quota', 'unavailable'.
        put(key, value) {
          if (arguments.length === 1) {
            value = key;
            key = spec.key && isObject(value) ? value[spec.key] : undefined;
          }
          if (!validKey(key)) return Promise.resolve(fail(app, coll, String(key), 'key'));
          const text = encode(value);
          if (text === null) return Promise.resolve(fail(app, coll, key, 'encode'));
          const size = bytesOf(text);
          if (size > maxRecord) return Promise.resolve(fail(app, coll, key, 'size', size, maxRecord));
          return whenReady(coll).then(() => P.entries(prefixOf(coll))).then((entries) => {
            let total = size;
            let n = 1;
            const mine = recordKey(coll, key);
            for (const e of entries) {
              if (e.key === mine) continue;
              total += e.size;
              n++;
            }
            if (n > maxRecords) return fail(app, coll, key, 'full', n, maxRecords);
            if (total > maxBytes) return fail(app, coll, key, 'full', total, maxBytes);
            return P.set(mine, value).then((r) => {
              if (!r.ok) return fail(app, coll, key, r.reason, size, maxRecord);
              if (spec.mirror) mirrorWrite(app, coll, key, text);
              notify(coll, key, 'local');
              return { ok: true, reason: '' };
            });
          });
        },
        delete(key) {
          if (!validKey(key)) return Promise.resolve(fail(app, coll, String(key), 'key'));
          return whenReady(coll).then(() => P.remove(recordKey(coll, key))).then((r) => {
            if (!r.ok) return fail(app, coll, key, r.reason);
            if (spec.mirror) mirrorClear(app, coll, key);
            notify(coll, key, 'local');
            return { ok: true, reason: '' };
          });
        },
        // list resolves [{key, value}] sorted by key, or by opts.orderBy
        // (a top-level field; opts.desc reverses), after opts.where
        // (equality on top-level fields, all of them), with opts.offset
        // and opts.limit applied last. Filters run here, over one
        // collection this browser owns; nothing is re-implemented that
        // the server does for server data.
        list(opts) {
          const o = opts && typeof opts === 'object' ? opts : {};
          return whenReady(coll).then(() => P.entries(prefixOf(coll))).then((entries) => {
            let out = [];
            for (const e of entries) {
              const rec = e.value;
              if (isObject(o.where)) {
                let hit = true;
                for (const f of Object.keys(o.where)) {
                  if (!isObject(rec) || rec[f] !== o.where[f]) { hit = false; break; }
                }
                if (!hit) continue;
              }
              out.push({ key: e.key.slice(prefixOf(coll).length), value: rec });
            }
            if (typeof o.orderBy === 'string' && o.orderBy !== '') {
              const f = o.orderBy;
              const pick = (r) => (isObject(r.value) ? r.value[f] : undefined);
              out.sort((a, b) => {
                const x = pick(a);
                const y = pick(b);
                if (x === y) return a.key < b.key ? -1 : a.key > b.key ? 1 : 0;
                if (x === undefined) return 1;
                if (y === undefined) return -1;
                return x < y ? -1 : 1;
              });
            }
            if (o.desc) out.reverse();
            const off = o.offset > 0 ? o.offset : 0;
            const lim = o.limit > 0 ? o.limit : out.length;
            if (off || lim < out.length) out = out.slice(off, off + lim);
            return out;
          });
        },
        count() { return whenReady(coll).then(() => P.keys(prefixOf(coll))).then((ks) => ks.length); },
        // subscribe calls fn({app, collection, key, source}) after every
        // write to this collection: source 'local' for this tab's own
        // put/delete (including one a response wrote), 'tab' for another
        // tab's. Returns the unsubscribe function.
        subscribe(fn) {
          if (typeof fn !== 'function') return () => {};
          let fns = subs.get(coll);
          if (!fns) { fns = new Set(); subs.set(coll, fns); }
          fns.add(fn);
          return () => { fns.delete(fn); };
        },
        // clear removes every record of the collection.
        clear() {
          return whenReady(coll).then(() => P.keys(prefixOf(coll))).then((ks) => Promise.all(ks.map((k) => P.remove(k))).then(() => {
            for (const k of ks) {
              const key = k.slice(prefixOf(coll).length);
              if (spec.mirror) mirrorClear(app, coll, key);
              notify(coll, key, 'local');
            }
            return { ok: true, reason: '' };
          }));
        },
      };
      return api;
    };

    for (const name of Object.keys(manifest.collections)) {
      if (RESERVED.test(name) || !specOf(name)) continue;
      collections[name] = collectionAPI(name);
    }

    const store = {
      app: app,
      collections: collections,
      collection(name) {
        return typeof name === 'string' && own(collections, name) ? collections[name] : null;
      },
      available() { return P.available(); },
      // clear drops every record of every collection: logout.
      clear() {
        const all = Object.keys(collections).map((c) => collections[c].clear());
        return Promise.all(all).then(() => ({ ok: true, reason: '' }));
      },
    };
    stores.set(app, store);

    // Clear-on-next-load: a full-navigation logout cannot ride an RPC
    // response header, so the Go side plants a short-lived bit in a
    // cookie and this module honours it once, then drops the cookie.
    if (cookieNamed(clearBit(app))) {
      store.clear().then(() => {
        // Spelled inline, not via clearBit: the cookie lint reads the
        // operands at the write.
        try { document.cookie = 'gofastr.local.clear.' + encodeURIComponent(app) + '=; path=/; max-age=0; SameSite=Lax'; } catch (_) { /* best-effort */ }
      });
    }
    // A mirrored collection re-stamps its cookies on open, so a cookie
    // the browser dropped while the record survived comes back.
    for (const name of Object.keys(collections)) {
      const spec = specOf(name);
      if (spec && spec.mirror) {
        whenReady(name).then(() => P.entries(prefixOf(name))).then((entries) => {
          for (const e of entries) mirrorWrite(app, name, e.key.slice(prefixOf(name).length), encode(e.value) || 'null');
        });
      }
    }
    return store;
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
  // data-fui-rpc-with="local-store", so rpc.js has this module loaded
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
    if (!store) return;
    if (el.hasAttribute('data-local-seed')) wireSeed(el, store);
  };
  const scan = (root) => {
    const scope = root && root.querySelectorAll ? root : document;
    if (scope.matches && scope.matches(MARKER)) wire(scope);
    scope.querySelectorAll(MARKER).forEach(wire);
  };

  // localStore(app) is the API an application script reaches after
  // __gofastr.loadModule('local-store'): the store for that app id, or
  // null when no manifest declared it.
  NS.localStore = (app) => (typeof app === 'string' && !RESERVED.test(app) ? openStore(app) : null);

  scan(document);
  NS.loadedModules = NS.loadedModules || {};
  NS.loadedModules[NAME] = true;
  NS._moduleScanners = NS._moduleScanners || {};
  NS._moduleScanners[NAME] = scan;
})();
