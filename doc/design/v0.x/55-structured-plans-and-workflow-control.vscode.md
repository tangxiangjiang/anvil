# 55 — Structured plans and workflow control in VS Code

**Milestone focus:** 0.55. **Design status:** planned. This chapter defines one read model
over Plan mode, `todo_write`, and Complex mode; delivery status belongs in the 0.55 roadmap.

## 1. Today and the problem

Anvil currently has three things the UI calls a plan, with three incompatible shapes:

1. **Plan mode** is a permission mode plus a directive. The assistant's prose is accumulated by
   `ChatPanel.planTurn`, kept in the tab's bounded `planTexts` map, and opened through the
   read-only `anvil-plan:` document provider
   ([plan.ts](../../../editors/vscode/src/plan.ts),
   [planDocument.ts](../../../editors/vscode/src/planDocument.ts)). Approval sends the fixed
   sentence `PLAN_APPROVED_PROMPT`.
2. **`todo_write`** replaces a complete list of `{text,status}` rows. The runtime emits
   `EventTodo`, and replay reconstructs it from persisted tool arguments
   ([todo_tool.go](../../../source/internal/tools/todo_tool.go),
   [transcript.ts](../../../editors/vscode/src/transcript.ts)).
3. **Complex mode** runs Decision → Analyze → Worker. Analyze emits the validated roadmap schema,
   and Worker fan-out executes `delegate` briefs
   ([mode_complex.go](../../../source/mode_complex.go), chapter
   [50](50-agent-modes.md)).

The user sees a prose document, a live checklist, and workflow/subagent cards, but cannot tell
which checklist belongs to which approved plan, whether a Complex item corresponds to a visible
row, or what finished after reopening the session. The answer is not a fourth planner. It is one
structured projection over records that already have distinct authorities.

The first control center is intentionally **read-only-first**. It does not automatically approve,
cancel, retry, review, or re-plan. Existing explicit buttons keep their existing behavior.

## 2. Goals

- Give Plan prose, `todo_write` rows, Complex roadmap items, workers, runs, and checkpoints one
  stable vocabulary and hierarchy.
- Preserve each source's authority rather than guessing equivalence from matching text.
- Make the projection replayable from persisted session/run data and durable-run records from
  0.54.
- Show dependencies, current state, ownership, acceptance, and evidence in one VS Code view.
- Keep all mutations explicit and routed through existing commands; the initial control center
  is observation and navigation.
- Define a format later phases can control without changing the identity model.

## 3. Unified data model

### 3.1 Terms

- A **plan artifact** is one plan-mode answer or one Complex Analyze roadmap.
- A **work item** is an explicit unit inside an artifact.
- An **execution** is a run or delegated worker that attempts a work item.
- **Evidence** is a terminal run result, tool result, test result, changed-path fact, or
  checkpoint reference. It is not a model's unsupported claim.

`todo_write` is progress presentation, not a scheduler. A todo row becomes a work item only when
it carries explicit identity under the new schema; legacy rows remain unlinked display rows.

### 3.2 Persisted projection records

Add two append-only store record kinds:

```go
const (
    TypePlanArtifact = "plan_artifact"
    TypeWorkState    = "work_state"
)

type PlanArtifactRecord struct {
    Schema     int             `json:"schema"` // exactly 1 in this milestone
    ArtifactID string          `json:"artifact_id"`
    RunID      string          `json:"run_id"`
    Kind       string          `json:"kind"` // "plan" | "complex"
    Revision   int             `json:"revision"`
    Title      string          `json:"title,omitempty"`
    Body       string          `json:"body,omitempty"`  // Plan prose
    Items      json.RawMessage `json:"items,omitempty"` // validated WorkItem[]
}

type WorkStateRecord struct {
    ArtifactID  string          `json:"artifact_id"`
    ItemID      string          `json:"item_id"`
    Revision    int             `json:"revision"`
    ExecutionID string          `json:"execution_id,omitempty"`
    State       string          `json:"state"`
    Actor       string          `json:"actor,omitempty"`
    Reason      string          `json:"reason,omitempty"`
    Evidence    json.RawMessage `json:"evidence,omitempty"`
}
```

