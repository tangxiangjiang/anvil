package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anvil-dev/anvil/internal/tools"
)

func TestPathSandboxRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	reg := tools.NewRegistry(dir, false)

	res := reg.Execute(context.Background(), "read_file", mustJSON(map[string]any{
		"path": "../outside.txt",
	}))
	if !res.IsError {
		t.Fatalf("expected error for path escape, got %q", res.Content)
	}
}

func TestEditFileRequiresUniqueMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("foo\nfoo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	res := reg.Execute(context.Background(), "edit_file", mustJSON(map[string]any{
		"path":       "a.txt",
		"old_string": "foo",
		"new_string": "bar",
	}))
	if !res.IsError {
		t.Fatalf("expected uniqueness error, got %q", res.Content)
	}
}

func TestEditFileHappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	res := reg.Execute(context.Background(), "edit_file", mustJSON(map[string]any{
		"path":       "a.txt",
		"old_string": "world",
		"new_string": "anvil",
	}))
	if res.IsError {
		t.Fatal(res.Content)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello anvil" {
		t.Fatalf("got %q", data)
	}
}

func TestAllowShellFalseOmitsShell(t *testing.T) {
	reg := tools.NewRegistry(t.TempDir(), false)
	res := reg.Execute(context.Background(), "shell", mustJSON(map[string]any{"command": "echo hi"}))
	if !res.IsError {
		t.Fatalf("expected unknown tool, got %q", res.Content)
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
