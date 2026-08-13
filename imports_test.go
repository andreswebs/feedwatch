package feedwatch_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestOnlyTheCLIImportsTheFramework enforces ADR 0003's no-leak rule now that it
// covers the library too: urfave/cli belongs to the replaceable CLI interior, so
// the library, the domain types, and any future frontend-neutral package must be
// free of it. Walking the module catches a stray import anywhere, including in
// the root package itself.
func TestOnlyTheCLIImportsTheFramework(t *testing.T) {
	const cliPackage = "internal/command"

	// The test runs in the package directory, which is the module root, so "."
	// covers every package in the module.
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(path, d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		f, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		for _, imp := range f.Imports {
			if !strings.Contains(imp.Path.Value, "urfave/cli") {
				continue
			}
			if !strings.Contains(filepath.ToSlash(filepath.Dir(path)), cliPackage) {
				t.Errorf("%s imports the framework (%s); only %s may", path, imp.Path.Value, cliPackage)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}
}

// TestTheCLIHoldsNoDomainCollaborators enforces the other half of ADR 0007: a
// frontend translates input into a request, calls one App method, and renders
// the result. Reaching for a store, fetcher, parser, orchestrator, discovery, or
// OPML package means domain logic has leaked back into the CLI, so those imports
// are banned outright rather than reviewed case by case.
func TestTheCLIHoldsNoDomainCollaborators(t *testing.T) {
	banned := []string{
		"internal/store/sqlite",
		"internal/fetch",
		"internal/parse",
		"internal/poll",
		"internal/discover",
		"internal/opml",
	}

	entries, err := filepath.Glob(filepath.Join("internal", "command", "*.go"))
	if err != nil {
		t.Fatalf("glob the CLI package: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no CLI sources found; the guard would pass vacuously")
	}

	for _, path := range entries {
		f, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		for _, imp := range f.Imports {
			for _, b := range banned {
				if strings.Contains(imp.Path.Value, "feedwatch/"+b) {
					t.Errorf("%s imports %s; the CLI must reach the domain through the library", path, b)
				}
			}
		}
	}
}

// skipDir reports whether a directory holds no module source worth parsing:
// test fixtures, build outputs, and dot directories such as .git.
func skipDir(path, name string) bool {
	if path == "." {
		return false
	}
	return name == "testdata" || name == "bin" || name == "dist" || strings.HasPrefix(name, ".")
}
