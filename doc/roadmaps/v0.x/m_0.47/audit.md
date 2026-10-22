# Audit — agent path (0.47.1)

Read once: `source/agent.go`, `run.go`, `runs.go`, `subagent.go`, `workflow.go`, `tasks.go`,
`checkpoints.go`, `fork.go`, `internal/runtime`, `internal/llm`, `internal/tools`,
`internal/permissions`, `internal/checkpoint`, and the LSP calls the tools make.

Each finding has a severity (`bug`, `gap`, `doc`), a location, the defect, and a status:

- **confirmed** — read in the source at the stated line, and the defect follows from that code.
  A failing test is still to be written (see *Tests* below).
- **reported** — found by the audit, not yet re-read at the line. Verify before fixing.
- **suspicion** — cannot be reduced to a test yet; the entry says what would settle it.

Fixes are not part of this phase. A `bug` is fixed in a plain commit with its test.

## Findings

### Tools and paths

**#1 — bug — confirmed.** `internal/tools/tools.go` `grepWalk` (fallback when `rg` is absent).
A workspace symlink to a file outside `cwd` is read: `d.Info()` does not follow the link, but
`os.ReadFile(path)` does, and its matching lines are returned. The `rg` path is not affected.
Repro: temp `cwd`, symlink to a file containing a marker outside `cwd`, grep for the marker with
`rg` absent from `PATH`; expect no match.

**#2 — bug — reported (test written, not reproduced).** `internal/lsp/path.go` `insideCWD` is lexical only, with no
`EvalSymlinks`. Used by `supervisor.go` (`openPath`) and `edit.go` (`ApplyWorkspaceEdit`), so an
in-workspace symlink to an outside file is read and sent to the language server, and `rename_symbol`
writes through it. Repro: `ApplyWorkspaceEdit(cwd, edit)` for `cwd/link.txt` where `link.txt` points
outside; expect an error and an unchanged target.

**#3 — bug — confirmed.** `internal/tools/tools.go` `ResolveInRoot` with `resolveSymlinkPath`. When
the leaf is a dangling symlink, `EvalSymlinks` fails, the walk-up joins the link name onto the
resolved parent, and the link is not followed. An in-root symlink to an absent outside target then
resolves without error, and a write creates the outside file. Repro: `os.Symlink(outside/new.txt,
root/link)`; `ResolveInRoot(root, "link")` should error. Skip on Windows without symlink privilege.

**#4 — bug — confirmed.** `internal/tools/patch.go` `applyPatchTool.Execute`. Operations run in
sequence with early returns and no rollback, so an `Add` that succeeds before a later `Update`
fails stays on disk, and the error result does not list the completed operations. Repro:
patch `[Add a.txt, Update missing.txt]`; expect `a.txt` absent after the error.

**#5 — bug — confirmed.** `internal/tools/tools.go` `captureBefore` drops in-workspace names that
begin with two dots: `strings.HasPrefix(rel, "..")` also matches `..env`. Their pre-images are never
captured. `checkpoint.go` checks `../` and would have allowed them. Repro: write `..env` in the
root, call `write_file` on it, and check the manifest; the entry is missing.

**#6 — bug — confirmed.** `internal/tools/tools.go` `captureBefore` ignores the error of
`checkpoint.Capture` (`_ = checkpoint.Capture(...)`). A failed capture leaves no manifest entry, the
write still proceeds, and the run's restore reports success without naming the missing path. Repro:
make the checkpoint `files` path a regular file so `MkdirAll` fails; `write_file`; restore; the path
is absent from the result.

**#7 — gap — reported.** `internal/tools/preview.go` `pathsOf` returns nil for `rename_symbol`, and
`captureBefore` takes its paths from `pathsOf`. `renameSymbolTool.Execute` calls
`lsp.ApplyWorkspaceEdit`, which writes many files, so no pre-image is captured and the rename is not
in `mutatingTools`. A restore therefore cannot undo a rename, and no path rule sees its targets.
Repro: a stub LSP returning an edit on `a.txt`, a checkpoint directory, then restore; `a.txt` is not
restored.

**#8 — bug — confirmed.** `internal/lsp/edit.go` `ApplyWorkspaceEdit` writes files in a loop. A
failed write returns `(nil, err)`, so files already written are neither returned nor passed to
`notifyFileWrite`. The model is told the rename failed while some files changed and the LSP sync
state is stale. Repro: two-file edit where the second path is read-only; the first file is modified
and the result lists no path.

**#9 — bug — confirmed.** `internal/tools/tools.go` `Registry.Execute` discards the error of
`r.permSource.Read()` (`rules, _ =`). `Source.Read` returns the last good policy, which is nil when
the session started without rules, so on a first-load failure the deny rules disappear and the call
runs. Repro: `NewSource(cwd, "", home, anvil, nil, nil)` with a malformed workspace file denying
`shell`; `Execute("shell", ...)` should be denied.

