package check

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Registered behaviours are runtime modules too. A package that calls
// registry.RegisterBehavior embeds its JavaScript beside the Go that
// renders the markup it binds (docs/spec-behavior-registry.md), so the
// module lives outside core-ui/runtime and outside every lint that
// walks that directory. The contract is the same for both: ES2020
// (no var), no selector built from an unescaped value, no storage key
// built raw. This file finds those modules so the lints can hold them
// to it.

// RegisteredBehaviorSources returns the JavaScript files every
// registry.RegisterBehavior call in the tree embeds: for each Go file
// under root (tests, vendor, node_modules, testdata and hidden
// directories skipped) that calls RegisterBehavior, every
// `//go:embed` directive naming a .js file, resolved beside that Go
// file. A directive whose file is missing is an error rather than a
// silence, because a module the lints cannot read is a module they
// cannot hold.
func RegisteredBehaviorSources(root string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (name == "vendor" || name == "node_modules" || name == "testdata" ||
				strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(raw)
		if !strings.Contains(src, "RegisterBehavior(") {
			return nil
		}
		for _, js := range embeddedJS(src) {
			full := filepath.Join(filepath.Dir(path), js)
			if _, err := os.Stat(full); err != nil {
				return fmt.Errorf("registered behaviour: %s embeds %s, which is not there: %w", path, js, err)
			}
			if !seen[full] {
				seen[full] = true
				out = append(out, full)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// embeddedJS lists the .js patterns named by the //go:embed directives
// in a Go source. A directive may name several files and may quote
// them; a pattern with a wildcard is left as written, and resolving it
// is the caller's problem the day one exists (none does today).
func embeddedJS(src string) []string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "//go:embed ") {
			continue
		}
		for _, f := range strings.Fields(strings.TrimPrefix(line, "//go:embed ")) {
			f = strings.Trim(f, "\"`")
			if strings.HasSuffix(f, ".js") {
				out = append(out, f)
			}
		}
	}
	return out
}

// LintNoVarJSFiles runs the no-var lint over the given JavaScript
// files, for modules that do not live under a directory the walker
// covers: the registered behaviours.
func LintNoVarJSFiles(paths ...string) (*Result, error) {
	result := &Result{}
	for _, p := range paths {
		if err := scanJSFileForVar(p, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}
