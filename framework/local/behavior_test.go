package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DonaldMurillo/gofastr/core-ui/check"
	"github.com/DonaldMurillo/gofastr/core-ui/registry"
	"github.com/DonaldMurillo/gofastr/core-ui/runtime"
)

// The registration is live: the host serves the module, the loader
// knows its requirement, and the marker is the one this package renders.
func TestBehaviorIsRegisteredAndServed(t *testing.T) {
	e, ok := registry.LookupBehavior(BehaviorName)
	if !ok {
		t.Fatalf("%s is not registered", BehaviorName)
	}
	if len(e.Markers) != 1 || e.Markers[0] != "[data-local-store]" {
		t.Fatalf("markers = %v", e.Markers)
	}
	if len(e.Requires) != 1 || e.Requires[0] != "local" {
		t.Fatalf("requires = %v, want the local primitive", e.Requires)
	}
	for _, name := range []string{BehaviorName, BridgeName, MigrateName} {
		if _, ok := runtime.Module(name); !ok {
			t.Fatalf("runtime.Module(%q) does not serve the registered source", name)
		}
	}
}

// The module is held to the JavaScript lints every embedded module is
// held to — and those lints still refuse a raw storage key. The clean
// half runs over this package's own source; the refusing half runs
// over a mutated copy, so the guard is watched failing, not reasoned
// about.
func TestStorageKeyLintReachesTheModuleAndStillRefusesARawKey(t *testing.T) {
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for name, lint := range map[string]func(...string) (*check.Result, error){
		"storage-key-raw":   check.LintStorageKeyRaw,
		"cookie-concat":     check.LintCookieConcat,
		"proto-key-write":   check.LintProtoKeyWrite,
		"registry-own-prop": check.LintRegistryOwnProps,
		"decode-uri-raw":    check.LintDecodeURIRaw,
	} {
		res, err := lint(here)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.HasErrors() {
			t.Errorf("%s over local-store.js:\n%s", name, res.Error())
		}
	}

	// Mutations: a key from a marker attribute reaches localStorage raw
	// (the raw arm), the cookie namespace is dropped in favour of an
	// application-chosen prefix (the namespace arm), and the mirror
	// cookie concatenates the key unencoded (the cookie lint).
	dir := t.TempDir()
	src := localStoreJS
	mutated := strings.Replace(src,
		"const wire = (el) => {",
		"const wire = (el) => {\n    localStorage.setItem(el.getAttribute('data-fui-signal'), '1');\n    localStorage.setItem('local.' + encodeURIComponent(el.getAttribute('data-local-seed')), '1');",
		1)
	if mutated == src {
		t.Fatal("the mutation did not apply: the anchors moved")
	}
	if err := os.WriteFile(filepath.Join(dir, "local-store.js"), []byte(mutated), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := check.LintStorageKeyRaw(dir)
	if err != nil {
		t.Fatal(err)
	}
	msg := res.Error()
	if !strings.Contains(msg, "raw") && !strings.Contains(msg, "storage-key-raw") {
		t.Fatalf("the storage-key lint let a raw attribute-borne key through:\n%s", msg)
	}
	if !strings.Contains(msg, "not the framework's") {
		t.Fatalf("the storage-key lint let a foreign namespace through:\n%s", msg)
	}
	// The cookies moved to local-bridge.js with the rest of the mirror.
	bridgeDir := t.TempDir()
	bridge := strings.Replace(localBridgeJS,
		"document.cookie = 'gofastr.local.' + encodeURIComponent(app + '.' + coll + '.' + key) + '=' + encodeURIComponent(text) + '; path=/; max-age=31536000; SameSite=Lax; Secure';",
		"document.cookie = 'gofastr.local.' + key + '=' + encodeURIComponent(text) + '; path=/; max-age=31536000; SameSite=Lax; Secure';",
		1)
	if bridge == localBridgeJS {
		t.Fatal("the cookie mutation did not apply: the anchor moved")
	}
	if err := os.WriteFile(filepath.Join(bridgeDir, "local-bridge.js"), []byte(bridge), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = check.LintCookieConcat(bridgeDir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasErrors() {
		t.Fatal("the cookie lint let an unencoded mirror key through")
	}
}
