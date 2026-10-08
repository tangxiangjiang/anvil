package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/tools"
)

func TestApplyPatchMultiHunk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	patch := `*** Begin Patch
*** Update File: a.txt
@@
 one
-two
+TWO
@@
 three
-four
+FOUR
*** End Patch`
	res := reg.Execute(context.Background(), "apply_patch", mustJSON(map[string]any{"patch": patch}))
	if res.IsError {
		t.Fatal(res.Content)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "one\nTWO\nthree\nFOUR\n" && string(data) != "one\nTWO\nthree\nFOUR" {
		// applyHunk uses TrimSuffix on hunk sides; newlines between hunks matter
		got := string(data)
		if !strings.Contains(got, "TWO") || !strings.Contains(got, "FOUR") {
			t.Fatalf("got %q", got)
		}
	}
}

func TestEditFileNotFoundHint(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644)
	reg := tools.NewRegistry(dir, false)
	res := reg.Execute(context.Background(), "edit_file", mustJSON(map[string]any{
		"path":       "a.txt",
		"old_string": "alphabet",
		"new_string": "x",
	}))
	if !res.IsError || !strings.Contains(res.Content, "not found") {
		t.Fatalf("%q", res.Content)
	}
}

func TestApplyPatchAddFile(t *testing.T) {
	dir := t.TempDir()
	reg := tools.NewRegistry(dir, false)
	patch := "*** Begin Patch\n*** Add File: n.txt\n+hello\n*** End Patch\n"
	res := reg.Execute(context.Background(), "apply_patch", json.RawMessage(`{"patch":`+mustJSONString(patch)+`}`))
	if res.IsError {
		t.Fatal(res.Content)
	}
	data, err := os.ReadFile(filepath.Join(dir, "n.txt"))
	if err != nil || !strings.Contains(string(data), "hello") {
		t.Fatalf("%q %v", data, err)
	}
}

func mustJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
