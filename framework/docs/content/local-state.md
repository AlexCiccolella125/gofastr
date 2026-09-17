# Local state (`framework/local`)

`framework/local` is local-first state for a GoFastr app: declared in Go,
persisted in the browser, with a documented contract. A `local.Store` is
declared once per app with named collections; each collection is a Go
record type with a JSON round-trip, an optional key field, a size cap per
record and per collection, and a schema version with migrations the
browser runs once. The browser API is generated from that declaration and
served by the runtime as two modules, `local-store` (the store, caps,
migrations and mirror) and `local-bridge` (the seed, upload and download
bridges, which requires the first), on top of the kernel's `local` storage
primitive ([Runtime contract](runtime-contract.md)):
IndexedDB, with a tiny-value `localStorage` fallback. Both are browser
APIs; no dependency was added.

It is **local-first state, not offline-first**. Offline-first CRDT
workspaces stay an explicit non-goal ([UI capability map](ui-capability-map.md)):
there is no queue of pending mutations, no conflict resolution and no
background reconciliation. Every bridge to the server is opt-in and
explicit, and the server is still where truth lives.

The motivating shape is a server-rendered app whose user owns a large,
account-less dataset in the browser (a team builder's teams and
preferences, a draft, a filter set) while a Go screen reads it at render
or action time and occasionally pushes a value back. Before this package
every such app re-implemented it in an island with a document script.

## Declare

<!-- gofastr:compile
import "github.com/DonaldMurillo/gofastr/framework/local"

type Draft struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
}
type View struct {
	Compact bool `json:"compact"`
}
-->
```go
var Site = local.New("site")

var Drafts = local.Define[Draft](Site, "drafts", local.CollectionConfig{
	Version:        2,
	KeyField:       "id",      // put(value) reads the key from value.id
	MaxRecordBytes: 32 << 10,  // per record, JSON text
	MaxRecords:     200,
	MaxBytes:       1 << 20,   // per collection
	Migrations: []local.Migration{
		{Version: 2, Steps: []local.Step{
			local.Rename("body", "text"),
			local.Default("tags", []string{}),
			local.Remove("legacy"),
		}},
	},
})

// Tiny values the server must know at first paint ride a cookie too.
var Prefs = local.Define[View](Site, "prefs", local.CollectionConfig{Version: 1, Mirror: true})
_, _ = Drafts, Prefs
```

- **Names.** An app id is `^[a-z][a-z0-9-]{0,31}$`, a collection
  `^[a-z][a-z0-9-]{0,63}$`. Declaring the same app id twice, or the same
  collection twice, panics at startup, like registering a screen twice.
- **Namespace.** A record is the primitive entry
  `local.<app>.<collection>:<key>`, stored under `gofastr.state.` plus the
  component encoding, so nothing an app writes can name another feature's
  storage. A collection's version lives at `local.<app>.<collection>`.
- **Caps.** Defaults are 64 KiB per record, 1000 records and 1 MiB per
  collection; the ceilings are 1 MiB, 100 000 and 64 MiB. A `Mirror`
  collection is clamped to 1 KiB per record and 16 records, because every
  record rides a cookie on every request.
- **Versions.** `Version` is 1 or more, and every step from 2 to `Version`
  needs a `Migration` (an empty one is fine). The browser runs the steps
  whose version lies in (stored, declared] once, before the page's first
  read or write of the collection, under the Web Locks API when the
  browser has it. `Rename`, `Default` and `Remove` are data transforms
  declared in Go; `local.Func("name")` runs
  `window.__gofastr._localMigrations["name"]`, a real function the app
  serves on the extra-script rail (never inline). A missing function
  leaves records and version untouched and raises `gofastr:local-error`
  with reason `migration`.
- **The record type** must round-trip through `encoding/json`; `Define`
  panics on one that does not.

## Serve the declaration

The browser reads the declaration from `window.__gofastr_local[<app>]`,
a script the store generates. It rides the host's extra-script rail, the
same rail computed reducers use, so it loads after `runtime.js` on every
full shell render and never inline. A declaration read from the DOM
instead would let markup planted in an island response redefine a
collection's caps or migrations.

<!-- gofastr:compile
import "github.com/DonaldMurillo/gofastr/core-ui/app"
import "github.com/DonaldMurillo/gofastr/framework/local"
import "github.com/DonaldMurillo/gofastr/framework/uihost"
import "net/http"

var Site = local.New("docs-serve")
var mux = http.NewServeMux()
var site = app.NewApp("docs")
-->
```go
mux.Handle(Site.ScriptPath(), Site.ScriptHandler())            // /__gofastr/local/site.js
host := uihost.New(site, uihost.WithExtraScripts(Site.ScriptURL())) // path + ?v=<hash>
_ = host
```

## The browser API

After `__gofastr.loadModule('local-store')` (or once any page markup
carries a `data-local-store` marker), `__gofastr.localStore('site')` is
the store and `.collection('drafts')` one collection:

