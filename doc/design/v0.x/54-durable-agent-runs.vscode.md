# 54 — Durable agent runs in VS Code

**Milestone focus:** 0.54. **Design status:** planned. This chapter defines the contract;
delivery status belongs in the 0.54 roadmap, not here.

## 1. Today and the problem

A session transcript is durable, but an executing run is not. These are different claims:

- `source/internal/store.Backend` persists `meta`, `message`, `summary`, `run_start`, and
  `run_end` records. JSONL is the default and SQLite is optional
  ([backend.go](../../../source/internal/store/backend.go)).
- `anvil.LoadTranscript` reads a capped, run-aligned tail
  ([session_transcript.go](../../../source/session_transcript.go)); `ListRuns` derives run
  metadata from records ([session_runs.go](../../../source/session_runs.go)).
- Live events travel through `Run.events`, a bounded channel. `Run.emit` may drop when that
  channel is full. The editor host's unbounded `outbound` queue prevents a slow stdout consumer
  from adding more back-pressure, but cannot reconstruct an event already dropped before it
  reached the queue ([outbound.go](../../../source/internal/editorhost/outbound.go)).
- Background shell and agent tasks live in `internal/tasks.Manager`: process handles,
  cancellation functions, and output rings are in memory. Closing the root agent cancels the
  table; restarting `anvil-host` destroys it. `tasks.list` therefore requires a live session.

The gap is not “make every process survive”. The gap is that after an extension reload, host
crash, broken pipe, or machine restart, the user cannot reliably answer:

1. Did the host accept my run?
2. Is it still attached to this host?
3. Did it finish, fail, get cancelled, or disappear?
4. Which durable transcript and checkpoint belong to it?

In particular, 0.54 **must not claim control over an OS process whose handle was lost**. A new
host cannot safely rediscover an arbitrary shell process, prove ownership, read its inherited
pipes, or cancel its complete process tree. Such a run is `interrupted`, not “running”.

## 2. Goals

- Give every accepted top-level run a durable lifecycle record and a stable run id before work
  starts.
- Let VS Code list and inspect active, terminal, and interrupted runs without requiring the
  original in-memory `Run` handle.
- Reconcile an unfinished `run_start` after host restart to an explicit terminal observation.
- Make live delivery resumable from a durable event sequence while stating that persistence
  begins only at 0.54; historical dropped events are not recoverable.
- Preserve the existing transcript, checkpoint, cancellation, and permission boundaries.
- Keep background-task truth separate from run truth: task handles do not become durable merely
  because their owner run does.

## 3. Protocol and data structures

### 3.1 Store records

Two additive record kinds join `store.Record`:

```go
const (
    TypeRunState = "run_state"
    TypeRunEvent = "run_event"
    TypeTaskState = "task_state"
)

type Record struct {
    // existing fields omitted
    RunID       string          `json:"run_id,omitempty"`
    RunSeq      uint64          `json:"run_seq,omitempty"`
    RunState    string          `json:"run_state,omitempty"`
    HostID      string          `json:"host_id,omitempty"`
    LeaseUntil  time.Time       `json:"lease_until,omitempty"`
    Event       json.RawMessage `json:"event,omitempty"`
    Cause       string          `json:"cause,omitempty"`
}
```

`RunState` is one of `accepted`, `running`, `finished`, `error`, `cancelled`, or
`interrupted`. `run_end` remains the canonical transcript boundary and usage record;
`run_state` is the operator-facing lifecycle journal. Terminal state is written before or in
the same serialized store operation as `run_end`; readers tolerate either order and prefer
`run_end` when both exist.

`RunSeq` is monotonic per run, starts at 1, and identifies persisted events. It is not a global
clock and does not order two runs. `HostID` is a random id for one host process lifetime, not a
machine identity. `LeaseUntil` is evidence that the named host recently renewed ownership; it
is not proof that work is still executing.

`task_state` journals the existing `internal/tasks.Info` identity plus `TaskSeq`, owning
agent/run/tool call, child agent id when known, status, timestamps, output offsets, owner
`HostID`, and interruption cause. It does **not** persist a PID as authority, a process handle,
cancel function, pipe, or output ring. Task output may be copied into bounded `run_event`
records; the live ring remains an in-memory implementation detail.

Both backends implement the same record semantics through `store.Backend`. JSONL remains
append-only. SQLite keeps opaque record payloads in insertion order, so this milestone does not
introduce a backend-specific run table or a second source of truth.

The SDK run receives a store-backed journal sink:

```go
type RunJournal interface {
    AppendEvent(agentID, runID string, seq uint64, event Event) error
}
```

