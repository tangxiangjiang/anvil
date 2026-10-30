# 51 — Protected paths in shell permission decisions

**Milestone focus:** 0.51. **Design status:** done.

## 1. Today / problem

The permission engine already has a useful distinction: a protected path is not forbidden, but
an `allow` rule may not settle a call that touches it. `permissions.Set.Evaluate` skips matching
allows when `Set.protectedPaths` returns anything, then a deny, ask, or the editor's permission
mode decides. The fixed set is implemented by `Protection.protectedRelative` in
[`source/internal/permissions/protect.go`](../../../source/internal/permissions/protect.go):
any `.git` segment, `.anvil/checkpoints/`, the Anvil home, and user `protected` globs.

That protection currently sees only `CallFacts.Paths`. File tools populate the field through
`pathsOf` in
[`source/internal/tools/preview.go`](../../../source/internal/tools/preview.go), while `shell`
populates `CallFacts.Command` and no paths. Consequently a rule such as
`{"effect":"allow","tool":"shell","pattern":"rm:*"}` can settle `rm -rf .git`. Audit finding
#10 records the reproducer in
[`source/internal/permissions/audit_test.go`](../../../source/internal/permissions/audit_test.go)
and remains skipped because the older test encoded the opposite policy.

The defect cannot be fixed by treating a command string as one path. Shell text contains
quoting, pipelines, redirections, substitutions, multiple commands, and two platform families.
It also cannot be fixed by previewing after the rule decision: a rule is intentionally decided
without executing or reading workspace content.

## 2. Goals

1. A shell `allow` must never settle a command that statically names a protected path.
2. If analysis cannot establish the path operands safely, the decision fails closed: shell
   allows are skipped and the existing permission mode decides.
3. Deny and ask rules retain their current precedence and matching semantics.
4. The approval card and resulting `tool_result` report the protected paths or the fact that
   path analysis was indeterminate.
5. The analysis is bounded, deterministic, side-effect free, and selected by `goos`.

## 3. Protocol and data structures

`CallFacts` in
[`source/internal/permissions/rule.go`](../../../source/internal/permissions/rule.go) gains
analysis facts; callers do not pass an unstructured sentinel through `Paths`:

```go
type PathKnowledge string

const (
    PathsKnown         PathKnowledge = "known"
    PathsIndeterminate PathKnowledge = "indeterminate"
)

type CallFacts struct {
    Tool        string
    Command     string
    Paths       []string
    PathState   PathKnowledge
    PathProblem string
}
```

The zero value remains today's behavior for non-shell callers. `pathsOf` is replaced internally
by a fact builder:

```go
func callFactsOf(name string, args json.RawMessage, goos string) CallFacts
func AnalyzeShellPaths(command, goos string) ShellPathAnalysis

type ShellPathAnalysis struct {
    Paths   []string // lexical spellings, in first-seen order
    State   PathKnowledge
    Problem string   // stable category, not raw command text
}
```

`AnalyzeShellPaths` is internal policy machinery, not a public SDK promise. Stable problem
categories are `unsupported_syntax`, `dynamic_path`, `unclosed_quote`, `limit_exceeded`, and
`unknown_dialect`.

Public `PermissionRequest`, tool `Result`, runtime `Event`, editor-host `EventParams`, and the
TypeScript `ApprovalRequest`/`HostEvent` gain:

```text
path_analysis: "known" | "indeterminate"       (omitted when not shell)
path_problem:  <stable category>               (only when indeterminate)
protected:     [<lexical path>, ...]            (existing field)
```

This is additive. `protected` keeps its current meaning and spelling.

## 4. Analyzer contract

The analyzer is a small lexer, not a shell interpreter.

- `goos == "windows"` recognizes `cmd.exe` quoting, separators (`&`, `&&`, `||`, `|`),
  redirections, `%NAME%`, delayed `!NAME!` expansion, and caret escaping.
- Other supported values use POSIX shell quoting, separators (`;`, newline, `&&`, `||`, `|`,
  `&`), redirections, `$NAME`, `${...}`, command substitution, backticks, and backslash
  escaping.
- The command word of each simple command is excluded. Every remaining non-option word is a
  path candidate. A value after `--name=` and every redirection target is also a candidate.
  This deliberately over-approximates: `echo .git` may ask, but `rm .git` may not pass.
- Relative, absolute, `~`-prefixed, slash and backslash spellings are preserved and passed to
  the existing `Protection.isProtected`; the analyzer does not duplicate path normalization.
- A quoted literal is known. A word containing expansion, globbing, command substitution,
  process substitution, an unclosed quote, or an unsupported control construct is
  indeterminate when it can contribute an operand or redirection target.
- Nested `sh -c`, `bash -c`, `cmd /c`, and PowerShell command strings are analyzed only when
  their nested text is one literal token and the dialect is supported. Interpreter text formed
  dynamically is indeterminate.
