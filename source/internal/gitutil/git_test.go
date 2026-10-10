// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package gitutil_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anvil-dev/anvil/internal/gitutil"
)

func initRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_TERMINAL_PROMPT=0",
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-m", "initial")
}

func TestStatusCleanAndDirty(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	r := gitutil.Runner{CWD: dir}
	st, err := r.Status(context.Background())
	if err != nil || !st.Clean {
		t.Fatalf("%+v %v", st, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err = r.Status(context.Background())
	if err != nil || st.Clean || len(st.Unstaged) == 0 {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestDiffTruncationAndPathEscape(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	r := gitutil.Runner{CWD: dir}
	big := strings.Repeat("x\n", 60_000)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	diff, err := r.Diff(context.Background(), gitutil.DiffOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "…[truncated]") {
		t.Fatalf("expected truncation, len=%d", len(diff))
	}
	_, err = r.Diff(context.Background(), gitutil.DiffOpts{Paths: []string{"../outside"}})
	if err == nil {
		t.Fatal("expected path escape")
	}
}

func TestLogRespectsN(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	r := gitutil.Runner{CWD: dir}
	for i := 0; i < 5; i++ {
		p := filepath.Join(dir, "a.txt")
		_ = os.WriteFile(p, []byte(strings.Repeat("c", i+2)+"\n"), 0o644)
		cmd := exec.Command("git", "add", "a.txt")
		cmd.Dir = dir
		_ = cmd.Run()
		cmd = exec.Command("git", "commit", "-m", fmt.Sprintf("commit-%d", i))
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_TERMINAL_PROMPT=0",
		)
		_ = cmd.Run()
	}
	entries, err := r.Log(context.Background(), gitutil.LogOpts{N: 3})
	if err != nil || len(entries) != 3 {
		t.Fatalf("%+v %v", entries, err)
	}
	entries, err = r.Log(context.Background(), gitutil.LogOpts{N: 999})
	if err != nil || len(entries) > 50 {
		t.Fatalf("max cap: %d", len(entries))
	}
}

func TestNotAGitRepo(t *testing.T) {
	dir := t.TempDir()
	r := gitutil.Runner{CWD: dir}
	if r.IsWorkTree(context.Background()) {
		t.Fatal("expected false")
	}
	_, err := r.Status(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStatusDoesNotMutateIndex(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("dirty\n"), 0o644)
	r := gitutil.Runner{CWD: dir}
	before, _ := r.Run(context.Background(), "status", "--porcelain=v1")
	_, _ = r.Status(context.Background())
	_, _ = r.Diff(context.Background(), gitutil.DiffOpts{})
	_, _ = r.Log(context.Background(), gitutil.LogOpts{N: 5})
	after, _ := r.Run(context.Background(), "status", "--porcelain=v1")
	if before != after {
		t.Fatalf("index mutated\nbefore=%q\nafter=%q", before, after)
	}
}