| Call | Resolves |
| --- | --- |
| `get(key)` | the record, or `undefined` |
| `put(key, value)` / `put(value)` with a key field | `{ok, reason}`; `reason` is `key`, `encode`, `size` (over the record cap), `full` (over the collection's record or byte cap), `quota` or `unavailable` |
| `delete(key)` | `{ok, reason}` |
| `list({where, orderBy, desc, limit, offset})` | `[{key, value}]`; `where` is equality on top-level fields, `orderBy` a top-level field |
| `count()` | the number of records |
| `subscribe(fn)` | unsubscribe; `fn({app, collection, key, source})` after every write, `source` `local` for this tab's own (a response's included) and `tab` for another tab's |
| `clear()` | drops every record of the collection |
| `available()` | the primitive's `{idb, ls}` |

The store itself has `collections`, `collection(name)`, `clear()` (every
collection: logout) and `available()`. Every call settles; none throws.
A refusal an app should tell the user about raises `gofastr:local-error`
on `window` with `{app, collection, key, reason, size, max}`, and a
completed migration raises `gofastr:local-migrated` with
`{app, collection, from, to}`. Two tabs of one origin converge: a write in
one is announced to the other, which re-reads.

## The bridges to Go screens

All four are explicit and bounded. Nothing undeclared is ever uploaded,
and nothing is synchronised in the background.

### Seed: a signal filled from a record

<!-- gofastr:compile
import "context"
import "github.com/DonaldMurillo/gofastr/core-ui/store"
import "github.com/DonaldMurillo/gofastr/framework/local"

type Draft struct{ Title string }
var Site = local.New("docs-seed")
var Drafts = local.Define[Draft](Site, "drafts", local.CollectionConfig{Version: 1})
var S = store.New("editor")
var ctx = context.Background()
-->
```go
current := local.SeedSignal(Drafts, "current", store.JSON[Draft](S, "current", Draft{Title: "untitled"}))
_ = current.Bind(ctx, "p", nil) // data-fui-signal + data-local-seed="drafts:current"
```

The server renders the slice's value. After hydration the runtime reads
the record and patches the signal in place, writes every later value of
the signal back to the record, and mirrors another tab's write in. The
marker rides on the bindings, so a page that never binds the slice never
restores it. `SeedSignal` implies `Global()` and refuses a slice that is
already `store.Persist`-ed: one owner per browser value.

**A persisted value cannot appear at first paint.** IndexedDB is
asynchronous by construction, so SSR always paints the server's value and
the record lands after hydration. A screen that must not flash needs the
value on the request: a `Mirror` collection, below.

### Mirror: tiny values on the request

A collection declared `Mirror: true` keeps each record in a cookie too,
`gofastr.local.<app>.<collection>.<key>`, written by the runtime on every
put and cleared on delete. A Go render reads it with `local.Get` or
`local.List` through the request on the context (`app.WithRequest`, which
the host installs for every screen):

<!-- gofastr:compile
import "context"
import "github.com/DonaldMurillo/gofastr/framework/local"

type View struct{ Compact bool }
var Site = local.New("docs-mirror")
var Prefs = local.Define[View](Site, "prefs", local.CollectionConfig{Version: 1, Mirror: true})
var ctx = context.Background()
-->
```go
view, found, err := local.Get(ctx, Prefs, "view")
_, _, _ = view, found, err
```

The cookie is a client hint the browser wrote: it is validated (declared
and mirrored collection, valid key, size cap, JSON shape into `T`) and
never trusted. It cannot be signed by the server, because the server never
saw the value; treat it as user input. It travels on every request, which
is why the caps are cookie-sized.

### Upload: what accompanies a request

<!-- gofastr:compile
import "context"
import "net/http"
import "github.com/DonaldMurillo/gofastr/core-ui/html"
import "github.com/DonaldMurillo/gofastr/framework/local"
import "github.com/DonaldMurillo/gofastr/framework/ui"

type Draft struct{ Title string }
var Site = local.New("docs-upload")
var Drafts = local.Define[Draft](Site, "drafts", local.CollectionConfig{Version: 1})
var mux = http.NewServeMux()
var ctx = context.Background()
-->
```go
upload := local.Send(Drafts.Key("current")) // or a whole collection: local.Send(Drafts)

form := ui.Form(ui.FormConfig{Action: "/drafts/upload", SubmitLabel: "Upload",
	ExtraAttrs: upload.Merge(html.Attrs{"data-fui-rpc": "/drafts/upload"})})
_ = form

mux.Handle("/drafts/upload", upload.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	draft, found, err := local.Get(r.Context(), Drafts, "current")
	_, _, _ = draft, found, err
}))
```

`Merge` puts three attributes on the RPC trigger: `data-local-store`,
`data-local-send="drafts:current"` and `data-fui-rpc-with="local-bridge"`.
The runtime loads the module before dispatching and its request hook
attaches exactly the named records as the reserved field `__local`
(`{"<collection>": [{"k": key, "v": value}, …]}`) in a JSON body, the form
field `__local` in a form body, or a fresh JSON body when the trigger had
none. A GET trigger carries nothing, and `Merge` panics on one.

