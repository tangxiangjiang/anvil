// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
	// A link that stays inside the repository is still readable.
	if err := os.WriteFile(filepath.Join(repo, "real.txt"), []byte("real"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repo, "real.txt"), filepath.Join(repo, "alias.txt")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if got := srv.ReadWorkerSandbox("worker-review", "alias.txt"); !got.OK || got.Content != "real" {
		t.Fatalf("in-repository link = %+v", got)
	}

	link := filepath.Join(repo, "escape")
	if err := os.Symlink(outside, link); err != nil {
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

func initReviewRepo(t *testing.T, repo string) {
	t.Helper()
	reviewGit(t, repo, "init")
	reviewGit(t, repo, "config", "user.email", "review@example.invalid")
	reviewGit(t, repo, "config", "user.name", "Review")
}

func writeReviewFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func gitOutput(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// markerScript writes a POSIX shell script that records that it ran. Git for
// Windows executes hooks and filters through its own sh, so the same script
// works on every host; the config value uses forward slashes, which that shell
// accepts on Windows as well.
func markerScript(t *testing.T) (script, marker string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "marker.txt")
	script = filepath.Join(dir, "marker.sh")
	body := "#!/bin/sh\necho ran > \"" + filepath.ToSlash(marker) + "\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(script, 0o755); err != nil && runtime.GOOS != "windows" {
		t.Fatal(err)
	}
	return filepath.ToSlash(script), marker
}

func plainGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	_ = cmd.Run() // a nonzero exit is fine: the control only checks the marker
}

func assertNotRun(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("read-only review executed a program configured by the repository")
	}
}

