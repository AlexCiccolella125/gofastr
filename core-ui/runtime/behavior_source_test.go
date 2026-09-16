package runtime

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DonaldMurillo/gofastr/core-ui/check"
	"github.com/DonaldMurillo/gofastr/core-ui/runtime/minify"
)

// Source-level contract gates for registered behaviours, over the
// packages that own them rather than the registrations this test binary
// happens to link (docs/spec-behavior-registry.md "The module
// contract"). framework/ui's behavior_test.go covers the two action
// adapters through the registry; this one walks the tree, so a module
// registered by a package core-ui/runtime cannot import is held to the
// same rule.

// loadedFlagAssign matches the loadedModules ASSIGNMENT (an equals
// sign follows the identifier), never the identifier alone: every
// module's early-return guard READS loadedModules before any listener,
// so a bare identifier match is satisfied by the guard and holds
// nothing — the first mutation run of this gate proved that the hard
// way (flag moved to the end of toggleaction.js, gate still green).
var loadedFlagAssign = regexp.MustCompile(`loadedModules\s*=`)

// sourceSkipPrefixes names the tree the gate does not hold yet, with
// the reason on each entry. Every entry is a debt: when the file
// moves onto the contract, the entry goes, and an empty list deletes
// this variable.
//
//   - framework/headless/behavior.js sets its flag at the END today
//     (after scan(document)); every install before it is once()-
//     guarded, so a retry cannot double-install. The headless fixes
//     in this same change move it onto the contract.
var sourceSkipPrefixes = []string{"framework/headless/"}

// A registered module sets its loadedModules flag before it installs
// anything: the loader resolves on registration, and a script that
// failed halfway with its flag unset has its cached promise dropped, so
// a later loadModule re-executes the file and installs every listener
// of the first pass a second time. The check runs on the minified
// source so a comment naming either token cannot satisfy it.
func TestRegisteredBehaviorsSetLoadedFlagBeforeInstalling(t *testing.T) {
	files, err := check.RegisteredBehaviorSources(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("registered behaviours: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no registered behaviour sources found under the repo root: the walk is broken, not the tree empty")
	}
	held := 0
	for _, f := range files {
		// The walk returns paths relative to this package's directory
		// (../../examples/...); strip the walk root so the skip
		// prefixes compare against tree-shaped paths.
		rel := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(f)), "../../")
		skip := false
		for _, p := range sourceSkipPrefixes {
			if strings.HasPrefix(rel, p) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		held++
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		src := string(raw)
		if !nominify() {
			src = minify.Minify(src)
		}
		am := loadedFlagAssign.FindStringIndex(src)
		if am == nil {
			t.Errorf("%s never sets its loaded flag: the kernel cannot tell it is armed", rel)
			continue
		}
		if inst := strings.Index(src, "addEventListener"); inst != -1 && inst < am[0] {
			t.Errorf("%s installs a listener before setting its loadedModules flag: a retry re-executes the file and would install it twice", rel)
		}
	}
	if held == 0 {
		t.Fatal("every registered source was skipped: the gate holds nothing")
	}
}
