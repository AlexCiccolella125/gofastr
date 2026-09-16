package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/DonaldMurillo/gofastr/core/render"
)

// Behaviour registers like style.
//
// A component's stylesheet is registered by the package that renders
// its markup (RegisterStyle), listed in the catalog the host puts in
// <head>, and loaded by the runtime when its marker appears. Its
// behaviour now takes the same seam: RegisterBehavior in the same
// package, the JavaScript embedded beside the Go, served by the host
// at /__gofastr/runtime/<name>.js like any runtime module, listed in
// the module manifest the kernel already reads, and scanned for by the
// markers it declares. The kernel treats a registered module and an
// embedded one identically after the fetch. See
// docs/spec-behavior-registry.md.
//
//	//go:embed runtime.js
//	var runtimeJS string
//
//	var Behavior = registry.RegisterBehavior("headless", runtimeJS,
//	    registry.Markers("[data-hui-reveal]", "[data-hui-when]"))
//
// This package stores the source as registered. Minification, hashing
// and serving happen in core-ui/runtime, where the embedded modules'
// do, so the two kinds of module are one kind from the host down.

// BehaviorEntry is one registered behaviour.
type BehaviorEntry struct {
	// Name is the module name: the URL is /__gofastr/runtime/<Name>.js
	// and the manifest key is Name. One namespace with the embedded
	// kernel modules; a style may share the name.
	Name string
	// Source is the JavaScript as registered.
	Source string
	// Markers are the attribute selectors the kernel scans for, each
	// "[data-x]" or "[data-x=\"v\"]".
	Markers []string
	// Idle defers the load to idle time after first paint, as the
	// kernel's `idle: true` modules do.
	Idle bool
	// Requires are the modules loaded before this one, in the order
	// they were named, deduplicated. They may be embedded kernel
	// modules or other registered behaviours.
	Requires []string

	sourceHash string
}

// SourceHash is the SHA-256 of the registered source, eight bytes as
// hex. The served version hash is computed over the served (possibly
// minified) bytes in core-ui/runtime; this one identifies the
// registration, for duplicate detection and caches keyed on it.
func (e *BehaviorEntry) SourceHash() string { return e.sourceHash }

// BehaviorOption configures a BehaviorEntry at registration time.
type BehaviorOption func(*BehaviorEntry)

// Markers declares the attribute selectors whose presence loads the
// module. At least one is required. Each is "[data-x]" or
// "[data-x=\"v\"]": an attribute selector on a data- attribute, nothing
// else, so the host can also match it in rendered HTML for preload.
func Markers(selectors ...string) BehaviorOption {
	return func(e *BehaviorEntry) { e.Markers = append(e.Markers, selectors...) }
}

// LoadIdle defers the load to idle time after first paint. The
// default loads as soon as the marker is seen.
func LoadIdle() BehaviorOption { return func(e *BehaviorEntry) { e.Idle = true } }

// Requires names the modules that must be loaded and registered
// before this one: the action adapters need the action primitive, and
// a dependency declared here is a dependency the loader honors on
// every path that loads the module (marker scan, idle queue, hover
// prefetch; the interaction bridge reads only the kernel's own table
// today). A name is an embedded kernel module or another registered
// behaviour; the manifest carries it and core-ui/runtime refuses a
// name that is neither, or a cycle, when it builds the block — at the
// first render that needs the manifest, not at startup, because the
// registry is only complete once every package's init has run. A
// module may not require itself.
func Requires(names ...string) BehaviorOption {
	return func(e *BehaviorEntry) { e.Requires = append(e.Requires, names...) }
}

// Behavior is the handle RegisterBehavior returns. Authors keep it in a
// package var; nothing on it is needed at render time, because the
// marker in the markup is what loads the module.
type Behavior struct{ e *BehaviorEntry }

// Name returns the registered name.
func (b *Behavior) Name() string { return b.e.Name }

// Entry returns the underlying entry.
func (b *Behavior) Entry() *BehaviorEntry { return b.e }

var (
	behaviors = map[string]*BehaviorEntry{}
	// reservedBehaviorNames are the embedded kernel modules' names, set
	// by core-ui/runtime at init so a registration that would shadow
	// one fails where it is written rather than where it is served.
	reservedBehaviorNames = map[string]bool{}
)

var (
	behaviorName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	// The value may not carry a control character (a tab included),
	// DEL, a backslash, a quote or a bracket: a CSS string with an
	// unescaped newline or a trailing backslash is not a selector,
	// querySelector throws on it, and one throw in the kernel's scan
	// aborts the boot pass for every module. Refused here, where it is
	// a startup failure.
	behaviorMarker = regexp.MustCompile(`^\[(data-[a-z0-9-]+)(="[^"\]\\\x00-\x1f\x7f]*")?\]$`)
)

// ReserveBehaviorNames records names no behaviour may register under:
// the embedded runtime modules, which share the URL and the manifest.
// core-ui/runtime calls it at init; a name registered before that is
// checked again where the two sets merge.
func ReserveBehaviorNames(names ...string) {
	mu.Lock()
	defer mu.Unlock()
	for _, n := range names {
		reservedBehaviorNames[n] = true
		// A package that imports registry but not runtime can register
		// before the reservation arrives; the collision is refused
		// here, at init, rather than where the two sets are served.
		if _, taken := behaviors[n]; taken {
			panic("registry.ReserveBehaviorNames: behaviour " + n + " is already registered under an embedded runtime module's name — the two share one URL and one manifest")
		}
	}
}