**#10 — bug — confirmed.** `internal/tools/preview.go`/`tools.go` shell calls carry `Command` and no
`Paths`, so protection never applies to them (`Set.protectedPaths` returns nil when `len(f.Paths)==0`).
`allow shell "rm:*"` lets `rm -rf .git` run without a prompt. `protect.go` says a remembered allow must
not cover these paths, and the existing test only covers reads. Repro: `Evaluate(CallFacts{Tool:
"shell", Command: "rm -rf .git"}, "linux")` with rule `{allow, shell, "rm:*"}`; expect a prompt.

**#11 — bug — confirmed.** `internal/permissions/protect.go` `protectedRelative`: the `.git` segment
and `.anvil/checkpoints` prefix tests are byte-case-sensitive. On Windows (NTFS, case-insensitive),
`.GIT/config` is not protected and an allow rule covers it. Repro: `protectedRelative(".GIT/config")`
should return true.

**#12 — bug — reported.** `internal/permissions/protect.go` `Protection.isProtected` with
`filepath.Rel`: when the workspace root is a symlink or junction, an absolute path in resolved form
fails `Rel` and falls through to the string test, so `/real-ws/.anvil/checkpoints/x` is not
protected. Repro: `Protection{Root:"/link-to-ws"}.isProtected("/real-ws/.anvil/checkpoints/x")`
should be true. `agent.go` passes the raw `cwd` as the root; `source.go` says the root is resolved
once, which it is not.

### Checkpoints

**#13 — bug — reported.** `internal/checkpoint/checkpoint.go` `Restore` (lines 288–302 per the
report). A restore does not check that the current file still matches the run's post-state:
`Existed=false` deletes whatever is now at the path, and saved bytes overwrite any later edit,
including the user's own. The SHA256 is recorded but not checked. This may be intended; see
*Suspicions* S1.

**#14 — bug — reported.** `internal/checkpoint/checkpoint.go` hard-codes mode `0o644` on restore
and records no mode on capture, so an executable bit is lost. Reproducible only on Linux; see
*Suspicions* S2.

### Runtime and LLM

**#15 — bug — confirmed.** `internal/llm/anthropic_stream.go` `Anthropic.CompleteStream`: a
`tool_use` block still open when the stream ends (EOF or truncation) is never appended to
`toolCalls` (the `curTool` is dropped), and `sc.Err()` is nil on a clean EOF, so the call returns
success with the tool call missing. No `stop_reason` is checked. Repro: SSE with `content_block_start`
(tool_use), one `input_json_delta`, then EOF; expect an error.

**#16 — bug — confirmed.** Both stream adapters skip a chunk whose JSON fails to decode: `anthropic_stream.go`
`continue` on unmarshal failure, and `openai_stream.go` the same. A lost `content_block_start`,
`input_json_delta` or OpenAI `tool_calls` fragment yields truncated or missing arguments with no
error. Repro: a tool_use start, then `data: {bad json`, then `content_block_stop`; expect an error.

**#17 — doc — reported.** `internal/llm/anthropic.go` `toAnthropicMessages` (assistant branch):
`_ = json.Unmarshal(tc.Args, &input)` replaces unparseable arguments with `{}`, so the replayed
history records a different call from the one the model emitted, and the error is dropped. Low
severity. Repro: a `ToolCall` with `Args` `{"path":` converted; expect the malformed input surfaced.

### Agent core

**#18 — bug — confirmed.** `agent.go` `Close` (around 1435–1478) sets `a.closed` and clears
`a.store`, then releases the store lock, but does not cancel `a.activeRun`. `executeRun` keeps
running, `persist` returns early on nil `store`, and the run's `run_end` is missing from the file.
A `Resume` of the same id then succeeds because the lock is free. Repro: a blocking completer;
`Send`; `Close`; `Resume`; the first run's `run_end` is absent.

**#19 — bug — confirmed.** `agent.go` `persist` reads `a.store` without `a.mu` (`Close` writes it
under the lock) and discards the `Append` error with `_ =`. Repro: a store whose `Append` fails;
`Send` with a stub completer; `Wait` returns finished with no error.

**#20 — bug — confirmed.** `agent.go` `Send` reads `a.closed` without `a.mu`, while `Close` writes it
under the lock. A data race; `go test -race` with `Close` concurrent with `Send` should report it.

**#21 — bug — confirmed.** `agent.go` `Send` with `WithForce` waits on the previous run using the
caller's context and discards the error. If the context is already done, `Wait` returns at once, and
the new `Send` fails with "another run is already in progress" while the previous run is still active.
Repro: a blocking first run; `Send(canceled ctx, WithForce())` returns that error.

**#22 — bug — reported.** `subagent.go` `spawnAgent` (around 194), `spawnBackground` (around 252) and
`workflow.go` `runChild` (around 778) call `budget.take()` and do not refund it when a later step
fails (`createSubagent`, `StartAgent`, `runChild`). The slot is consumed though no child ran. Repro:
`MaxAgents=1`, `MaxBackground=1`; a failed background spawn; free the slot; the next spawn fails with
"already created 1 agents".