func assertRan(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("control: plain git did not run the configured program: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
}

// A repository under review is untrusted content: core.fsmonitor and
// diff.external may name any program, so the read-only RPCs must override them
// rather than run them.
func TestSandboxReviewGitDoesNotExecuteRepositoryConfig(t *testing.T) {
	srv, repo := reviewServer(t)
	initReviewRepo(t, repo)
	writeReviewFile(t, filepath.Join(repo, "tracked.txt"), "old\n")
	reviewGit(t, repo, "add", "--", "tracked.txt")
	reviewGit(t, repo, "commit", "-m", "base")
	writeReviewFile(t, filepath.Join(repo, "tracked.txt"), "new\n")

	script, marker := markerScript(t)
	reviewGit(t, repo, "config", "core.fsmonitor", script)
	reviewGit(t, repo, "config", "diff.external", script)

	// The control proves the config would run the program without the guard.
	plainGit(t, repo, "status", "--porcelain")
	assertRan(t, marker)

	status := srv.GitStatusWorker("worker-review")
	if !status.OK {
		t.Fatalf("status = %+v", status)
	}
	diff := srv.GitDiffWorker("worker-review", "", false)
	if !diff.OK || !strings.Contains(diff.Diff, "+new") {
		t.Fatalf("diff = %+v", diff)
	}
	assertNotRun(t, marker)
}

// A filter driver cannot be overridden by name on the command line, so a
// repository whose config defines one is refused before git runs.
func TestSandboxReviewRefusesRepositoryFilters(t *testing.T) {
	configure := map[string]func(t *testing.T, repo, script string){
		"local": func(t *testing.T, repo, script string) {
			reviewGit(t, repo, "config", "filter.evil.clean", script)
		},
		"included": func(t *testing.T, repo, script string) {
			inc := filepath.Join(t.TempDir(), "extra.cfg")
			writeReviewFile(t, inc, "[filter \"evil\"]\n\tclean = "+script+"\n")
			reviewGit(t, repo, "config", "include.path", filepath.ToSlash(inc))
		},
		"worktree": func(t *testing.T, repo, script string) {
			reviewGit(t, repo, "config", "core.repositoryformatversion", "1")
			reviewGit(t, repo, "config", "extensions.worktreeConfig", "true")
			reviewGit(t, repo, "config", "--worktree", "filter.evil.clean", script)
		},
	}
	for name, setup := range configure {
		t.Run(name, func(t *testing.T) {
			srv, repo := reviewServer(t)
			initReviewRepo(t, repo)
			writeReviewFile(t, filepath.Join(repo, "tracked.txt"), "old\n")
			reviewGit(t, repo, "add", "--", "tracked.txt")
			reviewGit(t, repo, "commit", "-m", "base")
			writeReviewFile(t, filepath.Join(repo, ".gitattributes"), "* filter=evil\n")
			writeReviewFile(t, filepath.Join(repo, "tracked.txt"), "new\n")

			script, marker := markerScript(t)
			setup(t, repo, script)
			if name == "local" {
				plainGit(t, repo, "status", "--porcelain")
				assertRan(t, marker)
			}
			status := srv.GitStatusWorker("worker-review")
			if status.OK || status.Code != "unsafe_repository" {
				t.Fatalf("status = %+v", status)
			}
			diff := srv.GitDiffWorker("worker-review", "", false)
			if diff.OK || diff.Code != "unsafe_repository" {
				t.Fatalf("diff = %+v", diff)
			}
			assertNotRun(t, marker)
		})
	}
}

// A .git file can point the metadata at a host repository outside the sandbox,
// which would expose that repository's status and diffs.
func TestSandboxReviewRejectsGitDirOutsideRepository(t *testing.T) {
	srv, repo := reviewServer(t)
	outside := t.TempDir()
	initReviewRepo(t, outside)
	writeReviewFile(t, filepath.Join(outside, "outside.txt"), "outside-secret\n")
	reviewGit(t, outside, "add", "--", "outside.txt")
	reviewGit(t, outside, "commit", "-m", "outside")
	writeReviewFile(t, filepath.Join(outside, "outside.txt"), "outside-changed\n")

	gitdir := filepath.ToSlash(filepath.Join(outside, ".git"))
	writeReviewFile(t, filepath.Join(repo, ".git"), "gitdir: "+gitdir+"\n")

	status := srv.GitStatusWorker("worker-review")
	if status.OK || status.Code != "invalid_repository" {
		t.Fatalf("status = %+v", status)
	}
	diff := srv.GitDiffWorker("worker-review", "", false)
	if diff.OK || diff.Code != "invalid_repository" {
		t.Fatalf("diff = %+v", diff)
	}
	if strings.Contains(status.Error+diff.Diff, "outside-secret") {
		t.Fatalf("host repository content leaked: %+v %+v", status, diff)
	}
}

// A repository can also redirect its worktree through core.worktree; the git
// environment pins the worktree to the repository instead.
func TestSandboxReviewPinsWorktreeToRepository(t *testing.T) {
	srv, repo := reviewServer(t)
	initReviewRepo(t, repo)
	writeReviewFile(t, filepath.Join(repo, "inside.txt"), "inside\n")
	reviewGit(t, repo, "add", "--", "inside.txt")
	reviewGit(t, repo, "commit", "-m", "inside")

	outside := t.TempDir()
	initReviewRepo(t, outside)
	writeReviewFile(t, filepath.Join(outside, "outside-only.txt"), "outside\n")
	reviewGit(t, outside, "add", "--", "outside-only.txt")
	reviewGit(t, outside, "commit", "-m", "outside")
	writeReviewFile(t, filepath.Join(outside, "outside-only.txt"), "outside-changed\n")

	reviewGit(t, repo, "config", "core.worktree", filepath.ToSlash(outside))
	status := srv.GitStatusWorker("worker-review")
	if !status.OK {
		t.Fatalf("status = %+v", status)
	}
	if len(status.Status) != 0 {
		t.Fatalf("status followed the redirected worktree: %+v", status.Status)
	}
}

// Refs and the object store must stay inside the repository too; alternates
// are the cheapest way for a crafted repository to read another object store.
func TestSandboxReviewRejectsAlternateObjectStore(t *testing.T) {
	srv, repo := reviewServer(t)
	initReviewRepo(t, repo)
	writeReviewFile(t, filepath.Join(repo, "tracked.txt"), "one\n")
	reviewGit(t, repo, "add", "--", "tracked.txt")
	reviewGit(t, repo, "commit", "-m", "base")
	writeReviewFile(t, filepath.Join(repo, ".git", "objects", "info", "alternates"), filepath.ToSlash(t.TempDir())+"\n")

	got := srv.GitStatusWorker("worker-review")
	if got.OK || got.Code != "invalid_repository" {
		t.Fatalf("status = %+v", got)
	}
}

// Credential-bearing files are not reviewable, so neither the status list nor a
// whole-repository diff may mention them.
func TestSandboxReviewHidesCredentialPaths(t *testing.T) {
	srv, repo := reviewServer(t)
	initReviewRepo(t, repo)
	files := map[string]string{
		"visible.txt": "visible-v1\n",
		".env":        "SECRET=env\n",
		"sub/.env":    "SECRET=sub-env\n",
		"credentials": "SECRET=credentials\n",
		"sub/.netrc":  "SECRET=netrc\n",
	}
	for name, content := range files {
		writeReviewFile(t, filepath.Join(repo, filepath.FromSlash(name)), content)
	}
	reviewGit(t, repo, "add", "-A")
	reviewGit(t, repo, "commit", "-m", "base")
	for name := range files {
		writeReviewFile(t, filepath.Join(repo, filepath.FromSlash(name)), "changed-"+name+"\n")
	}

	status := srv.GitStatusWorker("worker-review")
	if !status.OK {
		t.Fatalf("status = %+v", status)
	}
	seen := map[string]bool{}
	for _, item := range status.Status {
		seen[item.Path] = true
		if item.Path != "visible.txt" {
			t.Fatalf("status exposed a credential path: %+v", item)
		}
	}
	if !seen["visible.txt"] {
		t.Fatalf("status dropped the ordinary file: %+v", status.Status)
	}

	unstaged := srv.GitDiffWorker("worker-review", "", false)
	if !unstaged.OK || !strings.Contains(unstaged.Diff, "visible.txt") {
		t.Fatalf("unstaged diff = %+v", unstaged)
	}
	reviewGit(t, repo, "add", "-A")
	staged := srv.GitDiffWorker("worker-review", "", true)
	if !staged.OK || !strings.Contains(staged.Diff, "visible.txt") {
		t.Fatalf("staged diff = %+v", staged)
	}
	for _, diff := range []ClusterSandboxResultBody{unstaged, staged} {
		if strings.Contains(diff.Diff, "SECRET") || strings.Contains(diff.Diff, ".env") || strings.Contains(diff.Diff, "credentials") || strings.Contains(diff.Diff, ".netrc") {
			t.Fatalf("diff exposed a credential path: %s", diff.Diff)
		}
	}
	explicit := srv.GitDiffWorker("worker-review", ".env", false)
	if explicit.OK || explicit.Code != "invalid_path" {
		t.Fatalf("explicit credential diff = %+v", explicit)
	}
}

func TestSandboxReviewStatusRenameAndSubmodule(t *testing.T) {
	srv, repo := reviewServer(t)
	initReviewRepo(t, repo)
	writeReviewFile(t, filepath.Join(repo, "old.txt"), "content\n")
	reviewGit(t, repo, "add", "--", "old.txt")
	reviewGit(t, repo, "commit", "-m", "base")
	reviewGit(t, repo, "mv", "old.txt", "new.txt")

	status := srv.GitStatusWorker("worker-review")
	if !status.OK {
		t.Fatalf("status = %+v", status)
	}
	var renamed *GitStatusEntry
	for i := range status.Status {
		if status.Status[i].Path == "new.txt" {
			renamed = &status.Status[i]
		}
	}
	if renamed == nil || renamed.Index != "R" || renamed.OrigPath != "old.txt" {
		t.Fatalf("rename not reported: %+v", status.Status)
	}

	sha := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "HEAD"))
	reviewGit(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+sha+",sub")
	staged := srv.GitDiffWorker("worker-review", "", true)
	if !staged.OK || !strings.Contains(staged.Diff, "Subproject commit "+sha) {
		t.Fatalf("submodule diff = %+v", staged)
	}
}

