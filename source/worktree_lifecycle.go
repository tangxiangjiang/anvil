// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/anvil-dev/anvil/internal/store"
)

// WorktreeWorkerState is the persisted lifecycle state of one isolated attempt.
type WorktreeWorkerState string

const (
	WorktreeRequested     WorktreeWorkerState = "requested"
	WorktreePreparing     WorktreeWorkerState = "preparing"
	WorktreeReady         WorktreeWorkerState = "ready"
	WorktreeRunning       WorktreeWorkerState = "running"
	WorktreeReviewReady   WorktreeWorkerState = "review_ready"
	WorktreeInterrupted   WorktreeWorkerState = "interrupted"
	WorktreeIntegrating   WorktreeWorkerState = "integrating"
	WorktreeIntegrated    WorktreeWorkerState = "integrated"
	WorktreeDiscarded     WorktreeWorkerState = "discarded"
	WorktreeCleaning      WorktreeWorkerState = "cleaning"
	WorktreeRemoved       WorktreeWorkerState = "removed"
	WorktreeCleanupFailed WorktreeWorkerState = "cleanup_failed"
)

const (
	worktreeManifestSchema = 1
	defaultWorktreeQuota   = 8
	maxWorktreeQuota       = 64
	defaultWorktreeLease   = 5 * time.Minute
	worktreeGitTimeout     = 30 * time.Second
	maxWorktreeGitOutput   = 2 << 20
	maxWorktreeOwnerBytes  = 128 << 10
)

var worktreeIDGenerator = randomWorktreeID
var errWorktreeLifecycleLockBusy = errors.New("worktree lifecycle lock is busy")

// WorktreeWorker is the coordinator-owned lifecycle manifest. Absolute paths
// remain local persistence facts and are not model input.
type WorktreeWorker struct {
	Schema            int                 `json:"schema"`
	Revision          uint64              `json:"revision"`
	ID                string              `json:"id"`
	AgentID           string              `json:"agent_id"`
	ArtifactID        string              `json:"artifact_id"`
	WorkItemID        string              `json:"work_item_id"`
	ExecutionID       string              `json:"execution_id"`
	Attempt           int                 `json:"attempt"`
	State             WorktreeWorkerState `json:"state"`
	BaseRoot          string              `json:"base_root"`
	WorktreeRoot      string              `json:"worktree_root"`
	GitCommonDir      string              `json:"git_common_dir"`
	BaseRef           string              `json:"base_ref"`
	BaseCommit        string              `json:"base_commit"`
	Branch            string              `json:"branch"`
	HeadCommit        string              `json:"head_commit,omitempty"`
	OwnerHostID       string              `json:"owner_host_id"`
	LeaseUntil        time.Time           `json:"lease_until"`
	CheckpointID      string              `json:"checkpoint_id,omitempty"`
	Review            *WorktreeReview     `json:"review,omitempty"`
	IntegrationCommit string              `json:"integration_commit,omitempty"`
	CleanupPolicy     string              `json:"cleanup_policy,omitempty"`
	Failure           string              `json:"failure,omitempty"`
	CreatedAt         time.Time           `json:"created_at"`
	UpdatedAt         time.Time           `json:"updated_at"`
}

// WorktreePrepareOptions contains only coordinator/operator inputs. Directory
// and branch names are generated from a random worker identity.
type WorktreePrepareOptions struct {
	Local        LocalOptions
	WorktreeRoot string
	AgentID      string
	ArtifactID   string
	WorkItemID   string
	ExecutionID  string
	Attempt      int
	BaseRef      string
	OwnerHostID  string
	MaxWorkers   int
	Lease        time.Duration
}

type WorktreeListOptions struct {
	Local        LocalOptions
	WorktreeRoot string
	AgentID      string
}

type WorktreeCleanupOptions struct {
	Local                LocalOptions
	WorktreeRoot         string
	AgentID              string
	WorkerID             string
	ExpectedRevision     uint64
	ExpectedReviewDigest string
	RetainBranch         bool
}

type WorktreeCleanupResult struct {
	Worker         WorktreeWorker
	RemovedPaths   []string
	RetainedBranch bool
}

// WorktreeLifecycleError is safe for callers to classify without parsing Git
// output. Details never include environment variables or credentials.
type WorktreeLifecycleError struct {
	Code    string
	Message string
}

