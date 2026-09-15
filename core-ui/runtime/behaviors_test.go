package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DonaldMurillo/gofastr/core-ui/registry"
)

const probeJS = "(() => {\n  // a registered behaviour\n  window.__probe = 1;\n})();\n"

// A registered behaviour is a module from the host down: listed,
// served, hashed, and preloaded like an embedded one.
func TestRegisteredBehaviorIsAModule(t *testing.T) {
	registry.IsolateForTest(t)
	registry.RegisterBehavior("probe-beh", probeJS, registry.Markers("[data-probe]", `[data-probe-kind="x"]`))

	names := ModuleNames()
	found := false
	for _, n := range names {
		if n == "probe-beh" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ModuleNames() lacks the registered behaviour: %v", names)
	}
	src, ok := Module("probe-beh")
	if !ok || !strings.Contains(src, "__probe") {
		t.Fatalf("Module() = %q, %v", src, ok)
	}
	if nominify() {
		if src != probeJS {
			t.Fatalf("with minification off the source must be served as registered")
		}
	} else if strings.Contains(src, "a registered behaviour") {
		t.Fatalf("with minification on the comment must be gone: %q", src)
	}
	h := ModuleHash("probe-beh")
	if len(h) != 16 {
		t.Fatalf("ModuleHash = %q", h)
	}
	if ModuleHash("probe-beh") != h {
		t.Fatal("hash is not stable across calls")
	}
	if ModuleHash("no-such-module") != "" {
		t.Fatal("an unknown name has a hash")
	}
	// Embedded modules keep theirs.
	if ModuleHash("copy") == "" {
		t.Fatal("embedded module lost its hash")
	}
}

// The hash follows the source: a different registration under the same
// name (in a fresh isolation) serves and versions the new bytes.
func TestRegisteredBehaviorHashFollowsSource(t *testing.T) {
	registry.IsolateForTest(t)
	registry.RegisterBehavior("probe-beh", probeJS, registry.Markers("[data-probe]"))
	h1 := ModuleHash("probe-beh")
	// A subtest, so its isolation is restored when it ends, before the
	// assertion below.
	t.Run("different source", func(t *testing.T) {
		registry.IsolateForTest(t)
		registry.RegisterBehavior("probe-beh", probeJS+"window.__probe2 = 2;\n", registry.Markers("[data-probe]"))
		if h2 := ModuleHash("probe-beh"); h2 == h1 {
			t.Fatal("a different source kept the old hash")
		}
		if src, _ := Module("probe-beh"); !strings.Contains(src, "__probe2") {
			t.Fatal("a different source was served from the stale cache")
		}
	})
	if h := ModuleHash("probe-beh"); h != h1 {
		t.Fatal("restoring the registration did not restore its hash")
	}
	if src, _ := Module("probe-beh"); strings.Contains(src, "__probe2") {
		t.Fatal("restoring the registration still serves the other source")
	}
}

// NeededModules preloads a registered behaviour when one of its
// markers appears in the page, at an attribute-name boundary, and
// never as the prefix of a longer attribute.
func TestNeededModulesMatchesRegisteredMarkers(t *testing.T) {
	registry.IsolateForTest(t)
	registry.RegisterBehavior("probe-beh", probeJS, registry.Markers("[data-probe]", `[data-probe-kind="x"]`))
	has := func(page string) bool {
		for _, n := range NeededModules(page) {
			if n == "probe-beh" {
				return true
			}
		}
		return false
	}
	if !has(`<div data-probe="">`) || !has(`<div data-probe>`) || !has(`<div data-probe-kind="x">`) {
		t.Fatal("a present marker was not matched")
	}
	if has(`<div data-probe-other="">`) {
		t.Fatal("a longer attribute matched as the marker")
	}
	if has(`<div data-probe-kind="y">`) {
		t.Fatal("a valued marker matched a different value")
	}
	if has(`<div>`) {
		t.Fatal("matched with no marker present")
	}
}

// The behaviours block carries every registered behaviour's markers
// and idle flag, nothing when none is registered, and escapes the one
// sequence that could end an inline script.
func TestBehaviorsJSON(t *testing.T) {
	registry.IsolateForTest(t)
	if BehaviorsJSON() != nil {
		t.Fatal("an empty registry produced a block")
	}
	registry.RegisterBehavior("b-idle", probeJS, registry.Markers("[data-b]"), registry.LoadIdle())
	registry.RegisterBehavior("a-now", probeJS, registry.Markers("[data-a]", `[data-a-k="</script>"]`))
	var got map[string]struct {
		S []string `json:"s"`
		I bool     `json:"i"`
	}
	if err := json.Unmarshal(BehaviorsJSON(), &got); err != nil {
		t.Fatal(err)
	}
	if !got["b-idle"].I || got["a-now"].I {
		t.Fatalf("idle flags wrong: %+v", got)
	}
	if strings.Join(got["a-now"].S, "|") != `[data-a]|[data-a-k="</script>"]` {
		t.Fatalf("markers wrong: %v", got["a-now"].S)
	}
}

// A behaviour registered under an embedded module's name, past the
// reservation (registered before this package's init could reserve
// it), is refused where the two sets meet rather than served in
// silence.
func TestBehaviorShadowingAnEmbeddedModuleIsRefused(t *testing.T) {
	registry.IsolateForTest(t)
	// Isolation drops the reservation set only for the entries map, not
	// the reserved names; so this exercises the registry's own refusal.
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "embedded runtime module") {
			t.Fatalf("expected the reservation to refuse, got %v", r)
		}
	}()
	registry.RegisterBehavior("copy", probeJS, registry.Markers("[data-copy-probe]"))
}