`Upload.Wrap` (or `HandlerFunc`) reads the field on the server: the body
is bounded by `Max` (256 KiB by default, 413 past it), a JSON body is
decoded strictly (400 on a duplicate or case-folded key), an undeclared
collection or key is a 400, a record over a cap is a 413, and the field
is stripped so the wrapped handler decodes the body it always did. The
records are then on the context for `Get`, `List` and `FromContext`, and
an upload wins over a mirror cookie for the same key.

### Download: records written from a response

<!-- gofastr:compile
import "net/http"
import "github.com/DonaldMurillo/gofastr/framework/local"

type Draft struct{ Title string }
var Site = local.New("docs-download")
var Drafts = local.Define[Draft](Site, "drafts", local.CollectionConfig{Version: 1})
var w http.ResponseWriter
var r *http.Request
-->
```go
_ = local.Put(w, Drafts, "current", Draft{Title: "saved on the server"})
_ = local.Delete(w, Drafts, "stale")
_ = local.Clear(w, Site)             // logout, on an RPC response
local.ClearOnNextLoad(w, r, Site)     // logout by a full navigation + redirect
```

`Put`, `Delete` and `Clear` accumulate on one `X-Gofastr-Local` header
(ASCII, 16 KiB cap, one store per response); the runtime's response hook
applies each op through the same put/delete a page write uses, so the
caps hold and subscribers hear it. A response that needs more than a few
records is pushing a dataset, which is what a body is for. `ClearOnNextLoad`
plants a script-readable cookie the module honours once on its next load,
for the logout that never reaches `rpc.js`.

## Rules the package keeps

- **No inline scripts.** Both modules are registered behaviours, the
  manifest and any migration function ride the extra-script rail;
  `make csp-check` stays green.
- **State survives soft navigation and the route cache.** The seed
  bridge re-applies the record on every scan, including the one after a
  client navigation whose DOM came back from the cache; a seeded slice is
  app-global, so the signal survives too. Proven in
  `framework/local/local_e2e_test.go`.
- **No server-side memory of browser state.** The server sees a record
  only on the request that carried it; nothing is kept between requests.
- **Multi-tab consistency.** Every write is announced to the origin's
  other tabs, which re-read; the seed bridge mirrors a sibling tab's
  write into its signal.
- **Best-effort.** Private mode, a blocked origin, a full quota and a
  hand-cleared store are all normal; every call settles and says why it
  refused. Never keep something here whose loss is a bug.
- **No secrets, no session tokens.** The store is readable by any script
  on the origin, and a mirrored record travels on every request as a
  cookie. A session is a signed token in an `HttpOnly` cookie
  ([Reactivity](reactivity.md) → Sessions); it never belongs here.
- **The storage-key lint still refuses a raw key.** The module reaches
  storage only through the primitive, whose sinks spell the namespace;
  `core-ui/check` holds the module to the same rules as every embedded
  module.

## The runnable proof

`examples/site` at `/forms/draft-notes`: a draft kept in the browser, a
signal seeded from a record, a mirrored preference the server renders at
first paint, an upload action whose Go handler reads the declared record
and answers with a receipt it also writes back. Browser coverage:
`examples/site/e2e_local_test.go` and `framework/local/local_e2e_test.go`.

## See also

- [Signal store](signal-store.md): `store.Persist`, the one-slice cousin
  with no server bridge.
- [Reactivity](reactivity.md): where local state sits on the ladder.
- [Runtime contract](runtime-contract.md): the `local` primitive and the
  `data-fui-rpc-with` seam.
- [UI capability map](ui-capability-map.md): the state boundary and the
  non-goals.

## Common mistakes

- **Expecting a record at first paint.** The IndexedDB read lands after
  hydration. A value the server must render on the first byte is a
  `Mirror` collection read with `local.Get`, not a seeded signal.
- **Keeping a secret or a session token in the store.** Any script on
  the origin can read it, and a mirrored record rides a cookie on every
  request. Sessions are signed `HttpOnly` cookies; leave them there.
- **Treating an upload or a cookie as trusted.** Both are client hints.
  The package validates shape, size and declaration; the handler still
  authorises and validates the content like any request body.
- **Reading local records in a handler that is not wrapped.** `Get` and
  `List` see the upload only inside `Upload.Wrap`, and the mirror cookie
  only when the request is on the context (the host does this for
  screens; `Wrap` does it for handlers).
- **Sending a collection on a GET.** An upload rides a mutating request;
  `Merge` panics on a GET trigger and the runtime sends nothing on one.
- **Pushing a dataset through the response header.** The header is
  capped at 16 KiB. A large result is a body the page's own script writes
  through the browser API.
- **Raising `Version` without a migration.** Every step from 2 to
  `Version` needs a `Migration`, even an empty one; `Define` panics
  otherwise, so a typo in the version cannot silently orphan data.
- **Persisting the same value twice.** A slice cannot be both
  `store.Persist`-ed and seeded; `SeedSignal` panics on one that is.
