// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anvil-dev/anvil/internal/permissions"
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

func TestShellTimeoutKillsTheCommand(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	if runtime.GOOS == "windows" {
		t.Skip("cmd has no sleep")
	}
	reg := tools.NewRegistry(t.TempDir(), true)
	start := time.Now()
	res := reg.Execute(context.Background(), "shell", mustJSON(map[string]any{
		"command":         "sleep 30",
		"timeout_seconds": 1,
	}))
	if !res.IsError {
		t.Fatalf("expected timeout error, got %q", res.Content)
	}
	if !strings.Contains(res.Content, "killed after") {
		t.Fatalf("expected kill note, got %q", res.Content)
	}
	if time.Since(start) > 8*time.Second {
		t.Fatalf("shell stayed blocked for %s", time.Since(start))
	}
}

func TestShellRejectsTimeoutOutOfRange(t *testing.T) {
	reg := tools.NewRegistry(t.TempDir(), true)
	res := reg.Execute(context.Background(), "shell", mustJSON(map[string]any{
		"command":         "echo hi",
		"timeout_seconds": 999,
	}))
	if !res.IsError {
		t.Fatalf("expected range error, got %q", res.Content)
	}
}

func TestAllowShellFalseOmitsShell(t *testing.T) {
	reg := tools.NewRegistry(t.TempDir(), false)
	res := reg.Execute(context.Background(), "shell", mustJSON(map[string]any{"command": "echo hi"}))
	if !res.IsError {
		t.Fatalf("expected unknown tool, got %q", res.Content)
	}
}

func TestGrepAndGlobSkipIntermediate(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Engine", "Source", "a.cpp")
	junk := filepath.Join(dir, "Engine", "Intermediate", "Build", "a.cpp")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(junk), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("FindStreamingTextureHere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(junk, []byte("FindStreamingTextureHere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, false)
	gres := reg.Execute(context.Background(), "grep", mustJSON(map[string]any{
		"pattern": "FindStreamingTextureHere",
	}))
	if gres.IsError {
		t.Fatal(gres.Content)
	}
	if !strings.Contains(gres.Content, "Engine/Source/a.cpp") {
		t.Fatalf("grep missing source hit: %q", gres.Content)
	}
	if strings.Contains(gres.Content, "Intermediate") {
		t.Fatalf("grep should skip Intermediate: %q", gres.Content)
	}
	globRes := reg.Execute(context.Background(), "glob", mustJSON(map[string]any{
		"pattern": "**/*.cpp",
	}))
	if globRes.IsError {
		t.Fatal(globRes.Content)
	}
	if !strings.Contains(globRes.Content, "Engine/Source/a.cpp") {
		t.Fatalf("glob missing source: %q", globRes.Content)
	}
	if strings.Contains(globRes.Content, "Intermediate") {
		t.Fatalf("glob should skip Intermediate: %q", globRes.Content)
	}
}

func TestCallFactsForShellIncludesPathAnalysis(t *testing.T) {
	facts := tools.CallFactsFor("shell", mustJSON(map[string]any{
		"command": "cat .git/config",
	}))
	if facts.PathState != permissions.PathsKnown || facts.PathProblem != "" {
		t.Fatalf("CallFactsFor analysis = %q %q", facts.PathState, facts.PathProblem)
	}
	if len(facts.Paths) != 1 || facts.Paths[0] != ".git/config" {
		t.Fatalf("CallFactsFor paths = %v, want [.git/config]", facts.Paths)
	}
}

func TestProtectedShellRequiresApprovalChannel(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, ".git", "marker")
	if err := os.WriteFile(marker, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := permissions.New([]permissions.Rule{{Effect: "allow", Tool: "shell"}})
	if err != nil {
		t.Fatal(err)
	}
	set.Protect(permissions.Protection{Root: dir})
	reg := tools.NewRegistry(dir, true)
	reg.SetPermissionRules(set)

	res := reg.Execute(context.Background(), "shell", shellArgsForTest(shellWriteProtectedCommand()))
	if !res.IsError || !strings.Contains(res.Content, "no approval channel") {
		t.Fatalf("Execute = %+v, want fail-closed no-channel refusal", res)
	}
	if got, _ := os.ReadFile(marker); string(got) != "old" {
		t.Fatalf("protected command ran without approval: %q", got)
	}
	if res.PathState != permissions.PathsKnown || len(res.Protected) != 1 {
		t.Fatalf("analysis finding missing from result: %+v", res)
	}
}

func TestIndeterminateShellRequiresApprovalChannel(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	if err := os.WriteFile(marker, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANVIL_TEST_TARGET", marker)
	reg := tools.NewRegistry(dir, true)

	res := reg.Execute(context.Background(), "shell", shellArgsForTest(shellWriteDynamicCommand()))
	if !res.IsError || !strings.Contains(res.Content, permissions.PathProblemDynamicPath) {
		t.Fatalf("Execute = %+v, want dynamic-path refusal", res)
	}
	if got, _ := os.ReadFile(marker); string(got) != "old" {
		t.Fatalf("indeterminate command ran without approval: %q", got)
	}
	if res.PathState != permissions.PathsIndeterminate ||
		res.PathProblem != permissions.PathProblemDynamicPath {
		t.Fatalf("analysis finding missing from result: %+v", res)
	}
}

func shellArgsForTest(command string) json.RawMessage {
	return mustJSON(map[string]any{"command": command})
}

func shellWriteProtectedCommand() string {
	if runtime.GOOS == "windows" {
		return `echo changed> .git\marker`
	}
	return `printf changed > .git/marker`
}

func shellWriteDynamicCommand() string {
	if runtime.GOOS == "windows" {
		return `echo changed> "%ANVIL_TEST_TARGET%"`
	}
	return `printf changed > "$ANVIL_TEST_TARGET"`
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
