# 37 — Permission modes

**Milestone focus:** a complete mode ladder, and where a session's starting mode comes from →
**[m_0.37_permission_modes.md](../../roadmaps/v1.0/m_0.37_permission_modes.md)**.

Chapter [36](36-tool-permissions.md) is the layer below: which calls may run, on what facts,
decided by rules the user wrote down. This chapter is the layer above: **whether to ask at
all**. [35](35-vscode-extension.md) is where a person picks a mode, and
[08](08-security.md) is what is reachable once a call is allowed to run.

The two layers are independent on purpose. A mode composes options; it never rewrites policy,
and no mode can talk a `deny` out of the way.

## What a mode is

A mode is a **composition of public SDK options**, owned by the host
(`source/internal/editorhost/`). The SDK has no mode concept, and does not need one: every row
below is reachable through `Options` the SDK already documents — `PermissionGroups`,
`Permission`, `DisallowedTools`.

| Mode | Wire value | Gated groups | Callback | Meaning |
|---|---|---|---|---|
| Ask | `ask` | the default six | the client | Ask about every gated call |
| Accept edits | `accept_edits` | default minus `write`, `edit` | the client | Edits are fine; shell, verify, egress and external tools still ask |
| Auto | `auto` | the default six | **classifier, then the client** | Prove the call harmless — read-only shell, in-workspace edit, configured test/build — and run it; ask otherwise |
| Plan | `plan` | the default six | the client | The workspace-changing tools are withheld and the model is told to plan |
| Don't ask | `dont_ask` | the default six | **always no** | Run only what the rules already allow, and never prompt |
| Bypass | `bypass` | none — the mutating built-ins | **always yes**, except for a rule that asked | Do not ask; the rules still hold |

"Gated groups" is what the mode gates when the client sends no `permission_groups`; an explicit
list overrides that default for every mode except `bypass`, whose column is the whole point of
the mode.

One row per column, and that is the whole design: a mode is a row in this table, and adding
one means answering three questions — what it gates, who answers, and what it says when it
answers no. Two of those answers live in one function each (`groupsForMode`,
`permissionHookForMode`) so that a mode cannot be added to one and forgotten in the other;
`TestEveryModeResolvesToAHook` is what keeps that true.

`auto` is the one row whose "who answers" is not the person: its callback approves a call only
when `anvil.AutoApproveCall` can prove it harmless, and otherwise delegates to the ordinary
card. The classifier is deterministic — it reads the facts a rule matches on and the same shell
analyzer the `readonly` pattern uses — and chapter [40](40-permission-auto-and-manager.md) is
where that decision, and what it deliberately does not prove, is written down.

What a mode does **not** do: it does not change the model, it does not change the rules, and
it does not remove tools (except `plan`, which withholds them through `Options.DisallowedTools`
— a restriction the model can see, rather than a gate it discovers by being refused).

## `dont_ask` is a callback that says no

The tempting implementation of "never ask" is to install no callback. The SDK says why that is
the opposite of what it sounds like, in `internal/tools/tools.go`: *groups alone open no gate —
with no `PermissionFunc` installed there is nobody to ask, and `Execute` keeps running every
tool.* No callback does not mean *refuse*; it means *run everything*. That is `bypass`.

So `dont_ask` installs a callback whose answer is always no — the exact mirror of the
always-yes `bypass` installs. The two modes are then the same mechanism read in two directions,
which is why neither needs special handling anywhere below the host.

Two things it does *not* refuse, and both matter:

- **A call the rules allow.** `allow` is decided in `Registry.Execute`, before the callback, so
  a call with a matching allow rule never reaches the always-no callback at all. This is what
  makes the mode useful: it runs the workspace's declared policy and nothing else.
- **A call the session already approved.** The callback consults the session allowlist first,
  because "always allow for this session" is a pre-approval a person gave, and refusing it
  would make an explicit answer lose to a mode. No protocol path reaches this today — a mode is
  fixed when the session opens, and the allowlist's only writer is the ordinary approval hook,
  which `dont_ask` does not install — so it is an invariant held ahead of the path that would
  need it, and `TestDontAskHonoursASessionAllowance` exercises the callback directly to keep it
  true. A future "change this session's mode" call is what would make it reachable; that call
  would have to consult the same list for the same reason.

