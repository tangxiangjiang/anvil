// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorktreeMergeReviewAndFinalCleanup(t *testing.T) {
	base, managed := newWorktreeTestRepository(t)
	worker := prepareReviewReadyWorktree(t, base, managed)
	workerFile := filepath.Join(worker.WorktreeRoot, "worker.txt")
	if err := os.WriteFile(workerFile, []byte("reviewed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, worker.WorktreeRoot, "add", "worker.txt")
	worktreeTestGit(t, worker.WorktreeRoot, "commit", "-m", "worker change")
	baseBefore := strings.TrimSpace(worktreeTestGit(t, base, "rev-parse", "HEAD"))

	review, err := ReviewWorktreeWorker(context.Background(), WorktreeReviewOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID,
		ExpectedRevision: worker.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !review.Integrable ||
		!containsWorktreeString(review.AllowedStrategies, "fast_forward") ||
		len(review.ChangedPaths) != 1 ||
		review.PatchDigest == "" {
		t.Fatalf("review=%+v", review)
	}
	if got := strings.TrimSpace(worktreeTestGit(t, base, "rev-parse", "HEAD")); got != baseBefore {
		t.Fatalf("review mutated base HEAD: %s -> %s", baseBefore, got)
	}

	result, err := IntegrateWorktreeWorker(context.Background(), WorktreeIntegrateOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID,
		ExpectedRevision: review.ManifestRevision,
		ExpectedBase:     review.CurrentBase, ExpectedHead: review.HeadCommit,
		ExpectedPatchDigest: review.PatchDigest, Strategy: "fast_forward",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Worker.State != WorktreeIntegrated ||
		result.ResultCommit != review.HeadCommit {
		t.Fatalf("integration=%+v", result)
	}
	if data, err := os.ReadFile(filepath.Join(base, "worker.txt")); err != nil ||
		string(data) != "reviewed\n" {
		t.Fatalf("integrated file=%q err=%v", data, err)
	}

	cleanup, err := CleanupWorktreeWorker(context.Background(), WorktreeCleanupOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID,
		ExpectedRevision: result.Worker.Revision,
	})
	if err != nil || cleanup.Worker.State != WorktreeRemoved {
		t.Fatalf("cleanup=%+v err=%v", cleanup, err)
	}
}

func TestWorktreeConflictPreviewAndStaleIntegrationFailClosed(t *testing.T) {
	base, managed := newWorktreeTestRepository(t)
	worker := prepareReviewReadyWorktree(t, base, managed)
	if err := os.WriteFile(
		filepath.Join(worker.WorktreeRoot, "README.md"), []byte("worker\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, worker.WorktreeRoot, "add", "README.md")
	worktreeTestGit(t, worker.WorktreeRoot, "commit", "-m", "worker edit")
	if err := os.WriteFile(filepath.Join(base, "README.md"), []byte("primary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, base, "add", "README.md")
	worktreeTestGit(t, base, "commit", "-m", "base edit")
	baseBefore := strings.TrimSpace(worktreeTestGit(t, base, "rev-parse", "HEAD"))

	review, err := ReviewWorktreeWorker(context.Background(), WorktreeReviewOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID,
		ExpectedRevision: worker.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if review.Integrable || len(review.Conflicts) == 0 {
		t.Fatalf("conflicting review=%+v", review)
	}
	_, err = IntegrateWorktreeWorker(context.Background(), WorktreeIntegrateOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID,
		ExpectedRevision: review.ManifestRevision,
		ExpectedBase:     review.CurrentBase, ExpectedHead: review.HeadCommit,
		ExpectedPatchDigest: review.PatchDigest, Strategy: "merge_no_ff",
	})
	assertWorktreeErrorCode(t, err, "conflict")
	if got := strings.TrimSpace(worktreeTestGit(t, base, "rev-parse", "HEAD")); got != baseBefore {
		t.Fatalf("refused integration changed base: %s -> %s", baseBefore, got)
	}
	if _, err := os.Stat(worker.WorktreeRoot); err != nil {
		t.Fatalf("conflict removed worktree: %v", err)
	}
}

func TestWorktreeAbortOnStaleReviewPreservesBothTrees(t *testing.T) {
	base, managed := newWorktreeTestRepository(t)
	worker := prepareReviewReadyWorktree(t, base, managed)
	if err := os.WriteFile(
		filepath.Join(worker.WorktreeRoot, "change.txt"), []byte("worker\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, worker.WorktreeRoot, "add", "change.txt")
	worktreeTestGit(t, worker.WorktreeRoot, "commit", "-m", "reviewed change")
	review, err := ReviewWorktreeWorker(context.Background(), WorktreeReviewOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID,
		ExpectedRevision: worker.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "later.txt"), []byte("later\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, base, "add", "later.txt")
	worktreeTestGit(t, base, "commit", "-m", "advance after review")
	baseAfter := strings.TrimSpace(worktreeTestGit(t, base, "rev-parse", "HEAD"))
	_, err = IntegrateWorktreeWorker(context.Background(), WorktreeIntegrateOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID,
		ExpectedRevision: review.ManifestRevision,
		ExpectedBase:     review.CurrentBase, ExpectedHead: review.HeadCommit,
		ExpectedPatchDigest: review.PatchDigest, Strategy: "fast_forward",
	})
	assertWorktreeErrorCode(t, err, "stale")
	if got := strings.TrimSpace(worktreeTestGit(t, base, "rev-parse", "HEAD")); got != baseAfter {
		t.Fatalf("stale integration changed base: %s -> %s", baseAfter, got)
	}
}

func TestWorktreeFinalCleanupRequiresReviewedDiscard(t *testing.T) {
	base, managed := newWorktreeTestRepository(t)
	worker := prepareReviewReadyWorktree(t, base, managed)
	dirty := filepath.Join(worker.WorktreeRoot, "discard.txt")
	if err := os.WriteFile(dirty, []byte("discard me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	review, err := ReviewWorktreeWorker(context.Background(), WorktreeReviewOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID,
		ExpectedRevision: worker.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if review.Integrable || len(review.Untracked) != 1 {
		t.Fatalf("discard review=%+v", review)
	}
	discarded, err := DiscardWorktreeWorker(context.Background(), WorktreeDiscardOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID,
		ExpectedRevision:    review.ManifestRevision,
		ExpectedPatchDigest: review.PatchDigest,
	})
	if err != nil || discarded.State != WorktreeDiscarded {
		t.Fatalf("discard=%+v err=%v", discarded, err)
	}
	_, err = CleanupWorktreeWorker(context.Background(), WorktreeCleanupOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID,
		ExpectedRevision: discarded.Revision,
	})
	assertWorktreeErrorCode(t, err, "stale")
	if _, err := os.Stat(dirty); err != nil {
		t.Fatalf("unconfirmed cleanup removed dirty file: %v", err)
	}
	cleanup, err := CleanupWorktreeWorker(context.Background(), WorktreeCleanupOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID,
		ExpectedRevision:     discarded.Revision,
		ExpectedReviewDigest: review.PatchDigest,
	})
	if err != nil || cleanup.Worker.State != WorktreeRemoved {
		t.Fatalf("reviewed cleanup=%+v err=%v", cleanup, err)
	}
}

func prepareReviewReadyWorktree(
	t *testing.T,
	base, managed string,
) WorktreeWorker {
	t.Helper()
	worker, err := PrepareWorktreeWorker(
		context.Background(), worktreeTestOptions(base, managed),
	)
	if err != nil {
		t.Fatal(err)
	}
	key, err := readWorktreeOwnerKey(managed)
	if err != nil {
		t.Fatal(err)
	}
	worker.State = WorktreeRunning
	worker.Revision++
	worker.LeaseUntil = time.Now().UTC().Add(time.Minute)
	worker.UpdatedAt = time.Now().UTC()
	if err := persistWorktreeTransition(
		LocalOptions{CWD: base}, managed, key, worker,
	); err != nil {
		t.Fatal(err)
	}
	worker.State = WorktreeReviewReady
	worker.Revision++
	worker.LeaseUntil = time.Time{}
	worker.UpdatedAt = time.Now().UTC()
	if err := persistWorktreeTransition(
		LocalOptions{CWD: base}, managed, key, worker,
	); err != nil {
		t.Fatal(err)
	}
	return worker
}
