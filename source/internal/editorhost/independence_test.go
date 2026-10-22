// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// modulePrefix is the Anvil module root.
const modulePrefix = "github.com/anvil-dev/anvil"

// allowedModuleImports is the set of in-module packages this host may import.
// The rule from doc/design/v0.x/35-vscode-extension.md is "the public anvil
// package only": the editor host is a standalone capability and must never
// reach into the automated coding tool-chain. Widening this list is a design
// change, not a test change.
var allowedModuleImports = map[string]bool{
	modulePrefix: true,
}

// TestNoToolChainImports enforces the independence rule at the source level, so
// a stray import fails here rather than in a code review.
func TestNoToolChainImports(t *testing.T) {
	// Both halves of the binary: the protocol package and its entry point.
	// Forward slashes only: filepath.Glob treats a backslash as an escape.
	dirs := []string{".", "../../cmd/anvil-host"}

	fset := token.NewFileSet()
	scanned := 0
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Fatalf("no Go files under %s; the test is looking at the wrong directory", dir)
		}
		for _, name := range files {
			scanned++
			src, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			af, err := parser.ParseFile(fset, name, src, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			for _, spec := range af.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					continue
				}
				if !strings.HasPrefix(path, modulePrefix) {
					continue
				}
				if allowedModuleImports[path] || strings.HasPrefix(path, modulePrefix+"/internal/editorhost") {
					continue
				}
				t.Errorf("%s imports %s\n"+
					"anvil-host must consume the public %s package only; importing the\n"+
					"tool-chain (internal/daemon, internal/scheduler, internal/web, cmd/anvil-*,\n"+
					"…) would couple this editor client to the platform. See\n"+
					"doc/design/v0.x/35-vscode-extension.md, section \"Independence (locked)\".",
					name, path, modulePrefix)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("scanned no files")
	}
}
