// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func buildFakeLSBin(t *testing.T) string {
	t.Helper()
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	out := filepath.Join(t.TempDir(), "fakels"+ext)
	cmd := exec.Command("go", "build", "-o", out, "./internal/lsp/fakels")
	cmd.Dir = findSourceRoot(t)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakels: %v\n%s", err, b)
	}
	return out
}

func TestLSPToolsAndGroup(t *testing.T) {
	bin := buildFakeLSBin(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nfunc Foo() {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.go"), []byte("package a\nfunc Bar() {}\n"), 0o644)

	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir},
		LSP: LSPOptions{
			Servers: []LSPServerConfig{{
				Language: "go", Pattern: "**/*.go", Command: bin,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if !toolPresent(a, "definition") || !toolPresent(a, "rename_symbol") {
		t.Fatalf("tools=%v", a.registry.Names())
	}

	def := a.registry.Execute(context.Background(), "definition", mustJSON(map[string]any{
		"path": "a.go", "line": 1, "character": 5,
	}))
	if def.IsError || !strings.Contains(def.Content, "a.go") {
		t.Fatalf("%+v", def)
	}

	ren := a.registry.Execute(context.Background(), "rename_symbol", mustJSON(map[string]any{
		"path": "a.go", "line": 0, "character": 0, "new_name": "XYZ",
	}))
	if ren.IsError || !strings.Contains(ren.Content, "2 file") {
		t.Fatalf("%+v", ren)
	}

	a2, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local:           LocalOptions{CWD: dir},
		LSP:             LSPOptions{Servers: []LSPServerConfig{{Language: "go", Pattern: "**/*.go", Command: bin}}},
		DisallowedTools: []string{"lsp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a2.Close()
	if toolPresent(a2, "definition") {
		t.Fatal("lsp group should strip tools")
	}
}

func TestLSPMissingServerNotRequired(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir},
		LSP: LSPOptions{
			Servers: []LSPServerConfig{{
				Language: "go", Pattern: "**/*.go", Command: "anvil-nonexistent-gopls-binary",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	res := a.registry.Execute(context.Background(), "definition", mustJSON(map[string]any{
		"path": "x.go", "line": 0, "character": 0,
	}))
	if !res.IsError {
		t.Fatalf("expected tool error, got %+v", res)
	}
}

func TestLSPEditSync(t *testing.T) {
	bin := buildFakeLSBin(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nfunc Foo() {}\n"), 0o644)
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local: LocalOptions{CWD: dir},
		LSP:   LSPOptions{Servers: []LSPServerConfig{{Language: "go", Pattern: "**/*.go", Command: bin}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_ = a.registry.Execute(context.Background(), "definition", mustJSON(map[string]any{
		"path": "a.go", "line": 1, "character": 5,
	}))
	edit := a.registry.Execute(context.Background(), "edit_file", mustJSON(map[string]any{
		"path": "a.go", "old_string": "Foo", "new_string": "Foo2",
	}))
	if edit.IsError {
		t.Fatal(edit.Content)
	}
	def := a.registry.Execute(context.Background(), "definition", mustJSON(map[string]any{
		"path": "a.go", "line": 1, "character": 5,
	}))
	if def.IsError {
		t.Fatalf("%+v", def)
	}
}
