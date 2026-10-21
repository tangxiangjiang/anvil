# 36 — Tool permissions

**Milestone focus:** the rule set, the shell analyzer, and the decision order → **[m_0.36_tool_permissions.md](../../roadmaps/v1.0/m_0.36_tool_permissions.md)**.

Chapter [08](08-security.md) covers confinement: which paths and commands are *reachable*.
This chapter covers **policy**: given a call that is reachable, who decides whether it runs,
in what order, and on what facts. [14](14-hooks-skills.md) and [35](35-vscode-extension.md)
are the layers this one sits between — file hooks below it, the approval prompt above it.

## The property being copied

A mature coding agent feels "intelligent" about approval because it stops asking for the
things that do not matter. That is **not** a model in the decision path; it is three
deterministic mechanisms:

| Mechanism | What it decides | Why it is not a model |
|---|---|---|
| **Rule set** | Matching calls are allowed, denied, or asked about | Rules are written by the user and readable in a file |
| **Shell analyzer** | Whether a command is *provably* read-only | A pure function of the string; it cannot be talked into a wrong answer |
| **Mode ladder** | Whether to ask at all | Four named modes, each with a fixed meaning |

The design rule that follows from the first two: **an automatic decision must be one the
user could have predicted from the file.** A rule matches literal tokens and paths that were
written down; the analyzer answers only when it can prove, and says `cannot-prove` (which
means *ask*) otherwise.

## Why the rules cannot live in the permission callback

`Options.Permission` is optional, and `Registry` documents the consequence in
`internal/tools/tools.go`: *groups alone open no gate — with no `PermissionFunc` installed
there is nobody to ask, and `Execute` keeps running every tool. A caller that wants an
approval prompt must install both.*

Therefore policy that must hold without a client cannot be implemented inside the callback:

- a library embedder that installs no callback would silently ignore a `deny`;
- the CLI has no callback at all;
- any code path that short-circuits the callback (a rule `allow`, a session allowlist hit)
  would skip hook denies that live there.

So the rule set is evaluated in **`Registry.Execute`**, before the callback, and the file
hooks' shell/write/edit denies move up into the `preExec` stage, which `Execute` runs
unconditionally. `preExec` already carries the read/MCP hook denies; this makes the hook
file's Registry role uniform.

## Decision order

High to low. A step that decides ends the call; nothing below it runs.

| # | Decision | Where | Can it be overridden? |
|---|----------|-------|-----------------------|
| 1 | File-hook deny (`.anvil/hooks.json`) | `preExec` | No |
| 2 | Rule **deny** | `Execute`, pre-callback | No — not by a session allowlist, not by `bypass` |
| 3 | Rule **allow** | `Execute`, pre-callback | No callback, no prompt; cannot undo 1 or 2 |
| 4 | Session allowlist `(session, tool, scope)` | inside the callback | Yes, it is per session and in memory |
| 5 | Rule **ask**, or the mode's gate | callback | The user's answer |
| 6 | Nothing matched, not gated | — | Executes, exactly as before this chapter |

Within the rule set: across effects, **`deny` > `ask` > `allow`** — a single matching deny
defeats every allow, wherever it sits in the file. Within one effect, **first match in file
order wins**. "Most specific wins" is deliberately rejected: specificity is not written in
the file, so a reader would have to recompute a metric nobody agrees on (`readonly` versus
`mcp_*`; `git status:*` versus a later `git *`). File order can be read top to bottom, and
the residual footgun — a broad rule shadowing an earlier specific one — is a reading problem
in a file the user wrote: the file is what a client displays, in order, numbered, and 0.36
does not attempt to analyse shadowing for them.

## The rule file

`.anvil/permissions.json`, workspace-relative, committable:

```json
{
  "version": 1,
  "rules": [
    { "effect": "allow", "tool": "shell", "pattern": "readonly", "reason": "read-only commands" },
    { "effect": "allow", "tool": "shell", "pattern": "git status:*" },
    { "effect": "deny",  "tool": "shell", "pattern": "rm -rf:*", "reason": "destructive" },
    { "effect": "allow", "tool": "write_file", "pattern": "src/**/*.go" },
    { "effect": "ask",   "tool": "apply_patch", "pattern": "**/*.md" },
    { "effect": "deny",  "tool": "mcp_*" }
  ]
}
```

