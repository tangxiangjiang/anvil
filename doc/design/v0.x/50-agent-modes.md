# 48 — Agent modes: Single, Multi, Complex

**Milestone focus:** 0.50. Statuses live in
[m_0.50_agent_modes.md](../../roadmaps/v0.x/m_0.50_agent_modes.md).

## 1. Today

Everything a mode needs to stand on already exists, and none of it is a mode:

- **Subagents** ([subagent.go](../../../source/subagent.go), chapter
  [41](42-subagents-and-tasks.vscode.md)): `spawn_agent` creates a child with a tool subset, a step
  ceiling, a model override, a shared tree budget, a depth limit, and optional background
  execution. The child inherits the parent's permission callback, rules and checkpoints.
- **Workflows** ([workflow.go](../../../source/workflow.go)): `run_workflow` takes a validated
  DAG of steps with dependencies, `{{id.result}}` substitution, `for_each` fan-out over a JSON
  array, bounded concurrency, and cancellation.
- **Tool groups** ([groups.go](../../../source/internal/tools/groups.go)): twelve named groups
  that a selector expands.

What is missing is the part that is not a tool: nothing decides *how* a session's work is
divided. A model that is never told it may delegate rarely does; a model that delegates through
`spawn_agent` hands over a free-text prompt nobody can check; and there is no path in which an
architect, a planner and an implementer are different calls with different budgets.

This chapter defines three modes as SDK interfaces. Which model and which effort plays each
part is configuration, read by the host or the worker and passed in through `Options`. The
SDK chooses no model.

## 2. A ladder, not three products

| Mode | Is | Adds |
|---|---|---|
| **Single** | today's agent | — |
| **Multi** | Single + `delegate` | a structured hand-off to a worker child, and a main-role prompt that says when to use it |
| **Complex** | Multi + a fixed three-stage run | Decision → Analyze → Worker, orchestrated by the SDK as a workflow; each stage a child agent |

The alternative — three parallel implementations selected by a flag — was rejected:

- Every invariant this SDK claims (the sandbox, the permission engine, the loop guard,
  checkpoints, "a child never widens its parent") is proven against one loop. Three loops would
  need three proofs, and the second and third would be written later and tested less.
- Each rung is built from the one below. Multi's child is a Single agent with a brief; a
  Complex Worker is that same child; a Complex run is a workflow. A bug fixed in Single is fixed
  in all three.
- The ladder gives the container one sentence to tell a user: a higher mode never removes a
  capability of a lower one; it adds structure and costs more tokens.

## 3. The mode is a session property

```go
type Mode string

const (
    ModeSingle  Mode = ""        // today
    ModeMulti   Mode = "multi"
    ModeComplex Mode = "complex"
)
```

`Options.Mode` is set at `Create` or `Resume` and does not change for the life of that `Agent`.
It is not a `SendOption`, and it is not a field a tool can change mid-run:

- Checkpoints, the loop guard and the agent budget are all per run. A run that changed mode
  halfway would have two sets of each, and a rewind would have to know which.
- The role prompt (chapter [47](49-roles-and-prompts.md)) is part of the system prompt, which
  the cache clock (chapter [42](43-context-management.vscode.md)) relies on being stable within a
  session.
- A container that wants a different mode for the next request resumes the session with new
  `Options`, which is the path `SystemPrompt`, `MCPServers` and the other non-persisted options
  already take. The transcript carries no mode; it is the container's configuration, like the
  model.

This is the same rule the permission mode already follows.

## 4. Multi: `delegate`

### 4.1 Why a new tool and not `spawn_agent`'s prompt

`spawn_agent` is the right tool for "go and find out": a free-text task whose answer is text.
It is the wrong tool for "go and do this part", for three reasons, each a cost of free text:

| With `spawn_agent` | With `delegate` |
|---|---|
| the plan lives inside a prompt string; nothing can validate that it names files or says what done means | the brief is a schema; a brief with no files or no acceptance is refused before a child starts |
| the child may edit anything its tool set allows | the brief's `files` become a deny rule for everything else, enforced by the permission engine, not by the prompt |
| the parent learns one string | the parent learns a report with one line per acceptance item, so it can see *which* part was not done |

A third option — the parent writes a plan file to the workspace and the child reads it — was
rejected because it breaks chapter 41's second invariant (a tool result is the only thing a
parent learns from a child; a shared file is a second channel), and because a plan file is
workspace state that a checkpoint would then have to track.

The cost: the `agent` tool group gains a tool, and a model has two ways to create a child. The
tool descriptions say which is for what, and the main-role prompt in Multi mode says it again.

### 4.2 The brief

```json
{
  "goal":       "one sentence: what the child is to achieve",
  "plan":       ["ordered steps the child follows"],
  "files":      ["paths the child may change; a directory ends with /"],
  "acceptance": ["checks the child reports against, one per line"],
  "tools":      ["as spawn_agent; default read, search, edit, verify"],
  "effort":     "low | medium | high; default: the parent's",
  "max_steps":  0,
  "background": false
}
```

