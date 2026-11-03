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

func TestWorktreeMergeReviewUsesVerifiedTransactionForDivergedBase(t *testing.T) {
	base, managed := newWorktreeTestRepository(t)
	worker := prepareReviewReadyWorktree(t, base, managed)
	if err := os.WriteFile(
		filepath.Join(worker.WorktreeRoot, "worker.txt"), []byte("worker\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, worker.WorktreeRoot, "add", "worker.txt")
	worktreeTestGit(t, worker.WorktreeRoot, "commit", "-m", "worker change")
	if err := os.WriteFile(filepath.Join(base, "primary.txt"), []byte("primary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, base, "add", "primary.txt")
	worktreeTestGit(t, base, "commit", "-m", "primary change")

	review, err := ReviewWorktreeWorker(context.Background(), WorktreeReviewOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID, ExpectedRevision: worker.Revision,
	})
	if err != nil || !review.Integrable ||
		!containsWorktreeString(review.AllowedStrategies, "merge_no_ff") {
		t.Fatalf("review=%+v err=%v", review, err)
	}
	result, err := IntegrateWorktreeWorker(context.Background(), WorktreeIntegrateOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID,
		ExpectedRevision: review.ManifestRevision,
		ExpectedBase:     review.CurrentBase, ExpectedHead: review.HeadCommit,
		ExpectedPatchDigest: review.PatchDigest, Strategy: "merge_no_ff",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Worker.State != WorktreeIntegrated ||
		result.Worker.Integration == nil ||
		result.Worker.Integration.Phase != "completed" {
		t.Fatalf("integration=%+v", result)
	}
	for _, name := range []string{"primary.txt", "worker.txt"} {
		if _, err := os.Stat(filepath.Join(base, name)); err != nil {
			t.Fatalf("integrated %s: %v", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(
		managed, result.Worker.Integration.WorktreeLabel,
	)); !os.IsNotExist(err) {
		t.Fatalf("transaction worktree remains: %v", err)
	}
}

func TestWorktreeAbortOnVerificationFailureLeavesPrimaryUnchanged(t *testing.T) {
	base, managed := newWorktreeTestRepository(t)
	worker := prepareReviewReadyWorktree(t, base, managed)
	if err := os.WriteFile(
		filepath.Join(worker.WorktreeRoot, "worker.txt"), []byte("worker\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, worker.WorktreeRoot, "add", "worker.txt")
	worktreeTestGit(t, worker.WorktreeRoot, "commit", "-m", "worker change")
	review, err := ReviewWorktreeWorker(context.Background(), WorktreeReviewOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID, ExpectedRevision: worker.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	configRoot := filepath.Join(base, ".anvil")
	if err := os.MkdirAll(configRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(configRoot, "project.json"),
		[]byte(`{"test":{"command":["git","rev-parse","--verify","refs/heads/missing"]}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	baseBefore := strings.TrimSpace(worktreeTestGit(t, base, "rev-parse", "HEAD"))
	result, err := IntegrateWorktreeWorker(context.Background(), WorktreeIntegrateOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID,
		ExpectedRevision: review.ManifestRevision,
		ExpectedBase:     review.CurrentBase, ExpectedHead: review.HeadCommit,
		ExpectedPatchDigest: review.PatchDigest, Strategy: "fast_forward",
	})
	assertWorktreeErrorCode(t, err, "integration")
	if result.Worker.State != WorktreeReviewReady {
		t.Fatalf("worker state=%s", result.Worker.State)
	}
	if got := strings.TrimSpace(worktreeTestGit(t, base, "rev-parse", "HEAD")); got != baseBefore {
		t.Fatalf("verification failure changed base: %s -> %s", baseBefore, got)
	}
	if _, err := os.Stat(worker.WorktreeRoot); err != nil {
		t.Fatalf("verification failure removed worker: %v", err)
	}
}

func TestWorktreeAbortRecoveryFinalizesVerifiedPrimary(t *testing.T) {
	base, managed := newWorktreeTestRepository(t)
	worker := prepareReviewReadyWorktree(t, base, managed)
	if err := os.WriteFile(
		filepath.Join(worker.WorktreeRoot, "worker.txt"), []byte("worker\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, worker.WorktreeRoot, "add", "worker.txt")
	worktreeTestGit(t, worker.WorktreeRoot, "commit", "-m", "worker change")
	review, err := ReviewWorktreeWorker(context.Background(), WorktreeReviewOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed,
		AgentID: worker.AgentID, WorkerID: worker.ID, ExpectedRevision: worker.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	key, err := readWorktreeOwnerKey(managed)
	if err != nil {
		t.Fatal(err)
	}
	worker, err = readWorktreeOwner(managed, key, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	transactionID := "tx-" + strings.Repeat("a", 24)
	now := time.Now().UTC()
	worker.State = WorktreeIntegrating
	worker.Integration = &WorktreeIntegrationTransaction{
		ID: transactionID, Phase: "prepared", ReviewDigest: review.PatchDigest,
		Strategy: "fast_forward", WorktreeLabel: transactionID,
		OriginalHead: review.CurrentBase, StartedAt: now, UpdatedAt: now,
	}
	worker.Revision++
	worker.UpdatedAt = now
	if err := persistWorktreeTransition(
		LocalOptions{CWD: base}, managed, key, worker,
	); err != nil {
		t.Fatal(err)
	}
	transactionRoot := filepath.Join(managed, transactionID)
	worktreeTestGit(t, base, "worktree", "add", "--detach", transactionRoot, review.CurrentBase)
	worktreeTestGit(t, transactionRoot, "merge", "--ff-only", review.HeadCommit)
	result := strings.TrimSpace(worktreeTestGit(t, transactionRoot, "rev-parse", "HEAD"))
	resultTree := strings.TrimSpace(worktreeTestGit(t, transactionRoot, "rev-parse", "HEAD^{tree}"))
	worker.Integration.Phase = "verified"
	worker.Integration.ResultCommit = result
	worker.Integration.ResultTree = resultTree
	worker.Integration.UpdatedAt = time.Now().UTC()
	worker.Revision++
	worker.UpdatedAt = time.Now().UTC()
	if err := persistWorktreeTransition(
		LocalOptions{CWD: base}, managed, key, worker,
	); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, base, "merge", "--ff-only", result)

	workers, err := ListWorktreeWorkers(context.Background(), WorktreeListOptions{
		Local: LocalOptions{CWD: base}, WorktreeRoot: managed, AgentID: worker.AgentID,
	})
	if err != nil || len(workers) != 1 {
		t.Fatalf("workers=%+v err=%v", workers, err)
	}
	if workers[0].State != WorktreeIntegrated ||
		workers[0].IntegrationCommit != result ||
		workers[0].Integration.Phase != "completed" {
		t.Fatalf("reconciled worker=%+v", workers[0])
	}
	if _, err := os.Lstat(transactionRoot); !os.IsNotExist(err) {
		t.Fatalf("reconciled transaction remains: %v", err)
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
