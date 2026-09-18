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

The bridges are **HTTP-shaped** — the upload is a request-body field,
the download a response header — so a WebSocket app uploads through an
action. The mirror is for a few small preferences (4 KiB of Cookie
header for all of a store's mirrored collections); a large record is
read at action time through the upload, or painted after hydration by a
seed.

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

This block compiles: `go test ./framework/docs -run TestDocExamplesCompile`
builds it along with the guides' snippets, so it cannot rot.

<!-- gofastr:compile
import "context"
import "net/http"
import "github.com/DonaldMurillo/gofastr/core-ui/app"
import "github.com/DonaldMurillo/gofastr/core-ui/store"
import "github.com/DonaldMurillo/gofastr/core/render"
import "github.com/DonaldMurillo/gofastr/core/router"
import "github.com/DonaldMurillo/gofastr/framework/local"
import "github.com/DonaldMurillo/gofastr/framework/uihost"

type Draft struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}
type View struct {
	Compact bool `json:"compact"`
}

var site = app.NewApp("site")
var rt = router.New()
var S = store.New("editor")
-->
```go
var Site = local.New("site")
var Drafts = local.Define[Draft](Site, "drafts", local.CollectionConfig{
	Version: 2, KeyField: "id", MaxRecordBytes: 32 << 10, MaxRecords: 200,
	Migrations: []local.Migration{{Version: 2, Steps: []local.Step{local.Rename("body", "text")}}},
})
var Prefs = local.Define[View](Site, "prefs", local.CollectionConfig{Version: 1, Mirror: true})

// Serve the declaration on the extra-script rail (once, in main).
// Script returns the URL for the rail AND the mount step; doing one
// half is a silent 404, an undefined window.__gofastr_local and a null
// store in the browser. The host is usually built before its router,
// so the URL goes on first and mount(app.Router()) runs later.
var scriptURL, mount = Site.Script()
var host = uihost.New(site, uihost.WithExtraScripts(scriptURL))

// Seed a signal from a record; the runtime patches it in after
// hydration. A page script writes it with setSignal, reading the name
// off the bound element's data-fui-signal.
var title = local.SeedSignal(Drafts, "current", store.JSON[Draft](S, "draft", Draft{}))

// Upload: the trigger declares what rides along; the handler reads it.
var up = local.Send(Drafts.Key("current")) // or local.Send(Drafts)

func screen(ctx context.Context) render.HTML {
	return title.Bind(ctx, "p", nil) // + up.Merge(...) on the RPC trigger
}

func routes() {
	mount(rt)
	rt.Post("/drafts/upload", up.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// src is local.SourceUpload, SourceMirror or SourceNone. A mirror
		// read is a CLIENT HINT — any script on the origin writes that
		// cookie and any client forges it — so never authorise on one.
		d, src, err := local.Get(r.Context(), Drafts, "current")
		if err != nil || src != local.SourceUpload {
			http.Error(w, "no draft arrived", http.StatusBadRequest)
			return
		}
		p, _, _ := local.Get(r.Context(), Prefs, "view") // mirrored: also at first paint
		_ = p
		local.Put(w, Drafts, "current", d) // download: write back
		local.Clear(w, Site)               // logout over RPC
		local.ClearOnNextLoad(w, r, Site)  // logout by full navigation
	}))
}
```

Browser side (after `__gofastr.loadModule('local-store')`):
`__gofastr.localStore('site').collection('drafts')` → `get`, `put`,
`delete`, `list({orderBy, desc})`, `count`, `subscribe`, `clear`. Every
call settles `{ok, reason}`; refusals raise `gofastr:local-error`. A
collection whose migration did not complete refuses every call with
reason `migration` rather than serving records on a schema this build
cannot read.

Docs: `gofastr docs local-state`.
