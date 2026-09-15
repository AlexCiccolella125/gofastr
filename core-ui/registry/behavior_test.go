package registry

import (
	"strings"
	"testing"

	"github.com/DonaldMurillo/gofastr/core-ui/style"
)

func mustPanic(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected a panic mentioning %q", want)
		}
		if !strings.Contains(r.(string), want) {
			t.Fatalf("panic %q does not mention %q", r, want)
		}
	}()
	fn()
}

// A behaviour registers under a name that is a URL segment and a
// manifest key, with at least one marker that is an attribute selector
// on a data- attribute. Every rule is a panic with its reason.
func TestRegisterBehaviorRules(t *testing.T) {
	IsolateForTest(t)
	mustPanic(t, "must match", func() { RegisterBehavior("Bad Name", "x", Markers("[data-x]")) })
	mustPanic(t, "must match", func() { RegisterBehavior("", "x", Markers("[data-x]")) })
	mustPanic(t, "must match", func() { RegisterBehavior("../evil", "x", Markers("[data-x]")) })
	mustPanic(t, "source is empty", func() { RegisterBehavior("empty", "  \n", Markers("[data-x]")) })
	mustPanic(t, "no Markers", func() { RegisterBehavior("nomarker", "x") })
	mustPanic(t, "attribute selector", func() { RegisterBehavior("cls", "x", Markers(".a-class")) })
	mustPanic(t, "attribute selector", func() { RegisterBehavior("role", "x", Markers(`[role="tree"]`)) })
	mustPanic(t, "attribute selector", func() { RegisterBehavior("bare", "x", Markers("data-x")) })
	mustPanic(t, "attribute selector", func() { RegisterBehavior("nested", "x", Markers(`[data-x="a]b"]`)) })
	// A value querySelector would throw on: an unescaped newline, a
	// backslash, DEL. One throw in the kernel's scan aborts the boot
	// pass for every module, so these are startup failures.
	mustPanic(t, "attribute selector", func() { RegisterBehavior("newline", "x", Markers("[data-x=\"a\nb\"]")) })
	mustPanic(t, "attribute selector", func() { RegisterBehavior("backslash", "x", Markers(`[data-x="a\"]`)) })
	mustPanic(t, "attribute selector", func() { RegisterBehavior("del", "x", Markers("[data-x=\"a\x7fb\"]")) })
	mustPanic(t, "attribute selector", func() { RegisterBehavior("quote", "x", Markers(`[data-x="a"b"]`)) })
	mustPanic(t, "attribute selector", func() { RegisterBehavior("tab", "x", Markers("[data-x=\"a\tb\"]")) })
	RegisterBehavior("spaced-ok", "x", Markers(`[data-x="a b"]`)) // a space is fine
	// The name is a URL segment and a manifest key, at most 64 bytes:
	// the same bound compute.ValidName holds every module name to.
	RegisterBehavior("a"+strings.Repeat("b", 63), "x", Markers("[data-x]"))
	mustPanic(t, "must match", func() { RegisterBehavior("a"+strings.Repeat("b", 64), "x", Markers("[data-x]")) })
	b := RegisterBehavior("good", "(()=>{})()", Markers("[data-x]", `[data-y="v"]`), LoadIdle())
	if b.Name() != "good" || !b.Entry().Idle || len(b.Entry().Markers) != 2 {
		t.Fatalf("entry not as registered: %+v", b.Entry())
	}
	if b.Entry().SourceHash() == "" || len(b.Entry().SourceHash()) != 16 {
		t.Fatalf("source hash %q", b.Entry().SourceHash())
	}
}

// Identical re-registration is a no-op, as RegisterStyle's is; a
// different definition under the same name panics so a misname
// surfaces at startup.
func TestRegisterBehaviorDuplicates(t *testing.T) {
	IsolateForTest(t)
	a := RegisterBehavior("dup", "(()=>{})()", Markers("[data-x]"))
	b := RegisterBehavior("dup", "(()=>{})()", Markers("[data-x]"))
	if a.Entry() != b.Entry() {
		t.Fatal("identical re-registration did not return the existing entry")
	}
	mustPanic(t, "duplicate name", func() { RegisterBehavior("dup", "(()=>{ /* other */ })()", Markers("[data-x]")) })
	mustPanic(t, "duplicate name", func() { RegisterBehavior("dup", "(()=>{})()", Markers("[data-z]")) })
	mustPanic(t, "duplicate name", func() { RegisterBehavior("dup", "(()=>{})()", Markers("[data-x]"), LoadIdle()) })
}

// A reserved name (an embedded runtime module's) is refused where it
// is written.
func TestRegisterBehaviorRefusesReservedNames(t *testing.T) {
	IsolateForTest(t)
	ReserveBehaviorNames("copy")
	mustPanic(t, "embedded runtime module", func() { RegisterBehavior("copy", "x", Markers("[data-x]")) })
}

// A behaviour registered before the reservation arrives (its package
// initialised first) is refused when the reservation does arrive,
// still at init.
func TestReservationRefusesAnEarlierRegistration(t *testing.T) {
	IsolateForTest(t)
	RegisterBehavior("early", "x", Markers("[data-x]"))
	mustPanic(t, "already registered", func() { ReserveBehaviorNames("other", "early") })
}

// Behaviors is sorted, Lookup finds by name, and isolation hides
// what was registered before and drops what was registered inside.
func TestBehaviorsListingAndIsolation(t *testing.T) {
	IsolateForTest(t)
	RegisterBehavior("zeta", "x", Markers("[data-z]"))
	RegisterBehavior("alpha", "x", Markers("[data-a]"))
	names := []string{}
	for _, e := range Behaviors() {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "alpha,zeta" {
		t.Fatalf("order %v", names)
	}
	if _, ok := LookupBehavior("alpha"); !ok {
		t.Fatal("lookup missed alpha")
	}
	func() {
		IsolateForTest(t)
		if len(Behaviors()) != 0 {
			t.Fatal("nested isolation sees outer registrations")
		}
	}()
}

// A style and a behaviour may share a name: a component registers both
// under its own.
func TestStyleAndBehaviorMayShareAName(t *testing.T) {
	IsolateForTest(t)
	RegisterStyle("shared", func(style.Theme) string { return ".x{}" })
	RegisterBehavior("shared", "x", Markers("[data-shared]"))
	if _, ok := Lookup("shared"); !ok {
		t.Fatal("style lost")
	}
	if _, ok := LookupBehavior("shared"); !ok {
		t.Fatal("behaviour lost")
	}
}

// MarkerSubstring is what the host looks for in rendered HTML.
func TestMarkerSubstring(t *testing.T) {
	for in, want := range map[string]string{
		"[data-x]":       "data-x",
		`[data-x="v w"]`: `data-x="v w"`,
		`[data-x="a&b"]`: `data-x="a&amp;b"`, // as the renderer writes it
		`[data-x="<v>"]`: `data-x="&lt;v&gt;"`,
		".class":         "",
		"data-x":         "",
	} {
		if got := MarkerSubstring(in); got != want {
			t.Errorf("MarkerSubstring(%q) = %q, want %q", in, got, want)
		}
	}
}
