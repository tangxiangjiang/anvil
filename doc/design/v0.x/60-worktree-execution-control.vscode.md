# 60 — End-to-end Worktree execution control

**Milestone focus:** 0.60. **Design status:** proposed. **Risk:** high.

## 1. Problem

0.57 delivers secure Worktree preparation, routing primitives, review, transactional integration,
discard, cleanup, and a read-first VS Code projection. The remaining gap is product orchestration:
an approved structured work item can prepare a Worktree, but the editor does not yet own one
atomic user flow that starts the routed Worker, follows its durable run, and returns it to review.

The missing boundary must not be filled by prompt conventions. Worktree execution is authorized
only from a stored 0.55 artifact and an approved, dependency-ready item.

## 2. Goals

- Start one isolated Worker from one approved work item through an explicit editor command.
- Bind preparation, durable execution, events, checkpoints, terminal evidence, and review to one
  execution identity.
- Make cancellation, retry, restart, and stale-plan outcomes visible and fail closed.
- Keep the primary checkout unchanged until a separately confirmed 0.57 integration.
- Present the whole lifecycle under the work item without creating duplicate session truths.

## 3. Dependency gate

0.60 depends on delivered 0.51–0.57 contracts. In particular:

- 0.54 owns durable run and interruption state.
- 0.55 owns artifact revision, approval, dependencies, and work-item state.
- 0.57 owns Worktree identity, routing, checkpoint namespace, review, and cleanup.

0.60 composes these records; it does not introduce another scheduler or lifecycle store.

## 4. Execution contract

The coordinator accepts:

```text
workflow.execute_worktree {
  agent_id,
  artifact_id,
  work_item_id,
  expected_artifact_revision,
  expected_item_state,
  worktree_root,
  model_profile?,
  store?
}
```

It returns an operation containing `worker_id`, `execution_id`, `run_id`, and the latest Worktree
manifest revision. The host performs one serialized transaction:

1. reload the artifact and require the exact revision;
2. require approval and completed dependencies;
3. reserve an execution id and append the work-item `starting` state;
4. prepare the owned Worktree;
5. revalidate topology and start the routed Worker;
6. append durable run and attribution records;
7. publish one authoritative operation result.

Any failure before Worker start cleans only coordinator-owned preparation artifacts. A failure
after start is a durable interrupted/failed run and is never represented as “not started”.

## 5. State projection

The work item projects, rather than duplicates:

```text
approved -> starting -> running -> review_ready
                 |          |
                 v          v
              blocked    interrupted
```

The Worktree manifest remains authoritative for repository resources. The durable run remains
authoritative for execution. The work item stores only references and the accepted terminal
projection.

Repeated commands require the current artifact and item revisions. They never silently reuse an
old Worker directory or execution id.

## 6. VS Code surface

The Workflow view adds:

- **Run in isolated Worktree** on an approved, dependency-ready item;
- one modal naming the item, base ref, model profile, and configured Worktree root;
- live state, task output, checkpoints, and terminal evidence nested under the item;
- **Cancel**, **Retry in new Worktree**, **Review changes**, and existing explicit integration,
  discard, and cleanup actions;
- setup guidance when `anvil.worktreeRoot` is absent or invalid.

Opening the view or expanding an item is read-only. No Worker starts merely because a plan is
opened, restored, or approved.

## 7. Retry and restart

- Retry always creates a new attempt, Worker id, branch, and execution id.
- The previous attempt remains reviewable or discardable.
- Restart reconciliation joins Worktree and durable-run facts; it never adopts an unproven
  process.
- A lost owner becomes interrupted. The UI may offer retry, but never automatic rerun.
- Cancellation targets the durable run first, waits for a terminal boundary, then finalizes the
  Worktree as reviewable or interrupted.

## 8. Security boundaries

- No model-facing tool can invoke lifecycle methods in 0.60.
- Paths, refs, attempt numbers, and identities are host-derived and revalidated.
- The child receives only its Worktree CWD and narrowed permissions.
- No credential or remote Git capability is added.
- Stale artifact, dependency, model profile, owner, or repository facts stop the operation before
  mutation.

## 9. Observability and audit

Every operation records source command, actor class, artifact/item revision, Worker/execution/run
ids, state transitions, refusal code, and timestamps. Absolute local paths stay in signed recovery
records and are not sent to the model or general protocol projection.

## 10. Acceptance

- One approved item can be started from VS Code and every event/checkpoint resolves to its assigned
  Worktree.
- Opening, expanding, approving, or restoring a plan never starts work implicitly.
- Dependency, revision, ownership, and topology races fail before Worker launch.
- Cancellation and host restart preserve a recoverable Worker and durable terminal evidence.
- Retry creates a new isolated attempt and cannot overwrite the previous attempt.
- The primary checkout remains unchanged until explicit 0.57 integration.

## 11. Non-goals

- Automatic integration, discard, conflict resolution, or retry.
- Remote Workers, PR creation, or CI orchestration.
- Running arbitrary prose as a work item.
- Reusing an interrupted Worktree for a new attempt.
