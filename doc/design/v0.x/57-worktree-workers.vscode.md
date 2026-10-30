# 57 — Worktree workers in VS Code

**Milestone focus:** 0.57. **Design status:** planned. **Risk:** high. This chapter may be
implemented only after the 0.51–0.56 contracts it depends on are delivered and tested.

## 1. Today and the problem

Anvil can already create a project worker backed by a Git worktree:
`daemon.Server.AddProjectWorker` accepts `worktree_base`, `project_path`, and `branch`, resolves
both paths through the daemon sandbox, then runs:

```text
git -C <base> worktree add -b <branch> <project_path>
```

([worker_project.go](../../../source/internal/daemon/worker_project.go)).

That is creation, not a lifecycle:

- `RemoveProjectWorker` removes the worker slot and repository allow-list entries but does not
  remove the Git worktree, decide what happens to its branch, inspect uncommitted files, or
  report cleanup failure.
- The SDK's subagents and Complex Workers intentionally run in the same workspace (chapters
  [42](42-subagents-and-tasks.vscode.md) and [50](50-agent-modes.md)); there is no worktree
  identity in a delegate brief, task, run, checkpoint, or event.
- Checkpoints are keyed by root agent/run and resolve paths against one workspace root
  ([session_checkpoints.go](../../../source/session_checkpoints.go)). A worktree needs a distinct
  restore boundary or a path valid in one checkout could overwrite another.
- Git linked worktrees use a `.git` file and shared repository metadata. The daemon has hardened
  Git review code against `core.worktree`, inherited `GIT_*`, includes, and worktree config
  ([sandbox.go](../../../source/internal/daemon/sandbox.go)); lifecycle commands must preserve
  those protections.

Running multiple editing agents in sibling worktrees reduces file-level interference, but
creates branch, credential, resource, conflict, and cleanup hazards. A worker must never become
“a path plus a goroutine”.

## 2. Dependency gate: 0.51–0.56

0.57 depends on the complete line, not only on “Git is installed”:

| Milestone | Required contract |
|---|---|
| 0.51 | protected-path shell analysis; saved shell allows cannot silently cover `.git` or indeterminate lifecycle commands |
| 0.52 | Single/Multi/Complex mode and structured worker events reach VS Code with stable child identity |
| 0.53 | model capability, retry, and truncation events are explicit; a truncated worker result cannot be mistaken for complete review evidence |
| 0.54 | durable run/event identity and interruption semantics; host restart never claims a lost worker/process is still controllable |
| 0.55 | structured artifact/work-item identity, dependencies, explicit approval, and read-first control-center projection |
| 0.56 | local, store-equivalent session/history search so archived worker evidence is discoverable without widening `session.list` |

No phase of 0.57 may duplicate or weaken those contracts. In particular, a worktree worker is an
execution of a 0.55 work item, and its liveness is a 0.54 durable state—not a daemon-specific
parallel truth.

## 3. Goals

- Create one isolated local Git worktree for one approved, dependency-ready work item.
- Define ownership, base commit, branch, filesystem boundary, permissions, checkpoint scope,
  review, integration, interruption, and cleanup as persisted state.
- Prevent a worker from writing the base checkout, sibling worktrees, shared Git administrative
  files, or lifecycle metadata.
- Make conflicts and dirty state reviewable before any integration mutates the base checkout.
- Require explicit human integration/discard decisions; never merge merely because a Worker
  reported `[met]`.
- Clean up safely and idempotently, retaining enough metadata to audit failures.

## 4. Lifecycle and data structures

### 4.1 States

```text
requested -> preparing -> ready -> running -> review_ready
                                      |          |
                                      v          v
                                 interrupted   integrating -> integrated
                                      |          |
                                      +------> discarded

ready/running/review_ready/interrupted/integrated/discarded
    -> cleaning -> removed
                 -> cleanup_failed
```

`failed` is an execution outcome attached to `review_ready`: failed work may still contain
useful commits worth reviewing. `interrupted` means host/ownership was lost under 0.54. It does
not mean the branch or files vanished.

Only `preparing`, `ready`, `running`, and `integrating` hold an exclusive lifecycle lease.
Terminal execution state does not authorize cleanup; cleanup is its own explicit transition.

### 4.2 Persisted manifest

The coordinator stores one manifest under its selected session backend and one owner-only
recovery file under the configured worktree root:

```go
type WorktreeWorker struct {
    ID             string
    AgentID        string
    ArtifactID     string
    WorkItemID     string
    ExecutionID    string
    State          string
    BaseRoot       string
    WorktreeRoot   string
    GitCommonDir   string
    BaseRef        string
    BaseCommit     string
    Branch         string
    HeadCommit     string
    OwnerHostID    string
    LeaseUntil     time.Time
    CheckpointID   string
    Review         *WorktreeReview
    CleanupPolicy  string
    Failure        string
}
```