func (e *WorktreeLifecycleError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type worktreeRepository struct {
	baseRoot  string
	commonDir string
	commit    string
}

type worktreeOwnerEnvelope struct {
	Schema    int             `json:"schema"`
	Worker    json.RawMessage `json:"worker"`
	Signature string          `json:"signature"`
}

type worktreeLockRecord struct {
	Token      string    `json:"token"`
	PID        int       `json:"pid"`
	AcquiredAt time.Time `json:"acquired_at"`
}

type worktreeGitRunner struct {
	bin               string
	expectedCommonDir string
}

type worktreeBoundedBuffer struct {
	buffer    bytes.Buffer
	remaining int
	truncated bool
}

func (b *worktreeBoundedBuffer) Write(data []byte) (int, error) {
	count := len(data)
	available := b.remaining
	if b.remaining > 0 {
		part := data
		if len(part) > b.remaining {
			part = part[:b.remaining]
		}
		_, _ = b.buffer.Write(part)
		b.remaining -= len(part)
	}
	if count > available {
		b.truncated = true
	}
	return count, nil
}

// PrepareWorktreeWorker creates one owned branch and linked worktree after all
// repository, path, quota, and configuration checks pass.
func PrepareWorktreeWorker(
	ctx context.Context,
	opts WorktreePrepareOptions,
) (WorktreeWorker, error) {
	if err := ctx.Err(); err != nil {
		return WorktreeWorker{}, err
	}
	if err := validateWorktreePrepareOptions(&opts); err != nil {
		return WorktreeWorker{}, err
	}
	baseInput, err := resolveListCWD(opts.Local.CWD)
	if err != nil {
		return WorktreeWorker{}, err
	}
	baseRoot, err := canonicalExistingPath(baseInput)
	if err != nil {
		return WorktreeWorker{}, wtError("security", "canonicalize base repository: %v", err)
	}
	if err := rejectWorktreeReparsePath(opts.WorktreeRoot); err != nil {
		return WorktreeWorker{}, err
	}
	rootCandidate, err := canonicalFuturePath(opts.WorktreeRoot)
	if err != nil {
		return WorktreeWorker{}, wtError("security", "canonicalize worktree root: %v", err)
	}
	if pathWithinWorktree(baseRoot, rootCandidate) {
		return WorktreeWorker{}, wtError(
			"security", "worktree root must be outside the base checkout",
		)
	}

	git := worktreeGitRunner{}
	repository, err := inspectWorktreeRepository(ctx, git, baseRoot, opts.BaseRef)
	if err != nil {
		return WorktreeWorker{}, err
	}
	git.expectedCommonDir = repository.commonDir
	now := time.Now().UTC()
	workerID, err := worktreeIDGenerator("w")
	if err != nil {
		return WorktreeWorker{}, wtError("identity", "generate worker id: %v", err)
	}
	if opts.ExecutionID == "" {
		opts.ExecutionID, err = worktreeIDGenerator("exec")
		if err != nil {
			return WorktreeWorker{}, wtError("identity", "generate execution id: %v", err)
		}
	}
	branch := generatedWorktreeBranch(opts.AgentID, opts.WorkItemID, opts.Attempt, workerID)
	if _, err := git.run(ctx, baseRoot, "check-ref-format", "--branch", branch); err != nil {
		return WorktreeWorker{}, wtError("identity", "generated branch was invalid: %v", err)
	}
	exists, err := git.branchExists(ctx, baseRoot, branch)
	if err != nil {
		return WorktreeWorker{}, err
	}
	if exists {
		return WorktreeWorker{}, wtError("collision", "generated branch already exists")
	}
	destination := filepath.Join(rootCandidate, "wt-"+workerID)
	if err := requireAbsentPath(destination); err != nil {
		return WorktreeWorker{}, err
	}
	if err := createManagedWorktreeRoot(rootCandidate); err != nil {
		return WorktreeWorker{}, err
	}
	worktreeRoot, err := canonicalExistingPath(rootCandidate)
	if err != nil || !sameWorktreePath(worktreeRoot, rootCandidate) {
		return WorktreeWorker{}, wtError("security", "worktree root changed while it was created")
	}
	key, err := initializeWorktreeManager(worktreeRoot)
	if err != nil {
		return WorktreeWorker{}, err
	}
	release, err := acquireWorktreeLifecycleLock(worktreeRoot)
	if err != nil {
		return WorktreeWorker{}, err
	}
	defer release()

	// Recheck mutable repository facts under the lifecycle lock. Validation
	// before creating manager files avoids mutation for an ineligible repo;
	// validation here closes the race before Git changes refs.
	repository, err = inspectWorktreeRepository(ctx, git, baseRoot, opts.BaseRef)
	if err != nil {
		return WorktreeWorker{}, err
	}
	owners, err := loadWorktreeOwners(worktreeRoot, key)
	if err != nil {
		return WorktreeWorker{}, err
	}
	active := 0
	for _, owner := range owners {
		if owner.State != WorktreeRemoved {
			active++
		}
		if owner.AgentID == opts.AgentID &&
			owner.ArtifactID == opts.ArtifactID &&
			owner.WorkItemID == opts.WorkItemID &&
			owner.Attempt == opts.Attempt &&
			owner.State != WorktreeRemoved {
			return WorktreeWorker{}, wtError(
				"ownership", "work item attempt is already owned by worker %s", owner.ID,
			)
		}
	}
	if active >= opts.MaxWorkers {
		return WorktreeWorker{}, wtError(
			"quota", "worktree quota reached: %d active of %d", active, opts.MaxWorkers,
		)
	}
	exists, err = git.branchExists(ctx, baseRoot, branch)
	if err != nil {
		return WorktreeWorker{}, err
	}
	if exists {
		return WorktreeWorker{}, wtError("collision", "generated branch already exists")
	}
	destination = filepath.Join(worktreeRoot, "wt-"+workerID)
	if err := requireAbsentPath(destination); err != nil {
		return WorktreeWorker{}, err
	}
	lease := opts.Lease
	if lease <= 0 {
		lease = defaultWorktreeLease
	}
	worker := WorktreeWorker{
		Schema: worktreeManifestSchema, Revision: 1, ID: workerID,
		AgentID: opts.AgentID, ArtifactID: opts.ArtifactID, WorkItemID: opts.WorkItemID,
		ExecutionID: opts.ExecutionID, Attempt: opts.Attempt, State: WorktreeRequested,
		BaseRoot: repository.baseRoot, WorktreeRoot: destination,
		GitCommonDir: repository.commonDir, BaseRef: opts.BaseRef,
		BaseCommit: repository.commit, Branch: branch, OwnerHostID: opts.OwnerHostID,
		LeaseUntil: now.Add(lease), CreatedAt: now, UpdatedAt: now,
	}
	if err := writeInitialWorktreeOwner(worktreeRoot, key, worker); err != nil {
		return WorktreeWorker{}, err
	}
	if err := appendWorktreeStoreRecord(opts.Local, baseRoot, worker); err != nil {
		return worker, wtError("persistence", "record requested worktree: %v", err)
	}
	worker.State = WorktreePreparing
	worker.Revision++
	worker.UpdatedAt = time.Now().UTC()
	if err := persistWorktreeTransition(opts.Local, worktreeRoot, key, worker); err != nil {
		return worker, err
	}

	_, createErr := git.run(
		ctx, baseRoot, "worktree", "add", "-b", branch,
		destination, repository.commit,
	)
	if createErr != nil {
		return persistWorktreeFailure(
			opts.Local, worktreeRoot, key, worker, "git worktree add failed: "+createErr.Error(),
		)
	}
	if err := validateCreatedWorktree(ctx, git, worker); err != nil {
		return persistWorktreeFailure(
			opts.Local, worktreeRoot, key, worker, "created worktree validation failed: "+err.Error(),
		)
	}
	worker.State = WorktreeReady
	worker.HeadCommit = repository.commit
	worker.Failure = ""
	worker.Revision++
	worker.UpdatedAt = time.Now().UTC()
	if err := persistWorktreeTransition(opts.Local, worktreeRoot, key, worker); err != nil {
		return worker, err
	}
	return worker, nil
}

// ListWorktreeWorkers verifies every active recovery record before returning
// it. One malformed or forged record fails the whole discovery operation.
func ListWorktreeWorkers(
	ctx context.Context,
	opts WorktreeListOptions,
) ([]WorktreeWorker, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := canonicalExistingPath(opts.WorktreeRoot)
	if err != nil {
		return nil, wtError("security", "canonicalize worktree root: %v", err)
	}
	key, err := readWorktreeOwnerKey(root)
	if err != nil {
		return nil, err
	}
	release, err := acquireWorktreeLifecycleLock(root)
	if err != nil {
		return nil, err
	}
	defer release()
	owners, err := loadWorktreeOwners(root, key)
	if err != nil {
		return nil, err
	}
	out := owners[:0]
	for _, worker := range owners {
		if _, err := reconcileWorktreeStore(opts.Local, worker); err != nil {
			return nil, err
		}
		if opts.AgentID == "" || worker.AgentID == opts.AgentID {
			out = append(out, worker)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// CleanupWorktreeWorker removes only a signed, store-matched, clean worktree.
// It never force-removes a worktree or branch and never recursively deletes.
func CleanupWorktreeWorker(
	ctx context.Context,
	opts WorktreeCleanupOptions,
) (WorktreeCleanupResult, error) {
	if err := ctx.Err(); err != nil {
		return WorktreeCleanupResult{}, err
	}
	if opts.AgentID == "" || opts.WorkerID == "" || opts.ExpectedRevision == 0 {
		return WorktreeCleanupResult{}, wtError(
			"invalid", "agent_id, worker_id, and expected_revision are required",
		)
	}
	if !safeGeneratedWorktreeID(opts.WorkerID) {
		return WorktreeCleanupResult{}, wtError("invalid", "worker_id is invalid")
	}
	root, err := canonicalExistingPath(opts.WorktreeRoot)
	if err != nil {
		return WorktreeCleanupResult{}, wtError("security", "canonicalize worktree root: %v", err)
	}
	key, err := readWorktreeOwnerKey(root)
	if err != nil {
		return WorktreeCleanupResult{}, err
	}
	archivePath := filepath.Join(root, ".anvil", "archive", opts.WorkerID+".json")
	if _, archiveStatErr := os.Lstat(archivePath); archiveStatErr == nil {
		archived, archiveErr := readArchivedWorktreeOwner(root, key, opts.WorkerID)
		if archiveErr != nil {
			return WorktreeCleanupResult{}, archiveErr
		}
		if archived.AgentID != opts.AgentID {
			return WorktreeCleanupResult{}, wtError("ownership", "archived worker owner mismatch")
		}
		if archived.Revision != opts.ExpectedRevision {
			return WorktreeCleanupResult{}, wtError(
				"stale", "expected revision %d, archived revision is %d",
				opts.ExpectedRevision, archived.Revision,
			)
		}
		requestedPolicy := "delete_branch"
		if opts.RetainBranch {
			requestedPolicy = "retain_branch"
		}
		if archived.CleanupPolicy != requestedPolicy {
			return WorktreeCleanupResult{}, wtError(
				"policy", "archived cleanup policy is %s, requested %s",
				archived.CleanupPolicy, requestedPolicy,
			)
		}
		return WorktreeCleanupResult{
			Worker: archived, RetainedBranch: archived.CleanupPolicy == "retain_branch",
		}, nil
	} else if !os.IsNotExist(archiveStatErr) {
		return WorktreeCleanupResult{}, wtError(
			"ownership", "inspect archived ownership record: %v", archiveStatErr,
		)
	}
	release, err := acquireWorktreeLifecycleLock(root)
	if err != nil {
		return WorktreeCleanupResult{}, err
	}
	defer release()
	worker, err := readWorktreeOwner(root, key, opts.WorkerID)
	if err != nil {
		return WorktreeCleanupResult{}, err
	}
	if worker.AgentID != opts.AgentID {
		return WorktreeCleanupResult{}, wtError("ownership", "worker belongs to another agent")
	}
	if worker.Revision != opts.ExpectedRevision {
		return WorktreeCleanupResult{}, wtError(
			"stale", "expected revision %d, current revision is %d",
			opts.ExpectedRevision, worker.Revision,
		)
	}
	stored, err := reconcileWorktreeStore(opts.Local, worker)
	if err != nil {
		return WorktreeCleanupResult{}, err
	}
	if worker.State == WorktreeRemoved {
		storeCurrent := sameWorktreeManifest(worker, stored)
		if !storeCurrent && !canReconcileRemovedWorktree(stored, worker) {
			return WorktreeCleanupResult{}, wtError(
				"ownership", "removed recovery record does not follow stored cleaning state",
			)
		}
		git := worktreeGitRunner{expectedCommonDir: worker.GitCommonDir}
		present, verifyErr := verifyCleanupPreconditions(ctx, git, root, worker, true)
		if verifyErr != nil {
			return WorktreeCleanupResult{}, verifyErr
		}
		if present {
			return WorktreeCleanupResult{}, wtError(
				"ownership", "removed manifest still has a live worktree",
			)
		}
		exists, branchErr := git.branchExists(ctx, worker.BaseRoot, worker.Branch)
		if branchErr != nil {
			return WorktreeCleanupResult{}, branchErr
		}
		if (worker.CleanupPolicy == "retain_branch") != exists {
			return WorktreeCleanupResult{}, wtError(
				"ownership", "removed manifest does not match retained branch state",
			)
		}
		if !storeCurrent {
			if err := appendWorktreeStoreRecord(opts.Local, worker.BaseRoot, worker); err != nil {
				return WorktreeCleanupResult{}, wtError(
					"persistence", "reconcile removed worktree: %v", err,
				)
			}
		}
		if err := archiveWorktreeOwner(root, worker.ID); err != nil {
			return WorktreeCleanupResult{}, err
		}
		return WorktreeCleanupResult{
			Worker: worker, RetainedBranch: worker.CleanupPolicy == "retain_branch",
		}, nil
	}
	if !sameWorktreeManifest(worker, stored) {
		return WorktreeCleanupResult{}, wtError(
			"ownership", "recovery and session ownership records do not match",
		)
	}
	if !cleanupEligibleState(worker.State) {
		return WorktreeCleanupResult{}, wtError(
			"state", "worker state %q is not eligible for cleanup", worker.State,
		)
	}
	git := worktreeGitRunner{expectedCommonDir: worker.GitCommonDir}
	recovering := worker.State == WorktreeCleaning
	requestedPolicy := "delete_branch"
	if opts.RetainBranch {
		requestedPolicy = "retain_branch"
	}
	if recovering && worker.CleanupPolicy == "" {
		return WorktreeCleanupResult{}, wtError(
			"ownership", "cleaning state has no persisted cleanup policy",
		)
	}
	if worker.CleanupPolicy != "" && worker.CleanupPolicy != requestedPolicy {
		return WorktreeCleanupResult{}, wtError(
			"stale", "cleanup policy differs from the persisted request",
		)
	}
	if worker.State == WorktreeDiscarded {
		if worker.Review == nil ||
			opts.ExpectedReviewDigest == "" ||
			worker.Review.PatchDigest != opts.ExpectedReviewDigest {
			return WorktreeCleanupResult{}, wtError(
				"stale", "discard cleanup requires the exact reviewed patch digest",
			)
		}
		if err := sanitizeReviewedDiscard(ctx, git, worker); err != nil {
			return WorktreeCleanupResult{}, err
		}
	}
	present, err := verifyCleanupPreconditions(ctx, git, root, worker, recovering)
	if err != nil {
		return WorktreeCleanupResult{}, err
	}
	if !recovering {
		worker.State = WorktreeCleaning
		worker.CleanupPolicy = requestedPolicy
		worker.Revision++
		worker.UpdatedAt = time.Now().UTC()
		if err := persistWorktreeTransition(opts.Local, root, key, worker); err != nil {
			return WorktreeCleanupResult{}, err
		}
	}

	if present {
		if _, err := git.run(ctx, worker.BaseRoot, "worktree", "remove", worker.WorktreeRoot); err != nil {
			failed, persistErr := persistCleanupFailure(
				opts.Local, root, key, worker, "git worktree remove failed: "+err.Error(),
			)
			if persistErr != nil {
				return WorktreeCleanupResult{}, persistErr
			}
			return WorktreeCleanupResult{Worker: failed}, wtError("cleanup", "%s", failed.Failure)
		}
	}
	var removed []string
	if present {
		removed = append(removed, worker.WorktreeRoot)
	}
	if stat, statErr := os.Lstat(worker.WorktreeRoot); statErr == nil {
		if !stat.IsDir() {
			failed, persistErr := persistCleanupFailure(
				opts.Local, root, key, worker, "worktree path remained as a non-directory",
			)
			if persistErr != nil {
				return WorktreeCleanupResult{}, persistErr
			}
			return WorktreeCleanupResult{Worker: failed}, wtError("cleanup", "%s", failed.Failure)
		}
		if removeErr := os.Remove(worker.WorktreeRoot); removeErr != nil {
			failed, persistErr := persistCleanupFailure(
				opts.Local, root, key, worker, "worktree directory was not empty after Git removal",
			)
			if persistErr != nil {
				return WorktreeCleanupResult{}, persistErr
			}
			return WorktreeCleanupResult{Worker: failed}, wtError("cleanup", "%s", failed.Failure)
		}
	} else if !os.IsNotExist(statErr) {
		return WorktreeCleanupResult{}, wtError("cleanup", "inspect removed worktree: %v", statErr)
	}

	retained := worker.CleanupPolicy == "retain_branch"
	branchExists, err := git.branchExists(ctx, worker.BaseRoot, worker.Branch)
	if err != nil {
		return WorktreeCleanupResult{}, err
	}
	if !retained && branchExists {
		if _, err := git.run(ctx, worker.BaseRoot, "branch", "-d", worker.Branch); err != nil {
			// `-d` is deliberately non-forcing. A branch Git cannot prove safe
			// remains as the recovery artifact instead of failing destructively.
			retained = true
			worker.CleanupPolicy = "retain_branch"
			worker.Failure = "branch retained because Git refused safe deletion"
		}
	} else if retained && !branchExists {
		failed, persistErr := persistCleanupFailure(
			opts.Local, root, key, worker, "retained branch disappeared during cleanup",
		)
		if persistErr != nil {
			return WorktreeCleanupResult{}, persistErr
		}
		return WorktreeCleanupResult{Worker: failed}, wtError("cleanup", "%s", failed.Failure)
	}
	worker.State = WorktreeRemoved
	worker.Revision++
	worker.LeaseUntil = time.Time{}
	worker.UpdatedAt = time.Now().UTC()
	if err := persistWorktreeTransition(opts.Local, root, key, worker); err != nil {
		return WorktreeCleanupResult{}, err
	}
	if err := archiveWorktreeOwner(root, worker.ID); err != nil {
		return WorktreeCleanupResult{}, err
	}
	return WorktreeCleanupResult{
		Worker: worker, RemovedPaths: removed, RetainedBranch: retained,
	}, nil
}

func canReconcileRemovedWorktree(previous, current WorktreeWorker) bool {
	return previous.State == WorktreeCleaning &&
		current.State == WorktreeRemoved &&
		current.Revision == previous.Revision+1 &&
		previous.Schema == current.Schema &&
		previous.ID == current.ID &&
		previous.AgentID == current.AgentID &&
		previous.ArtifactID == current.ArtifactID &&
		previous.WorkItemID == current.WorkItemID &&
		previous.ExecutionID == current.ExecutionID &&
		previous.Attempt == current.Attempt &&
		sameStoredWorktreePath(previous.BaseRoot, current.BaseRoot) &&
		sameStoredWorktreePath(previous.WorktreeRoot, current.WorktreeRoot) &&
		sameStoredWorktreePath(previous.GitCommonDir, current.GitCommonDir) &&
		previous.BaseRef == current.BaseRef &&
		previous.BaseCommit == current.BaseCommit &&
		previous.Branch == current.Branch &&
		previous.HeadCommit == current.HeadCommit &&
		previous.OwnerHostID == current.OwnerHostID &&
		previous.CreatedAt.Equal(current.CreatedAt)
}

func validateWorktreePrepareOptions(opts *WorktreePrepareOptions) error {
	if opts == nil {
		return wtError("invalid", "worktree options are required")
	}
	for field, value := range map[string]string{
		"agent_id": opts.AgentID, "artifact_id": opts.ArtifactID,
		"work_item_id": opts.WorkItemID, "owner_host_id": opts.OwnerHostID,
	} {
		if err := validateWorktreeIdentity(field, value); err != nil {
			return err
		}
	}
	if opts.ExecutionID != "" {
		if err := validateWorktreeIdentity("execution_id", opts.ExecutionID); err != nil {
			return err
		}
	}
	if strings.TrimSpace(opts.WorktreeRoot) == "" {
		return wtError("invalid", "worktree_root is required")
	}
	if opts.Attempt <= 0 {
		opts.Attempt = 1
	}
	if opts.Attempt > 9999 {
		return wtError("invalid", "attempt exceeds 9999")
	}
	if opts.BaseRef == "" {
		opts.BaseRef = "HEAD"
	}
	if err := validateWorktreeBaseRef(opts.BaseRef); err != nil {
		return err
	}
	if opts.MaxWorkers <= 0 {
		opts.MaxWorkers = defaultWorktreeQuota
	}
	if opts.MaxWorkers > maxWorktreeQuota {
		return wtError("quota", "max_workers exceeds hard limit %d", maxWorktreeQuota)
	}
	if opts.Lease > 24*time.Hour {
		return wtError("invalid", "lease exceeds 24 hours")
	}
	return nil
}

func validateWorktreeIdentity(field, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return wtError("invalid", "%s is required", field)
	}
	if len(value) > 256 || strings.IndexByte(value, 0) >= 0 {
		return wtError("invalid", "%s is invalid", field)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return wtError("invalid", "%s contains control characters", field)
		}
	}
	return nil
}

func validateWorktreeBaseRef(ref string) error {
	if ref == "HEAD" {
		return nil
	}
	if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, " \t\r\n\x00~^:?*[\\") {
		return wtError("invalid", "base_ref is not a plain local branch or full ref")
	}
	if strings.Contains(ref, "..") || strings.HasSuffix(ref, "/") {
		return wtError("invalid", "base_ref is invalid")
	}
	return nil
}

func inspectWorktreeRepository(
	ctx context.Context,
	git worktreeGitRunner,
	baseRoot, baseRef string,
) (worktreeRepository, error) {
	info, err := os.Lstat(filepath.Join(baseRoot, ".git"))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return worktreeRepository{}, wtError(
			"security", "base must be a primary Git checkout with a .git directory",
		)
	}
	topRaw, err := git.run(ctx, baseRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return worktreeRepository{}, wtError("repository", "resolve Git top-level: %v", err)
	}
	top, err := canonicalExistingPath(strings.TrimSpace(topRaw))
	if err != nil || !sameWorktreePath(top, baseRoot) {
		return worktreeRepository{}, wtError(
			"security", "Git top-level does not equal the configured base root",
		)
	}
	commonRaw, err := git.run(ctx, baseRoot, "rev-parse", "--git-common-dir")
	if err != nil {
		return worktreeRepository{}, wtError("repository", "resolve Git common dir: %v", err)
	}
	commonInput := strings.TrimSpace(commonRaw)
	if !filepath.IsAbs(commonInput) {
		commonInput = filepath.Join(baseRoot, commonInput)
	}
	common, err := canonicalExistingPath(commonInput)
	if err != nil || !sameWorktreePath(common, filepath.Join(baseRoot, ".git")) {
		return worktreeRepository{}, wtError(
			"security", "Git common dir is outside the primary checkout",
		)
	}
	if _, err := os.Lstat(filepath.Join(baseRoot, ".gitmodules")); err == nil {
		return worktreeRepository{}, wtError("unsupported", "submodules are not supported")
	} else if !os.IsNotExist(err) {
		return worktreeRepository{}, wtError("repository", "inspect .gitmodules: %v", err)
	}
	if err := rejectExecutingGitConfig(ctx, git, baseRoot); err != nil {
		return worktreeRepository{}, err
	}
	status, err := git.run(
		ctx, baseRoot, "status", "--porcelain=v1", "--untracked-files=all",
	)
	if err != nil {
		return worktreeRepository{}, wtError("repository", "read base status: %v", err)
	}
	if strings.TrimSpace(status) != "" {
		return worktreeRepository{}, wtError("dirty", "base checkout is not clean")
	}
	commit, err := git.run(
		ctx, baseRoot, "rev-parse", "--verify", "--end-of-options", baseRef+"^{commit}",
	)
	if err != nil {
		return worktreeRepository{}, wtError("repository", "resolve base_ref: %v", err)
	}
	commit = strings.TrimSpace(commit)
	if !isHexObjectID(commit) {
		return worktreeRepository{}, wtError("repository", "base_ref did not resolve to an object id")
	}
	return worktreeRepository{baseRoot: top, commonDir: common, commit: commit}, nil
}

