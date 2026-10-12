// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPrepareProjectPathRejectsNonGitNonempty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := prepareProjectPath(dir, "")
	if err == nil {
		t.Fatal("expected error for nonempty non-git dir")
	}
}

func TestPrepareProjectPathExistingGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "r")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	run("remote", "add", "origin", "https://example.com/demo.git")

	remote, err := prepareProjectPath(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if remote != "https://example.com/demo.git" {
		t.Fatalf("remote=%q", remote)
	}
}

func TestAddRemoveProjectWorkerInSandbox(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	sandbox := t.TempDir()
	repo := filepath.Join(sandbox, "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644)
	run("add", ".")
	run("commit", "-m", "init")
	run("remote", "add", "origin", "https://example.com/demo.git")

	cfg := DefaultConfig()
	cfg.Listen = "tcp://127.0.0.1:0"
	cfg.MaxWorkers = 1
	cfg.Repos = nil
	cfg.SandboxDir = sandbox
	_ = cfg.Normalize()
	s := &Server{cfg: cfg, slots: NewSlots(0)}
	ws, err := s.AddProjectWorker("", "Demo", "ops note", "proj", "")
	if err != nil {
		t.Fatal(err)
	}
	if ws.Name != "Demo" || ws.Description != "ops note" || ws.ProjectRel != "proj" {
		t.Fatalf("%+v", ws)
	}
	if ws.GitBranch == "" {
		t.Fatalf("expected git_branch on decorated worker: %+v", ws)
	}
	if ws.Status != SlotStopped {
		t.Fatalf("want stopped got %s", ws.Status)
	}
	if cwd, err := s.ResolveJobRepo("proj"); err != nil || cwd != ws.ProjectPath {
		t.Fatalf("resolve by project_rel: cwd=%q err=%v", cwd, err)
	}
	if cwd, err := s.ResolveJobRepo(ws.ID); err != nil || cwd != ws.ProjectPath {
		t.Fatalf("resolve by worker id: cwd=%q err=%v", cwd, err)
	}
	ws, err = s.StartProjectWorker(ws.ID)
	if err != nil || ws.Status != SlotIdle {
		t.Fatalf("start: %+v %v", ws, err)
	}
	ws, err = s.ShutdownProjectWorker(ws.ID)
	if err != nil || ws.Status != SlotStopped {
		t.Fatalf("shutdown: %+v %v", ws, err)
	}
	if err := s.RemoveProjectWorker(ws.ID); err != nil {
		t.Fatal(err)
	}
}

func TestProjectWorkersPersistAcrossNewServer(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	sandbox := t.TempDir()
	repo := filepath.Join(sandbox, "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644)
	run("add", ".")
	run("commit", "-m", "init")
	run("remote", "add", "origin", "https://example.com/demo.git")

	cfg := DefaultConfig()
	cfg.Listen = "tcp://127.0.0.1:0"
	cfg.Repos = nil
	cfg.SandboxDir = sandbox
	s1, err := NewServer(cfg, &EmbeddedRunner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := s1.AddProjectWorker("Demo", "Demo", "note", "proj", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.StartProjectWorker(ws.ID); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(sandbox, ".anvil", "workers.json")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("expected state file: %v", err)
	}

	s2, err := NewServer(cfg, &EmbeddedRunner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap := s2.WorkerSnapshot()
	if len(snap.Workers) != 1 {
		t.Fatalf("restored workers=%d want 1", len(snap.Workers))
	}
	got := snap.Workers[0]
	if got.ID != "Demo" || got.Status != SlotIdle || got.ProjectRel != "proj" {
		t.Fatalf("restored %+v", got)
	}
}

func TestResolveInSandboxRejectsEscape(t *testing.T) {
	sandbox := t.TempDir()
	cfg := DefaultConfig()
	cfg.Listen = "tcp://127.0.0.1:0"
	cfg.SandboxDir = sandbox
	_ = cfg.Normalize()
	s := &Server{cfg: cfg}
	_, _, err := s.resolveInSandbox(filepath.Join(sandbox, "..", "outside"))
	if err == nil {
		t.Fatal("expected escape error")
	}
}

func TestListAndMkdirSandbox(t *testing.T) {
	sandbox := t.TempDir()
	cfg := DefaultConfig()
	cfg.Listen = "tcp://127.0.0.1:0"
	cfg.SandboxDir = sandbox
	_ = cfg.Normalize()
	s := &Server{cfg: cfg}
	res, err := s.MkdirSandbox("a/b")
	if err != nil || !res.OK {
		t.Fatalf("%v %+v", err, res)
	}
	if err := os.WriteFile(filepath.Join(sandbox, "readme.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	listed, err := s.ListSandbox("")
	if err != nil {
		t.Fatal(err)
	}
	var foundDir, foundFile bool
	for _, e := range listed.Entries {
		if e.Name == "a" && e.IsDir {
			foundDir = true
		}
		if e.Name == "readme.txt" && !e.IsDir && e.Size == 2 && e.Mtime > 0 {
			foundFile = true
		}
	}
	if !foundDir || !foundFile {
		t.Fatalf("entries %+v", listed.Entries)
	}
}

func TestSanitizeWorkerID(t *testing.T) {
	if got := sanitizeWorkerID("My Repo!!"); got != "My-Repo" {
		t.Fatalf("got %q", got)
	}
}