Validation, before any agent starts: `goal` non-empty; `plan` and `acceptance` each have 1–20
items; `files` has 1–64 entries, every one inside the workspace (the same `ResolveInRoot` the
sandbox uses); `tools` resolves as `spawn_agent`'s does. A refused brief names every problem,
the way `run_workflow` does.

### 4.3 What the child gets

- **Role `worker`** (chapter 47 §3), so its L1 says to follow the brief and report against the
  acceptance list.
- **The brief rendered as L3**, from the validated struct, not from the model's JSON. The
  rendering is the SDK's, so a model cannot smuggle an instruction into the layer by formatting
  it oddly.
- **A write allowlist**: `PermissionRules` gains a generated deny for `write`, `edit` and
  `apply_patch` on every path outside `files`, placed after the parent's own rules. The parent's
  rules still apply first, so a path the parent may not touch stays untouchable whatever the
  brief says. The rule is generated from `files`, not from the prompt, and chapter
  [36](36-tool-permissions.vscode.md)'s engine is what enforces it.
- **The parent's effort** unless the brief sets one.
- Everything `spawn_agent`'s `childOptions` already copies, by the same whitelist.

### 4.4 What the parent gets

The tool result is the child's final message, preceded by a report the SDK assembles:

```
agent-xyz finished in 14 steps.
acceptance:
  [met]     the test in pkg/x passes
  [not met] no file outside pkg/x changed — edit_file on README.md was refused
changed: pkg/x/a.go, pkg/x/a_test.go
```

The `[met]`/`[not met]` marks come from the child: its worker role asks it to end with a line
per acceptance item, and the SDK parses that block if present. A child that reports nothing is
listed as `[unreported]` for every item, which the parent can read as "check it yourself". The
`changed:` line is the SDK's own, from the child's checkpoint record — it is a fact, not a
claim, and it is how the parent sees a child that edited outside its brief before the deny
rule existed in a session that disabled rules.

The brief is written to the child's meta record under `brief`, so a replay of the parent shows
what the child was asked, and `/tasks` can title the card by `goal`.

### 4.5 The main role in Multi

The main role's L1 gains one paragraph in Multi mode, saying: prefer to analyse and decide
yourself; delegate work that is **well specified** — you can name the files and say what done
looks like — and keep for yourself work that is not; read the acceptance report rather than
assuming. That paragraph is the whole behavioural difference between Single and Multi for the
main agent.

## 5. Complex: three stages as a workflow

### 5.1 The stages

| Stage | Role | Input | Output |
|---|---|---|---|
| Decision | `decision` | the user's message, the workspace (read-only tools) | a **decision document** (§5.3) |
| Analyze | `analyze` | the decision document, the workspace (read-only) | a **roadmap** (§5.3): ordered work items, each a complete `delegate` brief |
| Worker | `worker` | one roadmap item | the item's acceptance report (§4.4) |

Three stages, not four or two: Decision and Analyze are separated because they want different
effort and different budgets — Decision is the long chain of reasoning, Analyze is the mapping
of that reasoning onto files — and merging them gives one agent two jobs with one budget.
A fourth review stage was considered and left out: a Worker that reports against acceptance,
plus a `verify` tool it can run, covers what a reviewer would check, and a reviewer that
disagrees with a Worker has no stage to hand the disagreement to.

### 5.2 Expressed as `run_workflow`, not a new scheduler

A Complex run is a workflow spec the SDK builds and `executeWorkflow` runs:

```json
{
  "name": "complex",
  "steps": [
    {"id": "decide",  "role": "decision", "effort": "high",   "tools": ["read", "search", "git"], "prompt": "<user message>", "result_schema": "decision"},
    {"id": "analyze", "role": "analyze",  "effort": "medium", "tools": ["read", "search"], "depends_on": ["decide"], "prompt": "{{decide.result}}", "result_schema": "roadmap"},
    {"id": "work",    "role": "worker",   "effort": "low",    "for_each": {"from": "analyze", "path": "items"}, "prompt": "{{item}}", "brief_from_item": true}
  ]
}
```

Three fields are added to a workflow step, and they are the whole extension:

- `role` — the child's `Options.Role`. Default empty: today's subagent role.
- `effort` — the child's `Options.Effort`. Default: the parent's.
- `result_schema` — the name of a schema the step's final message must satisfy. A step whose
  message does not parse, or parses and fails validation, **fails the step** with the
  validation errors, exactly as a `for_each` whose target is not an array fails today. Its
  dependants do not run. This is what makes Analyze's output something Worker can consume
  rather than something it has to interpret.

`brief_from_item` is the fan-out's bridge: each item of the roadmap is a brief, and the Worker
child is created through `delegate`'s path (§4.3) rather than `spawn_agent`'s, so the file
allowlist and the acceptance report apply.

The reasons not to write a scheduler: the DAG, the budget, the depth limit, cancellation, the
task table and the event envelopes exist and are tested; a Complex run needs all of them and
nothing they lack. A second orchestrator would be a copy of `executeWorkflow` with three steps
hard-coded, and chapter 41's invariants would have to be proven for it separately. Building
Complex on the workflow also means a workspace that wants a *fourth* stage can write the
workflow by hand, with the same three fields, and the SDK has not decided that for it.