Plan mode writes `TypePlanArtifact` when the plan turn finishes, before presenting Approve.
Complex writes one after Analyze validates its roadmap and before any Worker starts. These
records do not replace transcript messages: they are the machine-readable index into them.

Artifact identity is `{agent_id, artifact_id, revision}`. `artifact_id` is stable for the plan
being revised; `revision` increments when the user rejects and requests another plan. A new
unrelated plan gets a new id. Work item identity is `{artifact_id, item_id}`.

### 3.3 Work item schema

```json
{
  "id": "stable within the artifact",
  "title": "one line",
  "goal": "what should become true",
  "plan": ["ordered implementation steps"],
  "files": ["workspace-relative write boundary"],
  "acceptance": ["observable completion checks"],
  "depends_on": ["item id"],
  "approval": "pending | approved | denied",
  "state": "proposed | ready | running | blocked | completed | failed | cancelled",
  "source": "plan | todo | complex",
  "execution_ids": ["run or child-agent id"],
  "checkpoint_run_id": "optional root run"
}
```

Required validation rejects unknown schema versions, duplicate item ids, missing dependencies,
cycles, approval values outside the enum, and state/approval combinations that could execute a
pending or denied item. Unknown fields are refused in the canonical SDK decoder; the host and
TypeScript client consume the same versioned fixtures rather than coercing them.

Complex Analyze's existing `roadmap.items` maps directly: `goal`, `plan`, `files`,
`acceptance`, and `depends_on` retain their chapter 50 validation. Its existing `id` becomes the
work item id. No text matching is needed.

Plan-mode prose has no machine-readable items today. The first phase persists it as an artifact
with `items: []`. It is still useful as the parent document for explicit approval and later
execution. A later phase adds an optional structured appendix generated in the same answer; if
it is absent or invalid, the prose plan remains reviewable and nothing is invented.

### 3.4 `todo_write` v2

The existing schema remains accepted. Add optional fields:

```json
{
  "artifact_id": "optional",
  "items": [{
    "id": "optional",
    "text": "one task, one line",
    "status": "pending | in_progress | done",
    "work_item_id": "optional"
  }]
}
```

When `artifact_id` and `work_item_id` name an existing artifact/item, the todo row is a
presentation of that item's progress. The SDK validates ownership and refuses duplicate ids.
When absent, behavior is byte-for-byte the current complete-list replacement and replay remains
possible from tool arguments.

Todo status does not by itself complete a work item. `done` is a model assertion; the control
center labels it “reported done” until terminal execution evidence satisfies the item's
acceptance projection.

## 4. Protocol

```text
workflow.get {
  agent_id,
  artifact_id?,
  store?
} -> {
  artifacts: WorkflowArtifact[],
  selected_artifact_id?,
  executions: WorkflowExecution[],
  last_seq?
}

workflow.decide {
  agent_id,
  artifact_id,
  expected_revision,
  decisions: [{ item_id, decision: "approve" | "deny", reason? }],
  store?
} -> {
  artifact: WorkflowArtifact,
  accepted_revision
}
```

`workflow.get` is read-only and works for a closed session. It folds plan/work-state records,
run metadata, child identity from `AgentInfo`, and checkpoints. It does not load every transcript
body: artifact records carry plan bodies and structured items; execution rows carry metadata and
links.

The existing `event` notification gains optional:

```json
{
  "artifact_id": "artifact-…",
  "work_item_id": "item-…",
  "execution_id": "run-…"
}
```

These fields annotate events when identity is known. They do not change event routing:
`session_id`, `run_id`, `agent_id`, and `parent_tool_call_id` keep their current meanings.

`workflow.decide` is the one mutation added here. It is explicit human approval/denial, accepts
an item subset, and is compare-and-swap on `expected_revision`. One accepted batch increments
the artifact revision once and appends one decision record per item with actor, timestamp, and
reason. Repeating the exact decided batch with the resulting revision is an idempotent read; a
conflicting or stale batch fails and starts nothing.