When it refuses, the message has to be actionable by the model, which cannot see the mode:

| What asked | What the tool error says |
|---|---|
| A rule of effect `ask` | `<tool> denied: .anvil/permissions.json rule 2 (ask shell "git push:*") asks for confirmation and this session runs in dont-ask mode` |
| The mode gate | `<tool> denied: this session runs in dont-ask mode, and only calls the workspace's rules allow may run` |

The wording is the host's because the mode is the host's, and it reuses the SDK's own
`Describe()` for the rule reference, so the same rule is spelled the same way in a refusal, on
a card, and in `permissions.get`.

## An explicit rule outranks `bypass`

`bypass` exists to skip the *asking*. It was never a statement that the rules do not apply —
p_0.36.1 already made a `deny` hold there — but it had been letting one effect through: an
`ask` rule, whose only purpose is to interrupt, was answered yes by the always-allow callback.

The two statements being compared are not the same kind of thing:

- the **mode** is a blanket answer about everything a session might do;
- the **rule** is a written statement about one tool, in a file the user can read, whose
  `ask` effect has no other meaning.

A blanket "do not ask" is therefore not an answer to `git push:*` in particular, and the rule
wins. The implementation is one branch: `bypass`'s callback delegates to the ordinary approval
hook when `PermissionRequest.Rule` carries an `ask` rule, and answers yes otherwise. Claude
Code resolves the same conflict the same way, which matters for a feature whose whole point is
that it behaves like the tool people already know.

The cost is visible: a card can appear in a mode whose banner says calls run without asking.
That is why the card carries the rule that asked (p_0.36.2) — it can say *why* it appeared in a
mode that promised it would not.

## Scopes and protected paths (0.47)

A rule written once for one repository is the wrong shape for a preference that is about the
person: "never run `rm -rf`" was being copied into every `.anvil/permissions.json`. 0.47 gives
rules a second scope.

**Where the rules come from.** The workspace file is unchanged. The user file is
`$ANVIL_HOME/permissions.json`, or `~/.anvil/permissions.json` when the variable is unset —
the same `.anvil` directory the MCP token store already uses. The two files have different
shapes on purpose: the user file may carry `protected`, the workspace file keeps `defaultMode`,
and each parser refuses the other's key. A repository cannot widen what a person may rewrite
without asking, and a machine-wide file cannot choose the mode of every session in every
repository.

**How they combine.** The compiled set is user rules, then workspace rules, then rules passed
in `Options.PermissionRules`. Severity decides across the whole set — `deny > ask > allow` — so a
workspace `allow` cannot weaken a user `deny`. Within one effect the first rule in that order
wins. Each rule keeps the file it came from and its one-based position *in that file*, so a
citation is traceable: `user permissions.json rule 2`, `.anvil/permissions.json rule 1`, or
`session rules rule 3`. Reporting the merged position, as 0.36 did, would name a line that is in
no file the user can open.

`permissions.get` keeps one `rules` array in that order, each entry carrying its `source`; the
manager groups by it. A same-severity conflict keeps both rules and displays both sources rather
than the merge picking a winner the file does not.

**Protected paths.** Three locations are never covered by an `allow` rule, however specific it
is: any `.git/` segment, the workspace's `.anvil/checkpoints/`, and `~/.anvil/`. The user file
may add globs of its own. Evaluation skips `allow` rules when the call touches one of them and
lets `deny` and `ask` decide as usual; with no rule left, the mode decides exactly as it would
have without the allow. So `bypass` still runs the call and `dont_ask` still refuses it — what
cannot happen is a remembered "always allow" silently including the repository's history. The
decision carries the protected paths, and they travel on the approval card and the tool result
so the record says after the fact why no allow covered the call. A shell rule is matched on the
command line, not on paths, so this protection is about tools whose calls name files; chapter
36's rule that a call's facts are what the card shows is what keeps that honest.

A malformed user file is reported like a malformed workspace file — session creation names the
file and refuses to run under a policy it cannot read — and the manager shows it read-only. The
manager itself needs no session, so the file can always be fixed.

