package headless

// The component harness: what each component IS, declared once, in
// data, next to nothing else.
//
// Until now every component was checked by four things written by
// hand and separately: a contract test, a stylesheet test, a golden,
// and a specimen in a gallery page. Four places to remember, so a component
// added in a hurry got one or two of them, and the ones it got were
// the ones whose absence nobody notices — a missing golden is silent.
//
// A Spec is the fixture, once. It says what the component is called,
// which parts it draws, which of those a caller may fill, and what
// runtime hooks it publishes; and it renders itself at any skin, so
// the SAME fixture drives the nil-skin contract sweep, the goldens,
// and the seam tests below. A component with a spec cannot be half
// tested, and one without a spec fails the build.
//
// What a Spec deliberately does not carry is the component's own
// assertions — that a dialog is named by its title, that a wizard's
// Back does not validate. Those are specific, they read as prose, and
// they belong in the contract test where a reviewer will find them.
// The harness is for what must hold for EVERYTHING.

import (
	"sort"

	"github.com/DonaldMurillo/gofastr/core/render"
)

// Case is one rendering of a component, with a reason for existing.
// The reason is not documentation: a case with nothing to show is a
// case that will be updated to match whatever the code does next.
type Case struct {
	Name string
	Why  string
	HTML render.HTML
}

// Spec describes one component and how to render it.
type Spec struct {
	// Name is the exported function's name, exactly. The coverage
	// gate matches on it.
	Name string
	// Parts are the parts this component draws. A skin styles these
	// and only these; a part listed here and never rendered is a
	// class in the stylesheet with nothing to land on.
	Parts []Part
	// Fillable are the parts a caller may replace through Slots. The
	// empty set is the correct answer for most components: a seam is
	// offered where the component's own content carries no guarantee.
	Fillable []Part
	// Hooks are the data-ds-* attributes this component publishes for
	// the runtime. Naming them here is what lets a test prove the
	// runtime is not bound to an attribute nothing renders.
	Hooks []string
	// WithSeams renders the component with caller slots and overrides
	// applied. A component that offers seams must provide it: it is
	// how the harness proves that filling a slot or adding an
	// attribute cannot break the contract, and a seam nothing tests
	// is a seam that will.
	WithSeams func(s Skin, seams Seams) render.HTML
	// Cases renders the component at a given skin. Nil skin means
	// unstyled, which is what the contract is asserted against.
	Cases func(k Kit) []Case
}

// Kit is what a fixture is handed: this component's skin, and a way to
// reach any other component's.
//
// It exists because a fixture composes. A Form fixture needs a Button
// and an Input inside it, and before this existed there was only one
// skin in scope — the Form's — so every fixture either passed the
// parent's skin to the child, which dresses an <input> in .ds-form and
// leaves it otherwise naked, or gave up and wrote the child as a raw
// HTML string, which no part check, no golden and no audit can see.
// Thirty-one components did one or the other.
//
// The lookup is a function rather than a map because the skins live in
// the skin package, which imports this one. Inverting that would put
// class names in the headless half, and the whole split is that they
// are not there.
type Kit struct {
	// Skin is this component's own skin.
	Skin Skin
	// of resolves another component's skin by name and variant. Nil
	// means every lookup is unstyled, which is what the contract
	// suite wants and what a caller who supplies nothing gets.
	of func(component, variant string) Skin
}

// For is the skin a child component should wear. The name is the
// component's, exactly as it is registered.
func (k Kit) For(component string) Skin { return k.Variant(component, "") }

// Variant is For, for a component whose skin depends on a tone or
// kind: an alert is danger or info, a badge neutral or warning. The
// catalogue drew every alert in the same blue until this existed,
// because one component mapped to one skin and a tone could not be
// asked for.
func (k Kit) Variant(component, variant string) Skin {
	if k.of == nil {
		return nil
	}
	return k.of(component, variant)
}

// NewKit builds a Kit with a resolver. The skin package calls this;
// the contract suite passes nil and gets an unstyled system.
func NewKit(own Skin, of func(component, variant string) Skin) Kit {
	return Kit{Skin: own, of: of}
}

var registry = map[string]Spec{}

// Register adds a component to the harness. Called from each
// component's own file, so the fixture lives beside the thing it
// describes and moves when it moves.
func Register(sp Spec) {
	if sp.Name == "" {
		panic("headless: a spec with no name")
	}
	if _, dup := registry[sp.Name]; dup {
		panic("headless: two specs named " + sp.Name)
	}
	registry[sp.Name] = sp
}

// Specs returns every registered spec, in name order so that anything
// built from them — a test's output, a gallery page — is stable.
func Specs() []Spec {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Spec, 0, len(names))
	for _, n := range names {
		out = append(out, registry[n])
	}
	return out
}

// SpecOf returns one spec by component name.
func SpecOf(name string) (Spec, bool) {
	sp, ok := registry[name]
	return sp, ok
}

// one is the common shape: a component with a single case worth
// rendering.
func one(html render.HTML, why string) []Case {
	return []Case{{Name: "default", Why: why, HTML: html}}
}
