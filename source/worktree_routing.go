// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package anvil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/anvil-dev/anvil/internal/tools"
)

const defaultWorktreeRouteLease = 5 * time.Minute

// WorktreeWorkerRouteOptions identifies a prepared lifecycle record. All
// identities are checked against both the signed owner record and the plan
// store; no path or identity is taken from model text.
type WorktreeWorkerRouteOptions struct {
	Local                LocalOptions
	WorktreeRoot         string
	AgentID              string
	WorkerID             string
	ExpectedRevision     uint64
	ExpectedPlanRevision int
	OwnerHostID          string
	Lease                time.Duration
}

type WorktreeWorkerReconcileOptions struct {
	Local         LocalOptions
	WorktreeRoot  string
	AgentID       string
	CurrentHostID string
}

// WorktreeWorkerRoute is the verified launch/restore boundary for one worker.
// Absolute paths are kept in unexported fields so wire projections expose only
// an opaque namespace and the generated worktree label.
type WorktreeWorkerRoute struct {
	WorkerID             string `json:"worker_id"`
	AgentID              string `json:"agent_id"`
	ArtifactID           string `json:"artifact_id"`
	WorkItemID           string `json:"work_item_id"`
	ExecutionID          string `json:"execution_id"`
	ManifestRevision     uint64 `json:"manifest_revision"`
	PlanRevision         int    `json:"plan_revision"`
	BaseCommit           string `json:"base_commit"`
	HeadCommit           string `json:"head_commit"`
	WorktreeLabel        string `json:"worktree_label"`
	CheckpointNamespace  string `json:"checkpoint_namespace"`
	WorkspaceFingerprint string `json:"workspace_fingerprint"`
	RootDigest           string `json:"root_digest"`

	baseRoot     string
	worktreeRoot string
	managerRoot  string
	gitCommonDir string
	branch       string
	ownerHostID  string
	store        string
	workItem     WorkItem
}

// WorktreeEventIdentity is the additive, path-free identity attached to every
// event, task, diff, and checkpoint projection for a routed worker.
type WorktreeEventIdentity struct {
	WorkerID             string `json:"worker_id"`
	WorktreeLabel        string `json:"worktree_label"`
	WorkspaceFingerprint string `json:"workspace_fingerprint"`
	BaseCommit           string `json:"base_commit"`
}

// StartWorktreeWorkerRoute verifies a ready worker immediately before launch
// and atomically records the running transition under the lifecycle lock.
func StartWorktreeWorkerRoute(
	ctx context.Context,
	opts WorktreeWorkerRouteOptions,
) (WorktreeWorkerRoute, error) {
	var zero WorktreeWorkerRoute
	if err := validateWorktreeRouteOptions(opts); err != nil {
		return zero, err
	}
	root, key, worker, _, release, err := loadWorktreeRouteOwner(ctx, opts)
	if err != nil {
		return zero, err
	}
	defer release()
	retrying := worker.State == WorktreeRunning &&
		worker.Revision == opts.ExpectedRevision+1
	if worker.Revision != opts.ExpectedRevision && !retrying {
		return zero, wtError(
			"stale", "worker revision is %d, expected %d", worker.Revision, opts.ExpectedRevision,
		)
	}
	if worker.State != WorktreeReady && !retrying {
		return zero, wtError("state", "worker state %q is not ready to start", worker.State)
	}
	if worker.OwnerHostID != opts.OwnerHostID {
		return zero, wtError("ownership", "worker belongs to another host incarnation")
	}
	item, err := verifyWorktreePlanRoute(ctx, opts, worker)
	if err != nil {
		return zero, err
	}
	if err := verifyWorktreeRouteTopology(ctx, root, worker, true); err != nil {
		return zero, err
	}
	route, err := routeFromWorktree(worker, opts.ExpectedPlanRevision, root)
	if err != nil {
		return zero, err
	}
	route.store = opts.Local.Store
	route.workItem = item
	if retrying {
		if worker.CheckpointID != route.CheckpointNamespace {
			return zero, wtError("ownership", "running worker checkpoint namespace changed")
		}
		return route, nil
	}
	lease := opts.Lease
	if lease <= 0 {
		lease = defaultWorktreeRouteLease
	}
	worker.State = WorktreeRunning
	worker.CheckpointID = route.CheckpointNamespace
	worker.LeaseUntil = time.Now().UTC().Add(lease)
	worker.Revision++
	worker.UpdatedAt = time.Now().UTC()
	if err := persistWorktreeTransition(opts.Local, root, key, worker); err != nil {
		return zero, err
	}
	route.ManifestRevision = worker.Revision
	return route, nil
}

