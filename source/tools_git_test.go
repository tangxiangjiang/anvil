// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initGitFixture(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_TERMINAL_PROMPT=0",
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".anvil/\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "readme.md"), []byte("hello\n"), 0o644)
	run("add", ".gitignore", "readme.md")
	run("commit", "-m", "init")
}

func TestGitToolsFixture(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	st := a.registry.Execute(context.Background(), "git_status", mustJSON(map[string]any{}))
	if st.IsError || !strings.Contains(st.Content, "clean: yes") {
		t.Fatalf("%+v", st)
	}
	_ = os.WriteFile(filepath.Join(dir, "readme.md"), []byte("hello world\n"), 0o644)
	st = a.registry.Execute(context.Background(), "git_status", mustJSON(map[string]any{}))
	if st.IsError || strings.Contains(st.Content, "clean: yes") {
		t.Fatalf("%+v", st)
	}
	diff := a.registry.Execute(context.Background(), "git_diff", mustJSON(map[string]any{}))
	if diff.IsError || !strings.Contains(diff.Content, "hello world") {
		t.Fatalf("%+v", diff)
	}
	log := a.registry.Execute(context.Background(), "git_log", mustJSON(map[string]any{"n": 5}))
	if log.IsError || !strings.Contains(log.Content, "init") {
		t.Fatalf("%+v", log)
	}
}

func TestGitToolsNonGitCWD(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"}, Local: LocalOptions{CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	res := a.registry.Execute(context.Background(), "git_status", mustJSON(map[string]any{}))
	if !res.IsError || !strings.Contains(res.Content, "not a git") {
		t.Fatalf("%+v", res)
	}
}

func TestGitToolsDisallowedGroup(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(context.Background(), Options{
		APIKey: "k", Model: Model{ID: "m"},
		Local:           LocalOptions{CWD: dir},
		DisallowedTools: []string{"git"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, name := range []string{"git_status", "git_diff", "git_log"} {
		if toolPresent(a, name) {
			t.Fatalf("expected %s removed", name)
		}
	}
}
