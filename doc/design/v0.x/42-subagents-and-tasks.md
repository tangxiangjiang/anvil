# 41 — Subagents, background tasks, and workflows

**Milestone focus:** agent-in-agent, work that outlives a tool call, a `/tasks` surface, and
declarative orchestration → **[m_0.42_subagents_and_tasks.md](../../roadmaps/v0.x/m_0.42_subagents_and_tasks.md)**.

Chapter [01](01-architecture.md) and chapter [03](03-runtime-loop.md) define a run as one model
loop for one agent, with tools as synchronous functions called from inside that loop. Every other
surface in this tree is built on that sentence. Claude Code's Agent tool, background shell,
`/tasks` and Workflow each assume a runtime that can start a second agent *inside a tool call* and
keep a process alive after the turn that started it. Anvil has neither, which is why 0.40 listed
"subagents (subagents)" as a non-goal rather than a phase.

This chapter adds both, and nothing else. The runtime's concurrency model does not change: a
subagent is an ordinary `anvil.Agent` created by a tool, and a background task is a table the
parent agent owns. What has to be added is the tool, enough **identity** for the store, events and
permissions to say which agent is speaking, and a **task table** for work that outlives the call
that started it.

## The facts this design is built on

1. **One run per agent.** `Agent.Send` refuses while `a.running`; `executeRun` owns the registry,
   the history write-back and the run result ([01](01-architecture.md)).
2. **Tools are synchronous.** `runtime.runToolCalls` calls `registry.Execute` and waits; a tool
   that blocks blocks the round, and a same-turn batch is only parallelised when no call is
   mutating.
3. **Events are per run.** `executeRun` passes every `runtime.Event` through `run.emit` into one
   `Run.events` channel. A tool has no emitter and no event sink.
4. **The store is per agent.** `{cwd}/.anvil/agents/agent-<id>.jsonl`, append-only, summarised by
   `store.Summarize`.
5. **The permission callback knows the session, not the agent.** The host installs
   `Options.Permission` per `session.new`; it resolves the active run from the session, and hands
   the user a card naming a tool.

Therefore a subagent **cannot** be a coroutine inside the parent's loop, a second goroutine
sharing the parent's registry, or a second run on the same `Agent`. Every one of those would
break fact 1, and the second would also break the run context that checkpoints and permissions
depend on. It is a second `Agent`, with its own store file, its own registry and its own run.

### Why a subagent is worth a second agent

The value is not "parallelism" — a foreground subagent is slower than doing the work inline,
because the parent's tool call blocks on it. The value is **context isolation**:

- the child's transcript, search results and failed attempts never enter the parent's history, so
  the parent's context stays about the task, not about the search that found its inputs;
- a child can be given a narrower tool set (`read`, `search` — no `write`) and run against the
  same workspace without the parent relinquishing its own tools;
- a child can be replaced, cancelled or retried without corrupting the parent's conversation —
  the only thing the parent sees is one tool result.

Everything in this chapter follows from that: identity, permissions, events and the task table all
exist so the second agent is observable, accountable and cancellable from the first.

## A subagent is an ordinary agent

`spawn_agent` is a built-in tool registered in `buildRegistry` when subagents are enabled. Its
`Execute` constructs child `Options`, calls `Create`, `Send`s the brief, and either waits
(foreground) or files the run in the task table (background).

The child inherits a **whitelist** of the parent's options, not the parent's struct:

| Inherited | Deliberately not inherited |
|---|---|
| `Provider`, `APIKey`, `BaseURL`, `Model` (overridable per call) | `Tools` handlers registered by the embedder |
| `Local.CWD`, `Local.LoadRules`, `Local.LoadCursorRules`, `Local.LoadHooks`, `Local.LoadPermissions`, `Local.Store`, `Local.SettingSources` | `Plugins` (a child does not load plugin processes) |
| `WebSearch`, `Vision`, `Test`, `Build`, `LSP` — but see the tool selector below | `Attachments` (a brief is text) |
| `Permission`, `PermissionGroups`, `PermissionRules` | `SystemPrompt` (the child gets a subagent brief; see below) |
| `MaxWallTime`, `ParallelTools`, `ParallelToolsMax`, `Checkpoints` | `Skills` (an always-skill is a statement about the main conversation's job) |
| `MCPServers`, `LSP` and `WebSearch` **only when the child's tool selector names the group** | `HistoryMaxChars` (a child starts with an empty history; compression has nothing to do) |

Three of those exclusions are load-bearing:

- **No inherited system prompt.** The parent's prompt describes the main agent's job. A child gets
  a short, fixed brief: it is a subagent, it answers the task in its final message, it does not
  address the user, and it has exactly the tools listed. A task-specific instruction is the
  `prompt` parameter, not a second system prompt.
- **No inherited `Options.Tools` handlers.** A Go function passed to the parent is not necessarily
  safe to run concurrently in a second agent, and an embedder that wants its tools in children
  can register a `spawn_agent` wrapper of its own. This is stated in the chapter rather than
  discovered at runtime.
- **No plugins.** A plugin registers tools and may own processes; a child's job is the subset of
  built-ins its task names. A session that needs plugin tools in a child is out of scope for
  this milestone.

### The tool selector

`spawn_agent` takes `tools` — the same selectors `Options.BuiltinTools` accepts: group names
(`read`, `search`, `write`, `edit`, `shell`, `verify`, `web`, `lsp`, `git`, `vision`) or literal
tool names. It becomes the child's `BuiltinTools` allow-list. Empty means the **read-only
default**: `read`, `search`, `skills`, `git`, `vision` — a child that was not told otherwise can
look but not change.

`mcp` in the selector is what makes the child inherit `Options.MCPServers`; without it a child
starts no MCP processes. The same rule governs `web`, `lsp`, `verify` and `build`: the
option is copied only when the selector includes it, so a child's declared tools and its
configuration cannot disagree.

### `spawn_agent`

```json
{
  "prompt": "Find every caller of Registry.Execute and list the files.",
  "description": "Find Registry.Execute callers",
  "tools": ["read", "search"],
  "model": "",
  "max_steps": 0,
  "background": false
}
```

- `prompt` (required) — the brief. The child's only user message.
- `description` (required, short) — what the task is, for the card title, the store's derived
  title and the task list. It is not sent to the model; `prompt` is.
- `tools` — the selector above.
- `model` — empty means the parent's model. A non-empty id that the provider rejects fails the
  call, not the run.
- `max_steps` — 0 means the child's default (the parent's `MaxSteps`, itself 0 = unlimited);
  otherwise a hard ceiling for the child. It can only narrow, never widen: a value larger than
  the parent's non-zero ceiling is clamped with a note in the tool result, so a model cannot buy
  itself more rounds by spawning.
- `background` — see *Background tasks*.