The effort per stage above is the **default** `Options.Complex` carries; the container's
configuration overrides each stage's model and effort.

### 5.3 The schemas

Both are JSON objects whose prose fields hold prose. Structure is for what a machine consumes;
the text inside is for the next model.

**`decision`:**

```json
{
  "summary":     "string — the decision in a paragraph",
  "constraints": ["string"],
  "options":     [{"name": "string", "rationale": "string", "rejected_because": "string|null"}],
  "chosen":      "string — an option's name",
  "risks":       ["string"],
  "out_of_scope": ["string"]
}
```

Required: `summary`, `chosen` naming an entry of `options`, at least one option. The Decision
agent is told the schema in its L3 and is told that a document that fails it is refused.

**`roadmap`:**

```json
{
  "items": [
    { "id": "string, unique",
      "goal": "string", "plan": ["string"], "files": ["string"], "acceptance": ["string"],
      "depends_on": ["id"] }
  ]
}
```

Required: 1–16 items, each a valid brief (§4.2 minus `tools`/`effort`, which the stage config
sets), ids unique, `depends_on` acyclic and known. The validation is `delegate`'s validator
applied per item plus the workflow's own dependency check applied to the items.

A roadmap's `depends_on` becomes the fan-out's ordering: items with no dependencies run
concurrently under the workflow's `max_parallel`; an item waits for the ids it names. This is
the one change to `for_each` — today's fan-out items are independent — and it reuses the
step-level dependency code on item granularity.

### 5.4 What the user sees

`Agent.Send` in Complex mode returns a `Run` as always. Its stream carries the workflow's
`SubagentEvent`s with `Depth`, so a client draws three cards (decide, analyze, work) and the
Worker fan-out under the third, as it already draws a workflow. The run's final message is the
workflow's rendered result: each stage's status, the decision's `summary`, and one acceptance
report per Worker item. The container is not required to understand any of it to display it.

## 6. The invariants under every mode

The capabilities that are Anvil's own have to be as true in Multi and Complex as in Single, and
the way to keep them true is to run their tests under every mode. Chapter [45](47-evaluation.md)
§6.1's `invariants` suite runs three times, once per `Options.Mode`:

| Invariant | In Multi | In Complex |
|---|---|---|
| permission rules and callback | a worker inherits both; the brief's file allowlist is an extra deny, never an allow | each stage inherits both; Decision and Analyze have no write tools at all |
| loop guard | per agent, per run; a worker that loops ends with `loop_detected` and its parent reads `[not met]` | the same; a looping Worker fails its item, not the workflow's other items |
| checkpoints | a worker's edits are captured under its own run, in the parent's tree, so `RestoreCheckpoint` on the parent's run restores what the parent *and* its children changed | the same, across all Worker items |
| plan mode (write tools removed) | `delegate` is in the removed set: a plan is a document, not a child that edits | `Send` refuses Complex in plan mode with a clear error — a Worker stage is editing by definition |
| agent budget and depth | a `delegate` child counts against both, as `spawn_agent`'s does | three stages plus N items count; a roadmap larger than the budget is refused by Analyze's validator before any Worker starts |
| a tool result is the only channel | the acceptance report is part of the tool result | `{{analyze.result}}` is the only thing Worker reads; the SDK renders it, not a file |

The checkpoint row is a real change: today a child's checkpoint is keyed by the child's own run
id, and restoring the parent's run misses it. The fix keys a child's captures under the root
run of the tree with the child's agent id as a path segment, so one `RestoreCheckpoint` undoes
a Complex run entirely. That is listed in p_0.50.3 and it is why the `inv_checkpoint_restore`
task exists per mode.

## 7. What the container does

Named here so the boundary is explicit; none of it is in this line's phases:

- reads mode, per-stage model and effort from its configuration (host settings, worker
  `llm` block, VS Code settings) and passes them in `Options`;
- offers the mode in a UI, and resumes the session when a person changes it;
- draws `delegate` and stage cards from the `SubagentEvent` stream it already consumes.

## 8. Non-goals

- A model-driven mode router ("pick Single or Complex for me"). The mode is configuration.
- Changing the mode within a session.
- A reviewer stage, a loop that re-plans after a Worker fails, or any retry of a stage.
- Stages that run on different machines or in different workspaces.
- Persisting the mode in the transcript.
- A plan-file hand-off between parent and child.

## 9. Delivery

This chapter is delivered by [Milestone 0.50](../../roadmaps/v0.x/m_0.50_agent_modes.md), through the phases below.

| Phase | Contract |
|-------|----------|
| p_0.50.1 | §3, §4: `Options.Mode`, `delegate`, the brief, the allowlist, the report, the Multi main prompt |
| p_0.50.2 | §5: the three step fields, the two schemas, item dependencies, `Options.Complex`, `Send` in Complex mode |
| p_0.50.3 | §6: the invariants suite under three modes, the tree-keyed checkpoint |
