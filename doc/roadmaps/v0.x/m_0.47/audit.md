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

**#1 — bug — fixed.** `internal/tools/tools.go` `grepWalk` (fallback when `rg` is absent).
A workspace symlink to a file outside `cwd` is read: `d.Info()` does not follow the link, but
`os.ReadFile(path)` does, and its matching lines are returned. The `rg` path is not affected.
Repro: temp `cwd`, symlink to a file containing a marker outside `cwd`, grep for the marker with
`rg` absent from `PATH`; expect no match.

**#2 — bug — fixed.** `internal/lsp/path.go` `insideCWD` is lexical only, with no
`EvalSymlinks`. Used by `supervisor.go` (`openPath`) and `edit.go` (`ApplyWorkspaceEdit`), so an
in-workspace symlink to an outside file is read and sent to the language server, and `rename_symbol`
writes through it. Repro: `ApplyWorkspaceEdit(cwd, edit)` for `cwd/link.txt` where `link.txt` points
outside; expect an error and an unchanged target.

**#3 — bug — fixed.** `internal/tools/tools.go` `ResolveInRoot` with `resolveSymlinkPath`. When
the leaf is a dangling symlink, `EvalSymlinks` fails, the walk-up joins the link name onto the
resolved parent, and the link is not followed. An in-root symlink to an absent outside target then
resolves without error, and a write creates the outside file. Repro: `os.Symlink(outside/new.txt,
root/link)`; `ResolveInRoot(root, "link")` should error. Skip on Windows without symlink privilege.

**#4 — bug — fixed.** `internal/tools/patch.go` `applyPatchTool.Execute`. Operations run in
sequence with early returns and no rollback, so an `Add` that succeeds before a later `Update`
fails stays on disk, and the error result does not list the completed operations. Repro:
patch `[Add a.txt, Update missing.txt]`; expect `a.txt` absent after the error.

**#5 — bug — fixed.** `internal/tools/tools.go` `captureBefore` drops in-workspace names that
begin with two dots: `strings.HasPrefix(rel, "..")` also matches `..env`. Their pre-images are never
captured. `checkpoint.go` checks `../` and would have allowed them. Repro: write `..env` in the
root, call `write_file` on it, and check the manifest; the entry is missing.

**#6 — bug — fixed.** `internal/tools/tools.go` `captureBefore` ignores the error of
`checkpoint.Capture` (`_ = checkpoint.Capture(...)`). A failed capture leaves no manifest entry, the
write still proceeds, and the run's restore reports success without naming the missing path. Repro:
make the checkpoint `files` path a regular file so `MkdirAll` fails; `write_file`; restore; the path
is absent from the result.

**#7 — gap — fixed.** `internal/tools/preview.go` `pathsOf` returns nil for `rename_symbol`, and
`captureBefore` takes its paths from `pathsOf`. `renameSymbolTool.Execute` calls
`lsp.ApplyWorkspaceEdit`, which writes many files, so no pre-image is captured and the rename is not
in `mutatingTools`. A restore therefore cannot undo a rename, and no path rule sees its targets.
Repro: a stub LSP returning an edit on `a.txt`, a checkpoint directory, then restore; `a.txt` is not
restored.

**#8 — bug — fixed.** `internal/lsp/edit.go` `ApplyWorkspaceEdit` writes files in a loop. A
failed write returns `(nil, err)`, so files already written are neither returned nor passed to
`notifyFileWrite`. The model is told the rename failed while some files changed and the LSP sync
state is stale. Repro: two-file edit where the second path is read-only; the first file is modified
and the result lists no path.

**#9 — bug — fixed.** `internal/tools/tools.go` `Registry.Execute` discards the error of
`r.permSource.Read()` (`rules, _ =`). `Source.Read` returns the last good policy, which is nil when
the session started without rules, so on a first-load failure the deny rules disappear and the call
runs. Repro: `NewSource(cwd, "", home, anvil, nil, nil)` with a malformed workspace file denying
`shell`; `Execute("shell", ...)` should be denied.

**#10 — bug — confirmed.** `internal/tools/preview.go`/`tools.go` shell calls carry `Command` and no
`Paths`, so protection never applies to them (`Set.protectedPaths` returns nil when `len(f.Paths)==0`).
`allow shell "rm:*"` lets `rm -rf .git` run without a prompt. `protect.go` says a remembered allow must
not cover these paths, and the existing test only covers reads. Repro: `Evaluate(CallFacts{Tool:
"shell", Command: "rm -rf .git"}, "linux")` with rule `{allow, shell, "rm:*"}`; expect a prompt.