Two keys, and one more added by [37](37-permission-modes.md): `"defaultMode"` names the mode a
session opens in when the client sends none, with the `bypass` ban and the refusal wording that
chapter describes. It is a field of this file, not a rule, so nothing here interprets it — but
anything that *rewrites* the file has to carry it, which is why the write path names it
explicitly ([37](37-permission-modes.md)).

`pattern` is interpreted per tool, because the useful pattern for a command string is not
the useful pattern for a path:

| Tool | `pattern` means |
|------|-----------------|
| `shell` | Literal `readonly` (the analyzer's verdict), an exact command, or `prefix:*` |
| `write_file`, `edit_file` | A workspace-relative path glob (with `**/`) |
| `apply_patch` | A path glob that must cover **every** path the patch touches |
| anything else | A glob over the tool **name** — a second way of writing the tool-name glob |

Tool names may be globbed (`mcp_*`). `run_tests` and `build` are in the last row on purpose:
their preview is the **configured** argv, not the model's arguments, so a command-shaped
rule on them could not see what it claims to see, and name matching is the honest form.

A call that carries neither a command nor paths has no fact for a shape-specific pattern to
be compared against, so its `pattern` is compared with the **tool name**. A path-shaped
pattern on such a call therefore matches nothing, and the call is asked about: `read_file`
carries no path fact, so `{"effect":"ask","tool":"read_file","pattern":"**/*.md"}` never
fires, while `{"effect":"ask","tool":"read_file"}` fires on every read. Letting a rule match
a read by path means widening the card first, so that a pattern still only ever matches
something the user was shown; 0.36 ships the boundary as it stands rather than widening it in
a phase about the client. The error is in the safe direction: a path-shaped rule on a call
with no path fact matches nothing, and a call no rule matches is one that gets asked about.

**An omission is not a wildcard in the dangerous direction.** An empty `pattern` matches by
tool name alone, and an empty path list — a call whose paths could not be determined — never
matches a path rule.

### Prefix rules match tokens, never strings

`git status:*` must not match `git status && rm -rf /`, and must not match `git statusx`.
So the matcher consumes `shellcmd.Split` and compares **argv token prefixes**, segment by
segment, and a prefix rule requires a single segment. String prefixing would pass both of
the wrong cases above; that is why `strings.HasPrefix` appears nowhere in this path.

## The shell analyzer

`shellcmd.Analyze(command, goos) Verdict` is a pure function returning three values:

| Verdict | Meaning | Consequence |
|---------|---------|-------------|
| `read-only` | Every segment is provably non-mutating | May be auto-allowed by a `readonly` rule |
| `not-read-only` | Some segment is provably mutating | Never auto-allowed |
| `cannot-prove` | The analysis could not complete | **Never auto-allowed** — ask |

Only `read-only` ever satisfies a rule. `cannot-prove` is not a soft failure; it is the
normal outcome for anything unusual, and the design accepts asking more often rather than
guessing once.

Pipeline: `Split` on `&&`, `||`, `;`, `|`, `&`, and newlines **outside quotes**; `Lex` each
segment into literal tokens, failing on `$` or backtick expansion, a POSIX backslash escape,
a redirection, an unquoted glob metacharacter, and the subshell/history characters. A quoted
argument is kept when what it encloses is itself literal — that is what lets `sed -n '1p' f`
and `grep -n "name" f` be proved, while `grep -n "$PATTERN" f` cannot be, and it is why an
escape *inside* double quotes is refused: its value is not the text on the line. The one
unquoted brace allowed is the exact token `{}`, find's placeholder — bash expands a brace
only when it holds a comma or a range, so `{}` survives as written, while `{-delete,}` would
expand and rewrite a later argument, and the shell would run something no rule matcher ever
saw. A line that cannot be split, or a segment that cannot be lexed, makes the *whole* line
`cannot-prove` rather than costing only that segment. Then strip leading `timeout N`, `nice`,
`env A=1`, and treat `cd X` as read-only;
then classify each remaining argv against a curated allowlist with per-command constraints —
`find` only without `-exec`/`-delete`/`-ok`/`-fprint*`, `sed` only without `-i` and without a
`w`/`r`/`e` script command, `git` by subcommand (`status`, `log`, `diff`, `show`,
`rev-parse` … are read-only; `fetch`, `push`, `commit`, `checkout`, `clean`, `config` are
not; an unknown subcommand is `cannot-prove`). `eval`, `exec`, `source`, `.`, `xargs`,
`sudo`, and any command outside the table are `cannot-prove`.

On Windows the shell tool runs `cmd /C`, whose metacharacters (`%VAR%`, `^`, `!`) are not
POSIX shell at all, so the analyzer treats them as `cannot-prove` rather than modelling a
second grammar.

**The analyzer is a proof, so its tests are property tests as well as table tests.** Beyond
the per-command cases, any `read-only` result must imply that the split succeeded, every
segment lexed, every argv[0] is in the allowlist, and no segment contains a redirection.

## Matching facts are the card's facts

A rule matches the same `PreviewFor` facts the approval prompt displays — tool name, the
shell command string, and the touched paths. That is a guarantee, not a convenience: it
means a rule cannot match on something the user would not have been shown.

`apply_patch`'s preview carries its touched paths as a comma-joined `Path` string today; the
matcher reads a new **`Paths []string`** field instead, populated from the same slice the
preview was built from. Re-splitting the joined string in the matcher is the bug this
avoids: a filename containing `, ` would have silently become two paths.

The rule engine never computes a diff or a patch body. It reads the cheap facts only
(`commandOf`, `pathsOf`), so a call that is allowed outright pays no preview cost, and the
expensive preview is computed exactly where it was before — when someone is actually going
to be asked.

The one case where that no longer holds is `bypass`. The mode requests no gated groups, but
an empty group set falls back to `IsMutating` (`internal/tools/tools.go`), so a mutating call
still reaches the `ask` branch — and because `bypass` now installs an always-allow callback
rather than none, `Execute` builds a preview for it and hands it to a callback that approves
without looking. A wasted dry run per mutating call in `bypass` is the accepted price of
keeping `deny` alive in that mode without a second decision path; before, the whole block was
skipped for want of a callback, which is exactly the hole this chapter closes. The preview is
built in memory from a file already being read, and `bypass` is not a default mode.

## Writing a rule from the card

The rule file is the interface, so a client does not edit it with a text cursor alone: the
approval card's "always allow" appends to it. That is the one place where the product's answer
to "who decides" gets *written down*, and it is where an explanation can most easily drift
from the decision it explains. Three rules keep them together.

**The host offers, the client labels.** `PermissionRuleOptions(tool, args)` builds the
candidate shapes — this exact command line, the leading token run as a prefix, `readonly`, one
path, the whole tool — and keeps a candidate only if **evaluating it against this very call**
matches (`decisionFor(set, facts).Matched`, the same function a real decision runs). What
comes back on the approval is therefore a set of buttons that all work, each with the rule it
would write. The button's text is chosen from the option's **`kind`**, never derived from the
pattern: a client that parsed `git status:*` to decide whether to call it a prefix would be
implementing the pattern grammar a second time, in the language least able to test it. The
pattern is displayed verbatim, because the pattern is what lands in the file.

The filter is what makes the boundary visible rather than surprising. A compound line offers
nothing: no exact rule covers `git status && rm -rf /`, no single token prefix does, and the
analyzer will not prove a two-command line read-only. A call that carries no command and no
paths — an MCP tool's — is offered "allow every call to this tool" only when it is not a
mutating built-in, because for `shell` that button *is* the all-or-nothing memory this rule
table exists to replace.

**The host answers, the client reports.** `permissions.add` takes the rule and, when the
client names the approval it was written for, a `request_id`. It appends the rule, re-reads
the file, and evaluates the call again against what the file now says. Only when the deciding
effect is **allow** does the host answer the approval itself and say `approved: true`. When
something else decides it — an earlier `deny`, an `ask` — the rule is *still written* (the
user asked for it, and it is a file they own) and the answer carries `decided_by` naming what
did decide. When *no* rule decides the call — it was gated by the mode alone, and the new rule
is of a shape that cannot cover it — `decided_by` is absent, which says the file changed and
this call did not. A client that assumed its own button had settled the call would be showing
an approval that is still waiting.

That leaves the card three honest endings, and it renders all three: **approved and
remembered**; **written, but this call is decided by `<decided_by>`** — with the remember
buttons removed, since no new rule can outrank what decided; and **not written**, with the
error, buttons re-enabled, and nothing claimed.

**No option is not a dead end.** When `remember` is empty the card offers the session-scoped
allowlist entry instead — the `(session, tool, scope)` memory from before this milestone. It
is offered *only* in that case, so it never competes with a rule button, and the all-or-nothing
memory is not silently lost where no rule can be written at all. Without it, "no rule can
describe this call" would mean "there is no way to stop being asked".

Composing the file is the SDK's job, not the client's: `LoadPermissionRules` reads it,
`AddPermissionRule` appends to it. The append parses the new text with the same parser the
loader uses *before* anything is written (a rule the UI produces is a rule the next session
accepts), writes a temporary file in the same directory and renames over the target (an
interruption leaves the old policy, not half of a new one), and **refuses to write at all**
when the existing file cannot be read — replacing policy it cannot parse is the failure the
whole design is arranged around.

## Reading the file while a session runs

The rule set a session decides by is not captured at `Create`. `permissions.Source` re-reads
`.anvil/permissions.json` and appends the caller's own `Options.PermissionRules` after it, per
call, and `Registry.SetPermissionRulesSource` is what the agent installs when
`LocalOptions.LoadPermissions` is on.

The reason is the promise the card makes. "Always allow this prefix" is answered by a rule
that must be in force for the *next* call of the same session, and the file can also be edited
by hand while the session is open — with a set captured once, a user watching a file change
would be governed by a policy the file no longer states, and there would be no way to tell.
Per call, the file decides and the `reason` a rule carries is the reason in force.

The price is a read per tool call, and one deliberate asymmetry: a file that has become
**unreadable mid-session** — mistyped, half-saved, deleted — falls back to the last set that
read cleanly rather than failing the call. A session already has a policy, and it is the one
the file stated a moment ago; refusing every call because somebody is halfway through editing
a file punishes the wrong person. A malformed file **at session start** stays a hard error
(above), because that is a policy the user asked to be governed by and there is no earlier
policy to fall back to.

Two more things the card can reach:

- `permissions.get` reports a file that will not compile as a **result** carrying the place
  (line, column, message), not as an error. "Half-edited" is a state of a file somebody is
  working in, and the client's job with it is to open the file there and put a squiggle on it
  — which is why `path` is absolute and the position is the parser's own.
- `permissions.add` **refuses** for that same file with `CodeHostError` naming
  `path:line:col`, and for a `request_id` that no longer names a pending approval with
  `CodeNoApproval`, and for a session created with `load_permissions: false` with
  `CodeInvalidRequest`: a rule written into a file that session does not read would not be in
  force, and saying so is better than writing a rule that does nothing.

## Failure modes

| Situation | Behaviour | Why |
|-----------|-----------|-----|
| `.anvil/permissions.json` missing | No rules; behaviour identical to before | A workspace need not have one |
| `.anvil/permissions.json` malformed | **`Create` fails**, naming the file | Silently ignoring a policy is worse than refusing to start |
| `ask` rule, no callback installed | **Denied**, with a message saying no approval channel exists | Fail closed; there is nobody to ask |
| `deny` rule in `bypass` | Still denied | `bypass` skips *asking*, not the policy the user wrote |
| `ask` rule in `bypass` | Allowed (the mode's always-allow callback answers it) | `bypass` is the user pre-answering yes |
| Rule `allow` meeting a hook deny | Denied | Order 1 > 3 |
| Rule `allow` meeting a session allowlist entry | Both allow; the rule resolves first and no prompt is raised | Order 3 > 4 |
| A rule written from the card that something else outranks | Written, reported as not deciding; `decided_by` names what does | The file is the user's; an append is not a verdict |
| `permissions.add` naming a call nobody is waiting on | `-32004`, and **nothing is written** | A rule for a call that has gone is a rule nobody asked for |
| The file becomes unreadable while a session runs | The session keeps the last set that read cleanly | A policy under edit is not a reason to refuse every call |
| A call no rule shape can describe (a compound line) | No "always allow" rule button; the session-scoped entry remains | Offering a button that writes a rule which does nothing teaches that rules do not work |

A denied call surfaces as a **tool error** the model can read and react to, which is the
existing deny contract from [35](35-vscode-extension.md) — the model is told why and can
choose another route.

## Loading

Two switches, both off by default for library embedders, mirroring `LocalOptions.LoadRules`:

- `LocalOptions.LoadHooks` — `.anvil/hooks.json`. It was previously reachable only through
  `SettingSources: [SettingSourceProject]`, which **neither the editor host nor the CLI ever
  set**, so its deny rules were inert in both. This switch is the fix.
- `LocalOptions.LoadPermissions` — `.anvil/permissions.json`.

The editor host defaults both to on (a per-session parameter can turn them off), because a
policy file the user wrote in their own workspace should apply when their editor opens it;
the CLI does the same. A malformed file therefore fails session creation with the path named
— accepted deliberately, and it is why the error must be specific.

## What this changes for existing callers

- Additive: `Options.PermissionRules`, `LocalOptions.LoadHooks`, `LocalOptions.LoadPermissions`,
  `ToolPreview.Paths`. Nothing changes unless one is set.
- Additive, and how a client implements against the file: `PermissionRulesPath`,
  `LoadPermissionRules`, `AddPermissionRule`, `EvaluatePermissionRules`,
  `DescribePermissionRules`, `PermissionRuleOptions` and `PermissionRuleError` — public
  wrappers over the same evaluation, so a client that *explains* a decision runs the decision's
  own code. `Event.Rule` and `PermissionRequest.Rule` carry the deciding rule outwards, set
  where the decision is made.
- Semantic, only with `LoadPermissions` on: the rule set is composed **per call** from the
  file as it now stands plus the caller's own rules (see *Reading the file while a session
  runs*). A session therefore sees a hand edit, and a rule written from the card, without being
  rebuilt.
- Semantic, opt-in: with a rule set and an `ask` rule, a callback-less agent now **denies**
  instead of running. This is the point of the feature and it is why the switches default off
  in the library.
- Behaviour-preserving refactor: the hook shell/write/edit denies move from
  `composePermission` to `preExec`. Deny still denies, whether or not a callback exists.
- Ordering: the group gate (`permission_groups`) is untouched. Rules sit above it; a call
  with no matching rule is decided exactly as it was before this chapter.

## Explicit non-goals

- A model in the decision path, at any point.
- OS-level isolation: containers, seccomp, filesystems — [32](32-sandbox-code-review.md).
- Rule inheritance (user level, enterprise managed settings) and rule distribution.
- Path rules on reads (`read_file`, `grep`, `glob`): those calls carry no path fact, so a rule
  on one matches by tool name. Widening them means widening the card in the same move, which
  0.36 does not do.
- A complete shell grammar; anything unproven is `cannot-prove`.
- Per-hunk approval policy, and remembering an approval as anything other than a rule in a
  file the user can read: the card writes `.anvil/permissions.json`, and there is no second,
  hidden place a decision is kept.
- A rule editor beyond appending one well-formed entry — no graphical builder, no reordering,
  no sync, no user-level or managed rule sources. (0.41 adds a structured manager for the rows
  this file already holds — [40](40-permission-auto-and-manager.md); reordering, search and sync
  stay out of scope. A user-level scope was added by 0.47 — [37](37-permission-modes.md)
  §*Scopes and protected paths*; managed/organization policy remains out of scope.)

## Roadmap mapping

[m_0.36_tool_permissions.md](../../roadmaps/v1.0/m_0.36_tool_permissions.md) phases
`p_0.36.1` (SDK rule engine, analyzer, loading) and `p_0.36.2` (protocol and extension
surface).
