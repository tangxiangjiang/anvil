// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func reviewServer(t *testing.T) (*Server, string) {
	t.Helper()
	sandbox := t.TempDir()
	repo := filepath.Join(sandbox, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Listen = "tcp://127.0.0.1:19876"
	cfg.SandboxDir = sandbox
	cfg.MaxWorkers = 0
	srv, err := NewServer(cfg, &EmbeddedRunner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.SeedBoundWorker("worker-review", repo); err != nil {
		t.Fatal(err)
	}
	return srv, repo
}

func reviewGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestSandboxReviewReadListAndConfinement(t *testing.T) {
	srv, repo := reviewServer(t)
	want := "first\r\nsecond\n"
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "binary.dat"), []byte{0, 1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("TOKEN=secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	list := srv.ListWorkerSandbox("worker-review", "")
	if !list.OK || len(list.Entries) != 2 {
		t.Fatalf("list = %+v", list)
	}
	read := srv.ReadWorkerSandbox("worker-review", "hello.txt")
	if !read.OK || read.Content != want || read.Encoding != "utf-8" {
		t.Fatalf("read = %+v", read)
	}
	binary := srv.ReadWorkerSandbox("worker-review", "binary.dat")
	if !binary.OK || !binary.Binary || binary.Content != "" {
		t.Fatalf("binary = %+v", binary)
	}
	for _, bad := range []string{"../outside", "/etc/passwd", `C:\Windows\win.ini`, `\\server\share\x`} {
		if got := srv.ReadWorkerSandbox("worker-review", bad); got.OK || got.Code != "invalid_path" {
			t.Fatalf("path %q = %+v", bad, got)
		}
	}
	if got := srv.ReadWorkerSandbox("worker-review", ".env"); got.OK || got.Code != "credential_path" {
		t.Fatalf("credential read = %+v", got)
	}
}

func TestSandboxReviewRejectsLinkEscape(t *testing.T) {
	srv, repo := reviewServer(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repo, "escape")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}
	got := srv.ReadWorkerSandbox("worker-review", "escape/secret.txt")
	if got.OK || got.Code != "invalid_path" {
		t.Fatalf("link escape = %+v", got)
	}
}

func TestSandboxReviewGitStatusDiffAndTruncation(t *testing.T) {
	srv, repo := reviewServer(t)
	reviewGit(t, repo, "init")
	reviewGit(t, repo, "config", "user.email", "review@example.invalid")
	reviewGit(t, repo, "config", "user.name", "Review")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reviewGit(t, repo, "add", "--", "tracked.txt")
	reviewGit(t, repo, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("new file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	status := srv.GitStatusWorker("worker-review")
	if !status.OK || len(status.Status) != 2 {
		t.Fatalf("status = %+v", status)
	}
	diff := srv.GitDiffWorker("worker-review", "tracked.txt", false)
	if !diff.OK || !strings.Contains(diff.Diff, "-old") || !strings.Contains(diff.Diff, "+new") {
		t.Fatalf("diff = %+v", diff)
	}
	untracked := srv.GitDiffWorker("worker-review", "untracked.txt", false)
	if !untracked.OK || !strings.Contains(untracked.Diff, "new file") || !strings.Contains(untracked.Diff, "+new file") {
		t.Fatalf("untracked diff = %+v", untracked)
	}
	if err := os.WriteFile(filepath.Join(repo, "image.bin"), []byte{0, 1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	binary := srv.GitDiffWorker("worker-review", "image.bin", false)
	if !binary.OK || !strings.Contains(binary.Diff, "Binary file") {
		t.Fatalf("binary diff = %+v", binary)
	}

	large := strings.Repeat("x", sandboxMaxFile+32)
	if err := os.WriteFile(filepath.Join(repo, "large.txt"), []byte(large), 0o600); err != nil {
		t.Fatal(err)
	}
	read := srv.ReadWorkerSandbox("worker-review", "large.txt")
	if !read.OK || !read.Truncated || len(read.Content) != sandboxMaxFile || read.Size != int64(len(large)) {
		t.Fatalf("large read = ok=%v truncated=%v len=%d size=%d", read.OK, read.Truncated, len(read.Content), read.Size)
	}
}

func TestSandboxReviewNonRepository(t *testing.T) {
	srv, _ := reviewServer(t)
	got := srv.GitStatusWorker("worker-review")
	if got.OK || got.Code != "not_repository" {
		t.Fatalf("status = %+v", got)
	}
}