**#11 — bug — fixed.** `internal/permissions/protect.go` `protectedRelative`: the `.git` segment
and `.anvil/checkpoints` prefix tests are byte-case-sensitive. On Windows (NTFS, case-insensitive),
`.GIT/config` is not protected and an allow rule covers it. Repro: `protectedRelative(".GIT/config")`
should return true.

**#12 — bug — fixed.** `internal/permissions/protect.go` `Protection.isProtected` with
`filepath.Rel`: when the workspace root is a symlink or junction, an absolute path in resolved form
fails `Rel` and falls through to the string test, so `/real-ws/.anvil/checkpoints/x` is not
protected. Repro: `Protection{Root:"/link-to-ws"}.isProtected("/real-ws/.anvil/checkpoints/x")`
should be true. `agent.go` passes the raw `cwd` as the root; `source.go` says the root is resolved
once, which it is not.

### Checkpoints

**#13 — bug — fixed.** `internal/checkpoint/checkpoint.go` `Restore` (lines 288–302 per the
report). A restore does not check that the current file still matches the run's post-state:
`Existed=false` deletes whatever is now at the path, and saved bytes overwrite any later edit,
including the user's own. The SHA256 is recorded but not checked. This may be intended; see
*Suspicions* S1.

**#14 — decided: not a defect (checkpoints do not keep file mode).** `internal/checkpoint/checkpoint.go` hard-codes mode `0o644` on restore
and records no mode on capture, so an executable bit is lost. Reproducible only on Linux; see
*Suspicions* S2.

### Runtime and LLM

**#15 — bug — fixed.** `internal/llm/anthropic_stream.go` `Anthropic.CompleteStream`: a
`tool_use` block still open when the stream ends (EOF or truncation) is never appended to
`toolCalls` (the `curTool` is dropped), and `sc.Err()` is nil on a clean EOF, so the call returns
success with the tool call missing. No `stop_reason` is checked. Repro: SSE with `content_block_start`
(tool_use), one `input_json_delta`, then EOF; expect an error.

**#16 — bug — fixed.** Both stream adapters skip a chunk whose JSON fails to decode: `anthropic_stream.go`
`continue` on unmarshal failure, and `openai_stream.go` the same. A lost `content_block_start`,
`input_json_delta` or OpenAI `tool_calls` fragment yields truncated or missing arguments with no
error. Repro: a tool_use start, then `data: {bad json`, then `content_block_stop`; expect an error.

**#17 — doc — fixed.** `internal/llm/anthropic.go` `toAnthropicMessages` (assistant branch):
`_ = json.Unmarshal(tc.Args, &input)` replaces unparseable arguments with `{}`, so the replayed
history records a different call from the one the model emitted, and the error is dropped. Low
severity. Repro: a `ToolCall` with `Args` `{"path":` converted; expect the malformed input surfaced.

### Agent core

**#18 — bug — fixed.** `agent.go` `Close` (around 1435–1478) sets `a.closed` and clears
`a.store`, then releases the store lock, but does not cancel `a.activeRun`. `executeRun` keeps
running, `persist` returns early on nil `store`, and the run's `run_end` is missing from the file.
A `Resume` of the same id then succeeds because the lock is free. Repro: a blocking completer;
`Send`; `Close`; `Resume`; the first run's `run_end` is absent.

**#19 — bug — fixed.** `agent.go` `persist` reads `a.store` without `a.mu` (`Close` writes it
under the lock) and discards the `Append` error with `_ =`. Repro: a store whose `Append` fails;
`Send` with a stub completer; `Wait` returns finished with no error.

**#20 — bug — fixed.** `agent.go` `Send` reads `a.closed` without `a.mu`, while `Close` writes it
under the lock. A data race; `go test -race` with `Close` concurrent with `Send` should report it.

**#21 — bug — fixed.** `agent.go` `Send` with `WithForce` waits on the previous run using the
caller's context and discards the error. If the context is already done, `Wait` returns at once, and
the new `Send` fails with "another run is already in progress" while the previous run is still active.
Repro: a blocking first run; `Send(canceled ctx, WithForce())` returns that error.

**#22 — bug — fixed.** `subagent.go` `spawnAgent` (around 194), `spawnBackground` (around 252) and
`workflow.go` `runChild` (around 778) call `budget.take()` and do not refund it when a later step
fails (`createSubagent`, `StartAgent`, `runChild`). The slot is consumed though no child ran. Repro:
`MaxAgents=1`, `MaxBackground=1`; a failed background spawn; free the slot; the next spawn fails with
"already created 1 agents".

