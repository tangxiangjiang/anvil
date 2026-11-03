// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/verify"
)

const worktreeReviewSchema = 1

type WorktreeIntegrationTransaction struct {
	ID            string    `json:"id"`
	Phase         string    `json:"phase"`
	ReviewDigest  string    `json:"review_digest"`
	Strategy      string    `json:"strategy"`
	WorktreeLabel string    `json:"worktree_label"`
	OriginalHead  string    `json:"original_head"`
	ResultCommit  string    `json:"result_commit,omitempty"`
	ResultTree    string    `json:"result_tree,omitempty"`
	Failure       string    `json:"failure,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type WorktreeCommitInfo struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
}

type WorktreeChangedPath struct {
	Status  string `json:"status"`
	Path    string `json:"path"`
	OldPath string `json:"old_path,omitempty"`
}

type WorktreeConflict struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Message string `json:"message,omitempty"`
}

// WorktreeReview is a path-relative, immutable integration preflight. Its
// digest includes committed and uncommitted content, status, and the exact
// base/head/revision tuple, so confirmation cannot be replayed after a change.
type WorktreeReview struct {
	Schema            int                   `json:"schema"`
	WorkerID          string                `json:"worker_id"`
	ManifestRevision  uint64                `json:"manifest_revision"`
	BaseCommit        string                `json:"base_commit"`
	CurrentBase       string                `json:"current_base"`
	HeadCommit        string                `json:"head_commit"`
	Commits           []WorktreeCommitInfo  `json:"commits"`
	ChangedPaths      []WorktreeChangedPath `json:"changed_paths"`
	DiffStat          string                `json:"diff_stat,omitempty"`
	PatchDigest       string                `json:"patch_digest"`
	DirtyTracked      []string              `json:"dirty_tracked"`
	Untracked         []string              `json:"untracked"`
	Ignored           []string              `json:"ignored"`
	Conflicts         []WorktreeConflict    `json:"conflicts"`
	AllowedStrategies []string              `json:"allowed_strategies"`
	Integrable        bool                  `json:"integrable"`
	RefusalReasons    []string              `json:"refusal_reasons"`
	Interrupted       bool                  `json:"interrupted"`
	ReviewedAt        time.Time             `json:"reviewed_at"`
}

type WorktreeReviewOptions struct {
	Local            LocalOptions
	WorktreeRoot     string
	AgentID          string
	WorkerID         string
	ExpectedRevision uint64
}

type WorktreeIntegrateOptions struct {
	Local               LocalOptions
	WorktreeRoot        string
	AgentID             string
	WorkerID            string
	ExpectedRevision    uint64
	ExpectedBase        string
	ExpectedHead        string
	ExpectedPatchDigest string
	Strategy            string
}

type WorktreeIntegrateResult struct {
	Worker       WorktreeWorker `json:"worker"`
	ResultCommit string         `json:"result_commit,omitempty"`
}

type WorktreeDiscardOptions struct {
	Local               LocalOptions
	WorktreeRoot        string
	AgentID             string
	WorkerID            string
	ExpectedRevision    uint64
	ExpectedPatchDigest string
}

func ReviewWorktreeWorker(
	ctx context.Context,
	opts WorktreeReviewOptions,
) (WorktreeReview, error) {
	if err := validateWorktreeDecisionOptions(
		opts.AgentID, opts.WorkerID, opts.ExpectedRevision,
	); err != nil {
		return WorktreeReview{}, err
	}
	root, key, worker, release, err := loadWorktreeDecisionOwner(
		ctx, opts.Local, opts.WorktreeRoot, opts.AgentID, opts.WorkerID,
	)
	if err != nil {
		return WorktreeReview{}, err
	}
	defer release()
	if worker.Revision != opts.ExpectedRevision {
		return WorktreeReview{}, staleWorktreeRevision(opts.ExpectedRevision, worker.Revision)
	}
	if worker.State != WorktreeReviewReady && worker.State != WorktreeInterrupted {
		return WorktreeReview{}, wtError(
			"state", "worker state %q cannot be reviewed", worker.State,
		)
	}
	git := worktreeGitRunner{expectedCommonDir: worker.GitCommonDir}
	head, err := verifiedWorktreeReviewHead(ctx, git, root, worker)
	if err != nil {
		return WorktreeReview{}, err
	}
	if head != worker.HeadCommit {
		worker.HeadCommit = head
	}
	review, err := buildWorktreeReview(ctx, git, worker, worker.Revision+1, time.Now().UTC())
	if err != nil {
		return WorktreeReview{}, err
	}
	worker.Review = &review
	worker.Revision++
	worker.UpdatedAt = review.ReviewedAt
	if err := persistWorktreeTransition(opts.Local, root, key, worker); err != nil {
		return WorktreeReview{}, err
	}
	return review, nil
}

func IntegrateWorktreeWorker(
	ctx context.Context,
	opts WorktreeIntegrateOptions,
) (WorktreeIntegrateResult, error) {
	if err := validateWorktreeDecisionOptions(
		opts.AgentID, opts.WorkerID, opts.ExpectedRevision,
	); err != nil {
		return WorktreeIntegrateResult{}, err
	}
	if opts.ExpectedBase == "" || opts.ExpectedHead == "" ||
		opts.ExpectedPatchDigest == "" {
		return WorktreeIntegrateResult{}, wtError(
			"invalid", "expected_base, expected_head, and expected_patch_digest are required",
		)
	}
	if opts.Strategy != "fast_forward" &&
		opts.Strategy != "merge_no_ff" &&
		opts.Strategy != "cherry_pick" {
		return WorktreeIntegrateResult{}, wtError("invalid", "unsupported integration strategy")
	}
	root, key, worker, release, err := loadWorktreeDecisionOwner(
		ctx, opts.Local, opts.WorktreeRoot, opts.AgentID, opts.WorkerID,
	)
	if err != nil {
		return WorktreeIntegrateResult{}, err
	}
	defer release()
	if worker.Revision != opts.ExpectedRevision {
		return WorktreeIntegrateResult{}, staleWorktreeRevision(
			opts.ExpectedRevision, worker.Revision,
		)
	}
	if worker.State != WorktreeReviewReady {
		return WorktreeIntegrateResult{}, wtError(
			"state", "worker state %q cannot be integrated", worker.State,
		)
	}
	if worker.Review == nil ||
		worker.Review.ManifestRevision != worker.Revision ||
		worker.Review.CurrentBase != opts.ExpectedBase ||
		worker.Review.HeadCommit != opts.ExpectedHead ||
		worker.Review.PatchDigest != opts.ExpectedPatchDigest {
		return WorktreeIntegrateResult{}, wtError(
			"stale", "integration confirmation does not match the current review",
		)
	}
	git := worktreeGitRunner{expectedCommonDir: worker.GitCommonDir}
	current, err := buildWorktreeReview(
		ctx, git, worker, worker.Review.ManifestRevision, time.Now().UTC(),
	)
	if err != nil {
		return WorktreeIntegrateResult{}, err
	}
	if !sameWorktreeReviewBinding(*worker.Review, current) {
		return WorktreeIntegrateResult{}, wtError(
			"stale", "repository or worker content changed after review",
		)
	}
	if !current.Integrable || !containsWorktreeString(current.AllowedStrategies, opts.Strategy) {
		return WorktreeIntegrateResult{}, wtError(
			"conflict", "review does not permit strategy %q", opts.Strategy,
		)
	}

	transactionID, err := worktreeIDGenerator("tx")
	if err != nil {
		return WorktreeIntegrateResult{}, wtError("identity", "generate integration id: %v", err)
	}
	now := time.Now().UTC()
	worker.State = WorktreeIntegrating
	worker.LeaseUntil = time.Now().UTC().Add(defaultWorktreeLease)
	worker.Failure = ""
	worker.Integration = &WorktreeIntegrationTransaction{
		ID: transactionID, Phase: "prepared",
		ReviewDigest: current.PatchDigest, Strategy: opts.Strategy,
		WorktreeLabel: transactionID, OriginalHead: current.CurrentBase,
		StartedAt: now, UpdatedAt: now,
	}
	worker.Revision++
	worker.UpdatedAt = now
	if err := persistWorktreeTransition(opts.Local, root, key, worker); err != nil {
		return WorktreeIntegrateResult{}, err
	}

	resultCommit, safelyAborted, integrateErr := applyReviewedWorktreeTransaction(
		ctx, git, opts.Local, root, key, &worker, current,
	)
	if integrateErr != nil {
		if !safelyAborted {
			if worker.Integration != nil &&
				worker.Integration.Phase != "recovery_required" {
				worker.Integration.Phase = "recovery_required"
				worker.Integration.Failure = integrateErr.Error()
				worker.Integration.UpdatedAt = time.Now().UTC()
				worker.Failure = "integration recovery requires operator review"
				worker.LeaseUntil = time.Time{}
				if persistErr := persistIntegrationPhase(
					opts.Local, root, key, &worker,
				); persistErr != nil {
					return WorktreeIntegrateResult{Worker: worker}, persistErr
				}
			}
			return WorktreeIntegrateResult{Worker: worker}, integrateErr
		}
		worker.State = WorktreeReviewReady
		worker.LeaseUntil = time.Time{}
		worker.Review = nil
		worker.Integration.Phase = "aborted"
		worker.Integration.Failure = integrateErr.Error()
		worker.Integration.UpdatedAt = time.Now().UTC()
		worker.Failure = "integration failed and was safely aborted: " + integrateErr.Error()
		worker.Revision++
		worker.UpdatedAt = time.Now().UTC()
		if err := persistWorktreeTransition(opts.Local, root, key, worker); err != nil {
			return WorktreeIntegrateResult{}, err
		}
		return WorktreeIntegrateResult{Worker: worker}, wtError(
			"integration", "%s", worker.Failure,
		)
	}

	worker.State = WorktreeIntegrated
	worker.LeaseUntil = time.Time{}
	worker.Integration.Phase = "completed"
	worker.Integration.UpdatedAt = time.Now().UTC()
	worker.IntegrationCommit = resultCommit
	worker.Failure = ""
	worker.Revision++
	worker.UpdatedAt = time.Now().UTC()
	if err := persistWorktreeTransition(opts.Local, root, key, worker); err != nil {
		return WorktreeIntegrateResult{}, err
	}
	return WorktreeIntegrateResult{Worker: worker, ResultCommit: resultCommit}, nil
}

func DiscardWorktreeWorker(
	ctx context.Context,
	opts WorktreeDiscardOptions,
) (WorktreeWorker, error) {
	if err := validateWorktreeDecisionOptions(
		opts.AgentID, opts.WorkerID, opts.ExpectedRevision,
	); err != nil {
		return WorktreeWorker{}, err
	}
	if opts.ExpectedPatchDigest == "" {
		return WorktreeWorker{}, wtError("invalid", "expected_patch_digest is required")
	}
	root, key, worker, release, err := loadWorktreeDecisionOwner(
		ctx, opts.Local, opts.WorktreeRoot, opts.AgentID, opts.WorkerID,
	)
	if err != nil {
		return WorktreeWorker{}, err
	}
	defer release()
	if worker.Revision != opts.ExpectedRevision {
		return WorktreeWorker{}, staleWorktreeRevision(opts.ExpectedRevision, worker.Revision)
	}
	if worker.State != WorktreeReviewReady && worker.State != WorktreeInterrupted {
		return WorktreeWorker{}, wtError(
			"state", "worker state %q cannot be discarded", worker.State,
		)
	}
	if worker.Review == nil ||
		worker.Review.ManifestRevision != worker.Revision ||
		worker.Review.PatchDigest != opts.ExpectedPatchDigest {
		return WorktreeWorker{}, wtError(
			"stale", "discard confirmation does not match the current review",
		)
	}
	git := worktreeGitRunner{expectedCommonDir: worker.GitCommonDir}
	current, err := buildWorktreeReview(
		ctx, git, worker, worker.Review.ManifestRevision, time.Now().UTC(),
	)
	if err != nil {
		return WorktreeWorker{}, err
	}
	if !sameWorktreeReviewBinding(*worker.Review, current) {
		return WorktreeWorker{}, wtError(
			"stale", "worker content changed after discard review",
		)
	}
	worker.State = WorktreeDiscarded
	worker.LeaseUntil = time.Time{}
	worker.Failure = ""
	worker.Revision++
	worker.UpdatedAt = time.Now().UTC()
	if err := persistWorktreeTransition(opts.Local, root, key, worker); err != nil {
		return WorktreeWorker{}, err
	}
	return worker, nil
}

func validateWorktreeDecisionOptions(agentID, workerID string, revision uint64) error {
	if agentID == "" || workerID == "" || revision == 0 {
		return wtError("invalid", "agent_id, worker_id, and expected_revision are required")
	}
	if err := validateWorktreeIdentity("agent_id", agentID); err != nil {
		return err
	}
	if !safeGeneratedWorktreeID(workerID) {
		return wtError("invalid", "worker_id is invalid")
	}
	return nil
}

func loadWorktreeDecisionOwner(
	ctx context.Context,
	local LocalOptions,
	worktreeRoot, agentID, workerID string,
) (string, []byte, WorktreeWorker, func(), error) {
	if err := ctx.Err(); err != nil {
		return "", nil, WorktreeWorker{}, nil, err
	}
	if err := rejectWorktreeReparsePath(worktreeRoot); err != nil {
		return "", nil, WorktreeWorker{}, nil, err
	}
	root, err := canonicalExistingPath(worktreeRoot)
	if err != nil {
		return "", nil, WorktreeWorker{}, nil,
			wtError("security", "canonicalize worktree root: %v", err)
	}
	key, err := readWorktreeOwnerKey(root)
	if err != nil {
		return "", nil, WorktreeWorker{}, nil, err
	}
	release, err := acquireWorktreeLifecycleLock(root)
	if err != nil {
		return "", nil, WorktreeWorker{}, nil, err
	}
	worker, err := readWorktreeOwner(root, key, workerID)
	if err != nil {
		release()
		return "", nil, WorktreeWorker{}, nil, err
	}
	if worker.AgentID != agentID {
		release()
		return "", nil, WorktreeWorker{}, nil,
			wtError("ownership", "worker belongs to another agent")
	}
	if _, err := reconcileWorktreeStore(local, worker); err != nil {
		release()
		return "", nil, WorktreeWorker{}, nil, err
	}
	return root, key, worker, release, nil
}

func verifiedWorktreeReviewHead(
	ctx context.Context,
	git worktreeGitRunner,
	managerRoot string,
	worker WorktreeWorker,
) (string, error) {
	base, err := canonicalExistingPath(worker.BaseRoot)
	if err != nil || !sameWorktreePath(base, worker.BaseRoot) {
		return "", wtError("ownership", "base repository identity changed")
	}
	worktree, err := canonicalExistingPath(worker.WorktreeRoot)
	if err != nil || !sameWorktreePath(worktree, worker.WorktreeRoot) ||
		!sameWorktreePath(filepath.Dir(worktree), managerRoot) ||
		filepath.Base(worktree) != "wt-"+worker.ID {
		return "", wtError("ownership", "worker path identity changed")
	}
	list, err := git.run(ctx, base, "worktree", "list", "--porcelain")
	if err != nil || !worktreeListOwns(list, worktree, worker.Branch) {
		return "", wtError("ownership", "Git no longer owns the worker path and branch")
	}
	branch, err := git.run(ctx, worktree, "branch", "--show-current")
	if err != nil || strings.TrimSpace(branch) != worker.Branch {
		return "", wtError("ownership", "worker branch changed")
	}
	headRaw, err := git.run(ctx, worktree, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", wtError("repository", "read worker HEAD: %v", err)
	}
	head := strings.TrimSpace(headRaw)
	ancestorRaw, err := git.run(
		ctx, worktree, "merge-base", "--all", worker.BaseCommit, head,
	)
	if err != nil || strings.TrimSpace(ancestorRaw) != worker.BaseCommit {
		return "", wtError(
			"ownership", "worker HEAD is not a descendant of its recorded base",
		)
	}
	return head, nil
}

func buildWorktreeReview(
	ctx context.Context,
	git worktreeGitRunner,
	worker WorktreeWorker,
	manifestRevision uint64,
	reviewedAt time.Time,
) (WorktreeReview, error) {
	headRaw, err := git.run(ctx, worker.WorktreeRoot, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return WorktreeReview{}, wtError("repository", "read worker HEAD: %v", err)
	}
	head := strings.TrimSpace(headRaw)
	if head != worker.HeadCommit {
		return WorktreeReview{}, wtError("stale", "worker HEAD changed before review")
	}
	baseRaw, err := git.run(ctx, worker.BaseRoot, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return WorktreeReview{}, wtError("repository", "read current base HEAD: %v", err)
	}
	currentBase := strings.TrimSpace(baseRaw)

	workerStatus, err := git.run(
		ctx, worker.WorktreeRoot, "status", "--porcelain=v1", "-z",
		"--untracked-files=all", "--ignored=matching",
	)
	if err != nil {
		return WorktreeReview{}, wtError("repository", "read worker status: %v", err)
	}
	dirty, untracked, ignored, err := parseWorktreeStatus(workerStatus)
	if err != nil {
		return WorktreeReview{}, err
	}
	baseStatus, err := git.run(
		ctx, worker.BaseRoot, "status", "--porcelain=v1", "-z",
		"--untracked-files=all",
	)
	if err != nil {
		return WorktreeReview{}, wtError("repository", "read base status: %v", err)
	}
	baseDirty, baseUntracked, _, err := parseWorktreeStatus(baseStatus)
	if err != nil {
		return WorktreeReview{}, err
	}

	logRaw, err := git.run(
		ctx, worker.WorktreeRoot, "log", "--format=%H%x00%s%x00",
		worker.BaseCommit+".."+head,
	)
	if err != nil {
		return WorktreeReview{}, wtError("repository", "read worker commits: %v", err)
	}
	commits := parseWorktreeCommits(logRaw)
	changedRaw, err := git.run(
		ctx, worker.WorktreeRoot, "diff", "--name-status", "-z",
		worker.BaseCommit, head,
	)
	if err != nil {
		return WorktreeReview{}, wtError("repository", "read changed paths: %v", err)
	}
	changed, err := parseWorktreeChangedPaths(changedRaw)
	if err != nil {
		return WorktreeReview{}, err
	}
	stat, err := git.run(
		ctx, worker.WorktreeRoot, "diff", "--stat", "--no-ext-diff",
		worker.BaseCommit, head,
	)
	if err != nil {
		return WorktreeReview{}, wtError("repository", "read diff stat: %v", err)
	}
	committedPatch, err := git.run(
		ctx, worker.WorktreeRoot, "diff", "--binary", "--no-ext-diff",
		worker.BaseCommit, head,
	)
	if err != nil {
		return WorktreeReview{}, wtError("repository", "read reviewed patch: %v", err)
	}
	dirtyPatch, err := git.run(
		ctx, worker.WorktreeRoot, "diff", "--binary", "--no-ext-diff", "HEAD",
	)
	if err != nil {
		return WorktreeReview{}, wtError("repository", "read dirty patch: %v", err)
	}
	untrackedDigest, err := digestWorktreeUntracked(
		worker.WorktreeRoot, append(append([]string{}, untracked...), ignored...),
	)
	if err != nil {
		return WorktreeReview{}, err
	}

	conflicts, mergeErr := previewWorktreeMerge(
		ctx, git, worker.BaseRoot, currentBase, head,
	)
	reasons := make([]string, 0)
	if mergeErr != nil && len(conflicts) == 0 {
		reasons = append(reasons, "merge preflight failed: "+mergeErr.Error())
	}
	if len(conflicts) != 0 {
		reasons = append(reasons, "merge preflight found conflicts")
	}
	if len(dirty) != 0 || len(untracked) != 0 || len(ignored) != 0 {
		reasons = append(reasons, "worker has uncommitted or untracked content")
	}
	if len(baseDirty) != 0 || len(baseUntracked) != 0 {
		reasons = append(reasons, "primary checkout is not clean")
	}
	if worker.State == WorktreeInterrupted {
		reasons = append(reasons, "worker execution was interrupted")
	}
	if worker.Failure != "" {
		reasons = append(reasons, "worker reported a terminal failure")
	}
	if head == worker.BaseCommit {
		reasons = append(reasons, "worker branch contains no committed changes")
	}

	allowed := make([]string, 0, 3)
	if len(reasons) == 0 {
		if mergeBase, mergeBaseErr := git.run(
			ctx, worker.BaseRoot, "merge-base", "--all", currentBase, head,
		); mergeBaseErr == nil && strings.TrimSpace(mergeBase) == currentBase {
			allowed = append(allowed, "fast_forward")
		}
		allowed = append(allowed, "merge_no_ff")
		if currentBase == worker.BaseCommit {
			allowed = append(allowed, "cherry_pick")
		}
	}

	review := WorktreeReview{
		Schema: worktreeReviewSchema, WorkerID: worker.ID,
		ManifestRevision: manifestRevision, BaseCommit: worker.BaseCommit,
		CurrentBase: currentBase, HeadCommit: head, Commits: commits,
		ChangedPaths: changed, DiffStat: strings.TrimSpace(stat),
		DirtyTracked: dirty, Untracked: untracked, Ignored: ignored,
		Conflicts: conflicts, AllowedStrategies: allowed,
		Integrable:     len(reasons) == 0 && len(allowed) != 0,
		RefusalReasons: reasons, Interrupted: worker.State == WorktreeInterrupted,
		ReviewedAt: reviewedAt,
	}
	digestPayload, err := json.Marshal(struct {
		WorkerID         string
		ManifestRevision uint64
		BaseCommit       string
		CurrentBase      string
		HeadCommit       string
		CommittedPatch   string
		DirtyPatch       string
		UntrackedDigest  string
		Status           string
	}{
		worker.ID, manifestRevision, worker.BaseCommit, currentBase, head,
		committedPatch, dirtyPatch, untrackedDigest, workerStatus,
	})
	if err != nil {
		return WorktreeReview{}, wtError("review", "encode review digest: %v", err)
	}
	sum := sha256.Sum256(digestPayload)
	review.PatchDigest = hex.EncodeToString(sum[:])
	return review, nil
}

func previewWorktreeMerge(
	ctx context.Context,
	git worktreeGitRunner,
	baseRoot, currentBase, head string,
) ([]WorktreeConflict, error) {
	if currentBase == head {
		return nil, nil
	}
	output, err := git.run(
		ctx, baseRoot, "merge-tree", "--write-tree", "--name-only",
		"--messages", currentBase, head,
	)
	if err == nil {
		return nil, nil
	}
	conflicts := parseWorktreeConflicts(output)
	return conflicts, err
}

func parseWorktreeConflicts(output string) []WorktreeConflict {
	seen := map[string]bool{}
	var conflicts []WorktreeConflict
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "CONFLICT (") {
			kind := "content"
			if end := strings.Index(line, "):"); end > len("CONFLICT (") {
				kind = line[len("CONFLICT ("):end]
			}
			path := ""
			if index := strings.LastIndex(line, " in "); index >= 0 {
				path = strings.TrimSpace(line[index+4:])
			}
			if path == "" {
				path = line
			}
			if !seen[path] {
				seen[path] = true
				conflicts = append(conflicts, WorktreeConflict{
					Path: path, Kind: kind, Message: line,
				})
			}
		}
	}
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Path < conflicts[j].Path })
	return conflicts
}

func applyReviewedWorktreeTransaction(
	ctx context.Context,
	git worktreeGitRunner,
	local LocalOptions,
	managerRoot string,
	key []byte,
	worker *WorktreeWorker,
	review WorktreeReview,
) (string, bool, error) {
	transaction := worker.Integration
	if transaction == nil || !safeWorktreeTransactionID(transaction.ID) ||
		transaction.WorktreeLabel != transaction.ID {
		return "", false, wtError("ownership", "integration transaction identity is invalid")
	}
	transactionRoot := filepath.Join(managerRoot, transaction.WorktreeLabel)
	if err := requireAbsentPath(transactionRoot); err != nil {
		return integrationSafelyAborted(ctx, git, worker, transactionRoot, err)
	}
	if _, err := git.run(
		ctx, worker.BaseRoot, "worktree", "add", "--detach",
		transactionRoot, review.CurrentBase,
	); err != nil {
		return integrationSafelyAborted(ctx, git, worker, transactionRoot, err)
	}
	transaction.Phase = "applying"
	transaction.UpdatedAt = time.Now().UTC()
	if err := persistIntegrationPhase(local, managerRoot, key, worker); err != nil {
		return "", false, err
	}

	switch transaction.Strategy {
	case "fast_forward":
		if _, err := git.run(
			ctx, transactionRoot, "merge", "--ff-only", review.HeadCommit,
		); err != nil {
			return integrationSafelyAborted(ctx, git, worker, transactionRoot, err)
		}
	case "merge_no_ff":
		if _, err := git.run(
			ctx, transactionRoot, "merge", "--no-ff", "--no-edit",
			"-m", "Merge Anvil worktree "+worker.ID, review.HeadCommit,
		); err != nil {
			return integrationSafelyAborted(ctx, git, worker, transactionRoot, err)
		}
	case "cherry_pick":
		commits := make([]string, 0, len(review.Commits))
		for index := len(review.Commits) - 1; index >= 0; index-- {
			commits = append(commits, review.Commits[index].Hash)
		}
		if len(commits) == 0 {
			err := errors.New("review contains no commits to cherry-pick")
			return integrationSafelyAborted(ctx, git, worker, transactionRoot, err)
		}
		if _, err := git.run(
			ctx, transactionRoot, append([]string{"cherry-pick"}, commits...)...,
		); err != nil {
			return integrationSafelyAborted(ctx, git, worker, transactionRoot, err)
		}
	}

	headRaw, err := git.run(ctx, transactionRoot, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return integrationSafelyAborted(ctx, git, worker, transactionRoot, err)
	}
	result := strings.TrimSpace(headRaw)
	resultTreeRaw, err := git.run(ctx, transactionRoot, "rev-parse", result+"^{tree}")
	if err != nil {
		return integrationSafelyAborted(ctx, git, worker, transactionRoot, err)
	}
	status, err := git.run(
		ctx, transactionRoot, "status", "--porcelain=v1", "--untracked-files=all",
	)
	if err != nil || strings.TrimSpace(status) != "" {
		verifyErr := errors.New("integration transaction is dirty")
		return integrationSafelyAborted(ctx, git, worker, transactionRoot, verifyErr)
	}
	if err := verifyIntegratedWorktree(ctx, worker.BaseRoot, transactionRoot); err != nil {
		return integrationSafelyAborted(ctx, git, worker, transactionRoot, err)
	}

	transaction.Phase = "verified"
	transaction.ResultCommit = result
	transaction.ResultTree = strings.TrimSpace(resultTreeRaw)
	transaction.UpdatedAt = time.Now().UTC()
	if err := persistIntegrationPhase(local, managerRoot, key, worker); err != nil {
		return "", false, err
	}

	baseHead, err := git.run(ctx, worker.BaseRoot, "rev-parse", "--verify", "HEAD")
	if err != nil || strings.TrimSpace(baseHead) != transaction.OriginalHead {
		staleErr := errors.New("primary HEAD changed before final integration")
		return integrationSafelyAborted(ctx, git, worker, transactionRoot, staleErr)
	}
	baseStatus, err := git.run(
		ctx, worker.BaseRoot, "status", "--porcelain=v1", "--untracked-files=all",
	)
	if err != nil || strings.TrimSpace(baseStatus) != "" {
		dirtyErr := errors.New("primary checkout changed before final integration")
		return integrationSafelyAborted(ctx, git, worker, transactionRoot, dirtyErr)
	}
	if _, err := git.run(ctx, worker.BaseRoot, "merge", "--ff-only", result); err != nil {
		return integrationSafelyAborted(ctx, git, worker, transactionRoot, err)
	}
	finalHead, headErr := git.run(ctx, worker.BaseRoot, "rev-parse", "--verify", "HEAD")
	finalTree, treeErr := git.run(ctx, worker.BaseRoot, "rev-parse", "HEAD^{tree}")
	finalStatus, statusErr := git.run(
		ctx, worker.BaseRoot, "status", "--porcelain=v1", "--untracked-files=all",
	)
	if headErr != nil || treeErr != nil || statusErr != nil ||
		strings.TrimSpace(finalHead) != result ||
		strings.TrimSpace(finalTree) != transaction.ResultTree ||
		strings.TrimSpace(finalStatus) != "" {
		verifyErr := errors.New("primary checkout could not prove the verified integration result")
		return integrationRecoveryRequired(
			local, managerRoot, key, worker, transactionRoot, verifyErr,
		)
	}
	if err := removeIntegrationWorktree(ctx, git, worker.BaseRoot, transactionRoot); err != nil {
		return integrationRecoveryRequired(
			local, managerRoot, key, worker, transactionRoot, err,
		)
	}
	return result, false, nil
}

func persistIntegrationPhase(
	local LocalOptions,
	managerRoot string,
	key []byte,
	worker *WorktreeWorker,
) error {
	worker.Revision++
	worker.UpdatedAt = time.Now().UTC()
	return persistWorktreeTransition(local, managerRoot, key, *worker)
}

func integrationSafelyAborted(
	ctx context.Context,
	git worktreeGitRunner,
	worker *WorktreeWorker,
	transactionRoot string,
	integrateErr error,
) (string, bool, error) {
	_, _ = git.run(ctx, transactionRoot, "merge", "--abort")
	_, _ = git.run(ctx, transactionRoot, "cherry-pick", "--abort")
	removeErr := removeIntegrationWorktree(ctx, git, worker.BaseRoot, transactionRoot)
	headRaw, headErr := git.run(ctx, worker.BaseRoot, "rev-parse", "--verify", "HEAD")
	status, statusErr := git.run(
		ctx, worker.BaseRoot, "status", "--porcelain=v1", "--untracked-files=all",
	)
	if removeErr != nil || headErr != nil || statusErr != nil ||
		strings.TrimSpace(headRaw) != worker.Integration.OriginalHead ||
		strings.TrimSpace(status) != "" {
		return "", false, wtError(
			"recovery", "integration transaction could not prove safe abort: %v", integrateErr,
		)
	}
	return "", true, integrateErr
}

func integrationRecoveryRequired(
	local LocalOptions,
	managerRoot string,
	key []byte,
	worker *WorktreeWorker,
	transactionRoot string,
	integrateErr error,
) (string, bool, error) {
	worker.Integration.Phase = "recovery_required"
	worker.Integration.Failure = integrateErr.Error()
	worker.Integration.UpdatedAt = time.Now().UTC()
	worker.Failure = "integration recovery requires operator review"
	worker.LeaseUntil = time.Time{}
	if err := persistIntegrationPhase(local, managerRoot, key, worker); err != nil {
		return "", false, err
	}
	return "", false, wtError(
		"recovery", "%s: %v; retained transaction %s",
		worker.Failure, integrateErr, filepath.Base(transactionRoot),
	)
}

func removeIntegrationWorktree(
	ctx context.Context,
	git worktreeGitRunner,
	baseRoot string,
	transactionRoot string,
) error {
	if _, err := os.Lstat(transactionRoot); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !safeWorktreeTransactionID(filepath.Base(transactionRoot)) {
		return wtError("ownership", "transaction worktree label is invalid")
	}
	if err := rejectWorktreeReparsePath(transactionRoot); err != nil {
		return err
	}
	canonical, err := canonicalExistingPath(transactionRoot)
	if err != nil || !sameWorktreePath(canonical, transactionRoot) {
		return wtError("ownership", "transaction worktree path changed")
	}
	_, err = git.run(ctx, baseRoot, "worktree", "remove", transactionRoot)
	return err
}

func safeWorktreeTransactionID(value string) bool {
	prefix, rest, ok := strings.Cut(value, "-")
	if !ok || prefix != "tx" || len(rest) == 0 || len(rest) > 64 {
		return false
	}
	for _, r := range rest {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

func verifyIntegratedWorktree(
	ctx context.Context,
	baseRoot, transactionRoot string,
) error {
	project, err := verify.LoadProject(baseRoot)
	if err != nil {
		return wtError("verification", "load configured verification: %v", err)
	}
	for _, named := range []struct {
		name string
		spec verify.CommandSpec
	}{
		{"test", project.TestSpec()},
		{"build", project.BuildSpec()},
	} {
		if len(named.spec.Command) == 0 {
			continue
		}
		result, runErr := verify.RunIsolated(ctx, transactionRoot, named.spec)
		if runErr != nil {
			return wtError("verification", "%s verification could not run: %v", named.name, runErr)
		}
		if !result.OK {
			return wtError(
				"verification", "%s verification failed: %s", named.name, result.Summary,
			)
		}
	}
	return nil
}

func reconcileWorktreeIntegration(
	ctx context.Context,
	local LocalOptions,
	managerRoot string,
	key []byte,
	worker WorktreeWorker,
) (WorktreeWorker, error) {
	transaction := worker.Integration
	if worker.State != WorktreeIntegrating || transaction == nil {
		return worker, nil
	}
	if !safeWorktreeTransactionID(transaction.ID) ||
		transaction.WorktreeLabel != transaction.ID {
		return worker, wtError("ownership", "persisted integration identity is invalid")
	}
	git := worktreeGitRunner{expectedCommonDir: worker.GitCommonDir}
	headRaw, headErr := git.run(ctx, worker.BaseRoot, "rev-parse", "--verify", "HEAD")
	statusRaw, statusErr := git.run(
		ctx, worker.BaseRoot, "status", "--porcelain=v1", "--untracked-files=all",
	)
	if headErr != nil || statusErr != nil || strings.TrimSpace(statusRaw) != "" {
		return persistWorktreeRecoveryRequired(
			local, managerRoot, key, worker,
			"primary checkout is not provably clean during integration recovery",
		)
	}
	head := strings.TrimSpace(headRaw)
	transactionRoot := filepath.Join(managerRoot, transaction.WorktreeLabel)
	if head == transaction.OriginalHead {
		_, _ = git.run(ctx, transactionRoot, "merge", "--abort")
		_, _ = git.run(ctx, transactionRoot, "cherry-pick", "--abort")
		if err := removeIntegrationWorktree(
			ctx, git, worker.BaseRoot, transactionRoot,
		); err != nil {
			return persistWorktreeRecoveryRequired(
				local, managerRoot, key, worker,
				"transaction cleanup failed during safe abort",
			)
		}
		worker.State = WorktreeReviewReady
		worker.Review = nil
		worker.LeaseUntil = time.Time{}
		worker.Failure = "interrupted integration was safely aborted during recovery"
		worker.Integration.Phase = "aborted"
		worker.Integration.Failure = worker.Failure
		worker.Integration.UpdatedAt = time.Now().UTC()
		worker.Revision++
		worker.UpdatedAt = time.Now().UTC()
		if err := persistWorktreeTransition(local, managerRoot, key, worker); err != nil {
			return worker, err
		}
		return worker, nil
	}
	if transaction.ResultCommit != "" && head == transaction.ResultCommit {
		treeRaw, treeErr := git.run(ctx, worker.BaseRoot, "rev-parse", "HEAD^{tree}")
		if treeErr != nil || strings.TrimSpace(treeRaw) != transaction.ResultTree {
			return persistWorktreeRecoveryRequired(
				local, managerRoot, key, worker,
				"integrated tree does not match the verified transaction",
			)
		}
		if err := removeIntegrationWorktree(
			ctx, git, worker.BaseRoot, transactionRoot,
		); err != nil {
			return persistWorktreeRecoveryRequired(
				local, managerRoot, key, worker,
				"transaction cleanup failed after verified integration",
			)
		}
		worker.State = WorktreeIntegrated
		worker.LeaseUntil = time.Time{}
		worker.Failure = ""
		worker.IntegrationCommit = transaction.ResultCommit
		worker.Integration.Phase = "completed"
		worker.Integration.UpdatedAt = time.Now().UTC()
		worker.Revision++
		worker.UpdatedAt = time.Now().UTC()
		if err := persistWorktreeTransition(local, managerRoot, key, worker); err != nil {
			return worker, err
		}
		return worker, nil
	}
	return persistWorktreeRecoveryRequired(
		local, managerRoot, key, worker,
		"primary HEAD is neither the original nor verified integration result",
	)
}

func persistWorktreeRecoveryRequired(
	local LocalOptions,
	managerRoot string,
	key []byte,
	worker WorktreeWorker,
	reason string,
) (WorktreeWorker, error) {
	if worker.Integration.Phase == "recovery_required" &&
		worker.Integration.Failure == reason {
		return worker, nil
	}
	worker.Integration.Phase = "recovery_required"
	worker.Integration.Failure = reason
	worker.Integration.UpdatedAt = time.Now().UTC()
	worker.Failure = reason
	worker.LeaseUntil = time.Time{}
	worker.Revision++
	worker.UpdatedAt = time.Now().UTC()
	if err := persistWorktreeTransition(local, managerRoot, key, worker); err != nil {
		return worker, err
	}
	return worker, nil
}

func sameWorktreeReviewBinding(expected, actual WorktreeReview) bool {
	return expected.WorkerID == actual.WorkerID &&
		expected.ManifestRevision == actual.ManifestRevision &&
		expected.BaseCommit == actual.BaseCommit &&
		expected.CurrentBase == actual.CurrentBase &&
		expected.HeadCommit == actual.HeadCommit &&
		expected.PatchDigest == actual.PatchDigest
}

func parseWorktreeStatus(raw string) ([]string, []string, []string, error) {
	var dirty, untracked, ignored []string
	parts := strings.Split(raw, "\x00")
	for index := 0; index < len(parts); index++ {
		entry := parts[index]
		if entry == "" {
			continue
		}
		if len(entry) < 4 || entry[2] != ' ' {
			return nil, nil, nil, wtError("repository", "Git status output is malformed")
		}
		code := entry[:2]
		path := entry[3:]
		if strings.ContainsAny(code, "RC") && index+1 < len(parts) {
			index++
		}
		switch code {
		case "??":
			untracked = append(untracked, path)
		case "!!":
			ignored = append(ignored, path)
		default:
			dirty = append(dirty, path)
		}
	}
	sort.Strings(dirty)
	sort.Strings(untracked)
	sort.Strings(ignored)
	return dirty, untracked, ignored, nil
}

func parseWorktreeCommits(raw string) []WorktreeCommitInfo {
	fields := strings.Split(raw, "\x00")
	var commits []WorktreeCommitInfo
	for index := 0; index+1 < len(fields); index += 2 {
		hash := strings.TrimSpace(fields[index])
		if hash == "" {
			continue
		}
		commits = append(commits, WorktreeCommitInfo{
			Hash: hash, Subject: strings.TrimSpace(fields[index+1]),
		})
	}
	return commits
}

func parseWorktreeChangedPaths(raw string) ([]WorktreeChangedPath, error) {
	fields := strings.Split(raw, "\x00")
	var changed []WorktreeChangedPath
	for index := 0; index < len(fields); {
		status := fields[index]
		index++
		if status == "" {
			continue
		}
		if index >= len(fields) {
			return nil, wtError("repository", "Git diff output is malformed")
		}
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			if index+1 >= len(fields) {
				return nil, wtError("repository", "Git rename output is malformed")
			}
			changed = append(changed, WorktreeChangedPath{
				Status: status, OldPath: fields[index], Path: fields[index+1],
			})
			index += 2
		} else {
			changed = append(changed, WorktreeChangedPath{
				Status: status, Path: fields[index],
			})
			index++
		}
	}
	return changed, nil
}

func digestWorktreeUntracked(root string, paths []string) (string, error) {
	hash := sha256.New()
	sort.Strings(paths)
	if len(paths) > 4096 {
		return "", wtError("bounds", "review contains too many untracked files")
	}
	total := 0
	for _, name := range paths {
		path, err := safeWorktreeReviewPath(root, name)
		if err != nil {
			return "", err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return "", wtError("stale", "reviewed path %q disappeared", name)
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return "", wtError("security", "reviewed path %q is not a regular file", name)
		}
		if info.Size() > maxWorktreeGitOutput {
			return "", wtError("bounds", "reviewed file %q exceeds the digest limit", name)
		}
		var content []byte
		if info.Mode()&os.ModeSymlink != 0 {
			target, linkErr := os.Readlink(path)
			if linkErr != nil {
				return "", wtError("repository", "read reviewed link %q: %v", name, linkErr)
			}
			content = []byte("symlink\x00" + target)
		} else {
			content, err = os.ReadFile(path)
			if err != nil {
				return "", wtError("repository", "read reviewed file %q: %v", name, err)
			}
		}
		total += len(name) + len(content)
		if total > maxWorktreeGitOutput {
			return "", wtError("bounds", "reviewed untracked content exceeds the digest limit")
		}
		_, _ = hash.Write([]byte(name))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(content)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func safeWorktreeReviewPath(root, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) || strings.IndexByte(name, 0) >= 0 {
		return "", wtError("security", "reviewed path is invalid")
	}
	for _, segment := range strings.Split(filepath.ToSlash(name), "/") {
		if segment == ".." || strings.EqualFold(segment, ".git") {
			return "", wtError("security", "reviewed path escapes the worker boundary")
		}
	}
	path := filepath.Join(root, filepath.FromSlash(name))
	if !pathWithinWorktree(root, path) {
		return "", wtError("security", "reviewed path escapes the worker boundary")
	}
	return path, nil
}

func sanitizeReviewedDiscard(
	ctx context.Context,
	git worktreeGitRunner,
	worker WorktreeWorker,
) error {
	if worker.Review == nil {
		return wtError("stale", "discarded worker has no bound review")
	}
	current, err := buildWorktreeReview(
		ctx, git, worker, worker.Review.ManifestRevision, time.Now().UTC(),
	)
	if err != nil {
		return err
	}
	if !sameWorktreeReviewBinding(*worker.Review, current) {
		return wtError("stale", "discarded worker content changed after review")
	}
	if len(current.DirtyTracked) != 0 {
		if _, err := git.run(
			ctx, worker.WorktreeRoot,
			"restore", "--source=HEAD", "--staged", "--worktree", "--", ".",
		); err != nil {
			return wtError("cleanup", "restore reviewed tracked files: %v", err)
		}
	}
	for _, name := range append(append([]string{}, current.Untracked...), current.Ignored...) {
		path, err := safeWorktreeReviewPath(worker.WorktreeRoot, name)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return wtError("stale", "reviewed discard path %q disappeared", name)
		}
		if err != nil {
			return wtError("cleanup", "inspect discard path %q: %v", name, err)
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return wtError(
				"security", "reviewed discard path %q is not a removable file", name,
			)
		}
		if err := os.Remove(path); err != nil {
			return wtError("cleanup", "remove reviewed discard path %q: %v", name, err)
		}
		removeEmptyWorktreeParents(worker.WorktreeRoot, filepath.Dir(path))
	}
	status, err := git.run(
		ctx, worker.WorktreeRoot, "status", "--porcelain=v1",
		"--untracked-files=all", "--ignored=matching",
	)
	if err != nil {
		return wtError("cleanup", "verify discarded worker status: %v", err)
	}
	if strings.TrimSpace(status) != "" {
		return wtError("dirty", "discard cleanup did not produce a clean worktree")
	}
	return nil
}

func removeEmptyWorktreeParents(root, directory string) {
	for !sameWorktreePath(directory, root) && pathWithinWorktree(root, directory) {
		if err := os.Remove(directory); err != nil {
			return
		}
		directory = filepath.Dir(directory)
	}
}

func containsWorktreeString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func staleWorktreeRevision(expected, actual uint64) error {
	return wtError(
		"stale", "expected revision %d, current revision is %d", expected, actual,
	)
}
