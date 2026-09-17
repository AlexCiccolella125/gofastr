// local.js: the browser's own store, as one primitive.
//
// The runtime had five hard-coded Web-storage keys and no way for
// anything else to keep a value in the browser: a sidebar boolean, a
// banner bit, a colour scheme, a form draft, a scroll map. Each was
// written where it was needed, each re-derived its own namespace, and
// an app that wanted to remember anything of its own had to leave the
// framework and write a document script. This is that capability with
// one owner.
//
// Engine: IndexedDB, because a saved value is not a preference. It is
// asynchronous, it is not capped at the ~5 MB localStorage shares
// across a whole origin, and it does not block the main thread on a
// large read. localStorage is the FALLBACK, for tiny values only
// (LS_MAX_BYTES), in a browser that has no usable IndexedDB. No
// external dependency: both are browser APIs.
//
// The contract is BEST-EFFORT and that is the design, not a caveat.
// Private mode, a blocked origin, a full quota, a hand-cleared store
// and a browser that refuses to open a database are all normal. Every
// call settles, none throws, and set() says why it refused instead of
// pretending. Never keep something here whose loss is a bug: the
// server is still where truth lives.
//
// Keys are namespaced: the stored key is always the literal
// 'gofastr.state.' plus the component-encoded application key, spelled
// at every sink, in BOTH engines. An application key can therefore
// never name another feature's storage, and core-ui/check's
// storage-key lint holds the spelling.
//
// It has no marker, so nothing loads it on its own: a module that
// needs it declares registry.Requires('local') and the loader has it
// registered before the dependent evaluates, and an application can
// reach it with __gofastr.loadModule('local'). window.__gofastr.local
// is the public API — get, set, remove, keys, subscribe, available.
//
// Opinionated storage — schemas, migrations, an upload channel, a
// sync lane — is a layer ABOVE this one and does not belong here.
(() => {
  'use strict';
  const NS = window.__gofastr = window.__gofastr || {};

  // The one namespace, shared with every other gofastr.* storage key on
  // the origin and spelled at each sink.
  const PREFIX = 'gofastr.state.';
  const DB_NAME = 'gofastr.state';
  const DB_STORE = 'kv';
  // The localStorage fallback is for tiny values only: it is synchronous
  // and its quota is the whole origin's.
  const LS_MAX_BYTES = 8192;

  // key -> Set<fn>. Maps, never plain objects: an application key is
  // often attribute-borne, and a bracket write keyed by one is how
  // __proto__ re-parents a store.
  const subs = new Map();

  let dbPromise = null;

  // openDB resolves the database, or null when this browser will not
  // give us one (no IndexedDB, private mode, a blocked upgrade). Cached:
  // the answer does not change within a document.
  const openDB = () => {
    if (dbPromise) return dbPromise;
    dbPromise = new Promise((resolve) => {
      let req;
      try { req = window.indexedDB.open(DB_NAME, 1); } catch (_) { resolve(null); return; }
      if (!req) { resolve(null); return; }
      req.onupgradeneeded = () => {
        try { req.result.createObjectStore(DB_STORE); } catch (_) { /* already there */ }
      };
      req.onsuccess = () => resolve(req.result || null);
      req.onerror = () => resolve(null);
      req.onblocked = () => resolve(null);
    });
    return dbPromise;
  };

  // idbRun runs one transaction and settles to { ok, value, reason }.
  // Never rejects: the caller's settlement is always one of the two
  // outcomes, the same rule src/action.js's request keeps.
  const idbRun = (mode, fn) => openDB().then((db) => {
    if (!db) return { ok: false, reason: 'unavailable' };
    return new Promise((resolve) => {
      let tx;
      let req;
      try {
        tx = db.transaction(DB_STORE, mode);
        req = fn(tx.objectStore(DB_STORE));
      } catch (_) { resolve({ ok: false, reason: 'unavailable' }); return; }
      tx.oncomplete = () => resolve({ ok: true, value: req ? req.result : undefined });
      const fail = () => {
        const name = (tx.error && tx.error.name) || (req && req.error && req.error.name) || '';
        resolve({ ok: false, reason: name === 'QuotaExceededError' ? 'quota' : 'unavailable' });
      };
      tx.onabort = fail;
      tx.onerror = fail;
    });
  });

  // lsAvailable probes the fallback the only way that is honest: by
  // writing. Safari's private mode exposes localStorage and throws on
  // every setItem.
  const lsAvailable = () => {
    try {
      window.localStorage.setItem(PREFIX + encodeURIComponent('__probe'), '1');
      window.localStorage.removeItem(PREFIX + encodeURIComponent('__probe'));
      return true;
    } catch (_) { return false; }
  };

  // Values travel as JSON text in both engines, so size accounting is
  // one number and a structured-clone surprise (a DOM node, a function)
  // cannot reach the database.
  const encode = (value) => {
    let text;
    try { text = JSON.stringify(value); } catch (_) { return null; }
    return typeof text === 'string' ? text : null;
  };
  const decode = (text) => {
    if (typeof text !== 'string') return undefined;
    try { return JSON.parse(text); } catch (_) { return undefined; }
  };

  let channel = null;
  try { channel = new BroadcastChannel(DB_NAME); } catch (_) { channel = null; }

  // announce tells the other tabs of this origin that a key moved. The
  // key travels, never the value: a subscriber re-reads, so a large
  // entry is not copied into every tab and a listener always sees what
  // the store actually holds.
  const announce = (key) => {
    if (!channel) return;
    try { channel.postMessage({ k: key }); } catch (_) { /* best-effort */ }
  };

  const notify = (key) => {
    const fns = subs.get(key);
    if (!fns || fns.size === 0) return;
    // eslint-disable-next-line no-use-before-define
    api.get(key).then((value) => {
      for (const fn of fns) {
        try { fn(value, key); } catch (_) { /* a throwing subscriber is its own problem */ }
      }
    });
  };

  if (channel) {
    channel.onmessage = (e) => {
      if (e && e.data && typeof e.data.k === 'string') notify(e.data.k);
    };
  }
  // The storage event covers the fallback engine and any writer that is
  // not this module. BroadcastChannel and storage both skip the writing
  // context, so a tab never hears its own write back.
  window.addEventListener('storage', (e) => {
    if (!e || typeof e.key !== 'string' || e.storageArea !== window.localStorage) return;
    if (e.key.indexOf(PREFIX) !== 0) return;
    let key;
    // decodeURIComponent throws URIError on a malformed escape; a
    // foreign key in our namespace must not take the listener down.
    try { key = decodeURIComponent(e.key.slice(PREFIX.length)); } catch (_) { return; }
    notify(key);
  });

  const api = {
    // available reports what this browser actually gives us, probed
    // rather than feature-detected. Use it to decide whether to offer a
    // "your work is saved here" affordance at all.
    available() {
      return openDB().then((db) => ({ idb: !!db, ls: lsAvailable() }));
    },

    // get resolves the stored value, or undefined when the key is
    // absent, the entry is unreadable, or no engine is available.
    get(key) {
      if (typeof key !== 'string' || key === '') return Promise.resolve(undefined);
      return openDB().then((db) => {
        if (db) {
          return idbRun('readonly', (s) => s.get(PREFIX + encodeURIComponent(key)))
            .then((r) => (r.ok ? decode(r.value) : undefined));
        }
        let text = null;
        // Guard spelled at the sink: literal namespace + component
        // encoding, so an application key names nothing outside it.
        try { text = window.localStorage.getItem(PREFIX + encodeURIComponent(key)); } catch (_) { text = null; }
        return text === null ? undefined : decode(text);
      });
    },

    // set stores the value and resolves { ok, reason }. reason is
    // 'encode' (the value is not JSON), 'size' (over the fallback's
    // tiny-value cap), 'quota' (the browser said no) or 'unavailable'
    // (no engine). It never throws and never rejects.
    set(key, value) {
      if (typeof key !== 'string' || key === '') return Promise.resolve({ ok: false, reason: 'unavailable' });
      const text = encode(value);
      if (text === null) return Promise.resolve({ ok: false, reason: 'encode' });
      return openDB().then((db) => {
        if (db) {
          return idbRun('readwrite', (s) => s.put(text, PREFIX + encodeURIComponent(key))).then((r) => {
            if (r.ok) announce(key);
            return { ok: r.ok, reason: r.ok ? '' : r.reason };
          });
        }
        if (text.length > LS_MAX_BYTES) return { ok: false, reason: 'size' };
        try {
          // Guard spelled at the sink (see get above).
          window.localStorage.setItem(PREFIX + encodeURIComponent(key), text);
        } catch (_) {
          return { ok: false, reason: 'quota' };
        }
        announce(key);
        return { ok: true, reason: '' };
      });
    },

    // remove drops the entry. Resolves { ok } and never throws.
    remove(key) {
      if (typeof key !== 'string' || key === '') return Promise.resolve({ ok: false, reason: 'unavailable' });
      return openDB().then((db) => {
        if (db) {
          return idbRun('readwrite', (s) => s.delete(PREFIX + encodeURIComponent(key))).then((r) => {
            if (r.ok) announce(key);
            return { ok: r.ok, reason: r.ok ? '' : r.reason };
          });
        }
        try {
          // Guard spelled at the sink (see get above).
          window.localStorage.removeItem(PREFIX + encodeURIComponent(key));
        } catch (_) {
          return { ok: false, reason: 'unavailable' };
        }
        announce(key);
        return { ok: true, reason: '' };
      });
    },

    // keys resolves the application keys this origin holds, namespace
    // stripped and decoded. Sorted, so a caller can diff two reads.
    keys() {
      const strip = (stored) => {
        if (typeof stored !== 'string' || stored.indexOf(PREFIX) !== 0) return null;
        // See the storage listener: a malformed escape must not take
        // the enumeration down.
        try { return decodeURIComponent(stored.slice(PREFIX.length)); } catch (_) { return null; }
      };
      return openDB().then((db) => {
        if (db) {
          return idbRun('readonly', (s) => s.getAllKeys()).then((r) => {
            const out = [];
            for (const k of (r.ok && r.value) || []) {
              const app = strip(k);
              if (app !== null) out.push(app);
            }
            return out.sort();
          });
        }
        const out = [];
        try {
          for (let i = 0; i < window.localStorage.length; i++) {
            const app = strip(window.localStorage.key(i));
            if (app !== null) out.push(app);
          }
        } catch (_) { return []; }
        return out.sort();
      });
    },

    // subscribe calls fn(value, key) when ANOTHER tab of this origin
    // changes the key. Returns the unsubscribe function. A tab never
    // hears its own writes: both transports skip the writing context,
    // so a subscriber is a cross-tab channel, not a change feed.
    subscribe(key, fn) {
      if (typeof key !== 'string' || typeof fn !== 'function') return () => {};
      let fns = subs.get(key);
      if (!fns) {
        fns = new Set();
        subs.set(key, fns);
      }
      fns.add(fn);
      return () => {
        const cur = subs.get(key);
        if (!cur) return;
        cur.delete(fn);
        if (cur.size === 0) subs.delete(key);
      };
    },
  };

  NS.local = api;
  (NS.loadedModules = NS.loadedModules || {}).local = true;
})();
