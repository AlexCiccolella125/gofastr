// local-migrate: schema evolution for framework/local, and nothing
// else.
//
// A collection declares a version and the steps that bring a record
// from one version to the next. Running them is a different job from
// keeping records: it happens once per browser per version, it rewrites
// every record of a collection at once, and when it goes wrong the
// damage is a collection on two schemas rather than one write lost. So
// it is its own module, registered LoadIdle — never on the critical
// path — and local-store asks for it by name at the moment a rewrite is
// due. A collection that declares no version step never runs a line of
// this file.
//
// The contract with local-store is one call, NS._localMigrate(ctx),
// resolving true when the collection reached its declared version and
// false when it did not. False GATES the collection: local-store
// refuses every method rather than serve records on a schema this build
// cannot read. Nothing here writes a version it did not earn.
(() => {
  'use strict';
  const NAME = 'local-migrate';
  const NS = window.__gofastr = window.__gofastr || {};
  if (NS.loadedModules && Object.prototype.hasOwnProperty.call(NS.loadedModules, NAME)) return;
  // Flag first (see local-store.js).
  (NS.loadedModules = NS.loadedModules || {})[NAME] = true;

  const own = (o, k) => Object.prototype.hasOwnProperty.call(o, k);
  const isObject = (v) => v !== null && typeof v === 'object' && !Array.isArray(v);
  const encode = (value) => {
    let text;
    try { text = JSON.stringify(value); } catch (_) { return null; }
    return typeof text === 'string' ? text : null;
  };

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
      case 'adopt':
        // Adoption is not a per-record transform: it runs once over a
        // foreign key, after the records this collection already holds
        // reached the target schema. See adoptAll.
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

  const bytesOf = (text) => {
    try { return new TextEncoder().encode(text).length; } catch (_) { return text.length; }
  };
  // The caps, read from the Go declaration the rail served — not from
  // ctx: local-store is 15 bytes under its budget and this module is
  // the one with room. Same object local-store reads, same numbers.
  const capsOf = (app, coll) => {
    const all = window.__gofastr_local;
    const m = all && isObject(all) && own(all, app) ? all[app] : null;
    const cs = m && isObject(m.collections) ? m.collections : null;
    return cs && own(cs, coll) && isObject(cs[coll]) ? cs[coll] : null;
  };

  // adoptAll runs the version's `adopt` steps: a once-only read of a
  // FOREIGN localStorage key the app already wrote, parsed by the app's
  // own registered function, written here so the lock, the caps and the
  // stamp are the package's. The foreign key is never written and never
  // deleted — the framework does not own it.
  //
  // Everything is validated before anything is written, and one refusal
  // fails the whole migration: a half-adopted library stamped as done
  // is the failure this step exists to make impossible.
  const adoptAll = (ctx) => {
    const steps = [];
    for (const m of ctx.steps) for (const st of m.steps || []) if (st && st.op === 'adopt') steps.push(st);
    if (steps.length === 0) return Promise.resolve(true);
    const caps = capsOf(ctx.app, ctx.coll);
    if (!caps) return Promise.resolve(false);
    return ctx.P.entries(ctx.prefix).then((er) => {
      if (!er.ok) return false;
      const writes = [];
      // A Map, not an object: the keys are the app's, not the
      // framework's. An adopted key the collection already holds
      // REPLACES that record, so it costs its difference, not a whole
      // new one — counting it twice would refuse an adoption that fits.
      const held = new Map();
      let n = er.entries.length;
      let total = 0;
      for (const e of er.entries) {
        held.set(e.key.slice(ctx.prefix.length), e.size);
        total += e.size;
      }
      for (const st of steps) {
        let text = null;
        try { text = window.localStorage.getItem(st.from); } catch (_) { text = null; }
        if (text === null) continue; // nothing there to adopt; the step is done
        const fns = NS._localAdopters;
        const fn = fns && own(fns, st.name) ? fns[st.name] : null;
        if (typeof fn !== 'function') throw new Error('adopt function not registered: ' + st.name);
        const recs = fn(text, st.from);
        if (!Array.isArray(recs)) throw new Error('adopt ' + st.name + ' did not return an array of {k, v}');
        for (const r of recs) {
          if (!isObject(r) || typeof r.k !== 'string' || r.k === '' || bytesOf(r.k) > 256) throw new Error('adopt ' + st.name + ': a record has no usable key');
          const enc = encode(r.v);
          if (enc === null) throw new Error('adopt ' + st.name + ': a record does not encode');
          const size = bytesOf(enc);
          if (size > caps.maxRecord) throw new Error('adopt ' + st.name + ': a record is over the record cap');
          if (held.has(r.k)) total -= held.get(r.k);
          else n++;
          held.set(r.k, size);
          total += size;
          if (n > caps.maxRecords || total > caps.maxBytes) throw new Error('adopt ' + st.name + ': the adopted records are over the collection cap');
          const key = r.k;
          const value = r.v;
          writes.push(ctx.P.set(ctx.prefix + key, value).then((res) => {
            if (res.ok && ctx.mirror) ctx.mirror(key, enc);
            return res;
          }));
        }
      }
      return Promise.all(writes).then((rs) => {
        for (const r of rs) if (!r || !r.ok) return false;
        return true;
      });
    });
  };

  // _localMigrate rewrites every record of one collection through the
  // declared steps, then stamps the new version — in that order, and
  // only if every write landed.
  //
  // P.set settles {ok:false} on a quota refusal or an aborted
  // transaction; it does not reject. Stamping the version over a
  // half-rewritten collection would record the migration as done, so it
  // would never run again and every later read would mix two schemas —
  // which is why every settlement is checked rather than awaited.
  NS._localMigrate = (ctx) => ctx.P.entries(ctx.prefix).then((er) => {
    if (!er.ok) return ctx.fail(ctx.app, ctx.coll, '', 'migration').ok;
    const writes = [];
    const done = [];
    for (const e of er.entries) {
      let rec = e.value;
      const key = e.key.slice(ctx.prefix.length);
      for (const m of ctx.steps) for (const st of m.steps || []) rec = applyStep(st, rec, key);
      done.push({ key: key, rec: rec });
      writes.push(ctx.P.set(e.key, rec));
    }
    return Promise.all(writes).then((rs) => {
      for (const r of rs) if (!r || !r.ok) return ctx.fail(ctx.app, ctx.coll, '', 'migration').ok;
      // Adoption last, over records that already reached this version,
      // and before the stamp: a refused adoption is a failed migration.
      return adoptAll(ctx).then((adopted) => {
        if (!adopted) return ctx.fail(ctx.app, ctx.coll, '', 'migration').ok;
        return ctx.P.set(ctx.meta, { v: ctx.to }).then((r) => {
          if (!r.ok) return ctx.fail(ctx.app, ctx.coll, '', 'migration').ok;
          ctx.emit('gofastr:local-migrated', { app: ctx.app, collection: ctx.coll, from: ctx.from, to: ctx.to });
          // The MIGRATED record, not the one the entry carried: mirroring
          // e.value would put the pre-migration shape in the cookie and
          // hand the server the old schema on every request.
          if (ctx.mirror) for (const d of done) ctx.mirror(d.key, encode(d.rec) || 'null');
          return true;
        });
      });
    });
    // A func or adopt step whose function is not registered throws; the
    // records and the version are left exactly as they were.
  }).catch(() => ctx.fail(ctx.app, ctx.coll, '', 'migration').ok);
})();
