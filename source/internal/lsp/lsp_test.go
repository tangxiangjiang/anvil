// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package lsp_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/lsp"
)

func buildFakeLS(t *testing.T) string {
	t.Helper()
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	out := filepath.Join(t.TempDir(), "fakels"+ext)
	cmd := exec.Command("go", "build", "-o", out, "./fakels")
	cmd.Dir = findLSPDir(t)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakels: %v\n%s", err, b)
	}
	return out
}

func findLSPDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Dir(file)
}

func TestInitializeShutdown(t *testing.T) {
	bin := buildFakeLS(t)
	dir := t.TempDir()
	sup := lsp.NewSupervisor(dir, lsp.Config{
		Enabled: true,
		Servers: []lsp.ServerConfig{{
			Language: "go", Pattern: "**/*.go", Command: bin,
		}},
		InitTimeoutMs:    10000,
		RequestTimeoutMs: 5000,
	})
	defer sup.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := sup.Warm(ctx); err != nil {
		t.Fatal(err)
	}
	sup.Close()
}

func TestDefinitionReferencesDiagnostics(t *testing.T) {
	bin := buildFakeLS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nfunc Foo() {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.go"), []byte("package a\nfunc Bar() {}\n"), 0o644)

	sup := lsp.NewSupervisor(dir, lsp.Config{
		Enabled: true,
		Servers: []lsp.ServerConfig{{
			Language: "go", Pattern: "**/*.go", Command: bin,
		}},
	})
	defer sup.Close()
	ctx := context.Background()

	locs, err := sup.Definition(ctx, "a.go", 1, 5)
	if err != nil || len(locs) == 0 {
		t.Fatalf("%v %+v", err, locs)
	}
	if !strings.Contains(locs[0].URI, "a.go") {
		t.Fatalf("%+v", locs)
	}

	refs, err := sup.References(ctx, "a.go", 1, 5, true)
	if err != nil || len(refs) < 2 {
		t.Fatalf("%v %+v", err, refs)
	}
	for _, r := range refs {
		if strings.Contains(r.URI, "..") || filepath.IsAbs(r.URI) && !strings.HasPrefix(r.URI, dir) {
			// URI rewritten to relative
		}
		abs := filepath.Join(dir, filepath.FromSlash(r.URI))
		rel, err := filepath.Rel(dir, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("escaped %+v", r)
		}
	}

	// diagnostics after open
	time.Sleep(50 * time.Millisecond)
	diags, err := sup.Diagnostics(ctx, "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) == 0 {
		t.Fatal("expected diagnostics")
	}
}

func TestRenameMultiFileAndEscape(t *testing.T) {
	bin := buildFakeLS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("OLD package a\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.go"), []byte("OLD package a\n"), 0o644)

	sup := lsp.NewSupervisor(dir, lsp.Config{
		Enabled: true,
		Servers: []lsp.ServerConfig{{Language: "go", Pattern: "**/*.go", Command: bin}},
	})
	defer sup.Close()
	ctx := context.Background()

	we, err := sup.Rename(ctx, "a.go", 0, 0, "NEW")
	if err != nil {
		t.Fatal(err)
	}
	paths, err := lsp.ApplyWorkspaceEdit(dir, we)
	if err != nil || len(paths) < 2 {
		t.Fatalf("n=%d err=%v", len(paths), err)
	}
	a, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	b, _ := os.ReadFile(filepath.Join(dir, "b.go"))
	if !strings.HasPrefix(string(a), "NEW") || !strings.HasPrefix(string(b), "NEW") {
		t.Fatalf("a=%q b=%q", a, b)
	}

	we2, err := sup.Rename(ctx, "a.go", 0, 0, "__escape__")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lsp.ApplyWorkspaceEdit(dir, we2); err == nil {
		t.Fatal("expected escape reject")
	}
}

func TestDidChangeSync(t *testing.T) {
	bin := buildFakeLS(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	_ = os.WriteFile(path, []byte("package a\nfunc Foo() {}\n"), 0o644)
	sup := lsp.NewSupervisor(dir, lsp.Config{
		Enabled: true,
		Servers: []lsp.ServerConfig{{Language: "go", Pattern: "**/*.go", Command: bin}},
	})
	defer sup.Close()
	ctx := context.Background()
	_, err := sup.Definition(ctx, "a.go", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(path, []byte("package a\nfunc FooRenamed() {}\n"), 0o644)
	sup.NotifyFileChanged(ctx, path)
	// subsequent definition should still work (server has new content)
	_, err = sup.Definition(ctx, "a.go", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
}
