package local

import (
	_ "embed"

	"github.com/DonaldMurillo/gofastr/core-ui/registry"
)

//go:embed local-store.js
var localStoreJS string

// BehaviorName is the runtime module that binds this package's
// data-local-* markers and exposes window.__gofastr.localStore(app).
// The host serves it at /__gofastr/runtime/local-store.js and the
// kernel loads it when a marker is on the page, or an RPC trigger
// names it in data-fui-rpc-with, or a script asks for it with
// __gofastr.loadModule('local-store').
const BehaviorName = "local-store"

// The marker: every element this package renders carries
// data-local-store="<app>", and the seed or send attribute beside it
// says what to do. Spelled as a literal because the runtime's
// hard-rule-5 gate reads every registry.Markers call in the tree.
//
// Requires("local"): the storage primitive is registered before this
// module evaluates, so it can call window.__gofastr.local without a
// guard for a primitive still in flight.
var _ = registry.RegisterBehavior(BehaviorName, localStoreJS,
	registry.Markers(`[data-local-store]`),
	registry.Requires("local"))