func rejectExecutingGitConfig(
	ctx context.Context,
	git worktreeGitRunner,
	baseRoot string,
) error {
	raw, err := git.run(ctx, baseRoot, "config", "--local", "--no-includes", "--null", "--list")
	if err != nil {
		return wtError("repository", "read local Git config: %v", err)
	}
	for _, entry := range strings.Split(raw, "\x00") {
		key, value, _ := strings.Cut(entry, "\n")
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "core.sparsecheckout" && strings.EqualFold(strings.TrimSpace(value), "true") {
			return wtError("unsupported", "sparse checkout is not supported")
		}
		switch {
		case strings.HasPrefix(key, "include."),
			strings.HasPrefix(key, "includeif."),
			strings.HasPrefix(key, "filter."),
			strings.HasPrefix(key, "extensions."),
			key == "core.worktree",
			key == "core.fsmonitor",
			key == "core.hookspath",
			key == "core.sshcommand",
			strings.HasPrefix(key, "credential."),
			(strings.HasPrefix(key, "diff.") &&
				(strings.HasSuffix(key, ".command") || strings.HasSuffix(key, ".textconv"))),
			(strings.HasPrefix(key, "merge.") && strings.HasSuffix(key, ".driver")):
			return wtError("unsupported", "repository has executable or unsupported config %q", key)
		}
	}
	return nil
}