- Input is capped at 64 KiB, 256 simple commands, and 4096 tokens. Crossing a bound is
  `limit_exceeded`, never a partial known result.

The analyzer does not claim to sandbox an executable. A trusted command allowed by its exact
command pattern can itself open paths not named in its arguments. This chapter closes the
permission-engine bypass for shell syntax and dynamically formed operands; OS-level process
confinement remains outside the product's current contract.

## 5. Data flow

1. `tools.Registry.Execute` builds one `CallFacts` before rule evaluation.
2. For shell, `AnalyzeShellPaths` runs over the exact `command` argument. For file tools the
   existing argument-only path extraction remains known.
3. `permissions.Set.Evaluate` computes `protected` from the extracted paths.
4. A shell allow is eligible only when `PathState == PathsKnown` and `protected` is empty.
   An indeterminate shell call skips every allow exactly as a protected call does.
5. Deny and ask rules still match the original `Command`. A deny can therefore stop an
   indeterminate call without opening an approval.
6. With no deciding rule, the host's existing mode/callback path runs. A shell call with a
   protected path or indeterminate analysis sets a new `mustConfirm` bit independently of the
   ordinary permission groups. If there is no callback, the registry refuses it with a
   no-channel error; analysis uncertainty never turns into execution.
7. The approval and final result carry `protected`, `path_analysis`, and `path_problem`.

## 6. Core invariants

- **No allow on protected:** if any extracted path is protected, no allow rule can decide.
- **No allow on uncertainty:** an indeterminate shell analysis is equivalent to protected for
  allow eligibility, but is not mislabeled as a particular protected path.
- **No weakening:** analyzer output can only remove an allow decision; it cannot create an
  allow, override a deny/ask, or alter rule precedence.
- **One set of facts:** rule evaluation, approval rendering, and `tool_result` use the same
  immutable analysis.
- **No I/O:** analysis does not expand environment variables, inspect aliases, resolve globs,
  read scripts, or touch the filesystem.
- **Bounded work:** malformed or adversarial command text reaches an explicit indeterminate
  result within fixed limits.

## 7. Security and failure semantics

This milestone is security-first and fails closed. Parser error, panic recovery, unknown
dialect, or a future syntax the lexer does not understand all produce
`PathsIndeterminate`; they do not fall back to today's allow behavior.

Protection still has its established meaning: it prevents remembered allows, not an explicit
decision by the active mode. `ask` presents the call; `deny` refuses it; `bypass` remains an
explicit user-selected policy and may run it. The editor host installs a callback for those
modes. A headless SDK call with no permission callback now refuses a shell call whose analysis
requires confirmation; this is a new fail-closed branch, not a claim about today's no-callback
behavior.

Cards must say either “Touches protected paths: …” or “Path analysis incomplete
(`dynamic_path`); a saved allow cannot cover this command.” They must not claim that a command
is safe merely because the candidate list is empty.

## 8. Migration and compatibility

- File tools, rule JSON, command-pattern matching, and protected-glob syntax do not change.
- Existing shell deny/ask rules behave identically.
- Some shell allows that currently execute without interaction will ask, especially commands
  with variables, globs, nested interpreters, or unsupported syntax. This is an intentional
  security migration.
- The skipped audit test is inverted and enabled in the same phase as the implementation.
  The older `TestShellRulesAreNotPathProtected` expectation is replaced, not retained as a
  contradictory compatibility promise.
- Older editor clients ignore the additive analysis fields and still receive an approval.

## 9. Test strategy

Table tests in `source/internal/permissions` cover POSIX and Windows quoting, separators,
redirections, `--flag=path`, case-insensitive `.git`, checkpoints, Anvil home, user globs,
dynamic expansions, nested shells, malformed text, and every bound. Fuzzing asserts no panic,
bounded completion, and “indeterminate never allows.”

Integration tests in `source/internal/tools` prove that `callFactsOf` supplies the same facts to
rule evaluation and result events. Editor-host tests prove the wire fields and no-channel
refusal. VS Code unit tests pin both card messages.

The acceptance reproducer is audit #10: `rm -rf .git` under `allow shell "rm:*"` must not be
settled by that allow. Additional mutation tests remove each uncertainty branch and must make
at least one dynamic-path case fail.

## 10. Non-goals

- An OS sandbox, syscall monitor, or post-execution filesystem rollback.
- Proving what an arbitrary executable or script does internally.
- Changing protected-path membership or letting workspaces define protected globs; the user
  layer remains their owner.
- Replacing shell command-pattern rules with argv rules.
- Making `bypass` behave like `ask`.

## 11. Delivery phase mapping

| Phase | Contract |
|---|---|
| p_0.51.1 | Analyzer, `CallFacts`/decision semantics, fail-closed allow suppression, audit #10 enabled, Go unit/fuzz/integration coverage |
| p_0.51.2 | Public event and editor-host wire fields, VS Code approval/result rendering, compatibility tests and end-to-end acceptance |
