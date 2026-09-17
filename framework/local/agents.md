# framework/local — local-first state, declared in Go

A `local.Store` declares, once per app, the collections a browser keeps
for that app: a Go record type, a schema version with migrations, a key
field, a size cap per record and per collection. The browser API is
generated from that declaration and served as three runtime modules,
split by responsibility: `local-store` (the store, the caps, the
collection API), `local-bridge` (every way the store reaches a Go
handler — seed, mirror cookie, upload, download; a store that keeps its
records to itself never loads it) and `local-migrate` (`LoadIdle`: the
version steps, asked for by name when a rewrite is due). All on top of
the kernel's `local` storage primitive (IndexedDB, with a tiny-value
localStorage fallback; no dependency).
Every record lives under `gofastr.state.local.<app>.<collection>:<key>`.

Three rules the package will not bend: a mirror read is a **client
hint** (check the `local.Source`), a mirrored collection costs the
Cookie header on every request (all of a store's share 4 KiB, a panic
at `Define`), and the upload **fails closed** — a request whose declared
records could not be attached is not sent.

**Use this when** the prompt mentions: local-first, browser state,
persisted draft, remember in the browser, teams/preferences without an
account, "keep it on the device", read a browser value on the server,
push a value into the browser after an action, clear on logout. NOT for
offline sync (no queue, no conflicts, no reconciliation), and NEVER for a
secret or a session token.

**Import:** `github.com/DonaldMurillo/gofastr/framework/local`

## Shape

```go
var Site = local.New("site")
var Drafts = local.Define[Draft](Site, "drafts", local.CollectionConfig{
    Version: 2, KeyField: "id", MaxRecordBytes: 32 << 10, MaxRecords: 200,
    Migrations: []local.Migration{{Version: 2, Steps: []local.Step{local.Rename("body", "text")}}},
})
var Prefs = local.Define[Prefs](Site, "prefs", local.CollectionConfig{Version: 1, Mirror: true})

// Serve the declaration on the extra-script rail (once, in main).
// Serve mounts the route AND returns the URL: doing only one half is
// a silent 404, an undefined window.__gofastr_local and a null store.
host := uihost.New(site, uihost.WithExtraScripts(Site.Serve(router)))

// Seed a signal from a record; the runtime patches it in after hydration.
title := local.SeedSignal(Drafts, "current", store.JSON[Draft](S, "draft", Draft{}))
title.Bind(ctx, "p", nil)

// Upload: the trigger declares what rides along; the handler reads it.
up := local.Send(Drafts.Key("current"))
form := render.Tag("form", up.Merge(map[string]string{"data-fui-rpc": "/drafts/upload"}), …)
router.Post("/drafts/upload", up.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // src is local.SourceUpload, SourceMirror or SourceNone. A mirror
    // read is a CLIENT HINT — any script on the origin writes that
    // cookie and any client forges it — so never authorise on one.
    d, src, err := local.Get(r.Context(), Drafts, "current")
    p, _, _ := local.Get(r.Context(), Prefs, "theme") // mirrored: also at first paint
    local.Put(w, Drafts, "current", d)                 // download: write back
}))
local.Clear(w, Site)                 // logout over RPC
local.ClearOnNextLoad(w, r, Site)    // logout by full navigation
```

Browser side (after `__gofastr.loadModule('local-store')`):
`__gofastr.localStore('site').collection('drafts')` → `get`, `put`,
`delete`, `list({where, orderBy, desc, limit, offset})`, `count`,
`subscribe`, `clear`, `available`. Every call settles `{ok, reason}`;
refusals raise `gofastr:local-error`. A collection whose migration did
not complete refuses every call with reason `migration` rather than
serving records on a schema this build cannot read.

Docs: `gofastr docs local-state`.
