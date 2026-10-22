# 45 — Evaluation: a baseline before the polish

**Milestone focus:** 0.47. Statuses live in
[m_0.47_evaluation_and_audit.md](../../roadmaps/v0.x/m_0.47_evaluation_and_audit.md).

## 1. Today

`source/eval/eval_test.go` holds six fixtures. Each one is a scripted `Completer` that replays a
fixed list of assistant messages into `runtime.Loop`, and a `Check` that inspects the temp
directory afterwards: `edit_file_unique`, `sandbox_escape_denied`, `apply_patch_add`,
`multi_file_edit_patch`, `max_steps_exceeded`, and one more of the same shape.

They are good regression tests for the tool layer and they answer nothing else:

- **One run, one agent.** They call `runtime.Loop` directly, so nothing above it — the public
  `Agent`, subagents, workflows, checkpoints, the permission callback, Resume — is exercised.
- **The script ignores the request.** `scriptCompleter.Complete` returns the next message
  whatever it was sent, so a change that breaks the system prompt, drops a tool definition or
  adds a request field passes every fixture.
- **Pass or fail, no counters.** Steps taken, tool errors, refused calls and retries are not
  recorded, so a change that makes a task cost twice as many steps is invisible.

The next two milestones change the request (effort, retry), the system prompt (roles) and the
number of agents in a run (modes). None of those can be judged by the fixtures above. This
chapter is the measurement they are judged by, and it is delivered **first**.

## 2. Why the baseline comes before the polish

The order is a decision, not a convenience:

- Every phase in 0.48, 0.49 and 0.50 claims "no regression". A claim made without a number recorded
  before the change is a hope. The baseline must exist before the first change it guards.
- The cheap failures of LLM polish are silent: a request field the provider ignores, a retry
  that double-counts usage, a role prompt that loses a safety clause. Each of them is a
  property of the **request** or of the **run record**, and both can be measured offline.
- Placing evaluation after the polish would make the baseline describe the polished code, and
  every later comparison would be against a target that had already moved.

The cost is that 0.47 delivers nothing a user sees. That is accepted: it is the milestone that
makes the next three honest.

## 3. What can be measured offline, and what cannot

The requirement is "quality on par with Codex / Claude Code". Stated plainly, that has two
parts, and only one of them can be a gate:

| Question | Depends on | Offline? |
|---|---|---|
| Given what the model said, did the SDK do the right thing? (tool routing, sandbox, permissions, loop guard, checkpoints, request shape, retries, delegation, schemas) | the SDK | yes — deterministic |
| Given the task, does the model say better things with high effort, role prompts, or Complex mode? | the model and the SDK | no — needs a real provider |

The gates in this line measure the first question exhaustively. The second is a **report**,
produced by an opt-in live tier (§4.3) and never a gate: a gate that depends on a remote model
is not repeatable, and a gate that is not repeatable trains people to rerun it until it passes.

This is the honest limit of the design. It is stated here so that no phase claims more.

## 4. The harness

`source/eval` becomes a package with three tiers. The tiers share one `Task` shape, one scorer
(§5) and one baseline file (§6); they differ in where the assistant's messages come from.

### 4.1 Tier 1 — scripted (today's six, kept)

The existing fixtures stay as they are, with one change: the scripted completer gains an
optional `Expect` per reply — a predicate on the `llm.Request` it is answering. A fixture that
wants to pin "the system prompt still contains the sandbox clause" or "the request carries no
`reasoning_effort`" can do so without a recording. Fixtures that set no `Expect` behave exactly
as today.

### 4.2 Tier 2 — recorded replay

A recording is a JSONL file under `source/eval/recordings/<name>.jsonl`, one line per model
call, holding the `llm.Request` the SDK sent and the `llm.Response` the provider returned. It is
produced once against a real provider (§4.3) and committed; after that it is data.

The replay completer answers a request by **digest**, not by position:

- The digest is a SHA-256 over the request with volatile fields removed: the model id, the
  `cwd` line of the system prompt, timestamps in session notes, and tool-result text that names
  a temp directory. The exact list is one function, `eval.RequestDigest`, and it is tested.
- A request whose digest matches a recorded line gets that line's response. A request that
  matches nothing **fails the fixture** and prints the nearest recorded request as a diff.

That second rule is what makes the tier a measurement rather than a mock. A change that alters
the request — a new field, a reordered prompt layer, a tool definition that changed shape —
shows up as a digest miss with a readable diff, and the author decides whether to re-record (the
change was intended) or to fix the code (it was not). Replays run through the public `Agent`
(`anvil.Create` + `Send`), so subagents, workflows, checkpoints and the permission callback are
all on the path.

Recordings carry no secrets: the recorder redacts the `Authorization` header before writing,
and the request digest never includes it. A recording whose response contains reasoning text
keeps it — it is the provider's output and the replay must reproduce it — but the scorer checks
that none of it reaches a later request (chapter [48](48-llm-effort-and-retry.md) §2).

