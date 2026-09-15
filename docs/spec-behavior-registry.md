# Spec: behaviour registers like style

Status: proposed, 2026-09-15. Implements the direction from the runtime
exploration: the browser runtime is composed on the fly per page and
loads its features lazily, and a component's behaviour is registered by
the package that renders its markup, the way its stylesheet already is.

## The problem

`core-ui/runtime` ships one kernel (`runtime.js`, eight fragments, 13.2 KB
gzip) and 55 demand-loaded modules under `core-ui/runtime/src`. The
kernel is sound and stays as it is. The modules are the problem:

- 34 of them bind markup that only `framework/ui` renders (banner, tabs,
  sidebar, menu, panehost, optimisticaction, themeswitch, and so on); 15
  bind core-ui's own patterns and widgets; six are kernel-side or
  explicit APIs. All 55 live in one directory that belongs to none of
  their owners.
- The kernel finds a module by a hand-written table, `_moduleMarkers` in
  `frag/boot.js`, mirrored by hand in `preload.go`'s `demandLoadMarkers`,
  with a test that keeps the two copies equal. About 450 `ui-*`
  component names are literals inside the runtime.
- A stylesheet has a seam: `registry.RegisterStyle` in the component's
  package, a catalog in `<head>`, one bundle per page, a marker scan
  after hydration. Behaviour has none. Adding a component with behaviour
  means a file in `core-ui/runtime/src`, an entry in the kernel's table,
  an entry in `preload.go`, an entry in `fragments.go`, a row in the
  ARCHITECTURE table, and a budget line.

## Goal

One seam for behaviour, shaped like the one for style:

```go
// framework/headless/behavior.go
//go:embed runtime.js
var runtimeJS string

var Behavior = registry.RegisterBehavior("headless", runtimeJS,
    registry.Markers("[data-hui-reveal]", "[data-hui-when]", "[data-hui-drop]"))
```

The registering package owns the JavaScript, embedded beside the Go
that renders the markup it binds. The host serves it at
`/__gofastr/runtime/<name>.js?v=<hash>` like any module, lists it in the
module manifest the kernel already reads, and tells the kernel its
markers in one more inert JSON block. The kernel scans registered
markers exactly as it scans its own table, loads the module once when a
marker appears, at boot, on DOM insertion, or after a client
navigation, and the module attaches. Nothing about how a module behaves
once loaded changes.

## Non-goals

- No trigger vocabulary. The marker is the one trigger, as today. What
  Angular's incremental hydration contributes is the loading side, a
  chunk fetched on demand whose behaviour attaches when it lands, and the
  kernel already does that on insertion.
- No change to the kernel's role or fragments, and none to the CSS
  pipeline.
- No module moves in the first change. `framework/ui`'s 34 modules stay
  where they are until the seam has a real client; moving them is its own
  change per package, and the kernel's table shrinks as they go.
- No event replay before load. The insertion scan precedes interaction
  in practice; if a measured case shows otherwise it is a separate
  change.

## Design

### Registration (`core-ui/registry`)

```go
func RegisterBehavior(name, js string, opts ...BehaviorOption) *Behavior

func Markers(selectors ...string) BehaviorOption // required: at least one
func LoadIdle() BehaviorOption                   // load after first paint, as `idle: true` modules do

func (b *Behavior) Name() string
func (b *Behavior) Entry() *BehaviorEntry

type BehaviorEntry struct {
    Name    string
    Source  string   // as registered
    Markers []string // attribute selectors
    Idle    bool
}

func Behaviors() []*BehaviorEntry          // sorted by name
func LookupBehavior(name string) (*BehaviorEntry, bool)
```

Rules, each a panic at registration with the reason, so a mistake is a
startup failure and not a dead marker:

- `name` matches `^[a-z][a-z0-9-]{0,63}$` (the bound every module
  name keeps) and is not the name of an embedded
  kernel module (`runtime.ModuleNames()`); the two are one namespace
  because they share one URL and one manifest. A style and a behaviour
  may share a name: a component registers both under its own name.