Paths are absolute only in the local recovery record. Protocol/store projections expose the
workspace-relative worktree label plus opaque ids unless the caller already has workspace
access. `GitCommonDir` is recorded for equality checks and is never passed to a model.

The identity tuple is `{agent_id, artifact_id, work_item_id, worker_id}`. One work item may have
multiple attempts, each with a new worker id and branch. A retry never reuses an interrupted
directory without explicit recovery.

### 4.3 Protocol

```text
worktree.prepare {
  agent_id, artifact_id, work_item_id, base_ref?, store?
} -> { worker: WorktreeWorkerInfo }

worktree.get/list {
  agent_id, worker_id?, store?
} -> { workers: WorktreeWorkerInfo[] }

worktree.review {
  agent_id, worker_id, store?
} -> { review: WorktreeReview }

worktree.integrate {
  agent_id, worker_id, expected_head, strategy, store?
} -> { worker, result }

worktree.discard {
  agent_id, worker_id, expected_head, store?
} -> { worker }

worktree.cleanup {
  agent_id, worker_id, retain_branch?, store?
} -> { worker, removed_paths, retained_branch? }
```

All mutating methods require an expected manifest revision in the implementation, even where the
compact examples show only `expected_head`. Stale calls fail with current state; they are never
last-write-wins.

The initial VS Code surface is read-first. Prepare, Integrate, Discard, and Cleanup are explicit
commands with confirmation and review. No model can call these lifecycle RPCs as tools in 0.57.

## 5. Creation and path/permission boundary

### 5.1 Roots

The operator configures one `worktree_root`, outside the base checkout's tracked tree and inside
the daemon sandbox. The host canonicalizes base and worktree roots (absolute, symlink-resolved
existing ancestors, volume-aware on Windows) before any Git command.

A worker path is generated, never model-supplied:

```text
<worktree_root>/wt-<worker-id>
```

The branch is generated:

```text
anvil/<short-agent>/<work-item>/<attempt>
```

Sanitization uses the existing worker-id rules plus `git check-ref-format --branch`. User/model
titles never become raw path segments or command text.

Creation refuses when:

- the base is not a clean Git checkout;
- `git rev-parse --show-toplevel` or `--git-common-dir` escapes configured boundaries;
- the destination exists, including a symlink or reparse point;
- the branch exists unexpectedly;
- the requested base ref does not resolve to the recorded `BaseCommit`;
- submodules, sparse checkout, or repository extensions are present but not explicitly supported
  by the current capability policy;
- a worker already owns the same `{artifact_id, work_item_id, attempt}`.

Git is invoked with argv, never shell text, `--` before path operands where supported, a minimal
environment, and pinned `GIT_DIR`/`GIT_WORK_TREE` derived from reviewed values. No inherited
`GIT_*`, hook path, pager, editor, credential helper override, or external diff is accepted.

### 5.2 Worker permissions

The child agent's `Local.CWD` is the worktree root. Its sandbox and generated 0.50 delegate
file allowlist are both relative to that root. The worker receives no path to the base checkout
in its brief.

Additional immutable deny rules cover:

- every path outside the worktree root;
- any `.git` segment and the linked worktree's `.git` file;
- the configured worktree management root outside this worker directory;
- coordinator manifests, checkpoints, credentials, sockets, and other workers;
- Git lifecycle commands: `worktree`, `branch -D`, `merge`, `rebase`, `cherry-pick`, `push`,
  `clean`, and destructive reset.

Read access outside the worktree is not implicitly granted. If a work item needs external
reference data, the coordinator must materialize an approved copy inside the worker or use an
existing read tool with an explicit boundary.

The worker may commit on its generated branch through a narrow host-owned commit operation after
status review. It never receives credentials and cannot push.

## 6. Data flow

### 6.1 Prepare and run

1. 0.55 confirms the item is approved and dependency-ready.
2. The coordinator records `requested`, validates roots/repository/capabilities, and takes a
   lifecycle lease.
3. It records `BaseCommit`, creates the branch/worktree, validates Git's reported topology, then
   records `ready`.
4. It creates the child agent with worktree `CWD`, inherited-but-narrowed permissions, work-item
   identity, 0.53 model profile, and 0.54 durable execution id.
5. Events flow through the 0.52 mode bridge and 0.54 journal. The 0.55 control center groups them
   under the work item.
6. On terminal execution, the coordinator records dirty status, commits (if explicitly allowed),
   head, changed paths, acceptance report, tests, truncation, and interruption facts, then enters
   `review_ready`.