Foreground result (the tool result the parent's model reads):

```text
agent-… finished in 7 steps.
<the child's final message>
```

The agent id is in the first line so a later `session.transcript` (or a click in `/tasks`) can
open the full conversation. A failed child is a **tool error**: `is_error: true`, the status and
the child's `RunResult.Err`, and the parent's loop continues — a subagent that failed is
information, not a reason to abort the parent's run. A child cancelled because the parent was
cancelled ends the parent run as cancelled, the same way a cancelled inline tool does.

### Depth, budget, and recursion

`Options.Subagents`:

```go
type SubagentOptions struct {
    Enabled      bool // off by default; the host turns it on
    MaxDepth     int  // default 2: a root (0) and one level of children (1)
    MaxAgents    int  // default 8 per root run tree
    MaxBackground int // default 4 concurrent background tasks per tree
    OnEvent      func(SubagentEvent)
    OnTask       func(TaskInfo) // a task started or ended
}
```

Depth is carried by the spawn closure: the root's spawn tool has `depth = 0`, a child's has
`depth = 1`. At `depth+1 >= MaxDepth` the tool is **not registered** in that agent's registry, so
the model never sees a name it cannot call; a model that tries it anyway gets the registry's
ordinary `unknown tool` error. The budget is a small mutex-guarded counter shared down the tree:
a spawn that would exceed `MaxAgents` returns a tool error naming the limit. The counter resets
with each top-level run — the budget is per run tree, not per session, because a session that
spawned eight agents yesterday should not be unable to spawn one today.

## Identity

A child that cannot be told apart from a top-level conversation is a support burden: it appears in
the session list as a stray conversation, `session.delete` leaves it behind, and a replay cannot
say where it came from.

### Store

`store.Record` (meta) and `store.Summary` gain:

| Field | Meaning |
|---|---|
| `ParentAgentID` | the agent whose tool call created this one; empty for a top-level session |
| `ParentRunID` | the run that contained the spawn call |
| `ParentToolCallID` | the `spawn_agent` call, so a replay can nest the result under its card |
| `Depth` | 0 for a top-level agent, 1 for its child, … |
| `Kind` | `subagent` for a child; empty for a top-level session |
| `TaskTitle` | the `description` parameter, as the child's title |

`Create` writes these only when a `SubagentIdentity` is supplied; the top-level path is byte-for-
byte what it was. `Summarize` carries them onto `Summary`.

`DeleteAgent` also deletes every agent whose `ParentAgentID` is the deleted id, transitively, and
their checkpoints. A child exists only in its parent's tree; leaving it behind would list a
conversation nobody can reach from any tab.

`ForkAgent` copies the parent's records only. The spawn calls in the transcript remain as tool
results, and the child files stay where they are — a fork is a copy of this conversation, not of
the work it delegated. The fork's transcript says so once, next to the first spawn result, rather
than pretending the children were copied.

### Session listing

`session.list` rows gain `parent_agent_id`, `depth`, `subagent` and `task_title` (additive). The
editor nests a child under its parent and collapses the group by default; a child has its own
transcript and can be opened in a tab (it is an agent like any other), but it is not a
conversation the user started, and the list must not present it as one.

## Permissions

A subagent runs with the **parent's callback**, so every gated call still reaches the same session
and the same person. What changes is who is asking, and the request now says so:

```go
type PermissionRequest struct {
    ToolName string
    Args     json.RawMessage
    Preview  ToolPreview
    Rule     *PermissionRuleMatch
    // AgentID is the agent whose call is being decided. Empty only for a caller
    // that installs its own callback outside Create/Resume.
    AgentID string
    // RunID is that agent's run.
    RunID string
    // Depth is 0 for the session's own agent, 1 for its child.
    Depth int
}
```

The registry fills these from the identity it is executing for; `buildRegistry` already wraps
`opts.Permission`, and the wrapper is where the fields are set. The host's approval card gains
`agent_id` and `depth` (additive), so the card can say *"subagent Find Registry.Execute callers
asks to run shell"* instead of implying the main agent is asking. The whole session's allow-list
("always allow") still applies: an approval answered for a tool in this session is an approval
for the same tool in its subagents, because it is the same person and the same policy.

Three cases are decided explicitly:

- **Rules and modes are unchanged.** The child's registry gets the same `PermissionGroups`,
  `PermissionRules`, mode hook and rule file source. A `deny` in the workspace file refuses a
  child's call exactly as it refuses the parent's; `auto` classifies a child's `git status` the
  same way.
- **The child cannot widen the gate.** The spawn tool's `tools` selector narrows which tools
  exist; it does not change which ones ask.
- **A background subagent may ask while no run is active.** The approval is still routed to its
  session; the session's cancel path withdraws it if the session closes or the task is cancelled.
  `session.cancel` with no active run keeps returning `no run`: stopping a turn is not stopping
  the tasks it started.

## Events

A foreground subagent's steps must be visible while the parent's card is waiting, and a
background subagent's steps must be visible after the card has returned. Neither can travel on
`Run.events` — that channel belongs to the parent's run and its emission points are in the parent
loop.

The SDK therefore exposes one optional callback:

```go
type SubagentEvent struct {
    ParentAgentID string // the agent that called spawn_agent
    ParentRunID   string
    ToolCallID    string // the spawn_agent call
    AgentID       string // the child
    RunID         string // the child's run
    Event         Event
}
```

`SubagentOptions.OnEvent` receives every event the child emits, including the child's terminal
`error` event. The child's `done` is not forwarded as a stream event; the spawn tool's result is
the terminal statement.

`ToolCallID` comes from the runtime, not from the model: `runToolCalls` puts the current tool call
id into the context before `registry.Execute`, and `tools.CallID(ctx)` reads it. The same context
value is what lets the result card and the child's events meet on one key even when a batch runs
several tools in parallel.

The host projects a `SubagentEvent` through the existing `event` notification, with two additive
fields on `EventParams`:

| Field | Meaning |
|---|---|
| `agent_id` | the agent that emitted this event; the session's own agent's id for parent events |
| `parent_tool_call_id` | on a child event, the `spawn_agent` call it belongs under |

The webview groups child events under the spawn card, which becomes an expandable sub-transcript:
the child's assistant text, its tool cards and its terminal status. It renders with the same
`textContent`-only rules as the parent transcript; a child event is not a second renderer.

`session_id` stays the root session's id on every event, including a child's and including a
background child's after the parent's turn has ended. The event stream is the session's, and
`parent_tool_call_id` — not a second session id — is what places a child's steps; that is also
why the editor's existing per-session routing works unchanged.

Replay is honest about the limit: the parent's stored transcript contains the spawn call and its
final result, not the child's steps. A resumed tab draws the spawn card from those two records and
offers **Open subagent transcript**, which loads the child's own store file through
`session.transcript`. Streaming and replay therefore share the card and differ only in whether
the steps are already inside it — the 0.38 rule that a replay never invents a stream (chapter
[38](38-session-identity-and-replay.md)).

## Background tasks

A background task is work the model starts and does not wait for. Two kinds share one table
because they need one surface:

| Kind | Started by | Ends when |
|---|---|---|
| `shell` | `shell` with `run_in_background: true` | the process exits, is killed, or the agent closes |
| `agent` | `spawn_agent` with `background: true` | the child's run finishes, is cancelled, or the agent closes |

### The manager

`internal/tasks` (a new package) owns a `Manager`: a mutex-guarded table of tasks, each with an
id (`task-<id>`), kind, title, status (`running`, `finished`, `failed`, `cancelled`), start and end
times, an owning agent id, and — for shell tasks — a bounded output ring and an exit code. For an
agent task the owning id is the agent whose tool call started it: the child does not exist yet
when its row is created.

- **Output** is kept in a ring of at most 1 MiB per task with a monotonically increasing byte
  offset; `Output(offset)` returns the bytes from that offset plus the next offset. A full ring
  drops the oldest bytes, and the reader is told the first offset it can still get, so a client
  never silently receives a gap.
- **Cancellation** is a `context.CancelCauseFunc` per task. A shell task's context is the one the
  process was started with, so cancelling it kills the process tree through the existing
  `configureShellCommand` path; an agent task's context is the child run's, so a cancel stops the
  child instead of only detaching from it. A cancelled shell task's output ends with
  `command cancelled` (or `command stopped when the session closed` when `Close` was the cause),
  so a reader is not left wondering why the bytes stop mid-line.
- **The manager is owned by the root agent's tree.** A child's shell job is filed in the same
  table, tagged with the child's agent id, so `Agent.Tasks()` on any agent in the tree lists the
  whole tree's work and `/tasks` is one list, not one per nesting level.
- **Close stops the tree.** The root agent's `Close` cancels every running task in its tree and
  waits up to two seconds for the processes to exit, then returns. It does not leave a shell job
  writing into a workspace whose conversation is gone. A child's own `Close` does not touch the
  table: a foreground child closes when its turn ends, and cancelling the tree from there would
  take its siblings' work with it. `session.close` therefore stops background work, and the task
  list says *"stopped when the session closed"* rather than showing a job that disappeared.

`Agent` gains:

```go
type TaskKind string   // "shell" | "agent"
type TaskStatus string // "running" | "finished" | "failed" | "cancelled"

type TaskInfo struct {
    ID        string
    Kind      TaskKind
    Title     string
    Status    TaskStatus
    AgentID   string
    StartedAt time.Time
    EndedAt   time.Time
    ExitCode  int // shell only; -1 when the process did not exit
}

func (a *Agent) Tasks() []TaskInfo
func (a *Agent) TaskOutput(taskID string, offset, limit int) (TaskOutput, error)
// CancelTask returns the status after the cancel, exactly as task_cancel does.
func (a *Agent) CancelTask(taskID string) (TaskInfo, error)
```

`TaskOutput` carries `Text`, `NextOffset`, `FirstOffset`, `Truncated` and `More` — the same
window `task_output` reads, including whether bytes remain after the chunk.

The wire form of `TaskInfo` (`proto.go`) makes `EndedAt` a `*time.Time` and omits it while the
task runs. The SDK type keeps the zero `time.Time` — an embedder reads it in-process — but a JSON
zero would be year 1, and a client cannot tell that from a date. `StartedAt` is a plain
timestamp, because a task always has one.

`TaskInfo` is a snapshot, not a subscription. `SubagentOptions.OnTask func(TaskInfo)` is called
when a task starts and when it ends, which is what the host turns into a `task` notification; it
is a table-change callback, not a second stream. There is no per-chunk task event, and a client
that wants progress reads the output ring.

### The tools

`shell` gains one parameter and loses nothing:

```json
{"command": "go test ./...", "timeout_seconds": 600, "run_in_background": true}
```

A background shell call returns immediately:

```text
task-… started in the background: go test ./...
Read it with task_output, stop it with task_cancel.
```

`timeout_seconds` still bounds the process (default 60) — a background job is not unbounded, it is
unwaited. A job that hits its timeout is a `failed` task whose output ends with the same *"command
killed after"* sentence the foreground tool writes.

Three task tools are registered beside it whenever `shell` or `spawn_agent` is:

| Tool | Arguments | Result |
|---|---|---|
| `task_list` | — | one line per task: id, kind, status, age, owning agent, title |
| `task_output` | `{task_id, offset?, limit?}` | status, exit code (shell tasks), the output chunk, the next offset |
| `task_cancel` | `{task_id}` | the task's status after the cancel |

`task_output` is how the model reads a background job without a second process; it is also the
tool a foreground turn uses after a background turn started something. `task_cancel` is a kill,
not a pause. A task that already ended returns its final status and no error: asking twice is not
a failure.

### Permission and scheduling

- A background **shell** call is a `shell` call: it is gated by the `shell` group, and its
  `timeout_seconds`/`run_in_background` arguments are part of the preview the card shows. The
  permission is asked **before** the process starts; a background job cannot be approved after it
  has run for a minute.
- A background **agent** call is a `spawn_agent` call: gated by `agent` (below), with the same
  inheritance rules as a foreground one.
- Background tasks do not participate in the parent batch's parallel scheduling. Starting one is
  quick, and it does not hold the round.

### The `agent` tool group

A new group joins the table in `internal/tools/groups.go`:

```go
"agent": {"spawn_agent", "run_workflow", "task_list", "task_output", "task_cancel"},
```

`DefaultPermissionGroups` gains `agent`, so `ask`, `accept_edits` and `auto` gate a spawn by
default. `AutoApproveCall` does **not** classify `spawn_agent` as safe: a call that spends tokens
and creates an agent with the session's permissions is exactly the kind of thing `auto` was built
to ask about. A user who wants unattended fan-out writes an `allow` rule, which is the same
mechanism they already use for `shell`.

## `/tasks`

Claude Code's `/tasks` is a view over work in flight. In the editor it becomes both a panel and a
composer command, and both are projections of the same host state.

### Host protocol

| Method | Params | Result |
|---|---|---|
| `tasks.list` | `{session_id}` | `{tasks: TaskInfo[]}` — the session's whole tree |
| `tasks.output` | `{session_id, task_id, offset?, limit?}` | `{status, exit_code, output, next_offset, first_offset, truncated}` |
| `tasks.cancel` | `{session_id, task_id}` | `{task: TaskInfo}` after the cancel |

A new notification, `task`, carries `{session_id, task}` whenever a task starts, ends, or changes
status. It is additive: a client that does not know the method ignores it, and one that does not
ask `tasks.list` still sees nothing until it does. No `ProtocolVersion` bump — the v1 client
already ignores unknown notifications and unknown fields, which is the rule every phase since 0.38
has followed.

Task RPCs route through the same session lookup as `session.cancel`, and a task id from another
session is refused with `no task` rather than leaked. `tasks.cancel` refuses a task that has
already ended by returning its final state, and `tasks.output` on an `agent` task returns the
child's final message once, then an empty chunk with `next_offset` at the end — a subagent's
"output" is its result, not a byte stream.

### The surface

- **A Tasks panel** (`anvil.tasks` webview, one per window, revealed like the permissions
  manager): a row per task with kind label, title, status, elapsed time and actions — *Open
  transcript* for an agent task, *View output* for a shell task, *Cancel* while running. It
  re-lists `tasks.list` on the `task` notification, on opening, and on an explicit Refresh, and
  never on a hidden panel. There is deliberately no timer: elapsed time is arithmetic on the two
  timestamps the row already has, and a poll that only refreshed it would be a host round trip
  per second to redraw a clock.
- **A `/tasks` composer command.** Typing `/tasks` in the composer opens the panel; the command is
  handled by the extension and never becomes a user message, because sending the literal word
  "tasks" to the model is not what the person meant. The composer's slash list gains `/tasks` with
  its one-line description, which is also how the command is discoverable.
- **A status-bar item** (`$(tasklist) 2`) whenever the window has running tasks, clickable to the
  panel. The count is across the window's live sessions, because the panel is: a task belongs to
  an agent tree, and the person looking at the status bar is looking at the window. The existing
  status bar already shows a running turn; the two are different facts and are drawn separately:
  a turn can be idle while two background jobs run.
- **Session tree nesting.** A child session appears under its parent, collapsed; opening it opens
  its transcript in a tab. The tree's delete action on a parent deletes its children, and the
  confirmation says how many.

The editor's **Stop** button keeps its meaning: it cancels the active run. It does not cancel
background tasks; the Tasks panel is where that happens, and its Cancel button names the task.

## Workflows

`/tasks` answers *what is running*. A workflow answers *what to run next*. Claude Code's Workflow
is a JavaScript program executed in a sandbox; Anvil does not add an interpreter. A workflow here
is a **declarative DAG of subagents**, evaluated deterministically by the runtime:

```json
{
  "name": "review-and-fix",
  "steps": [
    {"id": "scan",  "prompt": "List the files that mention Registry.Execute as JSON {\"files\": [...]}.", "tools": ["read", "search"]},
    {"id": "fix",   "depends_on": ["scan"], "prompt": "Fix the callers listed in {{scan.result}}", "tools": ["read", "edit", "verify"]},
    {"id": "check", "depends_on": ["fix"], "prompt": "Run the tests and report what changed", "tools": ["read", "verify"]}
  ]
}
```

The contract:

- Every step has an `id` unique within the workflow, a `prompt`, and optional `tools`, `model`,
  `max_steps`, `depends_on` and `for_each`. A workflow has 1–32 steps; `max_parallel` is 1–8 and
  defaults to 3. Unknown fields are refused, not ignored.
- A step runs when every id in `depends_on` has finished. Ready steps run concurrently, bounded
  by the workflow's `max_parallel` (default 3); the document's order decides which ready step
  starts first and how the result is listed. A cycle, a dependency on an unknown id, a duplicate
  id or a missing step is refused before any agent starts — a workflow that would fail at step 3
  fails at call time, not after spending two agents.
- `{{step_id.result}}` in a prompt is replaced with that step's final message. Substitution is
  literal and happens once, in the prompt string the child receives; the SDK does not evaluate
  expressions, so a model's output cannot become a template that expands further. A reference is
  also a dependency: the step waits for every step it reads even when `depends_on` omits it,
  because otherwise the substitution would race the answer it is reading.
- `for_each: {"from": "step_id", "path": "files"}` fans a step out over a JSON array that a
  previous step's result must contain. `path` is a key, or a dotted key path into nested objects.
  Each item becomes `{{item}}` and `{{index}}`. Fan-out is bounded by the step's `max_parallel`
  (default: the workflow's, itself default 3), by the workflow's own bound, and by the tree's
  remaining agent budget; a step whose target is not a JSON array fails that step rather than
  silently running zero or one child. One child per item is created, described as
  `<workflow name> / <step id> #<index>` so the session tree can tell them apart.
- A failed step fails the workflow. Later steps that depend on it do not run; independent steps
  that are already running finish. The tool result lists every step's status and every failure;
  a finished step's final message is carried up to 2000 characters, with the rest named by the
  child's agent id so the store file is one click away.

`run_workflow` is a tool in the `agent` group and returns a workflow result; `run_workflow` with
`"background": true` files the whole workflow in the task table as one `agent` task whose title is
the workflow's name, because a workflow is one unit of work from the parent's point of view.

The honest boundary: this is the part of Claude's Workflow that is about *orchestration* — fan-out,
joining, bounded concurrency, per-step tools — and not the part that is a programming language.
A sandboxed JS engine is a separate milestone with its own security chapter; shipping one under
this chapter's name would make the permission model's claims untrue.

## Limits, failure modes, and what the user sees

| Situation | Behaviour |
|---|---|
| Child's provider call fails | spawn result is a tool error with the child's `Err`; parent continues |
| Parent run cancelled while a foreground child runs | child context is derived from the parent's; both end cancelled |
| Parent run cancelled while a background child runs | the task keeps running; `/tasks` shows it; Cancel stops it |
| Session closes with tasks running | tasks are cancelled; panel marks them `cancelled` with *"stopped when the session closed"* |
| Child asks for approval after the parent's turn ended | card still appears in the session; answering it resolves the child's call |
| Depth limit reached | no `spawn_agent` in that child's registry; a call to it is `unknown tool` |
| Agent budget exhausted | spawn returns a tool error naming the limit; the turn continues |
| Host restarts | running tasks are gone; child sessions remain as stored transcripts under their parent |
| Child session deleted with its parent | both files and both checkpoint trees go |
| Replaying a parent | spawn card has call + result; *Open subagent transcript* loads the child's steps |

Two properties are stated as invariants, because every implementation detail above depends on
them:

1. **A subagent never widens what the parent could do.** Same sandbox, same rules, same
   permission callback, same workspace; its tool set is a subset chosen by the parent's model.
2. **A tool result is the only thing a parent learns from a child.** The parent's history carries
   the result string and nothing else; no child message, tool argument or reasoning is injected
   into the parent's context.

## Non-goals

- A JavaScript or Python workflow interpreter, a sandboxed code-execution engine, or user-defined
  functions inside a workflow.
- Subagents in a different working directory, a different workspace, or a git worktree of their
  own; a child runs in the session's workspace.
- Plugin tools, a caller's `Options.Tools` handlers, or skills inherited by a child.
- A persistent task queue that survives `anvil-host` exiting; tasks live in the host process.
- Cross-session task visibility, scheduling tasks at a time, or retrying a failed task
  automatically.
- A model-driven agent router ("choose a subagent for me"); the parent's model chooses by calling
  the tool, which is the same decision every other tool call is.
- Subagent-specific permission modes. A session has one mode; its children run in it.