func TestSandboxReviewDiffTruncation(t *testing.T) {
	srv, repo := reviewServer(t)
	initReviewRepo(t, repo)
	base := strings.Repeat("a\n", sandboxMaxGit/2)
	writeReviewFile(t, filepath.Join(repo, "large.txt"), base)
	reviewGit(t, repo, "add", "--", "large.txt")
	reviewGit(t, repo, "commit", "-m", "base")
	writeReviewFile(t, filepath.Join(repo, "large.txt"), strings.Repeat("b\n", sandboxMaxGit/2))

	diff := srv.GitDiffWorker("worker-review", "large.txt", false)
	if !diff.OK || !diff.Truncated || len(diff.Diff) != sandboxMaxGit {
		t.Fatalf("diff ok=%v truncated=%v len=%d", diff.OK, diff.Truncated, len(diff.Diff))
	}
}

func TestSandboxReviewStatusEntryLimit(t *testing.T) {
	srv, repo := reviewServer(t)
	initReviewRepo(t, repo)
	for i := 0; i <= sandboxMaxEntries; i++ {
		writeReviewFile(t, filepath.Join(repo, fmt.Sprintf("file-%05d.txt", i)), "x\n")
	}
	status := srv.GitStatusWorker("worker-review")
	if !status.OK || len(status.Status) != sandboxMaxEntries || !status.Truncated {
		t.Fatalf("status ok=%v entries=%d truncated=%v", status.OK, len(status.Status), status.Truncated)
	}
}

