package ui

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/DonaldMurillo/gofastr/core-ui/registry"
	"github.com/DonaldMurillo/gofastr/core-ui/runtime/minify"
)

// loadedFlagAssign matches the loadedModules ASSIGNMENT (an equals
// sign follows the identifier), never the identifier alone: the
// adapters' early-return guard READS loadedModules before any
// listener, so a bare identifier match is satisfied by the guard and
// holds nothing.
// It matches the per-module WRITE (`loadedModules[NAME] = true`,
// `(… loadedModules … {}).name = true`, minified `!0` included), not
// the table initialisation `loadedModules = loadedModules || {}`,
// which a module may put at the top while its own flag stays at the
// end; the fixture test below proves the difference.
var loadedFlagAssign = regexp.MustCompile(`loadedModules[^;]*?(\[[^\]]*\]|\.[A-Za-z_$][\w$]*)\s*=\s*(true|!0)`)

// flagBeforeInstall reports whether src writes its loaded flag before
// its first addEventListener; "" when it does, otherwise the reason.
func flagBeforeInstall(src string) string {
	am := loadedFlagAssign.FindStringIndex(src)
	if am == nil {
		return "never sets its loaded flag"
	}
	if inst := strings.Index(src, "addEventListener"); inst != -1 && inst < am[0] {
		return "installs a listener before setting its loaded flag"
	}
	return ""
}

// The gate's own fixtures: an init of the table at the top with the
// flag at the end must fail, the flag first must pass, and a
// minified `!0` counts as true.
func TestFlagBeforeInstallReadsTheWriteNotTheInit(t *testing.T) {
	cases := []struct {
		name, src string
		ok        bool
	}{
		{"init at top, flag at end", `NS.loadedModules = NS.loadedModules || {}; document.addEventListener('click', f); NS.loadedModules[NAME] = true;`, false},
		{"flag first", `NS.loadedModules = NS.loadedModules || {}; NS.loadedModules[NAME] = true; document.addEventListener('click', f);`, true},
		{"one-liner adapter form", `(NS.loadedModules = NS.loadedModules || {})[NAME] = true; document.addEventListener('click', f);`, true},
		{"kernel dotted form, minified", `(window.__gofastr.loadedModules||={}).reveal=!0;document.addEventListener("click",f)`, true},
		{"no flag at all", `document.addEventListener('click', f);`, false},
	}
	for _, c := range cases {
		if got := flagBeforeInstall(c.src) == ""; got != c.ok {
			t.Errorf("%s: pass=%v, want %v", c.name, got, c.ok)
		}
	}
}

// The two action adapters are registered behaviours on the kernel's
// action primitive: the registration must carry Requires("action") —
// without it the module evaluates with no primitive on the page, its
// first bind throws, and it never registers — and the source must bind
// through window.__gofastr.action rather than a machine of its own.
func TestActionAdaptersRequireThePrimitive(t *testing.T) {
	for name := range map[string]struct{}{"optimisticaction": {}, "toggleaction": {}} {
		e, ok := registry.LookupBehavior(name)
		if !ok {
			t.Fatalf("%s is not registered: the component's module is not on the page", name)
		}
		if !slices.Equal(e.Requires, []string{"action"}) {
			t.Errorf("%s requires %v, want exactly [action]: the primitive must be registered before the adapter evaluates", name, e.Requires)
		}
		if !strings.Contains(e.Source, "action.bind") {
			t.Errorf("%s does not bind through window.__gofastr.action: the machine is the primitive's, written once", name)
		}
		if !strings.Contains(e.Source, "loadedModules") {
			t.Errorf("%s never sets its loaded flag: the kernel cannot tell it is armed, so inserted markup is never handed to it", name)
		}
	}
}

// The loaded flag is set before the module installs anything: the
// loader resolves on registration, and a script that fails halfway (a
// throw after the first listener) with its flag still unset rejects
// its load, drops the cached promise, and a later loadModule
// re-executes the whole file — installing every listener a second
// time. With the flag first, the retry stops at the early-return
// guard. The check runs on the minified source so a comment naming
// either token cannot satisfy it, and it matches the ASSIGNMENT
// (loadedModules followed by =), never the identifier alone: the
// early-return guard reads loadedModules before any listener, so a
// bare identifier match is satisfied by the guard and holds nothing.
// Contract: core-ui/ARCHITECTURE.md "Component behaviour".
func TestActionAdaptersSetLoadedFlagBeforeInstalling(t *testing.T) {
	for _, name := range []string{"optimisticaction", "toggleaction"} {
		e, ok := registry.LookupBehavior(name)
		if !ok {
			t.Fatalf("%s is not registered: the component's module is not on the page", name)
		}
		src := minify.Minify(e.Source)
		if why := flagBeforeInstall(src); why != "" {
			t.Errorf("%s %s: a retry re-executes the file and would install it twice", name, why)
		}
	}
}