### 6.2 Review and conflict analysis

`worktree.review` is non-mutating. It verifies `HEAD`, refreshes:

```go
type WorktreeReview struct {
    BaseCommit       string
    CurrentBase      string
    HeadCommit       string
    Commits          []CommitInfo
    ChangedPaths     []ChangedPath
    DiffStat         string
    PatchDigest      string
    DirtyTracked     []string
    Untracked        []string
    Conflicts        []Conflict
    Acceptance       []AcceptanceEvidence
    Truncated        bool
    Interrupted      bool
    Integrable       bool
    RefusalReasons   []string
}
```

Conflict analysis uses a temporary index or Git's non-mutating merge-tree machinery with hooks,
filters, external diff, pager, and credential helpers disabled. It never checks out, merges, or
applies into the base merely to preview. The review is bound to `BaseCommit`, `CurrentBase`,
`HeadCommit`, and `PatchDigest`; any change invalidates approval.

Conflict review must show:

- paths changed by both base and worker;
- textual conflicts and rename/delete/type-change conflicts;
- dirty or untracked worker files excluded from commits;
- commits and diff against the recorded base;
- acceptance items with source-labelled evidence;
- whether provider output was truncated or the worker was interrupted.

`Integrable` is false when the worktree is dirty, review is stale, conflicts exist, a dependency
is no longer completed, the base checkout is dirty, or required acceptance evidence is missing.
The user may inspect failed work but cannot integrate through this command until the refusal is
resolved in a new explicit attempt.

### 6.3 Integration

Integration is coordinator-owned and serialized per base repository:

1. reacquire and verify lifecycle/repository locks;
2. require exact reviewed base/head/digest and clean base checkout;
3. capture the base integration checkpoint (§7);
4. apply the selected reviewed strategy (`fast_forward`, `merge_no_ff`, or `cherry_pick`);
5. run configured verification without credentials;
6. on success record resulting commit and `integrated`;
7. on conflict or verification failure abort the Git operation, restore only through the
   integration transaction/checkpoint rules, and return to `review_ready` with evidence.

No automatic force, conflict resolution, rebase, reviewer agent, re-plan, or push occurs.

## 7. Checkpoint ownership

There are two checkpoint domains:

1. **Worker checkpoint:** owned by `{worker_id, execution_id}`, rooted at `WorktreeRoot`. It
   captures edits made inside that worktree and can restore only there. It is displayed under
   the work item. It must never be passed to `RestoreCheckpoint` with `BaseRoot`.
2. **Integration checkpoint:** owned by the coordinator's root agent/run, rooted at `BaseRoot`,
   created immediately before integration. It captures base working-tree paths the reviewed
   operation may change. Git refs/index state are additionally recorded by the integration
   transaction; file checkpoints alone are not claimed to undo Git metadata.

Checkpoint manifests carry `WorkspaceFingerprint`, `WorkerID`, and canonical root digest.
Restore verifies all three before resolving any path. Moving a directory, reusing a worker id,
or presenting a worker checkpoint to the base produces a hard refusal.

Cleanup may remove worker checkpoint bodies only after the execution and review metadata have
been archived and the user selected the retention policy. Integration checkpoints follow the
normal coordinator retention policy.

## 8. Cleanup

Cleanup is idempotent and ordered:

1. refuse new work and cancel live worker tasks while the owning host still has handles;
2. wait for terminal/interrupted state and release session/store locks;
3. refresh status and head;
4. refuse deletion on dirty/untracked content unless the user explicitly chose Discard after a
   review naming those paths;
5. archive manifest, review digest, terminal execution, and branch/head;
6. run hardened `git worktree remove` (without `--force` except explicit Discard), then
   `git worktree prune` scoped to the reviewed repository;
7. remove only the exact generated worker directory after canonical-path revalidation;
8. retain or delete the generated branch according to the explicit policy; a merged branch is
   not assumed safe to delete when head changed;
9. mark `removed`, or `cleanup_failed` with the remaining path/branch and recovery command.

A host restart reconciles `preparing`, `running`, `integrating`, and `cleaning` from persisted
facts. It may inspect Git state, but it does not claim the old process. Ambiguous integration
state blocks both retry and cleanup until review establishes whether the resulting commit is
present.

## 9. Core invariants

1. **One item, one isolated attempt.** Every retry gets a new worker id, directory, branch, and
   execution id.
2. **Base is read-only to the Worker.** Only the coordinator integration transaction may mutate
   it.
3. **Sibling isolation.** A Worker cannot read/write another worker or its checkpoint through
   model tools.
4. **Shared Git metadata is protected.** The Worker cannot edit `.git`, refs, worktree records,
   hooks, config, or credentials.