- Every marker is an attribute selector, `[data-x]` or `[data-x="v"]`,
  on a `data-` attribute, and the value carries no control character,
  backslash, quote or bracket: a string `querySelector` would throw on
  is refused at registration, because one throw in the kernel's scan
  would abort the boot pass for every module. Nothing else scans (no
  `role=` selectors for registered behaviours; the two kernel modules
  that use them predate this seam). A `data-fui-*` marker is permitted
  only when the attribute is already in `core-ui/ARCHITECTURE.md`'s
  table, checked by a gate that reads every `registry.Markers(...)`
  call site in the tree rather than the registry of one test binary,
  which links only what imports it, so hard rule 5 holds through this
  seam as well.
- A duplicate name with identical source and options is a no-op, as
  `RegisterStyle` is; a different definition panics.
- `js` is non-empty and is served as registered, minified under the same
  gate as the embedded modules (`GOFASTR_ENV`, `GOFASTR_DEV`,
  `RUNTIME_NOMINIFY`, `RUNTIME_MINIFY`). The registry stores the source;
  minification and hashing happen where the embedded modules' do, in
  `core-ui/runtime`, so the two kinds of module are one kind from the
  host down.

### The module contract

A registered module is an IIFE with the contract every `src/*.js` module
already keeps, now written down:

1. It binds only to its own markers, by attribute, never by class.
2. It sets `window.__gofastr.loadedModules[<name>] = true` when it has
   attached, so the kernel does not fetch it twice.
3. It registers `window.__gofastr._moduleScanners[<name>] = fn(root)`,
   idempotent against already-wired elements, so the kernel can hand it
   newly inserted DOM and the document after a client navigation.
4. It reads no `data-fui-*` attribute it does not own, and writes none.
5. It fetches only same-origin, and forwards the CSRF token on unsafe
   methods the way `src/rpc.js` does.

The kernel treats a registered module and an embedded one identically
after the fetch.

### Serving and the manifest (`core-ui/runtime`, `core-ui/widget`, `framework/uihost`)

