package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The enumerator is what makes every clean-tree lint reach a registered
// behaviour. These tests catch it finding nothing: an enumerator that
// silently returns an empty list turns every widened lint back into the
// runtime-only walk it replaced.

// TestRegisteredBehaviorSources_FindsTheTreesModules pins the two
// registrations the tree carries today, by path, so a refactor that
// moves a module or renames its directive cannot slip out of the walk.
func TestRegisteredBehaviorSources_FindsTheTreesModules(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Skipf("can't locate repo root: %v", err)
	}
	files, err := RegisteredBehaviorSources(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	rel := map[string]bool{}
	for _, f := range files {
		r, err := filepath.Rel(repoRoot, f)
		if err != nil {
			t.Fatal(err)
		}
		rel[filepath.ToSlash(r)] = true
	}
	for _, want := range []string{
		"framework/headless/behavior.js",
		"examples/site/behavior_ping.js",
	} {
		if !rel[want] {
			t.Errorf("%s is a registered behaviour and the enumerator did not find it; found %v", want, files)
		}
	}
}

// TestRegisteredBehaviorSources_ReadsTheDirectiveNotTheDirectory pins
// the shape in a scratch tree: a Go file that registers and embeds is
// found; a Go file that only embeds is not (an asset is not a module);
// a test file is never read; a directive naming a missing file is an
// error, not an empty result.
func TestRegisteredBehaviorSources_ReadsTheDirectiveNotTheDirectory(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a/beh.go", "package a\n\n//go:embed beh.js\nvar js string\n\nvar _ = registry.RegisterBehavior(\"a\", js, registry.Markers(\"[data-a]\"))\n")
	write("a/beh.js", "(function () { 'use strict'; })();\n")
	write("b/asset.go", "package b\n\n//go:embed asset.js\nvar js string\n")
	write("b/asset.js", "var legacy = 1;\n")
	write("c/beh_test.go", "package c\n\n//go:embed probe.js\nvar js string\n\nvar _ = registry.RegisterBehavior(\"c\", js)\n")
	write("c/probe.js", "var legacy = 1;\n")

	files, err := RegisteredBehaviorSources(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || !strings.HasSuffix(files[0], filepath.Join("a", "beh.js")) {
		t.Fatalf("found %v, want only a/beh.js: an embed without a registration is an asset, and a test file is not read", files)
	}

	write("d/beh.go", "package d\n\n//go:embed gone.js\nvar js string\n\nvar _ = registry.RegisterBehavior(\"d\", js)\n")
	if _, err := RegisteredBehaviorSources(root); err == nil {
		t.Fatal("a directive naming a missing file returned no error: a module the lints cannot read is one they cannot hold")
	}
}

// TestLintNoVarJSFiles_HoldsARegisteredBehaviour is the gap this file
// closes, stated as a test: a `var` in a module beside its Go package
// is reported by path and line, the way one under the runtime is.
func TestLintNoVarJSFiles_HoldsARegisteredBehaviour(t *testing.T) {
	dir := t.TempDir()
	writeJS(t, dir, "beh.js", "(function () {\n  'use strict';\n  var NAME = 'x';\n})();\n")
	res, err := LintNoVarJSFiles(filepath.Join(dir, "beh.js"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Violations) != 1 || res.Violations[0].Line != 3 {
		t.Fatalf("a var in a registered behaviour was not reported at line 3: %v", res.Violations)
	}
	res, err = LintSelectorInterpolation(filepath.Join(dir, "beh.js"))
	if err != nil {
		t.Fatalf("a shape lint refused a file root: %v", err)
	}
	if res.HasErrors() {
		t.Fatalf("a shape lint reported a clean file: %s", res.Error())
	}
}