`Run.emit` assigns the next sequence under the run lock and calls `AppendEvent` **before** its
existing non-blocking send to the bounded `Run.events` channel. The channel may still drop a live
notification; the journal may not drop the event. A nil sink preserves current embedder
behavior, but the editor host requires one for a run it advertises as durable.

### 3.2 Public SDK projection

`RunInfo` gains additive fields:

```go
type RunInfo struct {
    // existing fields omitted
    State      string
    HostID     string
    LeaseUntil time.Time
    LastSeq    uint64
    Cause      string
}
```

`ListRuns` and `GetRun` fold `run_start`, `run_state`, `run_event`, and `run_end`. They never
mutate the store. Reconciliation is an explicit host operation, not a surprising side effect of
a read-only SDK call.

### 3.3 Editor-host protocol

```text
run.list       { agent_id, store? } -> { runs: DurableRunInfo[] }
run.get        { agent_id, run_id, store? } -> { run: DurableRunInfo }
run.events     { agent_id, run_id, after_seq?, limit?, store? }
               -> { events: EventParams[], next_seq, terminal, truncated }
run.reconcile  { store? } -> { interrupted: RunRef[] }
task.history   { agent_id, run_id?, store? } -> { tasks: DurableTaskInfo[] }
```

`session.send` continues to return the run id. Acceptance means the `accepted` state was synced
to the selected store; if that write fails, no model call or tool starts and the request fails.

Existing `tasks.list/output/cancel` keep addressing the live `Manager`. `task.history` is the
durable read-only projection and works after the session closes. A client may reconnect the live
surface only when the original host answers a challenge containing its `HostID`, task id, and
current task sequence. This supports editor reconnect while the owner host still lives; it does
not make a task cross an `anvil-host` restart.

The existing `event` notification gains optional `seq`. A client reconnects by reading
`run.events(after_seq)` and then subscribing to live notifications. The host takes one snapshot
of the durable high-water mark before attaching the live listener; events at or below that mark
come from the read, later events come from the listener. Duplicate sequence numbers are
discarded by the client. A jump in live sequence means the bounded SDK channel dropped a
notification; the client fills the gap from `run.events` rather than guessing.

### 3.4 Retention

Persist only user-visible `EventParams` needed to reconstruct the run surface: assistant text,
tool calls/results, todo state, retry/truncation/compaction notices, errors, and terminal events.
Reasoning remains non-persistent. A configurable per-run byte and event cap keeps the newest
events and writes a gap marker with the first retained sequence. A gap is reported as
`truncated: true`; sequence numbers are never renumbered.

Transcript messages remain the long-term conversation record. Durable events are a bounded
operational replay, not a second unbounded transcript.

## 4. Data flow

### 4.1 Starting and running

1. The host allocates `run_id`.
2. It appends and syncs `run_start`, then `run_state(accepted, host_id, lease_until)`.
3. It installs the required `RunJournal`, starts the SDK run, and appends
   `run_state(running)`.
4. `Run.emit` assigns and journals each sequence before attempting the bounded live channel.
   The host enqueues received notifications to `outbound`; a channel drop leaves a detectable
   sequence gap and a complete durable record.
5. A background task appends `task_state(running)` before its start is reported and appends each
   terminal transition once. Its owner uses the same host lease.
6. The host periodically renews the run/task lease with another `running` state record.
7. On completion it appends the terminal state and the normal `run_end`.

A journal failure while running is terminal for durable control. `Run.emit` records the first
failure, cancels the run, and `Wait` returns it; the host appends an error if possible and emits a
non-durable last error on the existing channel. It never continues executing while presenting
the run as durable.

### 4.2 Reconnect

The extension records `{agent_id, run_id, last_seq}` in workspace state. On reload it calls
`run.get`; if the same host still owns a fresh lease, it reads missed events and resumes live
display. If terminal, it reads the tail and renders the result. If no live owner can be proven,
the run is reconciled to `interrupted`.

### 4.3 Host restart

At startup, `run.reconcile` scans non-terminal states in the selected workspace store. A state
owned by a different or expired `HostID` becomes:

```text
interrupted: host ownership was lost; process state is unknown
```

No signal is sent to a guessed PID. No task is recreated. Stored messages, events, child
transcripts, and checkpoints remain inspectable. The user may start a new run or restore a
checkpoint; neither is called “resume execution”.

Every non-terminal task owned by the lost host becomes `interrupted` in the same reconciliation.
A terminal task remains terminal. A completion racing reconciliation is idempotent by
`{task_id, task_seq}` and terminal-state monotonicity. A bare PID, matching command line, or
existing child transcript is never sufficient to reconnect.

## 5. Core invariants