// RestoreWorktreeWorkerRoute revalidates a running worker before a resumed
// process or run is allowed to use it. It never substitutes the primary tree.
func RestoreWorktreeWorkerRoute(
	ctx context.Context,
	opts WorktreeWorkerRouteOptions,
) (WorktreeWorkerRoute, error) {
	var zero WorktreeWorkerRoute
	if err := validateWorktreeRouteOptions(opts); err != nil {
		return zero, err
	}
	root, _, worker, stored, release, err := loadWorktreeRouteOwner(ctx, opts)
	if err != nil {
		return zero, err
	}
	defer release()
	if !sameWorktreeManifest(worker, stored) {
		return zero, wtError("ownership", "recovery and session ownership records do not match")
	}
	if worker.Revision != opts.ExpectedRevision {
		return zero, wtError(
			"stale", "worker revision is %d, expected %d", worker.Revision, opts.ExpectedRevision,
		)
	}
	if worker.State != WorktreeRunning {
		return zero, wtError("state", "worker state %q cannot be restored", worker.State)
	}
	if worker.OwnerHostID != opts.OwnerHostID {
		return zero, wtError("ownership", "running worker belongs to a lost host incarnation")
	}
	if worker.LeaseUntil.Before(time.Now().UTC()) {
		return zero, wtError("interrupted", "running worker lease expired")
	}
	item, err := verifyWorktreePlanRoute(ctx, opts, worker)
	if err != nil {
		return zero, err
	}
	if err := verifyWorktreeRouteTopology(ctx, root, worker, false); err != nil {
		return zero, err
	}
	route, err := routeFromWorktree(worker, opts.ExpectedPlanRevision, root)
	if err != nil {
		return zero, err
	}
	route.store = opts.Local.Store
	route.workItem = item
	return route, nil
}

// InterruptWorktreeWorker records cancellation or lost execution ownership.
// It does not clean, reroute, or mutate either checkout.
func InterruptWorktreeWorker(
	ctx context.Context,
	opts WorktreeWorkerRouteOptions,
	reason string,
) (WorktreeWorker, error) {
	if err := validateWorktreeRouteOptions(opts); err != nil {
		return WorktreeWorker{}, err
	}
	if strings.ContainsAny(reason, "\r\n") {
		return WorktreeWorker{}, wtError("invalid", "interruption reason must be one line")
	}
	root, key, worker, _, release, err := loadWorktreeRouteOwner(ctx, opts)
	if err != nil {
		return WorktreeWorker{}, err
	}
	defer release()
	retrying := worker.State == WorktreeInterrupted &&
		worker.Revision == opts.ExpectedRevision+1 &&
		worker.Failure == strings.TrimSpace(reason)
	if worker.Revision != opts.ExpectedRevision && !retrying {
		return WorktreeWorker{}, wtError(
			"stale", "worker revision is %d, expected %d", worker.Revision, opts.ExpectedRevision,
		)
	}
	if retrying {
		return worker, nil
	}
	if worker.State != WorktreeRunning {
		return WorktreeWorker{}, wtError("state", "worker state %q is not running", worker.State)
	}
	if err := verifyWorktreeRouteTopology(ctx, root, worker, false); err != nil {
		return WorktreeWorker{}, err
	}
	worker.State = WorktreeInterrupted
	worker.LeaseUntil = time.Time{}
	worker.Failure = strings.TrimSpace(reason)
	worker.Revision++
	worker.UpdatedAt = time.Now().UTC()
	if err := persistWorktreeTransition(opts.Local, root, key, worker); err != nil {
		return WorktreeWorker{}, err
	}
	return worker, nil
}