5. **Review binds integration.** Integration applies exactly the reviewed base/head/digest or
   fails stale.
6. **Checkpoint roots never cross.** Worker restore affects only its worktree; integration
   restore affects only base.
7. **No liveness invention.** Lost host/process ownership is `interrupted`.
8. **Cleanup never destroys unreviewed work.** Dirty/untracked state requires named explicit
   discard.
9. **No automatic merge or push.** Human action is mandatory and remote mutation is outside
   this milestone.

## 10. Security, privacy, and failure semantics

- Worktree content duplicates workspace data and may multiply secret exposure and disk usage.
  The UI shows location, size estimate, retention, and cleanup state. The generated root should
  not be inside cloud-synced or broadly shared directories by default.
- Branch names and commit messages use sanitized ids and fixed templates; prompts and titles are
  never injected into commands.
- Git clean/smudge filters, hooks, external diff, pager, editor, includes, worktree config, and
  inherited environment are disabled or reviewed for every lifecycle command. Unsupported
  repository features fail closed.
- Credentials are not copied into worktrees or child options. Provider credentials stay in host
  memory under existing agent options; Git remote credentials are not available to workers.
- Symlinks, junctions, reparse points, case folding, UNC paths, and cross-volume paths are checked
  at creation and again before integration/cleanup. A path that changed identity causes refusal.
- A partial create records `cleanup_required`; it does not silently remove a directory it did
  not prove it created.
- Disk full, antivirus locks, Git lock files, host crash, and stale worktree metadata produce
  explicit recoverable states. Repeated cleanup is safe; force is never the automatic retry.

## 11. Migration and compatibility

- Existing project workers created with `worktree_base` have no lifecycle manifest. They are
  listed as `legacy_external` and may be observed, but 0.57 does not Integrate, Discard, or
  Cleanup them automatically.
- Existing same-workspace subagents and Complex Workers remain unchanged unless the user chooses
  worktree execution for an eligible 0.55 item.
- Worktree fields on events, tasks, and workflow projections are additive. Older clients show
  ordinary child agents and cannot issue lifecycle methods.
- JSONL and SQLite persist equivalent manifests/state through the same backend projection.
- Repositories without Git, with unsupported extensions, or outside the configured sandbox fall
  back to same-workspace execution; they are never silently “isolated”.
- This milestone does not change `daemon.RemoveProjectWorker` behavior for legacy callers; new
  lifecycle cleanup uses a separate explicit path.

## 12. Test strategy

- State-machine tests cover every transition, stale revision, duplicate command, restart point,
  and idempotent cleanup.
- Path-security tests cover Windows drive/UNC/case/junction behavior and POSIX symlink/mount
  boundaries, malicious ids, changed ancestors, `.git` files, `core.worktree`, inherited
  `GIT_*`, hooks, filters, includes, and external diff.
- Git integration fixtures cover clean success, base advance, textual conflict, rename/delete,
  file-mode/type change, dirty base, dirty worker, untracked files, branch head change, lock
  contention, and verification failure.
- Checkpoint tests prove a worker manifest cannot restore base or a sibling, and integration
  rollback accounts separately for working-tree files and Git metadata.
- Crash tests stop the host during every lifecycle state and assert conservative reconciliation,
  especially during integration and cleanup.
- Permission tests prove the child cannot access base/sibling paths, invoke denied Git lifecycle
  commands, or obtain credentials.
- Backend-parity tests compare worker manifests, review, state, and cleanup outcomes across JSONL
  and SQLite.
- VS Code tests prove read-first display, exact review binding, explicit confirmations, stale
  review refusal, conflict rendering, interruption wording, and cleanup-failure recovery.
- End-to-end gates run only after 0.51–0.56 acceptance suites pass.

## 13. Non-goals

- Containers, VMs, OS-level sandboxing, or a claim that a worktree isolates processes.
- Remote workers, remote Git hosts, push, pull-request creation, or CI orchestration.
- Automatic reviewer agents, automatic conflict resolution, rebase, merge, or re-plan.
- Sharing one worktree between concurrent work items.
- Supporting every Git extension, submodule topology, sparse checkout, filter, or hook in the
  first release.
- Recovering control of a process after its owning host is gone.

## 14. Delivery

| Phase | Contract |
|-------|----------|
| p_0.57.1 | Lifecycle manifest/state machine, dependency gate, hardened creation, path and permission boundaries |
| p_0.57.2 | Worker execution identity, checkpoint ownership, durable/restart semantics, read-first VS Code projection |
| p_0.57.3 | Conflict review, explicit integration/discard, idempotent cleanup and cross-platform crash/security gates |