**Writing one layer at a time.** `permissions.add` and `permissions.set` name the file they
write with `source`: `workspace` is the default and the only meaning an older client has, and
`user` names the person's file. A save carries the fields of one shape only — `protected` and
`create` belong to the user layer, `default_mode` to the workspace layer — and the host refuses
the other rather than dropping it, because a save that silently loses half of what it was asked
to write is how the two files would drift into each other's shape. `create` is the manager's
"create user file" step: an ordinary save of a user layer that does not exist is refused, while
the workspace layer keeps create-on-save, which is what its Open JSON button has always done.

## Where a session's starting mode comes from

Three steps, resolved in the host in this order:

| # | Source | When |
|---|--------|------|
| 1 | `permission_mode` on `session.new` / `session.resume` | The client sent one: a person chose it, or the client remembered their choice |
| 2 | `defaultMode` in `.anvil/permissions.json` | The client sent none, and the workspace declared one |
| 3 | `ask` | Nothing else said |

Step 2 is skipped entirely when the session is created with `load_permissions: false`. One
switch governs the file: a session that does not read the workspace's rules must not take its
mode from them either, or "ignore the file" would mean "ignore most of it".

The client is deliberately not part of this. A client with no opinion sends no
`permission_mode` and is told what the session is in — the mode is on the session result, and
the client renders it. Having the client read the file and decide would put the resolution in
the one place that cannot enforce it, and would have to be re-implemented by the next client.

`resolveMode` reports three things rather than one, because two of them are questions the user
will ask: the mode, **where it came from** (`request`, `file`, `default`), and anything the
file declared that the host would not honour. A workspace that starts sessions in
`accept_edits` should be able to explain itself without the user opening the file.

### A project file may not grant `bypass`

A file in a repository is content from that repository. A repository someone cloned is not a
person who may decide that its agent runs unattended — so `"defaultMode": "bypass"` is not a
mode a workspace can set, and neither is anything else the ladder does not have.

It is **refused and reported**, not fatal, and not silent:

| | Outcome |
|---|---|
| The session | Starts in `ask`, which is also what a file that declares nothing gets |
| The session result | `permission_mode_refused` carries a sentence naming the value |
| `permissions.get` | `default_mode_refused` carries the same sentence, and `default_mode` is **left empty** |
| The client | Says so once, where the user is looking |

`default_mode` is empty rather than filled with the mode that was substituted, and the
difference matters: a reader given `default_mode: "ask"` for a file that declares `bypass`
would take the file to have asked for `ask`. The two fields are one answer between them — the
declaration the host would honour, or the one it will not, in words — and never both. A file
that declares nothing has neither.

Fatal was rejected for a specific reason: a session that refuses to start because of a file
somebody else committed is a workspace that a repository can disable. Silence was rejected for
the opposite reason: a team that wrote `bypass` and found sessions in `ask` would conclude the
feature does not work, which is the failure mode 0.36.2 was written to avoid.