// FinishWorktreeWorkerRoute closes one execution into review_ready. It records
// no acceptance or integration claim; p_0.57.3 builds review evidence from this
// preserved worktree.
func FinishWorktreeWorkerRoute(
	ctx context.Context,
	opts WorktreeWorkerRouteOptions,
	failure string,
) (WorktreeWorker, error) {
	if err := validateWorktreeRouteOptions(opts); err != nil {
		return WorktreeWorker{}, err
	}
	if strings.ContainsAny(failure, "\r\n") {
		return WorktreeWorker{}, wtError("invalid", "worker failure must be one line")
	}
	root, key, worker, _, release, err := loadWorktreeRouteOwner(ctx, opts)
	if err != nil {
		return WorktreeWorker{}, err
	}
	defer release()
	retrying := worker.State == WorktreeReviewReady &&
		worker.Revision == opts.ExpectedRevision+1 &&
		worker.Failure == strings.TrimSpace(failure)
	if worker.Revision != opts.ExpectedRevision && !retrying {
		return WorktreeWorker{}, wtError(
			"stale", "worker revision is %d, expected %d", worker.Revision, opts.ExpectedRevision,
		)
	}
	if retrying {
		return worker, nil
	}
	if worker.State != WorktreeRunning {
		return WorktreeWorker{}, wtError("state", "worker state %q is not running", worker.State)
	}
	if worker.OwnerHostID != opts.OwnerHostID {
		return WorktreeWorker{}, wtError("ownership", "worker belongs to another host incarnation")
	}
	if _, err := verifyWorktreePlanRoute(ctx, opts, worker); err != nil {
		return WorktreeWorker{}, err
	}
	if err := verifyWorktreeRouteTopology(ctx, root, worker, false); err != nil {
		return WorktreeWorker{}, err
	}
	worker.State = WorktreeReviewReady
	worker.LeaseUntil = time.Time{}
	worker.Failure = strings.TrimSpace(failure)
	worker.Revision++
	worker.UpdatedAt = time.Now().UTC()
	if err := persistWorktreeTransition(opts.Local, root, key, worker); err != nil {
		return WorktreeWorker{}, err
	}
	return worker, nil
}