- `runtime.Module(name)` and `runtime.ModuleNames()` cover registered
  behaviours as well as embedded modules. `Module` returns the minified
  source under the existing gate; the version hash is the same eight
  hex bytes of SHA-256 over the served bytes. Every consumer that
  enumerates modules (the serve route, `RuntimeModuleHash`, the manifest,
  the PWA precache list, the static exporter's dump) therefore covers
  them with no change of its own.
- The manifest block `#gofastr-runtime-modules` keeps its shape (name to
  hash). A second inert block, `#gofastr-behaviors`, carries
  `{ "<name>": { "s": ["[data-x]", ...], "i": true } }` for registered
  behaviours only. Emitted wherever the manifest is emitted: live pages,
  the static export, the embed frame.
- Preload: `runtime.NeededModules(pageHTML)` also matches registered
  markers, so the host's `<link rel="preload" as="script">` covers them.
  A marker `[data-x]` matches as the attribute name `data-x`;
  `[data-x="v"]` as `data-x="v"`, with the same name-boundary rule the
  table uses today.

### The kernel (`frag/boot.js`)

One addition. At boot the kernel reads `#gofastr-behaviors` once and
appends its entries to the scan list:

```js
const _registered = (() => {
  try {
    const el = document.getElementById('gofastr-behaviors');
    const o = el ? JSON.parse(el.textContent) : {};
    return Object.keys(o).map(n => ({ name: n, selector: o[n].s.join(','), idle: !!o[n].i }));
  } catch (_) { return []; }
})();
```

and `_scanForModules` iterates `_moduleMarkers.concat(_registered)`.
`loadModule` needs no change: the name resolves through the manifest to
the same URL shape, and the identifier guard already rejects anything
that is not `[\w-]+`. `data-fui-prefetch="<name>"` works for a registered
name for the same reason.

The addition is measured, not guessed: the kernel currently sits at
13,200 bytes gzip against a 13,213 goal (and 15,238 against 15,246 at
level 1). The budget lines rise by the measured merged size plus the 8
bytes of clearance every raise there carries, with the reason on the
line, as `budget_test.go`'s history does.

### Ownership gate (`fragments.go`, `attrdoc_test.go`)

Every `data-fui-*` attribute in the runtime sources has one owner today.
The gate gains one clause: a registered behaviour's markers must be
`data-` attributes, and any `data-fui-*` among them must be in the
documented table. A registered behaviour is not an owner in
`fragments.go`; it owns its own prefix.

## What must be tested

Unit, in `core-ui/registry`:

- name rules, marker rules, the `data-fui-*` clause, duplicate
  identical no-op, duplicate different panic, `Behaviors()` order,
  reset for tests as `IsolateForTest` does for styles.

Unit, in `core-ui/runtime` and `core-ui/widget`:

- a registered behaviour appears in `ModuleNames()`, `Module()` returns
  it minified under each gating state, its hash is stable and changes
  with the source, `NeededModules` matches its markers with the boundary
  rule, `serveRuntimeModule` serves it immutable by hash and 404s an
  unknown name, the manifest and the `#gofastr-behaviors` block carry
  it and escape `</` in both.
- the existing gates still pass: byte-identical composition,
  `SYMBOLS.txt`, attrdoc ownership, both demand-load tables equal, the
  budgets at their new lines, and `TestCoreBudgetRejectsCliffOverflow`
  against the padded fixture.

Browser, in `core-ui/runtime` (chromedp against `httptest`):

- a page with the marker fetches the module once and the module attaches
  (a probe attribute the module writes);
- a page without the marker never fetches it;
- a marker inserted later (island swap, widget mount) loads it through
  the insertion scan;
- after a client navigation the module's scanner runs over the new
  document;
- `LoadIdle` defers to idle and still attaches;
- `data-fui-prefetch="<name>"` on hover fetches before any click;
- a failing fetch does not strand the page: the marker element stays,
  `loadedModules[<name>]` stays unset, nothing is thrown; the browser's
  own network error is the signal, as it is for a table module, because
  the scan path swallows the rejection on purpose (a warning would be
  kernel bytes for a case the console already reports).

Browser, in `examples/site`:

- the site registers one real behaviour from a package that is not
  `core-ui/runtime`, and the runtime-split suite's contract holds for it:
  no marker no fetch, marker fetches, manifest is content-addressed,
  mutation observer loads it, SPA navigation rescans it, hover prefetch.
- the static export contains the module file and the behaviours block;
  the static composition ships the same `boot` fragment the live suites
  cover, so the scan itself is not re-proven from an exported page.

Documentation, in the same change: `core-ui/ARCHITECTURE.md` ("Component
CSS" gains "Component behaviour"), the `data-fui-*` table if any marker
touches it, `framework/docs/content/ui-new-components.md` and
`runtime-minification.md`, and `runtime-contract.md`.

## Sequence

1. This seam, with the tests above and no module moved.
2. `framework/headless` registers its `data-hui-*` module: the first
   real client, and the proof the seam carries a whole design system's
   behaviour.
3. `framework/ui`, one package at a time: each module becomes a
   `RegisterBehavior` in the Go file that renders its markup; the
   kernel's table and `preload.go`'s mirror lose the entry; the `ui-*`
   literals leave the runtime with it.
4. `core-ui/patterns`, the same way. What remains in `core-ui/runtime`
   is the kernel, its fragments, and the six kernel-side modules.

## Open questions

- Whether `LoadIdle` is worth its option in the first change, or whether
  every registered behaviour is marker-immediate until one needs idle.
- Whether the static exporter should also dump the behaviours block
  into every page or only into pages whose markers it finds; today it
  dumps every embedded module regardless.
