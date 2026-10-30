# 52 — Editor-host bridge for Single, Multi, and Complex

**Milestone focus:** 0.52. **Design status:** in_progress.

## 1. Today / problem

The SDK side is delivered. `Mode`, `ModeSingle`, `ModeMulti`, `ModeComplex`, and
`Options.Mode` are defined in
[`source/agent_options.go`](../../../source/agent_options.go). Multi registers the structured
`delegate` path in
[`source/mode_delegate.go`](../../../source/mode_delegate.go); Complex runs Decision → Analyze
→ Worker using `ComplexOptions` in
[`source/mode_complex.go`](../../../source/mode_complex.go).

The editor bridge does not select those modes. `SessionParams` in
[`source/internal/editorhost/proto.go`](../../../source/internal/editorhost/proto.go) has
`subagents` but no agent-mode field, and `Server.buildOptions` in
[`source/internal/editorhost/server.go`](../../../source/internal/editorhost/server.go) never
sets `Options.Mode`.

VS Code already draws a collaboration picker in
[`editors/vscode/src/chatPanel.ts`](../../../editors/vscode/src/chatPanel.ts), but “Single”
and “Multi” currently toggle only `subagents`; Complex is disabled. Thus the visible “Multi”
does not enable `delegate`, and no editor session can enter SDK Complex mode. Child events do
reach the panel through `SubagentEvent` and `parent_tool_call_id`, but structured Brief and
Acceptance information is rendered only as generic tool arguments/output.

## 2. Goals

1. Carry the selected editor mode through `SessionParams` to `Options.Mode` on both new and
   resumed sessions.
2. Give VS Code exact, stable meanings for Single, Multi, and Complex.
3. Reopen the same conversation when mode changes; never mutate a running SDK `Agent`.
4. Render a delegate Brief and its Acceptance report as typed UI, without treating model text
   as trusted markup.
5. Make Complex stages and Worker fan-out legible using existing child-event identity.
6. Preserve behavior for clients that do not send the new field.

## 3. Mode protocol

The wire uses `agent_mode` to avoid collision with the existing permission
`permission_mode`:

```go
type SessionParams struct {
    // ...
    AgentMode string `json:"agent_mode,omitempty"` // single | multi | complex
}

type SessionResult struct {
    // ...
    AgentMode string `json:"agent_mode"` // always resolved and explicit
}
```

TypeScript mirrors it:

```ts
export type AgentMode = 'single' | 'multi' | 'complex';

export interface SessionParams {
  // ...
  agent_mode?: AgentMode;
}

export interface SessionResult {
  // ...
  agent_mode: AgentMode;
}
```

`agent_mode` is intentionally distinct from `subagents`. Mode chooses orchestration;
`subagents` controls whether child-agent infrastructure is registered.

### 3.1 Resolution

`buildOptions` applies this table:

| Wire value | `Options.Mode` | VS Code child policy |
|---|---|---|
| absent | `ModeSingle` | legacy `subagents` behavior |
| `single` | `ModeSingle` | extension sends `subagents: false` |
| `multi` | `ModeMulti` | extension sends `subagents: true` |
| `complex` | `ModeComplex` | extension sends `subagents: true` |
| anything else | session creation fails with `invalid params` | no fallback |

For generic editor-host clients, explicit `multi` or `complex` with `subagents: false` is
invalid and fails before `anvil.Create`/`Resume`. Explicit `single` with `subagents: true` is
allowed: the SDK's existing `spawn_agent` capability is orthogonal to `delegate`. VS Code uses
the stricter product definitions below and never sends that combination.

`SessionResult.agent_mode` is explicit even though SDK Single is represented by the empty
`ModeSingle` value. A client never has to know the SDK's zero-value encoding.

## 4. VS Code mode definitions

- **Single:** one root agent; VS Code sends `agent_mode: "single"` and
  `subagents: false`. No `spawn_agent`, background agent, or `delegate` is advertised.
- **Multi:** a root agent may investigate through `spawn_agent` and execute structured work
  through `delegate`; VS Code sends `agent_mode: "multi"` and `subagents: true`.
- **Complex:** the SDK owns the fixed Decision → Analyze → Worker run and Worker fan-out; VS
  Code sends `agent_mode: "complex"` and `subagents: true`.

For 0.52, Complex uses the SDK defaults already implemented by `complexSteps`: the session
model, high/medium/low effort defaults, read-only Decision/Analyze tools, and worker tools.
Per-stage model configuration is not invented in this bridge; a later explicit configuration
surface may populate `Options.Complex`.

Mode is conversation-local. `ChatSession.reopen` in
[`editors/vscode/src/session.ts`](../../../editors/vscode/src/session.ts) closes the live
session and resumes the same `agent_id` with the new options. A mode change while a turn is
running remains refused. The workspace-state key may migrate from the existing collaboration
value, but the source of truth after open is `SessionResult.agent_mode`.

## 5. Brief and Acceptance protocol

The SDK currently has typed `Brief` during execution and a deterministic textual report from
`report`, but `SubagentEvent` carries only identity plus an `Event`. This design adds optional
presentation metadata without making it part of model history:

```go
type BriefView struct {
    Goal       string
    Plan       []string
    Files      []string
    Acceptance []string
}

type AcceptanceItem struct {
    Text   string
    Status string // met | not_met | unreported
}

type AcceptanceView struct {
    Items           []AcceptanceItem
    Changed         []string
    ChangesRecorded bool
}

type SubagentEvent struct {
    // existing identity fields...
    Role  Role
    Stage string     // "", decide, analyze, or work
    Brief *BriefView // worker only
    Event Event
}
```