The distinction from a *malformed* file is worth stating, because both end in the same place —
a message. A file that will not parse is a file nobody can act on, and there is nothing safe to
do with it (p_0.36.1's contract, and it is still fatal at startup). A file that parses and
declares something the host declines is a policy question with a known-safe answer: run in
`ask`, and say why.

A declaration that is not a bare lowercase word at all — `"Bypass"`, `"accept edits"`, `""` —
is the parser's business, not the host's, and is reported like a misspelled effect: a problem
on the file, with the place, so it can be fixed where it is written.

## What the composer shows

The panel is where a person sees which mode is in force, and the resolution above gives it two
different things to show: the choice it stored for this workspace, and the mode the host
actually resolved. They are not the same, so the select and the banner are not driven by the
same value.

- **The select** lists the modes plus one entry that is not a mode: *Workspace default*. It
  renders that entry with the resolved mode in its label (*Workspace default (Plan)*) and is
  the only way back to letting the file decide, because the client has no other word for "I
  have no opinion". Choosing a mode is per-workspace state — it is a habit, so it lives in the
  window's storage rather than in a file.
- **The banner** is driven by the resolved mode, not the chosen one, and always says where it
  came from. A mode that arrived from the file is announced once per session, naming the file
  (`Mode Plan comes from this workspace's .anvil/permissions.json`), and a declaration the host
  refused is announced once, at the volume of a warning, with the sentence the host sent.
- **Once per session** is literal, and it is keyed on the whole resolved picture — mode,
  source, and refusal. A session is re-opened whenever an option changes, and a session can be
  re-opened by a skill or by the model; a notice tied to the open event would repeat itself on
  each one.

The client never reads `.anvil/permissions.json` to find the mode. It sends what it knows and
renders what it is told, so there is one implementation of the resolution and it is the one
that can enforce it.

## Writing a rule must not delete the declaration

`AddPermissionRule` rebuilds the file from the rules it parsed, because the file is rewritten
whole (temporary file, `rename`) and a partial rewrite of JSON is not a thing. That makes the
declaration a field the write path has to carry explicitly: without it, the first click on
"always allow" would remove the line the workspace used to say which mode it starts in, and the
symptom — sessions in this repository stop starting the way they used to — would appear a day
later in someone else's window. `TestAddPermissionRuleKeepsTheDefaultMode` is the guard, and it
is the same class of mistake as the ones chapter 36 collected: a file rewritten from a partial
model of itself.

## Failure modes

| Failure | What happens | Why |
|---|---|---|
| `dont_ask`, no rule covers the call | Refused, naming the mode; the run continues | A refusal is a tool error the model can adapt to, not a dead run |
| `dont_ask`, an `ask` rule matches | Refused, quoting the rule | A rule that wants a person never quietly becomes "run it" |
| `dont_ask`, an `allow` rule matches | Runs, with no prompt | `allow` is decided before the callback |
| `dont_ask`, the session allowlist has an entry | Runs | A person's explicit answer outranks a mode |
| `bypass` + `ask` rule | The card appears, and its answer is honoured | The narrower statement wins |
| `bypass` + `deny` rule | Refused | Policy is decided below the callback (ch36) |
| The file declares `bypass` | Session starts in `ask`, refusal reported | A repository may not grant this |
| The file declares a word that is not a mode | Same, naming the word | Safe answer, said out loud |
| The file declares a malformed value | A problem on the file, with its place | It is a file that will not read |
| The file declares a mode, `load_permissions: false` | Ignored, like its rules | One switch governs the file |

## What this changes for existing callers

- **`session.new` / `session.resume` may now be sent without `permission_mode`** and still get
  a definite mode back. Callers that always send one see no change at all.
- **The session result carries two new optional fields** — `permission_mode_source`,
  `permission_mode_refused` — beside the existing `permission_mode`, which keeps its meaning:
  the mode the session was actually created with, after defaults.
- **`permissions.get` carries two more**, `default_mode` and `default_mode_refused`.
- **A rule file may carry `defaultMode`.** It is one more key in a file whose parser refuses
  unknown keys, so a file that uses it is read by 0.37 and later only. A file that does not use
  it is unchanged on disk and on the wire.
- **In `bypass`, a rule of effect `ask` now raises a prompt.** If a workspace has such a rule
  and a person relies on `bypass` meaning silence, that is the one behaviour change to expect.
- `ProtocolVersion` stays **1**: every addition is a new optional field, and `dont_ask` is a
  new accepted value of an existing one.

## Explicit non-goals

- **A mode that decides by model.** Claude Code's `auto` mode reviews each call with a
  classifier model rather than a rule, falls back to its manual mode after repeated blocks, and
  suspends broad allow rules while it is on. It is the one mode in that ladder that is not a
  deterministic function of the file, so it is the one this design does not copy: a decision
  nobody can point at a line for is a decision nobody can audit. It would also put a second
  model call in front of every gated action, with its own model requirements and its own
  latency. Named here as a decision, not an omission.
- **OS-level enforcement.** A mode selects whether to ask. It is not a sandbox, and
  `dont_ask` is not a confinement boundary: its gates are the same group set `ask` uses, so an
  ungated read runs in either. Containers and filesystem isolation are
  [32](32-sandbox-code-review.md)'s line.
- **Per-tool or per-rule modes.** One mode governs a session, and a rule's effect is a rule's
  effect in every mode.
- **A precedence chain above the workspace file** — user-level defaults, managed settings,
  enterprise policy. The workspace file is the one a reader can find; more layers need their
  own argument.

## Roadmap mapping

- p_0.37.1 — `dont_ask`, and the rule that outranks `bypass`.
- p_0.37.2 — the declaration, the resolution, and the surface that reports it.