func validateCreatedWorktree(
	ctx context.Context,
	git worktreeGitRunner,
	worker WorktreeWorker,
) error {
	root, err := canonicalExistingPath(worker.WorktreeRoot)
	if err != nil || !sameWorktreePath(root, worker.WorktreeRoot) {
		return wtError("security", "created worktree path changed")
	}
	top, err := git.run(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	topCanonical, err := canonicalExistingPath(strings.TrimSpace(top))
	if err != nil || !sameWorktreePath(topCanonical, worker.WorktreeRoot) {
		return wtError("security", "created worktree reports another top-level")
	}
	commonRaw, err := git.run(ctx, root, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	commonInput := strings.TrimSpace(commonRaw)
	if !filepath.IsAbs(commonInput) {
		commonInput = filepath.Join(root, commonInput)
	}
	common, err := canonicalExistingPath(commonInput)
	if err != nil || !sameWorktreePath(common, worker.GitCommonDir) {
		return wtError("ownership", "created worktree has another Git common dir")
	}
	branch, err := git.run(ctx, root, "branch", "--show-current")
	if err != nil || strings.TrimSpace(branch) != worker.Branch {
		return wtError("ownership", "created worktree checked out another branch")
	}
	head, err := git.run(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil || strings.TrimSpace(head) != worker.BaseCommit {
		return wtError("ownership", "created worktree HEAD differs from recorded base")
	}
	return nil
}

func verifyCleanupPreconditions(
	ctx context.Context,
	git worktreeGitRunner,
	managedRoot string,
	worker WorktreeWorker,
	allowRemoved bool,
) (bool, error) {
	base, err := canonicalExistingPath(worker.BaseRoot)
	if err != nil || !sameWorktreePath(base, worker.BaseRoot) {
		return false, wtError("ownership", "base repository identity changed")
	}
	if !sameWorktreePath(filepath.Dir(worker.WorktreeRoot), managedRoot) ||
		filepath.Base(worker.WorktreeRoot) != "wt-"+worker.ID {
		return false, wtError("ownership", "worker path is not the generated owned path")
	}
	repository, err := inspectWorktreeRepository(ctx, git, base, "HEAD")
	if err != nil {
		return false, err
	}
	if !sameWorktreePath(repository.commonDir, worker.GitCommonDir) {
		return false, wtError("ownership", "Git common dir changed")
	}
	list, err := git.run(ctx, base, "worktree", "list", "--porcelain")
	if err != nil {
		return false, wtError("repository", "list worktrees: %v", err)
	}
	root, err := canonicalExistingPath(worker.WorktreeRoot)
	if err != nil {
		partial := worker.State == WorktreeRequested ||
			worker.State == WorktreePreparing ||
			(worker.State == WorktreeCleanupFailed && worker.HeadCommit == "")
		if (!allowRemoved && !partial) || !os.IsNotExist(err) {
			return false, wtError("ownership", "owned worktree is missing or redirected")
		}
		if worktreeListContainsPath(list, worker.WorktreeRoot) {
			return false, wtError("ownership", "missing path remains registered as a worktree")
		}
		exists, branchErr := git.branchExists(ctx, base, worker.Branch)
		if branchErr != nil {
			return false, branchErr
		}
		if exists {
			if worker.State == WorktreeRequested {
				return false, wtError(
					"ownership", "requested worker unexpectedly owns a branch",
				)
			}
			head, headErr := git.run(
				ctx, base, "rev-parse", "--verify", "refs/heads/"+worker.Branch,
			)
			if headErr != nil || strings.TrimSpace(head) != expectedWorktreeHead(worker) {
				return false, wtError("stale", "worker branch changed during cleanup")
			}
		}
		return false, nil
	}
	if !sameWorktreePath(root, worker.WorktreeRoot) {
		return false, wtError("ownership", "owned worktree is redirected")
	}
	if worker.State == WorktreeRequested {
		return false, wtError(
			"ownership", "requested worker unexpectedly has a worktree path",
		)
	}
	if !worktreeListOwns(list, worker.WorktreeRoot, worker.Branch) {
		return false, wtError("ownership", "Git does not attribute the path and branch to this worker")
	}
	commonRaw, err := git.run(ctx, root, "rev-parse", "--git-common-dir")
	if err != nil {
		return false, wtError("ownership", "read worker common dir: %v", err)
	}
	commonInput := strings.TrimSpace(commonRaw)
	if !filepath.IsAbs(commonInput) {
		commonInput = filepath.Join(root, commonInput)
	}
	common, err := canonicalExistingPath(commonInput)
	if err != nil || !sameWorktreePath(common, worker.GitCommonDir) {
		return false, wtError("ownership", "worker Git common dir does not match ownership record")
	}
	head, err := git.run(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil || strings.TrimSpace(head) != expectedWorktreeHead(worker) {
		return false, wtError("stale", "worker HEAD changed after ownership was recorded")
	}
	branch, err := git.run(ctx, root, "branch", "--show-current")
	if err != nil || strings.TrimSpace(branch) != worker.Branch {
		return false, wtError("ownership", "worker branch changed")
	}
	status, err := git.run(
		ctx, root, "status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching",
	)
	if err != nil {
		return false, wtError("repository", "read worker status: %v", err)
	}
	if strings.TrimSpace(status) != "" {
		return false, wtError(
			"dirty",
			"worker has tracked, untracked, or ignored files; explicit reviewed discard is required",
		)
	}
	return true, nil
}

func expectedWorktreeHead(worker WorktreeWorker) string {
	if worker.HeadCommit != "" {
		return worker.HeadCommit
	}
	return worker.BaseCommit
}

func worktreeListOwns(porcelain, root, branch string) bool {
	var path, ref string
	check := func() bool {
		if path == "" {
			return false
		}
		canonical, err := canonicalExistingPath(path)
		return err == nil && sameWorktreePath(canonical, root) && ref == "refs/heads/"+branch
	}
	for _, line := range strings.Split(strings.ReplaceAll(porcelain, "\r\n", "\n"), "\n") {
		if line == "" {
			if check() {
				return true
			}
			path, ref = "", ""
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			path = strings.TrimPrefix(line, "worktree ")
		} else if strings.HasPrefix(line, "branch ") {
			ref = strings.TrimPrefix(line, "branch ")
		}
	}
	return check()
}

func worktreeListContainsPath(porcelain, root string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(porcelain, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "worktree ") &&
			sameWorktreePath(strings.TrimPrefix(line, "worktree "), root) {
			return true
		}
	}
	return false
}

func cleanupEligibleState(state WorktreeWorkerState) bool {
	switch state {
	case WorktreeRequested, WorktreePreparing, WorktreeReady,
		WorktreeReviewReady, WorktreeInterrupted,
		WorktreeIntegrated, WorktreeDiscarded, WorktreeCleaning, WorktreeCleanupFailed:
		return true
	default:
		return false
	}
}

func persistWorktreeFailure(
	local LocalOptions,
	root string,
	key []byte,
	worker WorktreeWorker,
	message string,
) (WorktreeWorker, error) {
	worker.State = WorktreeCleanupFailed
	worker.Failure = message
	worker.Revision++
	worker.UpdatedAt = time.Now().UTC()
	if err := persistWorktreeTransition(local, root, key, worker); err != nil {
		return worker, err
	}
	return worker, wtError("prepare", "%s", message)
}

func persistCleanupFailure(
	local LocalOptions,
	root string,
	key []byte,
	worker WorktreeWorker,
	message string,
) (WorktreeWorker, error) {
	worker.State = WorktreeCleanupFailed
	worker.Failure = message
	worker.Revision++
	worker.UpdatedAt = time.Now().UTC()
	if err := persistWorktreeTransition(local, root, key, worker); err != nil {
		return worker, err
	}
	return worker, nil
}

func persistWorktreeTransition(
	local LocalOptions,
	root string,
	key []byte,
	worker WorktreeWorker,
) error {
	if err := writeWorktreeOwner(root, key, worker); err != nil {
		return err
	}
	if err := appendWorktreeStoreRecord(local, worker.BaseRoot, worker); err != nil {
		return wtError("persistence", "record worktree transition: %v", err)
	}
	return nil
}

// reconcileWorktreeStore repairs only a signed owner record that is exactly
// one valid lifecycle transition ahead of the append-only session projection.
// This makes an owner-file publication followed by a store crash recoverable
// without accepting identity, repository, or path changes.
func reconcileWorktreeStore(
	local LocalOptions,
	worker WorktreeWorker,
) (WorktreeWorker, error) {
	stored, err := latestStoredWorktree(local, worker.BaseRoot, worker.AgentID, worker.ID)
	if err != nil {
		var lifecycleErr *WorktreeLifecycleError
		if errors.As(err, &lifecycleErr) &&
			lifecycleErr.Code == "ownership" &&
			worker.Revision == 1 &&
			worker.State == WorktreeRequested {
			if err := appendWorktreeStoreRecord(local, worker.BaseRoot, worker); err != nil {
				return WorktreeWorker{}, wtError(
					"persistence", "recover initial worktree projection: %v", err,
				)
			}
			return projectWorktreeForStore(worker), nil
		}
		return WorktreeWorker{}, err
	}
	if sameWorktreeManifest(worker, stored) {
		return stored, nil
	}
	if !validWorktreeStoreTransition(stored, worker) {
		return WorktreeWorker{}, wtError(
			"ownership", "recovery and session ownership records do not match",
		)
	}
	if err := appendWorktreeStoreRecord(local, worker.BaseRoot, worker); err != nil {
		return WorktreeWorker{}, wtError(
			"persistence", "reconcile worktree transition: %v", err,
		)
	}
	return projectWorktreeForStore(worker), nil
}

func validWorktreeStoreTransition(previous, current WorktreeWorker) bool {
	allowed := false
	switch previous.State {
	case WorktreeRequested:
		allowed = current.State == WorktreePreparing || current.State == WorktreeCleaning
	case WorktreePreparing:
		allowed = current.State == WorktreeReady ||
			current.State == WorktreeCleanupFailed ||
			current.State == WorktreeCleaning
	case WorktreeReady:
		allowed = current.State == WorktreeRunning || current.State == WorktreeCleaning
	case WorktreeRunning:
		allowed = current.State == WorktreeReviewReady ||
			current.State == WorktreeInterrupted ||
			current.State == WorktreeCleaning
	case WorktreeReviewReady:
		allowed = current.State == WorktreeReviewReady ||
			current.State == WorktreeIntegrating ||
			current.State == WorktreeDiscarded ||
			current.State == WorktreeCleaning
	case WorktreeInterrupted:
		allowed = current.State == WorktreeInterrupted ||
			current.State == WorktreeDiscarded ||
			current.State == WorktreeCleaning
	case WorktreeIntegrated, WorktreeDiscarded:
		allowed = current.State == WorktreeCleaning
	case WorktreeIntegrating:
		allowed = current.State == WorktreeIntegrated ||
			current.State == WorktreeReviewReady
	case WorktreeCleaning:
		allowed = current.State == WorktreeRemoved ||
			current.State == WorktreeCleanupFailed
	case WorktreeCleanupFailed:
		allowed = current.State == WorktreeCleaning
	}
	headValid := previous.HeadCommit == current.HeadCommit ||
		(previous.State == WorktreePreparing &&
			current.State == WorktreeReady &&
			current.HeadCommit == current.BaseCommit) ||
		(current.State == previous.State &&
			(current.State == WorktreeReviewReady ||
				current.State == WorktreeInterrupted) &&
			current.Review != nil &&
			current.Review.BaseCommit == current.BaseCommit &&
			current.Review.HeadCommit == current.HeadCommit)
	policyValid := previous.CleanupPolicy == current.CleanupPolicy ||
		(current.State == WorktreeCleaning &&
			previous.CleanupPolicy == "" &&
			(current.CleanupPolicy == "delete_branch" ||
				current.CleanupPolicy == "retain_branch")) ||
		(previous.State == WorktreeCleaning &&
			current.State == WorktreeRemoved &&
			previous.CleanupPolicy == "delete_branch" &&
			current.CleanupPolicy == "retain_branch")
	checkpointValid := previous.CheckpointID == current.CheckpointID ||
		(previous.State == WorktreeReady &&
			current.State == WorktreeRunning &&
			previous.CheckpointID == "" &&
			current.CheckpointID != "")
	reviewValid := sameWorktreeReview(previous.Review, current.Review) ||
		((previous.State == WorktreeReviewReady ||
			previous.State == WorktreeInterrupted) &&
			current.State == previous.State &&
			current.Review != nil &&
			current.Review.ManifestRevision == current.Revision) ||
		(previous.State == WorktreeIntegrating &&
			current.State == WorktreeReviewReady &&
			current.Review == nil)
	integrationValid := previous.IntegrationCommit == current.IntegrationCommit ||
		(previous.State == WorktreeIntegrating &&
			current.State == WorktreeIntegrated &&
			current.IntegrationCommit != "")
	return allowed &&
		headValid &&
		policyValid &&
		checkpointValid &&
		reviewValid &&
		integrationValid &&
		current.Revision == previous.Revision+1 &&
		previous.Schema == current.Schema &&
		previous.ID == current.ID &&
		previous.AgentID == current.AgentID &&
		previous.ArtifactID == current.ArtifactID &&
		previous.WorkItemID == current.WorkItemID &&
		previous.ExecutionID == current.ExecutionID &&
		previous.Attempt == current.Attempt &&
		sameStoredWorktreePath(previous.BaseRoot, current.BaseRoot) &&
		sameStoredWorktreePath(previous.WorktreeRoot, current.WorktreeRoot) &&
		sameStoredWorktreePath(previous.GitCommonDir, current.GitCommonDir) &&
		previous.BaseRef == current.BaseRef &&
		previous.BaseCommit == current.BaseCommit &&
		previous.Branch == current.Branch &&
		previous.OwnerHostID == current.OwnerHostID &&
		previous.CreatedAt.Equal(current.CreatedAt) &&
		!current.UpdatedAt.Before(previous.UpdatedAt)
}

func sameWorktreeReview(left, right *WorktreeReview) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	return bytes.Equal(a, b)
}

func appendWorktreeStoreRecord(
	local LocalOptions,
	baseRoot string,
	worker WorktreeWorker,
) error {
	projection := projectWorktreeForStore(worker)
	payload, err := json.Marshal(projection)
	if err != nil {
		return err
	}
	backend, err := store.Open(baseRoot, local.Store)
	if err != nil {
		return err
	}
	defer backend.Close()
	return backend.Append(worker.AgentID, store.Record{
		Type: store.TypeWorktree, Time: worker.UpdatedAt,
		AgentID: worker.AgentID, ArtifactID: worker.ArtifactID,
		ItemID: worker.WorkItemID, ExecutionID: worker.ExecutionID,
		Revision: int(worker.Revision), State: string(worker.State),
		Worktree: payload,
	})
}

func latestStoredWorktree(
	local LocalOptions,
	baseRoot, agentID, workerID string,
) (WorktreeWorker, error) {
	backend, err := store.Open(baseRoot, local.Store)
	if err != nil {
		return WorktreeWorker{}, wtError("persistence", "open session store: %v", err)
	}
	defer backend.Close()
	records, err := backend.Load(agentID)
	if err != nil {
		return WorktreeWorker{}, wtError("ownership", "load session ownership: %v", err)
	}
	var found WorktreeWorker
	ok := false
	for _, record := range records {
		if record.Type != store.TypeWorktree || len(record.Worktree) == 0 {
			continue
		}
		var worker WorktreeWorker
		if json.Unmarshal(record.Worktree, &worker) != nil || worker.ID != workerID {
			continue
		}
		worker = projectWorktreeForStore(worker)
		if ok && worker.Revision == found.Revision && !sameWorktreeManifest(worker, found) {
			return WorktreeWorker{}, wtError(
				"ownership", "session ownership has conflicting records at revision %d",
				worker.Revision,
			)
		}
		if !ok || worker.Revision > found.Revision {
			found, ok = worker, true
		}
	}
	if !ok {
		return WorktreeWorker{}, wtError("ownership", "session ownership record is missing")
	}
	return found, nil
}

func initializeWorktreeManager(root string) ([]byte, error) {
	manager := filepath.Join(root, ".anvil")
	if err := ensurePrivateDirectory(manager); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(filepath.Join(manager, "owners")); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(filepath.Join(manager, "archive")); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(filepath.Join(manager, "tmp")); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(manager, "owner.key")
	info, err := os.Lstat(keyPath)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, wtError("ownership", "owner key is not a regular file")
		}
		if err := secureWorktreePath(keyPath, false); err != nil {
			return nil, wtError("ownership", "owner key access is not private: %v", err)
		}
		return readWorktreeOwnerKey(root)
	}
	if !os.IsNotExist(err) {
		return nil, wtError("ownership", "inspect owner key: %v", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, wtError("identity", "generate owner key: %v", err)
	}
	file, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, wtError("ownership", "create owner key: %v", err)
	}
	if err := secureWorktreePath(keyPath, false); err != nil {
		_ = file.Close()
		_ = os.Remove(keyPath)
		return nil, wtError("ownership", "secure owner key: %v", err)
	}
	if _, err := file.Write(key); err != nil {
		_ = file.Close()
		return nil, wtError("ownership", "write owner key: %v", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, wtError("ownership", "sync owner key: %v", err)
	}
	if err := file.Close(); err != nil {
		return nil, wtError("ownership", "close owner key: %v", err)
	}
	if err := syncWorktreeDirectory(manager); err != nil {
		return nil, wtError("ownership", "sync owner key directory: %v", err)
	}
	return key, nil
}

func readWorktreeOwnerKey(root string) ([]byte, error) {
	path := filepath.Join(root, ".anvil", "owner.key")
	info, err := os.Lstat(path)
	if err != nil {
		return nil, wtError("ownership", "read owner key: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, wtError("ownership", "owner key is not a regular file")
	}
	if info.Size() != 32 {
		return nil, wtError("ownership", "owner key has an invalid size")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, wtError("ownership", "owner key permissions are not private")
	}
	if err := secureWorktreePath(path, false); err != nil {
		return nil, wtError("ownership", "owner key access is not private: %v", err)
	}
	key, err := os.ReadFile(path)
	if err != nil || len(key) != 32 {
		return nil, wtError("ownership", "owner key is unreadable or invalid")
	}
	return key, nil
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return wtError("security", "%s is not a real directory", path)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			return wtError("security", "%s permissions are not private", path)
		}
		if err := secureWorktreePath(path, true); err != nil {
			return wtError("security", "%s access is not private: %v", path, err)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return wtError("security", "inspect managed directory: %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		return wtError("security", "create managed directory: %v", err)
	}
	if err := secureWorktreePath(path, true); err != nil {
		return wtError("security", "secure managed directory: %v", err)
	}
	if err := syncWorktreeDirectory(filepath.Dir(path)); err != nil {
		return wtError("persistence", "sync managed directory parent: %v", err)
	}
	return nil
}

func createManagedWorktreeRoot(path string) error {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		if err := os.Mkdir(path, 0o700); err != nil {
			return wtError("security", "create worktree root: %v", err)
		}
	} else if err != nil {
		return wtError("security", "inspect worktree root: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return wtError("security", "worktree root is not a real directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return wtError("security", "worktree root permissions are not private")
	}
	if err := secureWorktreePath(path, true); err != nil {
		return wtError("security", "secure worktree root: %v", err)
	}
	return nil
}

func acquireWorktreeLifecycleLock(root string) (func(), error) {
	path := filepath.Join(root, ".anvil", "lifecycle.lock")
	token, err := randomWorktreeID("lock")
	if err != nil {
		return nil, wtError("lock", "generate lock token: %v", err)
	}
	record, _ := json.Marshal(worktreeLockRecord{
		Token: token, PID: os.Getpid(), AcquiredAt: time.Now().UTC(),
	})
	lock, err := lockWorktreeLifecycleFile(path)
	if err != nil {
		if errors.Is(err, errWorktreeLifecycleLockBusy) {
			return nil, wtError("busy", "worktree lifecycle is locked")
		}
		return nil, wtError("lock", "create lifecycle lock: %v", err)
	}
	if err := lock.write(record); err != nil {
		_ = lock.release()
		return nil, wtError("lock", "persist lifecycle lock: %v", err)
	}
	release := func() {
		_ = lock.release()
	}
	return release, nil
}

func writeInitialWorktreeOwner(root string, key []byte, worker WorktreeWorker) error {
	data, err := marshalWorktreeOwner(key, worker)
	if err != nil {
		return err
	}
	path := worktreeOwnerPath(root, worker.ID)
	temp, err := writeWorktreeOwnerTemp(root, worker.ID, data)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	if err := os.Link(temp, path); err != nil {
		return wtError("ownership", "publish initial ownership record: %v", err)
	}
	if err := syncWorktreeDirectory(filepath.Dir(path)); err != nil {
		return wtError("ownership", "sync initial ownership record: %v", err)
	}
	return nil
}

func writeWorktreeOwner(root string, key []byte, worker WorktreeWorker) error {
	data, err := marshalWorktreeOwner(key, worker)
	if err != nil {
		return err
	}
	path := worktreeOwnerPath(root, worker.ID)
	temp, err := writeWorktreeOwnerTemp(root, worker.ID, data)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	if err := atomicReplace(temp, path); err != nil {
		return wtError("ownership", "replace ownership record: %v", err)
	}
	if err := syncWorktreeDirectory(filepath.Dir(path)); err != nil {
		return wtError("ownership", "sync ownership record: %v", err)
	}
	return nil
}

func writeWorktreeOwnerTemp(root, workerID string, data []byte) (string, error) {
	token, err := randomWorktreeID("tmp")
	if err != nil {
		return "", wtError("ownership", "generate ownership temp name: %v", err)
	}
	temp := filepath.Join(
		root, ".anvil", "tmp",
		workerID+"-"+strconv.Itoa(os.Getpid())+"-"+token,
	)
	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", wtError("ownership", "create ownership update: %v", err)
	}
	if err := secureWorktreePath(temp, false); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return "", wtError("ownership", "secure ownership update: %v", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return "", wtError("ownership", "write ownership update: %v", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return "", wtError("ownership", "sync ownership update: %v", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return "", wtError("ownership", "close ownership update: %v", err)
	}
	if err := syncWorktreeDirectory(filepath.Dir(temp)); err != nil {
		_ = os.Remove(temp)
		return "", wtError("ownership", "sync ownership update directory: %v", err)
	}
	return temp, nil
}

func marshalWorktreeOwner(key []byte, worker WorktreeWorker) ([]byte, error) {
	payload, err := json.Marshal(worker)
	if err != nil {
		return nil, wtError("ownership", "marshal ownership record: %v", err)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	envelope := worktreeOwnerEnvelope{
		Schema: worktreeManifestSchema, Worker: payload,
		Signature: hex.EncodeToString(mac.Sum(nil)),
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, wtError("ownership", "marshal ownership envelope: %v", err)
	}
	return append(data, '\n'), nil
}

func readWorktreeOwner(root string, key []byte, workerID string) (WorktreeWorker, error) {
	if !safeGeneratedWorktreeID(workerID) {
		return WorktreeWorker{}, wtError("invalid", "worker_id is invalid")
	}
	return readWorktreeOwnerFile(worktreeOwnerPath(root, workerID), key)
}

func readArchivedWorktreeOwner(root string, key []byte, workerID string) (WorktreeWorker, error) {
	if !safeGeneratedWorktreeID(workerID) {
		return WorktreeWorker{}, wtError("invalid", "worker_id is invalid")
	}
	return readWorktreeOwnerFile(filepath.Join(root, ".anvil", "archive", workerID+".json"), key)
}

func readWorktreeOwnerFile(path string, key []byte) (WorktreeWorker, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return WorktreeWorker{}, wtError("ownership", "ownership record is missing")
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return WorktreeWorker{}, wtError("ownership", "ownership record is not a regular file")
	}
	if info.Size() <= 0 || info.Size() > maxWorktreeOwnerBytes {
		return WorktreeWorker{}, wtError("ownership", "ownership record size is invalid")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return WorktreeWorker{}, wtError("ownership", "ownership record permissions are not private")
	}
	if err := secureWorktreePath(path, false); err != nil {
		return WorktreeWorker{}, wtError("ownership", "ownership record access is not private: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return WorktreeWorker{}, wtError("ownership", "read ownership record: %v", err)
	}
	var envelope worktreeOwnerEnvelope
	if json.Unmarshal(data, &envelope) != nil ||
		envelope.Schema != worktreeManifestSchema ||
		len(envelope.Worker) == 0 {
		return WorktreeWorker{}, wtError("ownership", "ownership record is malformed")
	}
	signature, err := hex.DecodeString(envelope.Signature)
	if err != nil {
		return WorktreeWorker{}, wtError("ownership", "ownership signature is malformed")
	}
	var worker WorktreeWorker
	if json.Unmarshal(envelope.Worker, &worker) != nil ||
		worker.Schema != worktreeManifestSchema ||
		!safeGeneratedWorktreeID(worker.ID) {
		return WorktreeWorker{}, wtError("ownership", "ownership manifest is invalid")
	}
	canonical, err := json.Marshal(worker)
	if err != nil {
		return WorktreeWorker{}, wtError("ownership", "canonicalize ownership manifest")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(canonical)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return WorktreeWorker{}, wtError("ownership", "ownership signature does not match")
	}
	return worker, nil
}

func loadWorktreeOwners(root string, key []byte) ([]WorktreeWorker, error) {
	dir := filepath.Join(root, ".anvil", "owners")
	handle, err := os.Open(dir)
	if err != nil {
		return nil, wtError("ownership", "list ownership records: %v", err)
	}
	defer handle.Close()
	entries, err := handle.ReadDir(maxWorktreeQuota + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, wtError("ownership", "list ownership records: %v", err)
	}
	if len(entries) > maxWorktreeQuota {
		return nil, wtError("bounds", "ownership record count exceeds %d", maxWorktreeQuota)
	}
	owners := make([]WorktreeWorker, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return nil, wtError("ownership", "unexpected file in ownership directory")
		}
		worker, err := readWorktreeOwnerFile(filepath.Join(dir, entry.Name()), key)
		if err != nil {
			return nil, err
		}
		if entry.Name() != worker.ID+".json" {
			return nil, wtError("ownership", "ownership filename does not match worker id")
		}
		owners = append(owners, worker)
	}
	return owners, nil
}

func archiveWorktreeOwner(root, workerID string) error {
	source := worktreeOwnerPath(root, workerID)
	target := filepath.Join(root, ".anvil", "archive", workerID+".json")
	if _, err := os.Lstat(target); err == nil {
		return wtError("ownership", "ownership archive already exists")
	} else if !os.IsNotExist(err) {
		return wtError("ownership", "inspect ownership archive: %v", err)
	}
	if err := os.Rename(source, target); err != nil {
		return wtError("ownership", "archive ownership record: %v", err)
	}
	if err := syncWorktreeDirectory(filepath.Dir(source)); err != nil {
		return wtError("ownership", "sync ownership directory: %v", err)
	}
	if err := syncWorktreeDirectory(filepath.Dir(target)); err != nil {
		return wtError("ownership", "sync ownership archive: %v", err)
	}
	return nil
}

func worktreeOwnerPath(root, workerID string) string {
	return filepath.Join(root, ".anvil", "owners", workerID+".json")
}

func sameWorktreeManifest(a, b WorktreeWorker) bool {
	if isWorktreeStoreProjection(a) || isWorktreeStoreProjection(b) {
		a = projectWorktreeForStore(a)
		b = projectWorktreeForStore(b)
	}
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

func projectWorktreeForStore(worker WorktreeWorker) WorktreeWorker {
	if isWorktreeStoreProjection(worker) {
		return worker
	}
	worker.BaseRoot = worktreePathDigest(worker.BaseRoot)
	worker.WorktreeRoot = worktreePathDigest(worker.WorktreeRoot)
	worker.GitCommonDir = worktreePathDigest(worker.GitCommonDir)
	return worker
}

func isWorktreeStoreProjection(worker WorktreeWorker) bool {
	return strings.HasPrefix(worker.BaseRoot, "sha256:") &&
		strings.HasPrefix(worker.WorktreeRoot, "sha256:") &&
		strings.HasPrefix(worker.GitCommonDir, "sha256:")
}

func worktreePathDigest(path string) string {
	if strings.HasPrefix(path, "sha256:") {
		return path
	}
	canonical := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		canonical = strings.ToLower(canonical)
	}
	digest := sha256.Sum256([]byte(canonical))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func sameStoredWorktreePath(stored, actual string) bool {
	if strings.HasPrefix(stored, "sha256:") {
		return hmac.Equal([]byte(stored), []byte(worktreePathDigest(actual)))
	}
	return sameWorktreePath(stored, actual)
}

func canonicalExistingPath(name string) (string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	resolved, err := resolveWorktreeReparsePoints(abs)
	if err != nil {
		return "", err
	}
	return finalWorktreePath(resolved)
}

func canonicalFuturePath(name string) (string, error) {
	if strings.TrimSpace(name) == "" || strings.IndexByte(name, 0) >= 0 {
		return "", errors.New("path is empty or contains NUL")
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	probe := filepath.Clean(abs)
	var missing []string
	for {
		if _, err := os.Lstat(probe); err == nil {
			canonical, err := canonicalExistingPath(probe)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				canonical = filepath.Join(canonical, missing[i])
			}
			return filepath.Clean(canonical), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", fmt.Errorf("no existing ancestor for %s", name)
		}
		missing = append(missing, filepath.Base(probe))
		probe = parent
	}
}

func rejectWorktreeReparsePath(name string) error {
	abs, err := filepath.Abs(name)
	if err != nil {
		return wtError("security", "resolve worktree root: %v", err)
	}
	volume := filepath.VolumeName(abs)
	rest := strings.TrimLeft(strings.TrimPrefix(abs, volume), `\/`)
	parts := strings.FieldsFunc(rest, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	current := volume + string(filepath.Separator)
	if volume == "" {
		current = string(filepath.Separator)
	}
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			return nil
		}
		if statErr != nil {
			return wtError("security", "inspect worktree root component: %v", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return wtError("security", "worktree root contains a symbolic link or junction")
		}
		if _, linkErr := os.Readlink(current); linkErr == nil {
			return wtError("security", "worktree root contains a symbolic link or junction")
		}
	}
	return nil
}

func resolveWorktreeReparsePoints(name string) (string, error) {
	for depth := 0; depth < 40; depth++ {
		next, changed := resolveFirstWorktreeReparse(name)
		if !changed {
			return next, nil
		}
		name = next
	}
	return "", fmt.Errorf("too many levels of links in %s", name)
}

func resolveFirstWorktreeReparse(name string) (string, bool) {
	volume := filepath.VolumeName(name)
	rest := strings.TrimLeft(strings.TrimPrefix(name, volume), `\/`)
	parts := strings.FieldsFunc(rest, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	current := volume + string(filepath.Separator)
	if volume == "" {
		current = string(filepath.Separator)
	}
	for index, part := range parts {
		current = filepath.Join(current, part)
		target, err := os.Readlink(current)
		if err != nil {
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(current), target)
		}
		if index+1 < len(parts) {
			target = filepath.Join(target, filepath.Join(parts[index+1:]...))
		}
		return target, true
	}
	return name, false
}

func pathWithinWorktree(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(rel) || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func sameWorktreePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func requireAbsentPath(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return wtError("collision", "worktree destination already exists")
	} else if !os.IsNotExist(err) {
		return wtError("security", "inspect worktree destination: %v", err)
	}
	return nil
}

func generatedWorktreeBranch(agentID, itemID string, attempt int, workerID string) string {
	agent := sanitizeWorktreeRefPart(agentID, 12)
	item := sanitizeWorktreeRefPart(itemID, 24)
	short := strings.TrimPrefix(workerID, "w-")
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("anvil/%s/%s/%d-%s", agent, item, attempt, short)
}

func sanitizeWorktreeRefPart(value string, limit int) string {
	var out strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(value) {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			out.WriteRune(r)
			lastDash = false
		} else if !lastDash && out.Len() > 0 {
			out.WriteByte('-')
			lastDash = true
		}
		if out.Len() >= limit {
			break
		}
	}
	result := strings.Trim(out.String(), "-.")
	if result == "" {
		return "item"
	}
	return result
}

func randomWorktreeID(prefix string) (string, error) {
	var data [12]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(data[:]), nil
}

func safeGeneratedWorktreeID(value string) bool {
	prefix, rest, ok := strings.Cut(value, "-")
	if !ok || prefix != "w" || len(rest) == 0 || len(rest) > 64 {
		return false
	}
	for _, r := range rest {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

func isHexObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range strings.ToLower(value) {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

func (g worktreeGitRunner) run(
	ctx context.Context,
	cwd string,
	args ...string,
) (string, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, worktreeGitTimeout)
	defer cancel()
	bin := g.bin
	if bin == "" {
		bin = "git"
	}
	top, gitDir, _, err := g.pinnedIdentity(timeoutCtx, bin, cwd)
	if err != nil {
		return "", err
	}
	fixed := []string{
		"--no-pager",
		"-c", "color.ui=false",
		"-c", "core.quotepath=false",
		"-c", "core.fsmonitor=",
		"-c", "core.hooksPath=",
		"-c", "core.attributesFile=" + os.DevNull,
		"-c", "credential.helper=",
		"-c", "diff.external=",
		"--git-dir=" + gitDir,
		"--work-tree=" + top,
	}
	command := exec.CommandContext(timeoutCtx, bin, append(fixed, args...)...)
	command.Dir = top
	command.Env = worktreeGitEnvironment()
	stdout := &worktreeBoundedBuffer{remaining: maxWorktreeGitOutput}
	stderr := &worktreeBoundedBuffer{remaining: 64 << 10}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if timeoutCtx.Err() != nil {
			return "", fmt.Errorf("git operation timed out")
		}
		message := strings.TrimSpace(stderr.buffer.String())
		if message == "" {
			message = err.Error()
		}
		return stdout.buffer.String(), fmt.Errorf("git %s: %s", strings.Join(args, " "), message)
	}
	if stdout.truncated || stderr.truncated {
		return "", wtError("bounds", "Git output exceeded the lifecycle limit")
	}
	return stdout.buffer.String(), nil
}

func (g worktreeGitRunner) pinnedIdentity(
	ctx context.Context,
	bin, cwd string,
) (string, string, string, error) {
	command := exec.CommandContext(
		ctx, bin,
		"--no-pager",
		"-c", "core.hooksPath=",
		"-c", "credential.helper=",
		"-C", cwd,
		"rev-parse", "--show-toplevel", "--absolute-git-dir", "--git-common-dir",
	)
	command.Env = worktreeGitEnvironment()
	output, err := command.Output()
	if err != nil {
		return "", "", "", wtError("repository", "resolve pinned Git identity: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != 3 {
		return "", "", "", wtError("repository", "Git identity output is malformed")
	}
	top, err := canonicalExistingPath(strings.TrimSpace(lines[0]))
	if err != nil {
		return "", "", "", wtError("repository", "canonicalize Git work tree: %v", err)
	}
	gitDir, err := canonicalExistingPath(strings.TrimSpace(lines[1]))
	if err != nil {
		return "", "", "", wtError("repository", "canonicalize Git directory: %v", err)
	}
	commonInput := strings.TrimSpace(lines[2])
	if !filepath.IsAbs(commonInput) {
		commonInput = filepath.Join(top, commonInput)
	}
	commonDir, err := canonicalExistingPath(commonInput)
	if err != nil {
		return "", "", "", wtError("repository", "canonicalize Git common directory: %v", err)
	}
	if g.expectedCommonDir != "" &&
		!sameWorktreePath(commonDir, g.expectedCommonDir) {
		return "", "", "", wtError("ownership", "Git repository identity changed")
	}
	return top, gitDir, commonDir, nil
}

func (g worktreeGitRunner) branchExists(
	ctx context.Context,
	cwd, branch string,
) (bool, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, worktreeGitTimeout)
	defer cancel()
	bin := g.bin
	if bin == "" {
		bin = "git"
	}
	top, gitDir, _, pinErr := g.pinnedIdentity(timeoutCtx, bin, cwd)
	if pinErr != nil {
		return false, pinErr
	}
	fixed := []string{
		"--no-pager", "-c", "core.hooksPath=", "-c", "credential.helper=",
		"--git-dir=" + gitDir, "--work-tree=" + top,
		"show-ref", "--verify", "--quiet", "refs/heads/" + branch,
	}
	command := exec.CommandContext(timeoutCtx, bin, fixed...)
	command.Dir = top
	command.Env = worktreeGitEnvironment()
	err := command.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	if timeoutCtx.Err() != nil {
		return false, wtError("repository", "branch collision check timed out or was cancelled")
	}
	return false, wtError("repository", "check branch collision: %v", err)
}

func worktreeGitEnvironment() []string {
	keep := map[string]bool{
		"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "SYSTEMDRIVE": true,
		"COMSPEC": true, "TMP": true, "TEMP": true, "HOME": true, "USERPROFILE": true,
	}
	env := make([]string, 0, len(keep)+8)
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if ok && keep[strings.ToUpper(key)] {
			env = append(env, item)
		}
	}
	return append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_PAGER=cat",
		"GIT_PROTOCOL_FROM_USER=0",
	)
}

func wtError(code, format string, args ...any) error {
	return &WorktreeLifecycleError{Code: code, Message: fmt.Sprintf(format, args...)}
}