// ReconcileWorktreeWorkerRoutes marks running records from a lost/expired host
// interrupted. It never claims the old process, starts a replacement, or
// changes Git state.
func ReconcileWorktreeWorkerRoutes(
	ctx context.Context,
	opts WorktreeWorkerReconcileOptions,
) ([]WorktreeWorker, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.AgentID == "" || opts.CurrentHostID == "" {
		return nil, wtError("invalid", "agent_id and current_host_id are required")
	}
	if err := validateWorktreeIdentity("agent_id", opts.AgentID); err != nil {
		return nil, err
	}
	if err := validateWorktreeIdentity("current_host_id", opts.CurrentHostID); err != nil {
		return nil, err
	}
	if err := rejectWorktreeReparsePath(opts.WorktreeRoot); err != nil {
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
	now := time.Now().UTC()
	var changed []WorktreeWorker
	for _, worker := range owners {
		if worker.AgentID != opts.AgentID || worker.State != WorktreeRunning {
			continue
		}
		if worker.OwnerHostID == opts.CurrentHostID && worker.LeaseUntil.After(now) {
			continue
		}
		stored, loadErr := reconcileWorktreeStore(opts.Local, worker)
		if loadErr != nil {
			return nil, loadErr
		}
		if !sameWorktreeManifest(worker, stored) {
			return nil, wtError(
				"ownership", "recovery and session ownership records do not match",
			)
		}
		worker.State = WorktreeInterrupted
		worker.LeaseUntil = time.Time{}
		worker.Failure = "owning host or lease was lost during restart reconciliation"
		worker.Revision++
		worker.UpdatedAt = now
		if err := persistWorktreeTransition(opts.Local, root, key, worker); err != nil {
			return nil, err
		}
		changed = append(changed, worker)
	}
	return changed, nil
}

// WorktreeWorkerOptions binds an Agent to a verified worktree. The worker gets
// no model shell, external tool, MCP, plugin, or nested-agent escape hatch in
// the initial capability policy. Host-configured verify tools still run with
// the worktree as their cwd.
func WorktreeWorkerOptions(
	parent Options,
	route WorktreeWorkerRoute,
) (Options, error) {
	if err := validateMaterializedRoute(route); err != nil {
		return Options{}, err
	}
	if len(parent.Tools) != 0 || len(parent.MCPServers) != 0 ||
		parent.MCPRuntime != nil || len(parent.Plugins) != 0 {
		return Options{}, errors.New(
			"worktree workers do not support external tools, MCP, or plugins in this phase",
		)
	}
	if parent.HostID == "" || parent.HostID != route.ownerHostID {
		return Options{}, errors.New(
			"worktree worker options require the lifecycle owner host id",
		)
	}
	opts := parent
	opts.Local.CWD = route.worktreeRoot
	opts.storeCWD = route.baseRoot
	opts.worktreeRoute = cloneWorktreeRoute(route)
	disabled := false
	opts.Local.AllowShell = &disabled
	opts.MCPServers = nil
	opts.MCPRuntime = nil
	opts.Plugins = nil
	opts.PluginDirs = nil
	opts.Subagents.Enabled = false
	opts.skipFileMCP = true
	opts.AllowHookCommands = false
	opts.Role = RoleWorker
	brief := &Brief{
		Goal:       route.workItem.Goal,
		Plan:       clonePlanStrings(route.workItem.Plan),
		Files:      clonePlanStrings(route.workItem.Files),
		Acceptance: clonePlanStrings(route.workItem.Acceptance),
	}
	opts.brief = renderBrief(brief)
	opts.confine = append(opts.confine, confineRules(brief.Files)...)
	if opts.Checkpoints != nil {
		cp := *opts.Checkpoints
		cp.Dir = filepath.Join(
			route.baseRoot, ".anvil", "checkpoints", "worktrees",
			route.CheckpointNamespace,
		)
		opts.Checkpoints = &cp
	}
	return opts, nil
}

func revalidateAttachedWorktreeOptions(ctx context.Context, opts Options) error {
	if opts.worktreeRoute == nil {
		return nil
	}
	route := *opts.worktreeRoute
	if err := validateMaterializedRoute(route); err != nil {
		return err
	}
	verified, err := RestoreWorktreeWorkerRoute(ctx, WorktreeWorkerRouteOptions{
		Local: opts.Local, WorktreeRoot: route.managerRoot,
		AgentID: route.AgentID, WorkerID: route.WorkerID,
		ExpectedRevision:     route.ManifestRevision,
		ExpectedPlanRevision: route.PlanRevision,
		OwnerHostID:          route.ownerHostID,
	})
	if err != nil {
		return err
	}
	if verified.WorkspaceFingerprint != route.WorkspaceFingerprint ||
		verified.RootDigest != route.RootDigest ||
		verified.ExecutionID != route.ExecutionID ||
		verified.CheckpointNamespace != route.CheckpointNamespace {
		return wtError("ownership", "worktree route identity changed before agent start")
	}
	return nil
}

func interruptAttachedWorktreeOptions(opts *Options, reason string) error {
	if opts == nil || opts.worktreeRoute == nil {
		return nil
	}
	route := opts.worktreeRoute
	ctx, cancel := context.WithTimeout(context.Background(), worktreeGitTimeout)
	defer cancel()
	worker, err := InterruptWorktreeWorker(ctx, WorktreeWorkerRouteOptions{
		Local: opts.Local, WorktreeRoot: route.managerRoot,
		AgentID: route.AgentID, WorkerID: route.WorkerID,
		ExpectedRevision:     route.ManifestRevision,
		ExpectedPlanRevision: route.PlanRevision,
		OwnerHostID:          route.ownerHostID,
	}, reason)
	if err != nil {
		return err
	}
	route.ManifestRevision = worker.Revision
	return nil
}

func finishAttachedWorktreeOptions(opts *Options, failure string) error {
	if opts == nil || opts.worktreeRoute == nil {
		return nil
	}
	route := opts.worktreeRoute
	ctx, cancel := context.WithTimeout(context.Background(), worktreeGitTimeout)
	defer cancel()
	worker, err := FinishWorktreeWorkerRoute(ctx, WorktreeWorkerRouteOptions{
		Local: opts.Local, WorktreeRoot: route.managerRoot,
		AgentID: route.AgentID, WorkerID: route.WorkerID,
		ExpectedRevision:     route.ManifestRevision,
		ExpectedPlanRevision: route.PlanRevision,
		OwnerHostID:          route.ownerHostID,
	}, failure)
	if err != nil {
		return err
	}
	route.ManifestRevision = worker.Revision
	return nil
}

func validateWorktreeRouteOptions(opts WorktreeWorkerRouteOptions) error {
	if err := ctxRequiredIDs(opts.AgentID, opts.WorkerID, opts.OwnerHostID); err != nil {
		return err
	}
	if opts.ExpectedRevision == 0 || opts.ExpectedPlanRevision < 1 {
		return wtError("invalid", "expected worker and plan revisions are required")
	}
	if opts.Lease < 0 || opts.Lease > 24*time.Hour {
		return wtError("invalid", "lease must be between zero and 24 hours")
	}
	if !safeGeneratedWorktreeID(opts.WorkerID) {
		return wtError("invalid", "worker_id is invalid")
	}
	return nil
}

func ctxRequiredIDs(agentID, workerID, hostID string) error {
	if agentID == "" || workerID == "" || hostID == "" {
		return wtError("invalid", "agent_id, worker_id, and owner_host_id are required")
	}
	if err := validateWorktreeIdentity("agent_id", agentID); err != nil {
		return err
	}
	if err := validateWorktreeIdentity("owner_host_id", hostID); err != nil {
		return err
	}
	return nil
}

func loadWorktreeRouteOwner(
	ctx context.Context,
	opts WorktreeWorkerRouteOptions,
) (string, []byte, WorktreeWorker, WorktreeWorker, func(), error) {
	if err := ctx.Err(); err != nil {
		return "", nil, WorktreeWorker{}, WorktreeWorker{}, nil, err
	}
	if err := rejectWorktreeReparsePath(opts.WorktreeRoot); err != nil {
		return "", nil, WorktreeWorker{}, WorktreeWorker{}, nil, err
	}
	root, err := canonicalExistingPath(opts.WorktreeRoot)
	if err != nil {
		return "", nil, WorktreeWorker{}, WorktreeWorker{}, nil,
			wtError("security", "canonicalize worktree root: %v", err)
	}
	key, err := readWorktreeOwnerKey(root)
	if err != nil {
		return "", nil, WorktreeWorker{}, WorktreeWorker{}, nil, err
	}
	release, err := acquireWorktreeLifecycleLock(root)
	if err != nil {
		return "", nil, WorktreeWorker{}, WorktreeWorker{}, nil, err
	}
	worker, err := readWorktreeOwner(root, key, opts.WorkerID)
	if err != nil {
		release()
		return "", nil, WorktreeWorker{}, WorktreeWorker{}, nil, err
	}
	if worker.AgentID != opts.AgentID {
		release()
		return "", nil, WorktreeWorker{}, WorktreeWorker{}, nil,
			wtError("ownership", "worker belongs to another agent")
	}
	stored, err := reconcileWorktreeStore(opts.Local, worker)
	if err != nil {
		release()
		return "", nil, WorktreeWorker{}, WorktreeWorker{}, nil, err
	}
	return root, key, worker, stored, release, nil
}

func canReconcileWorktreeRouteTransition(previous, current WorktreeWorker) bool {
	allowed := (previous.State == WorktreeReady && current.State == WorktreeRunning) ||
		(previous.State == WorktreeRunning &&
			(current.State == WorktreeReviewReady || current.State == WorktreeInterrupted))
	return allowed &&
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

func verifyWorktreePlanRoute(
	ctx context.Context,
	opts WorktreeWorkerRouteOptions,
	worker WorktreeWorker,
) (WorkItem, error) {
	local := opts.Local
	local.CWD = worker.BaseRoot
	artifact, err := GetStoredPlan(ctx, worker.AgentID, worker.ArtifactID, ListOptions{Local: local})
	if err != nil {
		return WorkItem{}, wtError("plan", "load routed plan item: %v", err)
	}
	if artifact.Revision != opts.ExpectedPlanRevision {
		return WorkItem{}, wtError(
			"stale", "plan revision is %d, expected %d",
			artifact.Revision, opts.ExpectedPlanRevision,
		)
	}
	for _, item := range artifact.Items {
		if item.ID != worker.WorkItemID {
			continue
		}
		if item.Approval != WorkApprovalApproved || item.State != WorkStateRunning {
			return WorkItem{}, wtError("plan", "work item is not approved and running")
		}
		if !planContainsString(item.ExecutionIDs, worker.ExecutionID) {
			return WorkItem{}, wtError("plan", "work item does not own the worker execution")
		}
		for _, name := range item.Files {
			if _, resolveErr := tools.ResolveInRoot(
				worker.WorktreeRoot, filepath.FromSlash(name),
			); resolveErr != nil {
				return WorkItem{}, wtError(
					"plan", "work item file %q escapes the assigned worktree", name,
				)
			}
			for _, segment := range strings.Split(filepath.ToSlash(name), "/") {
				if strings.EqualFold(segment, ".git") {
					return WorkItem{}, wtError(
						"plan", "work item file %q names protected Git metadata", name,
					)
				}
			}
		}
		return item, nil
	}
	return WorkItem{}, wtError("plan", "work item is missing from the routed plan")
}

func verifyWorktreeRouteTopology(
	ctx context.Context,
	managerRoot string,
	worker WorktreeWorker,
	requireClean bool,
) error {
	base, err := canonicalExistingPath(worker.BaseRoot)
	if err != nil || !sameWorktreePath(base, worker.BaseRoot) {
		return wtError("ownership", "base repository identity changed")
	}
	if !sameWorktreePath(filepath.Dir(worker.WorktreeRoot), managerRoot) ||
		filepath.Base(worker.WorktreeRoot) != "wt-"+worker.ID {
		return wtError("ownership", "worker path is not the generated owned path")
	}
	git := worktreeGitRunner{}
	repository, err := inspectWorktreeRepository(ctx, git, base, "HEAD")
	if err != nil {
		return err
	}
	if !sameWorktreePath(repository.commonDir, worker.GitCommonDir) {
		return wtError("ownership", "Git common dir changed")
	}
	root, err := canonicalExistingPath(worker.WorktreeRoot)
	if err != nil || !sameWorktreePath(root, worker.WorktreeRoot) {
		return wtError("ownership", "assigned worktree is missing or redirected")
	}
	list, err := git.run(ctx, base, "worktree", "list", "--porcelain")
	if err != nil || !worktreeListOwns(list, root, worker.Branch) {
		return wtError("ownership", "Git no longer attributes the assigned path and branch")
	}
	commonRaw, err := git.run(ctx, root, "rev-parse", "--git-common-dir")
	if err != nil {
		return wtError("ownership", "read worker common dir: %v", err)
	}
	commonInput := strings.TrimSpace(commonRaw)
	if !filepath.IsAbs(commonInput) {
		commonInput = filepath.Join(root, commonInput)
	}
	common, err := canonicalExistingPath(commonInput)
	if err != nil || !sameWorktreePath(common, worker.GitCommonDir) {
		return wtError("ownership", "worker Git common dir changed")
	}
	branch, err := git.run(ctx, root, "branch", "--show-current")
	if err != nil || strings.TrimSpace(branch) != worker.Branch {
		return wtError("ownership", "worker branch changed")
	}
	head, err := git.run(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil || strings.TrimSpace(head) != worker.HeadCommit {
		return wtError("ownership", "worker HEAD changed outside its lifecycle record")
	}
	if requireClean {
		status, statusErr := git.run(
			ctx, root, "status", "--porcelain=v1", "--untracked-files=all",
		)
		if statusErr != nil {
			return wtError("repository", "read worker status: %v", statusErr)
		}
		if strings.TrimSpace(status) != "" {
			return wtError("dirty", "worker is dirty before launch")
		}
	}
	return nil
}

func routeFromWorktree(
	worker WorktreeWorker,
	planRevision int,
	managerRoot string,
) (WorktreeWorkerRoute, error) {
	rootDigest := worktreeDigest(canonicalFingerprintPath(worker.WorktreeRoot))
	fingerprintPayload, err := json.Marshal([]any{
		worker.AgentID, worker.ArtifactID, worker.WorkItemID, worker.ID,
		worker.ExecutionID, worker.BaseCommit, rootDigest,
	})
	if err != nil {
		return WorktreeWorkerRoute{}, wtError("identity", "encode workspace fingerprint")
	}
	fingerprint := sha256.Sum256(fingerprintPayload)
	namespaceHash := sha256.Sum256([]byte(
		worker.AgentID + "\x00" + worker.ID + "\x00" +
			worker.ExecutionID + "\x00" + worker.BaseCommit,
	))
	return WorktreeWorkerRoute{
		WorkerID: worker.ID, AgentID: worker.AgentID,
		ArtifactID: worker.ArtifactID, WorkItemID: worker.WorkItemID,
		ExecutionID: worker.ExecutionID, ManifestRevision: worker.Revision,
		PlanRevision: planRevision, BaseCommit: worker.BaseCommit,
		HeadCommit: worker.HeadCommit, WorktreeLabel: filepath.Base(worker.WorktreeRoot),
		CheckpointNamespace:  "wtcp-" + hex.EncodeToString(namespaceHash[:16]),
		WorkspaceFingerprint: hex.EncodeToString(fingerprint[:]),
		RootDigest:           rootDigest, baseRoot: worker.BaseRoot,
		worktreeRoot: worker.WorktreeRoot, managerRoot: managerRoot,
		gitCommonDir: worker.GitCommonDir, branch: worker.Branch,
		ownerHostID: worker.OwnerHostID,
	}, nil
}

func validateMaterializedRoute(route WorktreeWorkerRoute) error {
	if route.WorkerID == "" || route.AgentID == "" ||
		route.WorkspaceFingerprint == "" || route.RootDigest == "" ||
		route.CheckpointNamespace == "" || route.baseRoot == "" ||
		route.worktreeRoot == "" || route.managerRoot == "" ||
		route.ownerHostID == "" || route.ManifestRevision == 0 ||
		route.PlanRevision < 1 {
		return wtError("invalid", "worktree route was not produced by lifecycle verification")
	}
	if route.RootDigest != worktreeDigest(canonicalFingerprintPath(route.worktreeRoot)) {
		return wtError("ownership", "worktree route root digest changed")
	}
	if route.workItem.ID != route.WorkItemID ||
		route.workItem.Goal == "" ||
		route.workItem.Plan == nil ||
		route.workItem.Files == nil ||
		route.workItem.Acceptance == nil {
		return wtError("invalid", "worktree route has no trusted work item")
	}
	return nil
}

func cloneWorktreeRoute(route WorktreeWorkerRoute) *WorktreeWorkerRoute {
	copy := route
	return &copy
}

func (route WorktreeWorkerRoute) eventIdentity() WorktreeEventIdentity {
	return WorktreeEventIdentity{
		WorkerID: route.WorkerID, WorktreeLabel: route.WorktreeLabel,
		WorkspaceFingerprint: route.WorkspaceFingerprint, BaseCommit: route.BaseCommit,
	}
}

func stampWorktreeEvent(route *WorktreeWorkerRoute, event *Event) error {
	if route == nil || event == nil {
		return nil
	}
	if err := validateMaterializedRoute(*route); err != nil {
		return err
	}
	if (event.ArtifactID != "" && event.ArtifactID != route.ArtifactID) ||
		(event.WorkItemID != "" && event.WorkItemID != route.WorkItemID) ||
		(event.ExecutionID != "" && event.ExecutionID != route.ExecutionID) ||
		(event.WorkerID != "" && event.WorkerID != route.WorkerID) ||
		(event.WorkspaceFingerprint != "" &&
			event.WorkspaceFingerprint != route.WorkspaceFingerprint) {
		return wtError("attribution", "worker event identity does not match its verified route")
	}
	event.ArtifactID = route.ArtifactID
	event.WorkItemID = route.WorkItemID
	event.ExecutionID = route.ExecutionID
	event.WorkerID = route.WorkerID
	event.WorktreeLabel = route.WorktreeLabel
	event.WorkspaceFingerprint = route.WorkspaceFingerprint
	event.BaseCommit = route.BaseCommit
	return nil
}

func stampWorktreeStage(route *WorktreeWorkerRoute, stage *StageEvent) error {
	if route == nil || stage == nil {
		return nil
	}
	if (stage.WorkerID != "" && stage.WorkerID != route.WorkerID) ||
		(stage.WorkspaceFingerprint != "" &&
			stage.WorkspaceFingerprint != route.WorkspaceFingerprint) {
		return wtError("attribution", "worker stage identity does not match its verified route")
	}
	stage.WorkerID = route.WorkerID
	stage.WorktreeLabel = route.WorktreeLabel
	stage.WorkspaceFingerprint = route.WorkspaceFingerprint
	stage.BaseCommit = route.BaseCommit
	return nil
}

func worktreeDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func canonicalFingerprintPath(path string) string {
	clean := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		clean = strings.ToLower(clean)
	}
	return filepath.ToSlash(clean)
}

func (route WorktreeWorkerRoute) String() string {
	return fmt.Sprintf("%s/%s", route.AgentID, route.WorkerID)
}