The parent tool result gains an optional typed acceptance payload:

```go
type Event struct {
    // existing fields...
    Acceptance *AcceptanceView
}
```

`mode_delegate.go` assembles both the existing text report (the parent/model contract) and
`AcceptanceView` (the container contract) from the same validated `Brief`, parsed claims, and
checkpoint-derived changed paths. The typed view never parses rendered text after the fact.

Editor-host `EventParams` and `HostEvent` add:

```text
agent_role: "subagent" | "worker" | "decision" | "analyze"
stage:      "decide" | "analyze" | "work"
brief:      { goal, plan[], files[], acceptance[] }
acceptance: { items:[{text,status}], changed:[], changes_recorded:boolean }
```

All are optional and additive. Repeated child events may carry role/stage for stateless routing;
`brief` is sent only on the first event for that child. `acceptance` appears on the parent
`tool_result`. If a child fails before a report, the existing `is_error`/`content` path remains
authoritative and no successful acceptance object is synthesized.

## 6. UI data flow

1. The picker updates the conversation's `agent_mode` and matching `subagents` option.
2. `ChatSession.reopen` resumes the same agent; the panel updates from the returned resolved
   mode, not from the click optimistically.
3. Multi `delegate` tool calls render a **Brief** card: Goal, ordered Plan, allowed Files, and
   Acceptance checklist. Every string is assigned with `textContent`, following the existing
   rule in [`editors/vscode/media/chat.js`](../../../editors/vscode/media/chat.js).
4. Child events attach under that card through the existing `parent_tool_call_id`.
5. The final tool result settles the card with one badge per typed acceptance item:
   Met, Not met, or Unreported, plus checkpoint-derived changed files.
6. Complex events use `stage` to create Decision, Analyze, and Work sections. Worker cards use
   the same Brief/Acceptance component as Multi; they are not a second renderer.
7. Raw result text remains available behind the existing output affordance for diagnostics and
   compatibility.

## 7. Core invariants

- **Mode reaches the SDK:** every explicit wire value maps to exactly one `anvil.Mode`; no UI
  label is implemented only by hiding tools.
- **Session property:** mode changes only through close/resume and never during a run.
- **No capability widening:** Multi/Complex require child infrastructure; a child still
  inherits permissions, budgets, and confinement from the SDK.
- **One report source:** parent text and UI acceptance status are projections of the same
  report computation.
- **Facts versus claims:** changed files come only from checkpoint records. Model-reported
  acceptance remains labeled Met/Not met/Unreported and is not presented as verified fact.
- **Safe rendering:** Brief and Acceptance strings are data, never HTML or commands.

## 8. Security and failure semantics

Unknown modes and incompatible explicit mode/subagent combinations fail session creation; they
never degrade to Single. A failed reopen rolls back through `ChatSession.reopen`'s existing
configuration rollback.

Complex in permission Plan mode continues to be refused by the SDK because Worker is editing
by definition. The editor shows that error and keeps the prior live mode. A malformed Brief is
refused before a worker starts by `validateBrief`; the card displays the tool error and does
not invent acceptance rows. Missing checkpoint data is rendered as “changes not recorded,”
not as an empty changed-file list.

## 9. Migration and compatibility

- An older client omits `agent_mode`; host behavior remains Single plus its existing
  `subagents` switch.
- A current client talking to the synchronized current host sends the field. This repository
  ships host and extension together, so no speculative capability negotiation is added.
- Existing stored collaboration values `single` and `multi` migrate directly. No stored value
  migrates to Complex automatically.
- Existing transcript and `SubagentEvent` consumers ignore optional metadata. No mode is
  persisted into SDK transcript history, consistent with chapter
  [50](50-agent-modes.md); VS Code keeps the conversation choice and resends it on resume.

## 10. Test strategy

Go protocol tests assert all mode mappings, absent-field compatibility, invalid combinations,
`SessionResult.agent_mode`, and new/resume parity. Host integration tests prove that Multi
advertises `delegate`, Single does not, and Complex emits all three stage identities.

SDK tests pin typed Brief/Acceptance metadata against the existing text report, including
unreported claims, failed workers, and checkpoints off. TypeScript unit tests cover option
migration, reopen rollback, current-state reconciliation, and all three picker values.
Webview tests assert safe Brief rendering, status badges, changed-file provenance, and Complex
nesting. One real-host smoke uses scripted completions only; no provider is required.

## 11. Non-goals

- Automatic mode selection or changing mode mid-turn.
- New Complex stages, reviewer/re-plan loops, or stage retries.
- Per-stage model settings in VS Code.
- Treating a worker's `[met]` claim as independent verification.
- Persisting mode in the SDK transcript or rewriting existing transcripts.
- Replacing the existing Agent Map or task table.

## 12. Delivery phase mapping

| Phase | Contract |
|---|---|
| p_0.52.1 | `agent_mode` request/result protocol, validation, `SessionParams` → `Options.Mode`, new/resume and compatibility tests |
| p_0.52.2 | Three-value VS Code picker, conversation-local persistence/migration, close/resume behavior, Complex enabled |
| p_0.52.3 | Typed role/stage/Brief/Acceptance metadata, shared Multi/Complex cards, security and end-to-end display tests |
