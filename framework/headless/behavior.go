package headless

import (
	_ "embed"

	// Aliased because this package's own spec registry (spec.go) is a
	// package-level var named registry, and the behaviour registry is
	// its own package.
	uiregistry "github.com/DonaldMurillo/gofastr/core-ui/registry"
)

//go:embed behavior.js
var behaviorJS string

// BehaviorName is the runtime module the hooks are bound by. The host
// serves it at /__gofastr/runtime/headless.js and the kernel loads it
// when one of its markers is on the page.
const BehaviorName = "headless"

// behaviorMarkers are the hooks whose presence needs the module: one
// per behaviour, the root hook of each, because the module finds the
// rest from there. The affix shell hook is implied by reveal and
// color, and grow, lines and skeleton-last are read by a stylesheet
// and no script, so none of the four is a marker.
var behaviorMarkers = []string{
	"[data-hui-reveal]", "[data-hui-color]", "[data-hui-when]",
	"[data-hui-form-errors]", "[data-hui-action]", "[data-hui-drop]",
	"[data-hui-system]",
}

// The module that binds the data-hui-* hooks, registered the way a
// component package registers its stylesheet: the JavaScript lives
// beside the Go that renders the markup it binds, and the host serves
// it as the runtime module "headless" (docs/spec-behavior-registry.md,
// sequence step 2). Until this file existed the hooks were a contract
// with nothing written to them: the reveal button was inert, a failed
// submit moved no focus, a drop listed no files. The markers are
// spelled as literals on the call because the runtime's hard-rule-5
// gate reads every registry.Markers call in the tree and refuses an
// argument it cannot read.
var _ = uiregistry.RegisterBehavior(BehaviorName, behaviorJS,
	uiregistry.Markers("[data-hui-reveal]", "[data-hui-color]", "[data-hui-when]",
		"[data-hui-form-errors]", "[data-hui-action]", "[data-hui-drop]",
		"[data-hui-system]"))