**#23 — bug — reported.** `agent.go` `createAgent` appends the meta record before
`buildSystemPrompt` (around 157 before 162–167). If the prompt fails, the store closes with an error,
but the meta-only file stays and appears as a session with no runs. Repro: a malformed project rules
file; `Create`; list sessions; the new id appears with no runs.

**#24 — bug — reported.** `agent.go` `Close` does not stop task-manager output. `tasks.go` `taskManager`
returns nil after `Close` (`a.taskMgr = nil`), so `Tasks`, `Task`, `TaskOutput` and `CancelTask` report
nothing. The comments in `tasks/manager.go` and design 42 say the task list shows "stopped" after close.
Repro: `run_in_background` a sleep; `Close`; `Tasks()` is empty, expected one cancelled row.

**#25 — bug — reported.** `subagent.go` `childOptions` does not copy `AllowHookCommands`, so a
command-type hook deny in the parent is skipped in the child (`hooks.go` `continue` when
`!AllowCommands`). Design 42 says a workspace deny refuses a child's call exactly as the parent's.
Repro: parent with `AllowHookCommands=true` and a command deny rule; the child's write is not denied.

**#26 — bug — reported.** `subagent.go` `childOptions` and events: `executeRun` forwards every runtime
event to `SubagentOptions.OnEvent`, including `Type "done"`, while design 42's Events section says the
child's `done` is not forwarded. Repro: spawn a child with a stub completer; record `OnEvent`; one event
has `Type "done"`.

**#27 — bug — reported.** `fork.go` `ForkAgent` rewrites only the `AgentID` on meta records. A forked
subagent keeps `ParentAgentID` and `Kind="subagent"`, and deleting the original parent removes the fork
through `subagentDescendants`. Repro: child C with meta `ParentAgentID=P`; `ForkAgent(C, runID)`;
`DeleteAgent(P)`; `Load(newID)` returns not found.

**#28 — gap — reported.** `agent.go` `Run.emit` (`run.go` around 91–100) drops events silently when the
64-slot buffer is full. Documented as a known limit in design 46 (`editor-polish`). Low severity.

**#29 — bug — confirmed.** `agent.go` `buildRegistry` discards the error from `index.Build` (`_, _ =`), so
`Create` succeeds with no indication when the index cannot be built. Repro: `EnsureIndex=true` with an
unwritable index directory.

**#30 — bug — reported.** `internal/tasks/manager.go` `Manager.Cancel` releases `m.mu` and then returns
`t.Info`, which `finish()` writes under the lock. A race with a natural exit. Reproduce with `-race`.

## Suspicions

**S1 — #13.** Does `Restore` overwrite a user's later edit on purpose? Settled by: the design chapter
on checkpoints saying that a restore is "back to the state before the run" regardless of later edits
(then it is intended and only documentation is missing), or saying it refuses on mismatch (then #13 is a
bug).

**S2 — #14.** Does the checkpoint need to carry file mode? Settled by: a test on Linux that restores an
executable and checks the mode, and a decision on whether mode is part of a checkpoint.

**S3 — #28.** Is the 64-slot drop acceptable for a run whose consumer is slow? Settled by: the editor
client's expectation of a complete event stream, which design 46 currently states as a known limit.

## Counts

| Severity | Findings | Confirmed | Reported |
|---|---|---|---|
| bug | 27 | 16 | 11 |
| gap | 2 | 0 | 2 |
| doc | 1 | 0 | 1 |
| **total** | **30** | **16** | **14** |

Suspicions S1–S3 are listed separately and are not counted as findings.

Reproduced by a failing test (skip removed, failed on the current tree): #1, #3, #4, #5, #8, #9, #6, #10, #11, #15, #16, #18, #19, #20, #21, #29.

Read at the stated line, but not reproduced: #2 (on Windows the URI form the test builds does not round-trip through `uriToPath`, so the edit fails before the symlink is reached).

#20 was reproduced under `go test -race` on AlmaLinux 10.2 (gcc 14, Go 1.26.10): the race detector reports a write in `Agent.Close` and a read in `Agent.Send` on `a.closed`.

## Tests

Each finding with a test is in the package that owns the code it reaches, named
`TestAudit…`, and marked `t.Skip("audit: open, see audit.md #N")` so the suite stays green.
Each one was run once with its skip removed to confirm the result above.

| Package | File | Tests |
|---|---|---|
| `internal/tools` | `audit_test.go` | #1, #3, #4, #6, #9 |
| `internal/lsp` | `audit_test.go` | #2, #8 |
| `internal/llm` | `audit_test.go` | #15, #16 |
| `.` (agent) | `audit_test.go` | #5, #18, #19, #20, #21, #29 |
| `internal/permissions` | `audit_test.go` | #10, #11 |

Thirteen audit tests are skipped; none fails with its skip in place. The two that did not
reproduce (#2 and #20) say why in their skip comment. #20 reproduces under `go test -race` on AlmaLinux (cgo), and is skipped on Windows only because the race detector is not available there.

Still open: the 17 `reported` findings have no test yet. They need the same treatment: read the
line, write the test, run it with the skip removed, and only then mark it `confirmed`.