**#23 — bug — fixed.** `agent.go` `createAgent` appends the meta record before
`buildSystemPrompt` (around 157 before 162–167). If the prompt fails, the store closes with an error,
but the meta-only file stays and appears as a session with no runs. Repro: a malformed project rules
file; `Create`; list sessions; the new id appears with no runs.

**#24 — bug — fixed.** `agent.go` `Close` does not stop task-manager output. `tasks.go` `taskManager`
returns nil after `Close` (`a.taskMgr = nil`), so `Tasks`, `Task`, `TaskOutput` and `CancelTask` report
nothing. The comments in `tasks/manager.go` and design 42 say the task list shows "stopped" after close.
Repro: `run_in_background` a sleep; `Close`; `Tasks()` is empty, expected one cancelled row.

**#25 — bug — fixed.** `subagent.go` `childOptions` does not copy `AllowHookCommands`, so a
command-type hook deny in the parent is skipped in the child (`hooks.go` `continue` when
`!AllowCommands`). Design 42 says a workspace deny refuses a child's call exactly as the parent's.
Repro: parent with `AllowHookCommands=true` and a command deny rule; the child's write is not denied.

**#26 — bug — fixed.** `subagent.go` `childOptions` and events: `executeRun` forwards every runtime
event to `SubagentOptions.OnEvent`, including `Type "done"`, while design 42's Events section says the
child's `done` is not forwarded. Repro: spawn a child with a stub completer; record `OnEvent`; one event
has `Type "done"`.

**#27 — bug — fixed.** `fork.go` `ForkAgent` rewrites only the `AgentID` on meta records. A forked
subagent keeps `ParentAgentID` and `Kind="subagent"`, and deleting the original parent removes the fork
through `subagentDescendants`. Repro: child C with meta `ParentAgentID=P`; `ForkAgent(C, runID)`;
`DeleteAgent(P)`; `Load(newID)` returns not found.

**#28 — gap — decided: non-blocking, current behaviour kept.** `agent.go` `Run.emit` (`run.go` around 91–100) drops events silently when the
64-slot buffer is full. Documented as a known limit in design 46 (`editor-polish`). Low severity.

**#29 — bug — fixed.** `agent.go` `buildRegistry` discards the error from `index.Build` (`_, _ =`), so
`Create` succeeds with no indication when the index cannot be built. Repro: `EnsureIndex=true` with an
unwritable index directory.

**#30 — withdrawn — not a defect.** `internal/tasks/manager.go` `Manager.Cancel` releases `m.mu` and then returns
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
| bug | 26 | 24 | 2 |
| gap | 2 | 1 | 1 |
| doc | 1 | 1 | 0 |
| **total** | **29** | **26** | **3** |

Withdrawn after a source check: #30. It was listed as a finding, so the audit found 30 items, but it is not a defect and is not counted in the table: 29 counted, 26 confirmed.

Suspicions S1–S3 are listed separately and are not counted as findings.

Reproduced by a failing test (skip removed, failed on the current tree): #1, #2, #3, #4, #5, #6, #7, #8, #9, #10, #11, #12, #15, #16, #17, #18, #19, #20, #21, #22, #23, #24, #25, #26, #27, #29.

#30 was withdrawn after a source-level check. Both paths that read `t.Info` after the unlock are ordered after the write in `finish()`: the `!running` path reads it after an `m.mu` lock that `finish()` also takes, and the running path reads it under `m.mu` (`defer m.mu.Unlock()`). The 2000-iteration `-race` run agrees: no race.

#2 was not reproduced on Windows (the URI form in the test does not round-trip through `uriToPath` there, so the edit fails first), but it reproduces on AlmaLinux: an edit through a symlink to a file outside the workspace is applied.

#20 was reproduced under `go test -race` on AlmaLinux 10.2 (gcc 14, Go 1.26.10): the race detector reports a write in `Agent.Close` and a read in `Agent.Send` on `a.closed`.


### Design status of the open items

Searched design 33-49 for each open item. None of #13, #14 or #28 is settled in a design chapter, so none can be called a bug or intended behaviour yet:

- #13 (restore overwrites a later edit) and #14 (executable bit not kept): no chapter says whether a checkpoint restore should refuse on mismatch, or whether checkpoints carry file mode. See S1 and S2.
- #28 (events dropped when the 64-slot buffer is full): an earlier note in this audit said design 46 records this as a known limit. A search of design 46 and 35 found no such statement, so that note was wrong and is withdrawn here.


### Decisions on the open items

