# 47 — Evaluation: a baseline before the polish

**Milestone focus:** 0.47. Statuses live in
[m_0.47_evaluation_and_audit.md](../../roadmaps/v0.x/m_0.47_evaluation_and_audit.md).

## 1. Starting point

Before p_0.47.2, `source/eval/eval_test.go` held six request-blind fixtures. The limitations
below describe that starting point, not the delivered harness. p_0.47.2 now has request
predicates, scores and five synthetic public Agent capture/replay scenarios; genuine
provider recordings remain pending. Baseline comparison and invariants belong to p_0.47.3.

The original file held six fixtures. Each one is a scripted `Completer` that replays a
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

The next three milestones change the request (effort, retry), the system prompt (roles) and the
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

A recording is versioned JSONL, one line per completed model call, holding the request,
response, digest and explicit `provider` or `synthetic` provenance. Genuine recordings are
produced against a real provider (§4.3), reviewed and committed under `source/eval/recordings/`.
Synthetic integration captures are made only in temporary test directories; they test the
harness but do not satisfy delivery of genuine recordings.

The replay completer answers a request by **digest**, not by position:

- The digest is SHA-256 over canonical request JSON, preserving integer precision. Clear
  model id; normalize only the actual introductory cwd line, valid RFC3339 timestamps under
  `# Session notes`, and recognized temporary workspace prefixes in tool results. Sort tool
  definitions by name because registry iteration uses a map. Temp-root recognition is a
  request-only grammar for standard POSIX, Windows and macOS locations, not local `os.TempDir`;
  custom temp locations stay significant. Normalize generated IDs only in spawn headings
  (success/failure/cancellation) and initial workflow tables, including rows with no child ID.
  Recover missing tool names from preceding calls rather than future calls reusing the ID.
  Go elapsed seconds normalize only in `run_tests` package summaries and their exact copies
  in the generated failure list corroborated by `logTail`. User/file/child body text, relative
  subpaths, prompt layers, schemas and cache flags stay significant.
  `eval.CanonicalRequest` and `eval.RequestDigest` define and test the exact boundary.
- A matching digest gets a cloned response with finite occurrence counts. Conflicting answers
  for one digest are rejected. A miss or exhaustion **fails the fixture** with a unified diff of
  the nearest request; unconsumed expected calls also fail verification.

That second rule is what makes the tier a measurement rather than a mock. A change that alters
the request — a new field, a reordered prompt layer, a tool definition that changed shape —
shows up as a digest miss with a readable diff, and the author decides whether to re-record (the
change was intended) or to fix the code (it was not). Replays use `-tags anvileval`: the small
`CreateForEval` bridge assigns the existing private completer and calls real `Create`, followed
by `Send`, immediate stream draining, `Wait` and `Close`. Default SDK builds expose no bridge.
Five synthetic scenarios cover editing/testing, subagents, workflows, deny rules and loop guard;
callback refusal has an additional test. Checkpoint/Resume coverage is not claimed for p_0.47.2.
Root tool-event coverage is checked against persisted history so buffer loss fails the harness.

Recordings contain only typed `llm.Request`/`llm.Response`, not provider config or HTTP headers;
there is no header-redaction layer at this boundary. `llm.Response` has no reasoning field, so
plain Complete capture does not retain or reconstruct streaming reasoning. Fixture contents and
model output can still contain sensitive text: use controlled workspaces and review recordings
before committing. Transport/streaming assertions belong beside the adapters, not in this recorder.

### 4.3 Tier 3 — live (report only)

The test requires both `-live` and `ANVIL_EVAL_LIVE=1`, with `ANVIL_MODEL` and `ANVIL_API_KEY`;
`ANVIL_PROVIDER` and `ANVIL_BASE_URL` select the adapter and endpoint. It exclusively creates
`<name>.live.jsonl` and never overwrites an existing recording. A conflicting response for an
already recorded digest fails capture before writing an unreplayable line, including differences
in tool-call IDs or usage; identical duplicate responses are allowed with finite counts.
Runs use controlled fresh
workspaces, restricted tools and bounded delegation/time. No shell, executable hooks, MCP or
model-chosen commands are enabled. The two-file scenario runs a fixed verifier that ignores its
arguments and only reads the two edited files; real `go test` of model-written code would need an
OS sandbox and is not part of the live tier.

