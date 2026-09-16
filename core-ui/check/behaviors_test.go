package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The enumerator is what makes every clean-tree lint reach a registered
// behaviour. These tests catch it finding nothing, finding too much, or
// finding through a guard that a later edit removed: an enumerator
// that silently returns an empty list turns every widened lint back
// into the runtime-only walk it replaced, and one that returns an
// asset beside the module lints a file no kernel ever loads.

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

// scratchTree writes files under a temp root and returns a writer for
// more. Every scratch Go file below is valid Go: the enumerator parses
// rather than greps, so a fixture has to be what a package would be.
func scratchTree(t *testing.T) (string, func(rel, body string)) {
	t.Helper()
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
	return root, write
}

const registeringGo = `package a

import (
	_ "embed"

	"example.com/registry"
)

//go:embed beh.js
var js string

//go:embed asset.js
var asset string

var _ = registry.RegisterBehavior("a", js, registry.Markers("[data-a]"))
var _ = asset
`

func relSet(t *testing.T, root string, files []string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, f := range files {
		r, err := filepath.Rel(root, f)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.ToSlash(r)] = true
	}
	return out
}

// TestRegisteredBehaviorSources_FollowsTheRegisteredVariable pins that
// only what a registration passes is a module: the same file embeds an
// asset it never registers, and the asset stays out.
func TestRegisteredBehaviorSources_FollowsTheRegisteredVariable(t *testing.T) {
	root, write := scratchTree(t)
	write("a/beh.go", registeringGo)
	write("a/beh.js", "(function () { 'use strict'; })();\n")
	write("a/asset.js", "var legacy = 1;\n")
	files, err := RegisteredBehaviorSources(root)
	if err != nil {
		t.Fatal(err)
	}
	got := relSet(t, root, files)
	if len(got) != 1 || !got["a/beh.js"] {
		t.Fatalf("found %v, want only a/beh.js: an embed the registration does not pass is an asset, not a module", files)
	}
}

// TestRegisteredBehaviorSources_SkipsWhatTheWalkersSkip is removal-
// sensitive for each directory guard: a registration under vendor,
// node_modules, testdata or a hidden directory is not the tree's, and
// a test file is never read. Drop one guard and its case appears.
func TestRegisteredBehaviorSources_SkipsWhatTheWalkersSkip(t *testing.T) {
	root, write := scratchTree(t)
	for _, dir := range []string{"vendor/x", "node_modules/x", "testdata/x", ".hidden/x"} {
		write(dir+"/beh.go", registeringGo)
		write(dir+"/beh.js", "var legacy = 1;\n")
		write(dir+"/asset.js", "")
	}
	write("c/beh_test.go", strings.Replace(registeringGo, "package a", "package c", 1))
	write("c/beh.js", "var legacy = 1;\n")
	write("c/asset.js", "")
	files, err := RegisteredBehaviorSources(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("found %v under directories the walkers skip or in a test file; a guard was removed", files)
	}
}

// TestRegisteredBehaviorSources_ReportsOneFileOnce is removal-sensitive
// for the deduplication: two Go files in one package registering the
// same module (a package that registers under two names, say) yield
// the file once, so no lint reports one finding twice.
func TestRegisteredBehaviorSources_ReportsOneFileOnce(t *testing.T) {
	root, write := scratchTree(t)
	write("a/beh.go", registeringGo)
	write("a/again.go", strings.Replace(registeringGo, `"a", js`, `"a-again", js`, 1))
	write("a/beh.js", "(function () { 'use strict'; })();\n")
	write("a/asset.js", "")
	files, err := RegisteredBehaviorSources(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("one module registered twice was listed %d times: %v", len(files), files)
	}
}

// TestRegisteredBehaviorSources_ReadsADirectiveAsGoDoes pins the
// directive parser: a quoted pattern with a space is one name, a
// []byte embed passed through string(...) is followed, and a var
// block with several specs carries its directive on the spec.
func TestRegisteredBehaviorSources_ReadsADirectiveAsGoDoes(t *testing.T) {
	root, write := scratchTree(t)
	write("a/beh.go", `package a

import (
	_ "embed"

	reg "example.com/registry"
)

var (
	//go:embed "behavior ping.js"
	js []byte
	//go:embed other.txt
	other string
)

var _ = reg.RegisterBehavior("a", string(js))
var _ = other
`)
	write("a/behavior ping.js", "(function () { 'use strict'; })();\n")
	write("a/other.txt", "")
	files, err := RegisteredBehaviorSources(root)
	if err != nil {
		t.Fatal(err)
	}
	got := relSet(t, root, files)
	if len(got) != 1 || !got["a/behavior ping.js"] {
		t.Fatalf("found %v, want the quoted name kept whole and followed through string(...)", files)
	}
}

// TestRegisteredBehaviorSources_RefusesWhatItCannotRead pins the three
// errors: a source that is not an embedded variable, a variable with
// no directive, and a directive naming a file that is not there. Each
// is a module the lints cannot hold, said rather than skipped.
func TestRegisteredBehaviorSources_RefusesWhatItCannotRead(t *testing.T) {
	cases := map[string]string{
		"a literal source": `package a

import "example.com/registry"

var _ = registry.RegisterBehavior("a", "(function () {})()")
`,
		"a variable with no directive": `package a

import "example.com/registry"

var js = "(function () {})()"

var _ = registry.RegisterBehavior("a", js)
`,
		"a directive naming a missing file": `package a

import (
	_ "embed"

	"example.com/registry"
)

//go:embed gone.js
var js string

var _ = registry.RegisterBehavior("a", js)
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			root, write := scratchTree(t)
			write("a/beh.go", src)
			if _, err := RegisteredBehaviorSources(root); err == nil {
				t.Fatalf("%s returned no error: a module the lints cannot read is one they cannot hold", name)
			}
		})
	}
}

// TestLintNoVarJSFiles_HoldsARegisteredBehaviour is the gap this file
// closes, stated as a test: a `var` in a module beside its Go package
// is reported by path and line, the way one under the runtime is, and
// a shape lint takes the same file as a root.
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