1. **Accepted means durable.** No provider call or tool starts before the acceptance record is
   synced.
2. **One writer per session.** Existing `Backend.TryLock(agentID)` remains mandatory for a host
   that may append run state.
3. **Event sequence is append-only and per run.** A sequence is emitted only after its record is
   durable.
4. **Terminal is monotonic.** No state transition leaves `finished`, `error`, `cancelled`, or
   `interrupted`.
5. **Unknown is not running.** An expired lease or lost host becomes `interrupted`.
6. **No recovered handle, no control claim.** A restarted host never offers Cancel for a process
   it did not create and still hold.
7. **Transcript and checkpoint identity remain `(agent_id, run_id)`.** Durable events do not
   invent a third identity.
8. **A child never widens its parent.** Persisted child events and identities preserve chapter
   [42](42-subagents-and-tasks.vscode.md)'s permission and channel boundaries.
9. **Task persistence is not task survival.** Records survive; handles and execution ownership
   do not survive host restart.

## 6. Security, privacy, and failure semantics

- Durable events contain local prompts, model output, tool arguments, paths, and already-redacted
  tool results. They are sensitive local data under `.anvil`, with the same filesystem boundary
  as session records. They are never indexed or uploaded by this milestone.
- Reasoning, provider-private state, API keys, approval secrets, environment snapshots, and raw
  process handles are never persisted.
- `run.events` resolves the agent through the request's workspace and selected backend, as
  `session.transcript` does. A run id from another agent returns `run not found`.
- A malformed JSONL tail keeps the existing crash-safe rule: valid records before it load; the
  affected run is shown as interrupted with a storage warning. SQLite skips malformed payloads
  only under the existing behavior, and parity tests require the same projected outcome.
- A lease is advisory liveness. Clock skew can make reconciliation conservative; it may delay
  interruption, but may not transfer ownership or enable Cancel.
- If the client stops reading, `outbound` may grow; store retention bounds replay data, not the
  host's transport memory. Transport back-pressure remains a separate operational concern.

## 7. Migration and compatibility

- Old stores contain no `run_state` or `run_event`. Their completed `run_end` records project as
  terminal; an unmatched historical `run_start` projects as `interrupted` with cause
  `legacy unfinished run`.
- Existing `event` consumers ignore optional `seq`; existing hosts answer `-32601` to new
  methods, and the extension falls back to `session.transcript` plus today's live behavior.
- `ProtocolVersion` does not move for additive fields and methods under the existing host rule.
- JSONL remains the default. Selecting SQLite does not migrate JSONL automatically, merge two
  stores, or search both.
- Existing task ids written before task journaling are not imported. After a host restart the
  live `tasks.list` is empty until new tasks start; `task.history` shows journaled former tasks
  as terminal or interrupted.

## 8. Test strategy

- Store contract tests run the same lifecycle fixtures against JSONL and SQLite: state folding,
  sequence order, terminal monotonicity, malformed tails, deletion, and locks.
- SDK tests pin `ListRuns`/`GetRun` projections for legacy and new records.
- Task tests cover running/terminal transitions, output offsets, cancel/completion races,
  duplicate replay, live-owner challenge success, host-incarnation mismatch, and the rule that a
  PID alone never reconnects.
- Host tests block stdout as `outbound_test.go` does and prove persisted sequences remain
  complete even when live `Run.events` would otherwise overflow.
- Crash tests terminate a host after `accepted`, during a tool call, and after terminal state but
  before `run_end`; restart reconciliation must produce the specified state without attempting
  process control.
- Reconnect tests race snapshot and subscription and assert exactly-once rendering after client
  deduplication.
- Privacy fixtures assert that reasoning, credentials, provider state, and environment values do
  not appear in `run_event`.
- Windows and POSIX integration tests cover process-tree cancellation while the original host is
  alive, and the absence of Cancel after that handle is lost.

## 9. Non-goals

- Keeping `anvil-host`, a shell process, or a provider stream alive across machine restart.
- Reattaching stdout/stderr pipes or cancellation to an arbitrary surviving OS process.
- A distributed task queue, remote worker recovery, or exactly-once tool execution.
- Persisting chain-of-thought.
- Removing transcript tail limits or making event storage an unlimited audit log.
- Automatically rerunning an interrupted prompt.

## 10. Delivery

| Phase | Contract |
|-------|----------|
| p_0.54.1 | Ordered run/event journal, `RunInfo` projection, backend parity, legacy folding |
| p_0.54.2 | Durable task state, live-owner reconnect handshake, interruption and cancel-race semantics |
| p_0.54.3 | `run.*`/`task.history`, VS Code rehydrate/reconcile, bounded retention and crash/process-handle tests |
