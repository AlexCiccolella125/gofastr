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
// list, and a change feed that includes this tab's own writes. The
// three bridges to Go screens — a signal seeded from a record, an
// upload declared per request, and records written from a response —
// are local-bridge.js, a second module that Requires this one: two
// files because the per-module byte budget holds every registered
// behaviour, and a page that only reads records never pays for the
// bridges.
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
// user about (key, size, full, quota, encode, unavailable, migration,
// version). A collection whose migration did not complete is GATED:
// every method refuses with reason 'migration' rather than answering
// from records on a schema this build cannot read.
// Truth still lives on the server. This is not offline sync: no queue,
// no conflict resolution, no reconciliation.
(() => {
  'use strict';
  const NAME = 'local-store';
  const NS = window.__gofastr = window.__gofastr || {};
  // The kernel fetches a module once per page, but anything that
  // evaluates this file twice must bind nothing twice.
  if (NS.loadedModules && Object.prototype.hasOwnProperty.call(NS.loadedModules, NAME)) return;
  // Flag first: the loader resolves on registration, and a file that
  // failed halfway with its flag unset would be re-executed by the
  // next loadModule and install everything twice.
  (NS.loadedModules = NS.loadedModules || {})[NAME] = true;

  const MARKER = '[data-local-store]';
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
    try { window.dispatchEvent(new CustomEvent(type, { detail })); } catch (_) { /* best-effort */ }
  };
  const fail = (app, collection, key, reason, size, max) => {
    emit('gofastr:local-error', { app, collection, key, reason, size: size || 0, max: max || 0 });
    return { ok: false, reason };
  };
  const encode = (value) => {
    let text;
    try { text = JSON.stringify(value); } catch (_) { return null; }
    return typeof text === 'string' ? text : null;
  };
  const bytesOf = (text) => {
    try { return new TextEncoder().encode(text).length; } catch (_) { return text.length; }
  };
  const validKey = (key) => typeof key === 'string' && key !== '' && key.length <= 256 && !RESERVED.test(key);
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
  // mirror writes the record's cookie, or clears it when text is ''
  // (a zero max-age is the deletion). Two writes, every operand a
  // literal or component-encoded: the cookie lint reads them as such.
  const mirror = (app, coll, key, text) => {
    try {
      if (text) document.cookie = 'gofastr.local.' + encodeURIComponent(app + '.' + coll + '.' + key) + '=' + encodeURIComponent(text) + '; path=/; max-age=31536000; SameSite=Lax';
      else document.cookie = 'gofastr.local.' + encodeURIComponent(app + '.' + coll + '.' + key) + '=; path=/; max-age=0; SameSite=Lax';
    } catch (_) { /* best-effort */ }
  };
  const cookieNamed = (name) => {
    try { return ('; ' + document.cookie).indexOf('; ' + name + '=') >= 0; } catch (_) { return false; }
  };

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
    }
    return record;
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
        try { fn({ app, collection: coll, key, source }); } catch (_) { /* a throwing subscriber is its own problem */ }
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

    const specOf = (coll) => (own(manifest.collections, coll) && isObject(manifest.collections[coll]) ? manifest.collections[coll] : null);

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
        if (have === target) return true;
        // A stored version ABOVE the declared one is a rollback: this
        // build's steps cannot undo what a newer build wrote, and
        // reading those records as if they were this schema is how a
        // deploy that gets rolled back corrupts the browsers that
        // already moved on. Refuse the collection and say so.
        if (have > target) {
          fail(app, coll, '', 'version', have, target);
          return false;
        }
        return P.entries(prefixOf(coll)).then((er) => {
          if (!er.ok) throw new Error('entries: ' + er.reason);
          const entries = er.entries;
          if (have === 0 && entries.length === 0) return P.set(metaKey(coll), { v: target }).then((r) => r.ok || fail(app, coll, '', 'migration').ok);
          const from = have === 0 ? 1 : have;
          const steps = [];
          for (const m of spec.migrations || []) {
            if (m && m.v > from && m.v <= target) steps.push(m);
          }
          steps.sort((a, b) => a.v - b.v);
          const writes = [];
          const done = [];
          for (const e of entries) {
            let rec = e.value;
            const key = e.key.slice(prefixOf(coll).length);
            for (const m of steps) for (const st of m.steps || []) rec = applyStep(st, rec, key);
            done.push({ key: key, rec: rec });
            writes.push(P.set(e.key, rec));
          }
          return Promise.all(writes).then((rs) => {
            // P.set settles {ok:false} on a quota refusal or an aborted
            // transaction; it does not reject. Stamping the version over
            // a half-rewritten collection records the migration as done,
            // so it never runs again and every later read mixes two
            // schemas. Every write has to have landed.
            for (const r of rs) if (!r || !r.ok) return fail(app, coll, '', 'migration').ok;
            return P.set(metaKey(coll), { v: target }).then((r) => {
              if (!r.ok) return fail(app, coll, '', 'migration').ok;
              emit('gofastr:local-migrated', { app, collection: coll, from, to: target });
              // The MIGRATED record, not the one the entry carried:
              // mirroring e.value would put the pre-migration shape in
              // the cookie and hand the server the old schema.
              if (spec.mirror) for (const d of done) mirror(app, coll, d.key, encode(d.rec) || 'null');
              return true;
            });
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
    // gate is whenReady as a refusal: null when the collection is
    // usable, {ok:false, reason:'migration'} when it is not. EVERY
    // method goes through it. A collection whose migration failed holds
    // records on a schema this build cannot read, and serving them
    // anyway — which is what discarding whenReady's answer did — turns
    // one failed rewrite into corrupt data everywhere the records go.
    const gate = (coll) => whenReady(coll).then((ok) => (ok ? null : fail(app, coll, '', 'migration')));

    const collectionAPI = (coll) => {
      const spec = specOf(coll);
      const maxRecord = spec.maxRecord > 0 ? spec.maxRecord : DEFAULT_MAX_RECORD;
      const maxRecords = spec.maxRecords > 0 ? spec.maxRecords : DEFAULT_MAX_RECORDS;
      const maxBytes = spec.maxBytes > 0 ? spec.maxBytes : DEFAULT_MAX_BYTES;

      const api = {
        name: coll,
        // available resolves what the engine underneath offers, after
        // the collection's migration settled.
        available() { return gate(coll).then((no) => no || P.available()); },
        // get resolves undefined on a gated collection, like a missing
        // key; the refusal itself rides gofastr:local-error.
        get(key) {
          if (!validKey(key)) return Promise.resolve(undefined);
          return gate(coll).then((no) => (no ? undefined : P.get(recordKey(coll, key))));
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
          return gate(coll).then((no) => no || P.entries(prefixOf(coll)).then((er) => {
            if (!er.ok) return fail(app, coll, key, er.reason || 'unavailable');
            const entries = er.entries;
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
              if (spec.mirror) mirror(app, coll, key, text);
              notify(coll, key, 'local');
              return { ok: true, reason: '' };
            });
          }));
        },
        delete(key) {
          if (!validKey(key)) return Promise.resolve(fail(app, coll, String(key), 'key'));
          return gate(coll).then((no) => no || P.remove(recordKey(coll, key)).then((r) => {
            if (!r.ok) return fail(app, coll, key, r.reason);
            if (spec.mirror) mirror(app, coll, key, '');
            notify(coll, key, 'local');
            return { ok: true, reason: '' };
          }));
        },
        // list resolves [{key, value}] sorted by key, or by opts.orderBy
        // (a top-level field; opts.desc reverses), after opts.where
        // (equality on top-level fields, all of them), with opts.offset
        // and opts.limit applied last. Filters run here, over one
        // collection this browser owns; nothing is re-implemented that
        // the server does for server data.
        list(opts) {
          const o = opts && typeof opts === 'object' ? opts : {};
          return gate(coll).then((no) => (no ? [] : P.entries(prefixOf(coll)).then((er) => {
            const entries = er.entries;
            let out = [];
            for (const e of entries) {
              if (isObject(o.where) && !Object.keys(o.where).every((f) => isObject(e.value) && e.value[f] === o.where[f])) continue;
              out.push({ key: e.key.slice(prefixOf(coll).length), value: e.value });
            }
            if (typeof o.orderBy === 'string' && o.orderBy !== '') {
              const pick = (r) => (isObject(r.value) ? r.value[o.orderBy] : undefined);
              // Entries arrive sorted by key, and the sort is stable, so
              // equal fields keep key order; undefined sorts last.
              out.sort((a, b) => {
                const x = pick(a);
                const y = pick(b);
                if (x === y) return 0;
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
          })));
        },
        count() { return gate(coll).then((no) => (no ? 0 : P.keys(prefixOf(coll)).then((r) => r.keys.length))); },
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
        // clear removes every record of the collection, and says so
        // only when it did. An enumeration that aborted used to look
        // exactly like an empty collection, so the logout path reported
        // {ok:true} over records — and mirror cookies — that all
        // survived. A removal that failed is the same lie one record
        // deep, so both are checked.
        clear() {
          return gate(coll).then((no) => no || P.keys(prefixOf(coll)).then((r) => {
            if (!r.ok) return fail(app, coll, '', r.reason || 'unavailable');
            const ks = r.keys;
            return Promise.all(ks.map((k) => P.remove(k))).then((rs) => {
              let bad = '';
              for (let i = 0; i < ks.length; i++) {
                if (!rs[i] || !rs[i].ok) { bad = (rs[i] && rs[i].reason) || 'unavailable'; continue; }
                const key = ks[i].slice(prefixOf(coll).length);
                if (spec.mirror) mirror(app, coll, key, '');
                notify(coll, key, 'local');
              }
              if (bad) return fail(app, coll, '', bad);
              return { ok: true, reason: '' };
            });
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
      app,
      collections,
      collection(name) {
        return typeof name === 'string' && own(collections, name) ? collections[name] : null;
      },
      available() { return P.available(); },
      // clear drops every record of every collection: logout. It
      // reports the first collection that could not be cleared, because
      // "the previous user's records are gone" is the only thing a
      // logout is for.
      clear() {
        return Promise.all(Object.keys(collections).map((c) => collections[c].clear())).then((rs) => {
          for (const r of rs) if (!r || !r.ok) return { ok: false, reason: (r && r.reason) || 'unavailable' };
          return { ok: true, reason: '' };
        });
      },
    };
    stores.set(app, store);

    // Clear-on-next-load: a full-navigation logout cannot ride an RPC
    // response header, so the Go side plants a short-lived bit in a
    // cookie and this module honours it once, then drops the cookie.
    if (cookieNamed('gofastr.local.clear.' + encodeURIComponent(app))) {
      store.clear().then(() => {
        try { document.cookie = 'gofastr.local.clear.' + encodeURIComponent(app) + '=; path=/; max-age=0; SameSite=Lax'; } catch (_) { /* best-effort */ }
      });
    }
    // A mirrored collection re-stamps its cookies on open, so a cookie
    // the browser dropped while the record survived comes back.
    for (const name of Object.keys(collections)) {
      const spec = specOf(name);
      if (spec && spec.mirror) {
        whenReady(name).then(() => P.entries(prefixOf(name))).then((er) => {
          if (!er.ok) return;
          for (const e of er.entries) mirror(app, name, e.key.slice(prefixOf(name).length), encode(e.value) || 'null');
        });
      }
    }
    return store;
  };

  // ─── scan ───────────────────────────────────────────────────────

  // Opening a store on its marker is all the core module does with
  // the DOM: it honours the clear bit and re-stamps mirror cookies.
  // The seed and send markers are the bridge module's (local-bridge.js,
  // Requires this one).
  const wire = (el) => {
    const app = el.getAttribute('data-local-store');
    if (app && !RESERVED.test(app)) openStore(app);
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
  // Shared with the bridge module.
  NS._localHelpers = { validKey: validKey, encode: encode, isObject: isObject, RESERVED: RESERVED };

  scan(document);
  NS._moduleScanners = NS._moduleScanners || {};
  NS._moduleScanners[NAME] = scan;
})();