// RegisterBehavior registers a component's runtime module under a
// process-wide unique name and returns a handle. Identical
// re-registration (same source and options) is a no-op; any other
// duplicate panics so misnames surface at startup, as RegisterStyle's
// do. Every rule below is a panic with its reason: a mistake here is a
// startup failure, never a dead marker in production.
func RegisterBehavior(name, js string, opts ...BehaviorOption) *Behavior {
	if !behaviorName.MatchString(name) {
		panic(fmt.Sprintf("registry.RegisterBehavior: name %q must match ^[a-z][a-z0-9-]{0,63}$ — it is a URL segment and a manifest key", name))
	}
	if strings.TrimSpace(js) == "" {
		panic("registry.RegisterBehavior(" + name + "): the source is empty — a module that binds nothing")
	}
	e := &BehaviorEntry{Name: name, Source: js}
	for _, o := range opts {
		o(e)
	}
	if len(e.Markers) == 0 {
		panic("registry.RegisterBehavior(" + name + "): no Markers — the kernel loads a behaviour when a marker appears, and this one would never load")
	}
	for _, m := range e.Markers {
		if !behaviorMarker.MatchString(m) {
			panic(fmt.Sprintf("registry.RegisterBehavior(%s): marker %q must be an attribute selector on a data- attribute, [data-x] or [data-x=\"v\"]", name, m))
		}
	}
	// A requirement is a module name: the same shape rule the
	// behaviour's own name keeps, because it names the same URL shape.
	// A self-requirement is a cycle of one and is refused here rather
	// than by the graph check, where its path would read as noise.
	// Repeats collapse, keeping the first position.
	deduped := e.Requires[:0]
	for _, r := range e.Requires {
		if r == name {
			panic("registry.RegisterBehavior(" + name + "): a behaviour cannot require itself — the loader would wait on its own registration")
		}
		if !behaviorName.MatchString(r) {
			panic(fmt.Sprintf("registry.RegisterBehavior(%s): requirement %q must match ^[a-z][a-z0-9-]{0,63}$ — it names a runtime module", name, r))
		}
		if !slices.Contains(deduped, r) {
			deduped = append(deduped, r)
		}
	}
	e.Requires = deduped
	sum := sha256.Sum256([]byte(js))
	e.sourceHash = hex.EncodeToString(sum[:8])

	mu.Lock()
	defer mu.Unlock()
	if reservedBehaviorNames[name] {
		panic("registry.RegisterBehavior(" + name + "): the name belongs to an embedded runtime module — the two share one URL and one manifest")
	}
	if existing, ok := behaviors[name]; ok {
		if !sameBehavior(existing, e) {
			panic(fmt.Sprintf("registry.RegisterBehavior: duplicate name %q with a different definition. Pick a unique name in one of the two call sites\n"+
				"  existing: source=%s markers=%v idle=%v requires=%v\n"+
				"  new:      source=%s markers=%v idle=%v requires=%v",
				name, existing.sourceHash, existing.Markers, existing.Idle, existing.Requires, e.sourceHash, e.Markers, e.Idle, e.Requires))
		}
		return &Behavior{e: existing}
	}
	behaviors[name] = e
	return &Behavior{e: e}
}

// LookupBehavior returns the behaviour registered under name.
func LookupBehavior(name string) (*BehaviorEntry, bool) {
	mu.Lock()
	defer mu.Unlock()
	e, ok := behaviors[name]
	return e, ok
}

// Behaviors returns a snapshot of every registered behaviour, sorted
// by name.
func Behaviors() []*BehaviorEntry {
	mu.Lock()
	defer mu.Unlock()
	return slices.SortedFunc(maps.Values(behaviors), func(a, b *BehaviorEntry) int {
		return strings.Compare(a.Name, b.Name)
	})
}

// MarkerSubstring is what a marker selector looks like inside rendered
// HTML, for the host's preload scan: "[data-x]" is the attribute name
// data-x, "[data-x=\"v\"]" is data-x="v" with the value escaped the way
// the renderer escapes an attribute value (& as &amp; and so on), since
// the scan runs over rendered HTML. The host matches it at an
// attribute-name boundary, so a marker never fires as the prefix of a
// longer attribute.
func MarkerSubstring(selector string) string {
	m := behaviorMarker.FindStringSubmatch(selector)
	if m == nil {
		return ""
	}
	if m[2] == "" {
		return m[1]
	}
	value := m[2][2 : len(m[2])-1] // strip ="…"
	return m[1] + `="` + render.Escape(value) + `"`
}

func sameBehavior(a, b *BehaviorEntry) bool {
	return a.Name == b.Name && a.sourceHash == b.sourceHash && a.Idle == b.Idle && slices.Equal(a.Markers, b.Markers) && slices.Equal(a.Requires, b.Requires)
}