There is no `workflow.retry`, `workflow.review`, or `workflow.replan` method. Whole prose-plan
approval continues through the existing mode transition and `PLAN_APPROVED_PROMPT`; task
cancellation continues through `tasks.cancel`; run cancellation continues through
`session.cancel`.

## 5. Data flow

### 5.1 Plan mode

1. The user enters plan mode and sends a request.
2. The host/runtime produces the ordinary transcript.
3. On terminal assistant text, the host writes a plan artifact tied to that run.
4. VS Code opens the existing read-only `anvil-plan:` document and selects the same artifact in
   the control center.
5. Approve or Reject remains an explicit user action. Approval does not duplicate plan text in
   the next prompt.
6. For a structured appendix, the user may approve/deny an item subset. Approving a dependent
   does not make it ready until every dependency is completed; denying a prerequisite marks its
   transitive dependants blocked with a stable reason.
7. The next execution run links to the approved artifact. Its `todo_write` calls may link rows
   to structured items; unlinked rows remain visible under “Unlinked progress”.

### 5.2 Complex mode

1. Decision remains a read-only reasoning stage.
2. Analyze validates the existing roadmap schema.
3. The SDK persists the Complex artifact before worker fan-out.
4. Every Worker receives one work item id in its validated brief; its child identity,
   acceptance report, changed paths, and tree-keyed checkpoint are folded under that item.
5. Independent items may execute concurrently, but state transitions respect `depends_on`.

### 5.3 Replay

On opening a stored session, VS Code calls `workflow.get` independently of
`session.transcript`. The transcript stays tail-limited; the workflow projection stays compact
because it contains artifacts and metadata, not all messages. A legacy session with only
`todo_write` calls still gets its todo replay from `transcript.ts`, but those rows appear as
legacy/unlinked and are never retroactively assigned to a plan by text similarity.

## 6. State and core invariants

Allowed work-item transitions:

```text
proposed/pending --approve--> proposed/approved
proposed/pending --deny-----> blocked/denied
proposed/approved + dependencies completed -> ready -> running -> completed
                                                     \-> failed
                                                     \-> cancelled
blocked/approved -> ready only when its dependency block clears
```

The transition source is recorded. A terminal execution can make an item `failed` or
`cancelled`; only explicit evidence can make it `completed`. A new attempt is a new
`execution_id`, not resurrection of a terminal execution.

Core invariants:

1. **One identity, no text guesses.** Items link only by explicit artifact/item ids.
2. **Source authority is preserved.** Plan prose describes intent; todo reports progress;
   run/task state describes execution; checkpoints describe captured file history.
3. **Read-only means read-only.** Opening or refreshing the control center starts no run,
   changes no mode, writes no todo, and restores no checkpoint.
4. **Dependencies are structural.** An item is not `ready` while a named dependency is not
   completed, and approval of a dependent cannot bypass that gate.
5. **Model claims are labelled.** Todo `done` and Worker `[met]` are reports until backed by the
   evidence available to the system.
6. **Child boundaries remain intact.** The parent sees the child result and metadata authorized
   by chapter 42, not the child's hidden context.
7. **Complex remains fixed-stage.** The control center observes Decision → Analyze → Worker; it
   does not add Reviewer or a re-plan loop.
8. **Plan approval remains human.** No state projection auto-approves prose or switches
   permission mode.
9. **Revision guards execution.** A stale or conflicting decision changes nothing and starts no
   Worker; replay of accepted records starts an execution at most once.

## 7. VS Code surface

The “Workflow” view is a tree/detail surface:

- artifact header: title, source, revision, originating run, approval/execution state;
- dependency graph rendered as an accessible ordered tree, not a mandatory canvas;
- item rows: state, owner/execution, acceptance count, changed paths, checkpoint link;
- unlinked todo rows in a separate section;
- navigation is the default state: Open plan, Open transcript, Open worker transcript, Review
  changes, View checkpoint, and Refresh;
- after the approval phase is available, Approve selected and Deny selected require an explicit
  click, confirmation, displayed revision, and reason for denial. A stale response refreshes
  state and never retries automatically.