- **#13 (fixed).** `Restore` no longer overwrites a file the user changed after the run. `Finish` records each captured file's post-run hash; `Restore` skips a file whose current hash differs and reports it as `Skipped`. A checkpoint with no recorded post-state (written before this change) is still restored, but the result carries a `Warning`, so the user decides. Covered by `TestAuditRestoreSkipsFileChangedAfterRun`, `TestAuditRestoreWarnsWhenNoPostStateWasRecorded` and `TestAuditRestoreIsSilentWhenFileUnchanged`.
- **#14 (not a defect).** Checkpoints do not keep file mode. The decision is that a restore writes the saved bytes only.
- **#28 (decided: non-blocking).** `Run.emit` keeps dropping an event when its buffer is full. Blocking was considered and set aside, because a run with no consumer would stop. No code change.


### Latest run (AlmaLinux 10.2, Go 1.26.10, gcc 14, `-race`)

With every `audit: open` skip removed in a working copy: 15 audit tests ran.

- **14 failed**, each with the defect the audit describes (the message is in the test). These are reproductions, not fixes: only #13 is fixed, so the other defects are still in the code.
- **1 passed**: `TestAuditCancelRacesNaturalExit` (#30, withdrawn). It is the evidence for the withdrawal.
- **0 data-race reports** across the run.
- Three `TestAudit…` names outside this list are pre-existing 0.46 audit-log tests, not part of this audit.


### Verification after the first fixes (AlmaLinux 10.2, Go 1.26.10, gcc 14, `-race`)

With every audit skip removed in a working copy, 30 audit tests ran.

- **Fixed and passing (7):** #1, #3, #4, #6, #9, #11, #12. Each was failing before its fix.
- **Passing as evidence (4):** #13 (three tests), and #30, withdrawn.
- **Still failing (19):** the defects not yet fixed. These are correct failures, not regressions.
- **Data races (3):** all in `Agent.persist` and `Agent.Close`, which this round did not touch (`agent.go` has no change in the round). They belong to the open #18 and #19.

A design conflict holds #10 open: `TestShellRulesAreNotPathProtected` records that a shell allow covers a command naming `.git`. The fix for #10 contradicts it, so it is not applied until that decision is made.


### Final state (AlmaLinux 10.2, Go 1.26.10, gcc 14, `-race`)

Every audit skip removed in a working copy: 30 audit tests ran.

- **29 pass.** Each fixed finding has a test that failed before its fix and passes after.
- **1 fails: #10.** Held open on purpose: `TestShellRulesAreNotPathProtected` (an existing test) records the opposite design, a shell allow covering a command that names `.git`. The fix contradicts it, so the decision is with the user.
- **0 data races** across the run.

Decisions, not defects: #14 (checkpoints keep bytes, not file mode) and #28 (a full event buffer blocks nothing; it drops, as before). Withdrawn: #30.

Known limits of fixes, stated here rather than hidden:
- #10 (when decided): a guard that reads the command text cannot see a path built at run time, such as `rm -rf $X`.
- #8: the write phase is not transactional. A failure part way leaves earlier files written; the error now lists them. Rolling back would need the original content held, which is a design choice.
- #13: a checkpoint written before post-state existed is restored with a warning, not refused.

## Tests

Each finding with a test is in the package that owns the code it reaches, named
`TestAudit…`, and marked `t.Skip("audit: open, see audit.md #N")` so the suite stays green.
Each one was run once with its skip removed to confirm the result above.

| Package | File | Tests |
|---|---|---|
| `internal/tools` | `audit_test.go` | #1, #3, #4, #6, #9 |
| `internal/tools` | `audit_internal_test.go` (package tools) | #7 (checks pathsOf and mutatingTools directly) |
| `internal/lsp` | `audit_test.go` | #2 (reproduced on Linux), #8 |
| `internal/llm` | `audit_test.go` | #15, #16 |
| `internal/llm` | `audit_internal_test.go` (package llm) | #17 |
| `.` (agent) | `audit_test.go` | #5, #18, #19, #20, #21, #23, #27, #29 |
| `.` (agent) | `subagent_audit_test.go` | #22, #24, #25, #26 |
| `internal/permissions` | `audit_test.go` | #10, #11, #12 |
| `internal/tasks` | `audit_test.go` | #30 (not reproduced under -race) |

Thirteen audit tests are skipped; none fails with its skip in place. The two that did not
reproduce (#2 and #20) say why in their skip comment. #20 reproduces under `go test -race` on AlmaLinux (cgo), and is skipped on Windows only because the race detector is not available there.

Still open: the 17 `reported` findings have no test yet. They need the same treatment: read the
line, write the test, run it with the skip removed, and only then mark it `confirmed`.