### 4.3 Tier 3 — live (report only)

With `ANVIL_EVAL_LIVE=1` and a provider configured in the environment, the same `Task` list
runs against the real model, writes a fresh recording next to the committed one, and prints
the scorer's numbers for both. The tier has no pass/fail: it produces the recordings that tier
2 replays, and the numbers that a person reads when deciding whether a prompt change or an
effort level was worth it.

It is also the only place the "better things" question of §3 is asked, and the answer is a
table in a phase document, not a gate.

## 5. The scorer

Every task produces one `Score`:

| Field | Meaning | Direction |
|---|---|---|
| `completed` | the `Check` passed | must hold |
| `steps` | model rounds the run took | lower is better |
| `tool_errors` | tool calls that returned an error to the model | lower |
| `refused` | tool calls the sandbox, the rules or the permission callback refused | exact: an invariant fixture expects a number, not "fewer" |
| `loop_stops` | runs the loop guard ended | exact |
| `retries` | provider retries (0.49+) | recorded |
| `request_bytes` | total request body size across the run | recorded; the cost proxy |
| `agents` | agents created in the tree (0.51+) | recorded |

A fixture declares which fields are **gated** for it. A behaviour fixture gates `completed` and
`steps`; an invariant fixture gates `refused` or `loop_stops` exactly. A field no fixture gates
is still recorded, so a later phase can start gating it against a number that already exists.

## 6. The baseline

`source/eval/baseline.json` holds the last accepted `Score` per task. `go test ./eval/...`
compares against it:

- A gated field that moved in the wrong direction fails the test and prints both numbers.
- A gated field that improved passes and prints a note; the baseline is updated by running
  with `-update`, which is a deliberate act that shows in the diff of the commit.
- A task missing from the baseline fails: a new fixture must record its number before it can
  guard anything.

`scripts/eval.sh` is the one-line form (`go test ./eval/... -count=1`, with `-update` and
`-live` passed through) so the roadmap gates can name a single command.

### 6.1 The invariants suite

One task group is named `invariants` and exists for a single reason: to prove that the
capabilities Anvil has and Codex does not are intact after every change in this line. It is
run by every later phase's gate and it must stay at 100%:

| Task | What it fixes |
|---|---|
| `inv_permission_deny` | a rule denying `shell` holds with no callback set; the call is refused, the run continues |
| `inv_permission_ask` | with a callback that answers *no*, a gated write is refused and reported |
| `inv_loop_guard` | three identical tool calls end the run with `loop_detected` |
| `inv_checkpoint_restore` | a run that edits two files leaves a checkpoint that restores both |
| `inv_sandbox_escape` | a read outside `cwd` is refused (today's fixture, moved here) |
| `inv_plan_readonly` | with the plan tool set removed, a write tool call is `unknown tool` |
| `inv_child_never_widens` | a child spawned with `tools: [read]` cannot write, whatever its prompt says |
| `inv_child_result_only` | a child's messages never appear in the parent's history |

0.50 adds the same list under each mode (chapter [50](50-agent-modes.md) §6).

## 7. The audit

The first phase of 0.47 is a read of the whole agent path — `agent.go`, `run.go`,
`subagent.go`, `workflow.go`, `internal/runtime`, `internal/llm`, `internal/tools`,
`internal/permissions` — with one output: `doc/roadmaps/v0.x/m_0.47/audit.md`, a list of
findings, each with a severity, a file reference, and **a failing test that reproduces it**. A
finding without a test is a suspicion and is listed in a separate section as such.

Fixes for findings are not a phase of this milestone. A finding that is a bug is fixed in a
plain commit with its test (the versioning rule: bug fixes do not open a phase). A finding that
needs design is referred to the chapter that owns it, or becomes a "待定" entry in chapter 48,
47 or 48 with the information it needs.

What the audit is for in this line: it is the source of the tier-1 `Expect` predicates and the
tier-2 recordings that the later phases guard. An audit that finds nothing still produces those.

## 8. Non-goals

- A benchmark that ranks Anvil against Codex or Claude Code. The live tier reports numbers for
  Anvil's own tasks; it has no fixtures for other products and no scoring that would make a
  cross-product number mean anything.
- A tokenizer. `request_bytes` is a byte count, for the same reason chapter
  [42](43-context-management.vscode.md) estimates rather than tokenizes.
- Fixtures that need network, a real git remote, or a real MCP server.
- Replaying a recording against a **different** model id and expecting the same digest: the
  model is a volatile field so that a config change does not invalidate recordings, not so that
  two models' answers are compared.

## 9. Delivery

| Phase | Contract |
|-------|----------|
| p_0.47.1 | §7: the audit, its finding list, one reproducing test per finding |
| p_0.47.2 | §4, §5: the three tiers, the digest, the scorer, the first recordings |
| p_0.47.3 | §6: `baseline.json`, the compare rule, `scripts/eval.sh`, the invariants suite |