The view never hides partial truth. A lost live event shows a durable sequence gap from 0.54.
An old session says “legacy progress; no structured item identity”. An interrupted run says so
and offers no Cancel.

## 8. Security, privacy, and failure semantics

- Plan bodies, goals, file lists, acceptance text, model output, and paths are sensitive local
  workspace data. They remain in the selected local session backend and are not sent to a search
  service or telemetry.
- Artifact `files` are display and delegation constraints, not permission grants. Existing
  sandbox resolution and generated deny rules remain authoritative.
- Tool results are already redacted before persistence. The projection does not reread files to
  “prove” acceptance and therefore does not create an unredacted copy.
- Malformed structured appendices do not discard a plan-mode answer. The artifact is stored as
  prose-only with validation errors available to the UI. Malformed Complex roadmaps retain
  chapter 50's behavior: Analyze fails and Workers do not start.
- Missing child transcripts, pruned checkpoints, deleted sessions, and truncated durable events
  are represented as unavailable evidence, never as successful completion.
- Concurrent writes fold by store order and execution sequence. Conflicting terminal state for
  one execution is a corruption error; readers do not choose the more favorable state.
- Decision actors are host-derived local identities, not arbitrary model text. Approval is
  authorization to schedule the selected item under existing permissions; it is not a tool
  permission grant or a bypass of file/shell approvals.

## 9. Migration and compatibility

- Existing plan answers have no artifact records. They remain transcript content; the extension
  does not manufacture artifacts from arbitrary historical assistant messages.
- Existing `todo_write` calls keep their schema and replay. Optional ids are additive.
- Existing Complex transcripts remain readable as workflow/subagent cards. Structured control
  begins with newly persisted artifacts; no migration parses old model prose.
- JSONL and SQLite produce the same `workflow.get` projection through `store.Backend`.
- Older hosts return `-32601`; VS Code keeps the current Plan document, todo panel, and task
  cards independently.
- The `anvil-plan:` URI remains keyed by agent and run for compatibility. The control center
  stores artifact identity beside that target rather than changing old document URIs.

## 10. Test strategy

- Pure state-fold tests cover every allowed and refused transition, dependency readiness,
  duplicate identities, revisions, and evidence precedence.
- Backend-parity fixtures append the same plan/work records to JSONL and SQLite and compare the
  complete `workflow.get` result.
- Protocol tests prove `workflow.get` reads closed sessions and does not create a live agent.
- Plan integration tests cover approve, reject/revise, fixed approval prompt, and prose-only
  fallback.
- Todo tests cover legacy schema, linked schema, complete-list replacement, replay, empty-list
  clearing, and refusal of cross-artifact ids.
- Complex tests prove the persisted item is the exact validated brief a Worker receives and the
  checkpoint belongs to the root run.
- Approval tests cover item subsets, dependency blocks, denial propagation, optimistic revision
  conflicts, idempotent replay, restart, and exactly-once Worker start.
- VS Code unit tests cover old-host fallback, unlinked legacy rows, interrupted execution, and
  navigation without mutation.
- Negative tests assert that refresh triggers no `session.send`, `tasks.cancel`, checkpoint
  restore, provider call, or permission-mode change.

## 11. Non-goals

- Automatic Reviewer, automatic re-planning, or retrying a failed Worker.
- Generating a plan from a todo list or inferring links by semantic/text similarity.
- Free-form plan editing or drag-to-reorder dependencies.
- Replacing `run_workflow` with a second scheduler.
- Treating todo `done` as verified acceptance.
- Cross-workspace orchestration or remote worker control.

## 12. Delivery

| Phase | Contract |
|-------|----------|
| p_0.55.1 | Versioned unified schema, artifact/work records, validation, Plan/Complex/todo identity, backend parity |
| p_0.55.2 | Revision-safe partial approval/denial, dependency readiness, audit records, exactly-once execution gate |
| p_0.55.3 | Read-first Workflow view, explicit decision controls, evidence/navigation, replay and fallbacks |