func TestSandboxReviewReportsMissingGit(t *testing.T) {
	srv, repo := reviewServer(t)
	initReviewRepo(t, repo)
	writeReviewFile(t, filepath.Join(repo, "tracked.txt"), "one\n")
	reviewGit(t, repo, "add", "--", "tracked.txt")
	reviewGit(t, repo, "commit", "-m", "base")

	t.Setenv("PATH", "")
	got := srv.GitStatusWorker("worker-review")
	if got.OK || got.Code != "git_unavailable" {
		t.Fatalf("status = %+v", got)
	}
}

func TestSandboxReviewGitTimeout(t *testing.T) {
	srv, repo := reviewServer(t)
	initReviewRepo(t, repo)
	writeReviewFile(t, filepath.Join(repo, "tracked.txt"), "one\n")
	reviewGit(t, repo, "add", "--", "tracked.txt")
	reviewGit(t, repo, "commit", "-m", "base")

	previous := sandboxGitTimeout
	sandboxGitTimeout = time.Nanosecond
	t.Cleanup(func() { sandboxGitTimeout = previous })

	got := srv.GitStatusWorker("worker-review")
	if got.OK || got.Code != "git_timeout" {
		t.Fatalf("status = %+v", got)
	}
}

// Windows junctions are reparse points, not symbolic links; confinement must
// reject them too.
func TestSandboxReviewRejectsJunctionEscape(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are a Windows reparse point")
	}
	srv, repo := reviewServer(t)
	outside := t.TempDir()
	writeReviewFile(t, filepath.Join(outside, "secret.txt"), "secret\n")
	link := filepath.Join(repo, "escape")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
		t.Skipf("junction unavailable: %v: %s", err, out)
	}
	if got := srv.ReadWorkerSandbox("worker-review", "escape/secret.txt"); got.OK || got.Code != "invalid_path" {
		t.Fatalf("junction escape read = %+v", got)
	}
	if got := srv.ListWorkerSandbox("worker-review", "escape"); got.OK || got.Code != "invalid_path" {
		t.Fatalf("junction escape list = %+v", got)
	}
}