Model-dependent completion/scores are reports, not regression gates. Runner/recorder errors
are surfaced, not counted as successful captures. No real provider calls or genuine recordings
were made during the p_0.47.2 implementation verification.

It is also the only place the "better things" question of §3 is asked, and the answer is a
table in a phase document, not a gate.

## 5. The scorer

Every task produces one `Score`:

| Field | Meaning | Direction |
|---|---|---|
| `completed` | the `Check` passed | must hold |
| `steps` | root model rounds (child rounds are not added) | lower is better |
| `tool_errors` | tool calls that returned an error to the model | lower |
| `refused` | tool calls the sandbox, the rules or the permission callback refused | exact: an invariant fixture expects a number, not "fewer" |
| `loop_stops` | runs the loop guard ended | exact |
| `retries` | zero placeholder here; provider retries are introduced in 0.48 | recorded |
| `request_bytes` | original typed request JSON bytes across root/child calls, not HTTP-wire bytes | recorded; the cost proxy |
| `agents` | zero placeholder here; mode-aware agent counts are introduced in 0.50 | recorded |

A fixture declares which fields are **gated** for it. A behaviour fixture gates `completed` and
`steps`; an invariant fixture gates `refused` or `loop_stops` exactly. A field no fixture gates
is still recorded, so a later phase can start gating it against a number that already exists.

An explicitly checked loop-stop task may have `completed=true`. Count policy refusals by
structured decisions and narrow SDK/callback markers, not arbitrary "permission denied"
file errors. Tool errors include children; `loop_stops` uses the authoritative root outcome
and terminal child errors, not warnings counted a second time.

## 6. The baseline (p_0.47.3, delivered)

p_0.47.3 added `source/eval/baseline.json` for the last accepted `Score` per task.
`go test -tags anvileval ./eval/...` compares against it:

- A gated field that moved in the wrong direction fails the test and prints both numbers.
- A gated field that improved passes and prints a note; the baseline is updated by running
  with `-update`, which is a deliberate act that shows in the diff of the commit.
- A task missing from the baseline fails: a new fixture must record its number before it can
  guard anything.

`scripts/eval.sh` wraps `go test -tags anvileval -count=1 ./eval/...`, with
`-update` and `-live` passed through, so later roadmap gates can name a single command.

### 6.1 The invariants suite

One task group is named `invariants` and exists for a single reason: to prove that the
capabilities Anvil has and Codex does not are intact after every change in this line. It is
run by every later phase's gate and it must stay at 100%:

| Task | What it fixes |
|---|---|
| `inv_permission_deny` | a rule denying `shell` holds with no callback set; the call is refused, the run continues |
| `inv_permission_ask` | with a callback that answers *no*, a gated write is refused and reported |
| `inv_loop_guard` | default guard warns on the third identical call and ends on the fourth with `loop_detected` |
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
needs design is referred to the chapter that owns it, or remains explicitly open with the
information it needs. Audit #10 remains open because the existing shell-policy test records
the opposite expectation; the harness does not decide that policy.

What the audit is for in this line: it is the source of the tier-1 `Expect` predicates and the
tier-2 recordings that the later phases guard. An audit that finds nothing still produces those.

## 8. Non-goals

- A benchmark that ranks Anvil against Codex or Claude Code. The live tier reports numbers for
  Anvil's own tasks; it has no fixtures for other products and no scoring that would make a
  cross-product number mean anything.
- A tokenizer. `request_bytes` is a byte count, for the same reason chapter
  [43](43-context-management.vscode.md) estimates rather than tokenizes.
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
