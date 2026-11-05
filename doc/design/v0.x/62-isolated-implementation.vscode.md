# 62 — Isolated implementation and execution control

**Status:** planned
**Product version focus:** 0.62

## 1. Goal

Execute one approved, dependency-ready 0.61 work item in an isolated Worker Worktree and optional
container sandbox. One host-owned transaction binds product artifact, policy, Worker, Worktree,
execution, run, checkpoints, and evidence. VS Code is the only interactive execution client.

## 2. Start transaction

`workflow.execute` accepts exact project/requirement/plan/item revisions, base commit, policy digest,
model profile, runtime requirements, and idempotency key. Before mutation the coordinator proves:

- approval and dependency readiness;
- unchanged artifact, repository, and release-target revisions;
- available Worker/Worktree root and supported runtime capabilities;
- permission, model, budget, sandbox, network, and secret policy;
- no accepted or in-flight execution for the same item revision/idempotency key.

Duplicate, stale, denied, blocked, or unsupported starts create no Worker run. Pre-start failures
clean only coordinator-owned artifacts. Once a run exists, every failure becomes durable
`failed`, `cancelled`, `interrupted`, or `recovery_required`.

## 3. Identity and evidence

One immutable identity tuple binds:

- requirement, design, milestone, phase, work-item, and policy revisions;
- Worker, branch, Worktree, sandbox spec/instance, execution, attempt, session, and run;
- base/head commit, patch digest, tasks, tool events, checkpoints, logs, and terminal manifest.

Every event is sequenced and idempotent. A restored view is derived from snapshot plus event cursor,
not a process handle. Retry creates a new attempt, Worker branch, Worktree, execution, and run while
retaining prior evidence.

## 4. Runtime and sandbox boundary

The runtime-neutral contract separates capability, immutable specification, and owned instance.
The first real adapter supports tested Docker/Podman/rootless configurations.

The sandbox receives one writable Worker Worktree. It cannot mount host home, primary checkout,
sibling Worktrees, credentials, runtime sockets, or unrestricted devices. It runs non-root with
dropped capabilities, canonical mount validation, bounded CPU/memory/PIDs/disk/output/time, and
default-deny network policy.

Secrets are opaque, workload-scoped, process-bound, redacted, and absent from specifications,
events, model context, layers, checkpoints, and caches. Unsupported limits or weaker isolation are
reported unavailable rather than silently accepted.

## 5. Lifecycle, cancellation, and recovery

Sandbox and Worker lifecycle records carry signed ownership and policy/image digests. Cancellation
targets the exact execution, terminates its process tree, waits for a durable terminal boundary,
then permits cleanup. Restart reconciles leases, processes, Worktrees, and containers but never
adopts an unproven process or silently reruns work.

Cleanup is idempotent and ownership-bound. Checkpoints contain Workspace files only. The primary
checkout remains unchanged until an explicit integration transaction in the later delivery stage.

## 6. VS Code execution surface

The selected work item shows:

- preflight summary of revisions, Worker/Worktree, runtime, model, budgets, policies, and risks;
- explicit Run confirmation bound to the displayed digest;
- queued/running/cancelling/recovery/terminal state with sequenced logs and tool evidence;
- checkpoints, changed files, terminal manifest, and links to verification/review;
- Stop, Retry, Review Worktree, Discard, and Cleanup commands with revision-bound confirmations;
- diagnostics for missing tools, runtime, root, policy, image, credentials, or permissions.

Opening, expanding, refreshing, approving, or reconnecting performs no execution mutation.

## 7. Web cutover

Web job start/stop/retry, Worker-workflow controls, and interactive workbench are removed after the
VS Code execution gates pass. Web retains read-only run, event, diff metadata, and audit export.
Repository content remains bounded/redacted and never becomes an interactive Web coding surface.

## 8. Acceptance

- One approved item starts at most one execution for an idempotency key.
- Every event and artifact resolves to one product/work-item/Worker/execution identity.
- Escape, host-read, secret, network, resource, timeout, cancellation, and orphan fixtures fail
  closed.
- Restart/retry preserve evidence and never mutate the primary checkout.
- Live and restored VS Code views agree; viewing creates no execution.
- Web can audit execution but cannot start, stop, retry, or configure it.

## 9. Non-goals

- Remote fleet scheduling (0.66).
- Review, publication, PR, CI, merge, or release (0.64–0.65).
- Whole-container snapshots, arbitrary compatibility mounts, or automatic retry.

## 10. Dependencies

- [61 — Planning and execution policy](61-planning-and-policy.vscode.md).
- Delivered 0.54 durable runs, 0.55 structured workflow, and 0.57 Worktree lifecycle.

## 11. Roadmap

- [Milestone 0.62](../../roadmaps/v0.x/m_0.62_isolated_implementation.vscode.md).
