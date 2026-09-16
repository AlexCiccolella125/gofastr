package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/DonaldMurillo/gofastr/core-ui/registry"
)

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
