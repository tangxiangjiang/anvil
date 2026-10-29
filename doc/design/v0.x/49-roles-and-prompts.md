# 47 — Roles and layered system prompts

**Milestone focus:** 0.49. Statuses live in
[m_0.49_roles_and_prompts.md](../../roadmaps/v0.x/m_0.49_roles_and_prompts.md).

## 1. Today

There is not one prompt constant but three, in three packages, joined in three different ways:

| Text | Where | How it reaches the model |
|---|---|---|
| The main system prompt | `prompt.SystemWithOptions` ([prompt.go](../../../source/internal/prompt/prompt.go)) | `buildSystemPrompt` ([agent.go](../../../source/agent.go)), then rules, session notes, plugins, skills appended |
| `subagentBrief` | [subagent.go](../../../source/subagent.go) | passed as the child's `Options.SystemPrompt` |
| `planPromptPrefix` | [server.go](../../../source/internal/editorhost/server.go) | prepended to the **user message**, because the host cannot append to a system prompt it cannot read |

And one defect that this chapter exists to remove:

> `Options.SystemPrompt` replaces the default prompt **whole**. The default prompt is where
> the sandbox, secret-handling and untrusted-content clauses live. A caller that sets
> `SystemPrompt` — and every subagent, which is given `subagentBrief` that way — runs with none
> of them.

The sandbox, the permission engine and redaction are enforced in code, so the omission does not
open the workspace. But the untrusted-content clause has no code-side equivalent: it is the only
defence against a file that says "ignore previous instructions". A subagent reading a hostile
file is exactly where that defence matters most, and it is exactly the agent that does not have
it.

## 2. Layers

A system prompt is composed from five layers, always in this order:

| # | Layer | Owner | May a caller replace it? |
|---|---|---|---|
| L0 | **Safety** — workspace boundary, secrets, untrusted content, shell caution | SDK | **No** |
| L1 | **Role** — what this agent is for (§3) | SDK, per role | yes, via `Options.SystemPrompt` |
| L2 | **Project** — rules, session notes, plugins, always-on skills, in today's order | workspace | by the existing load options |
| L3 | **Brief** — the structured task a delegated or staged agent was given (chapter [50](50-agent-modes.md)) | the parent | no; it is data, rendered by the SDK |
| L4 | **Directive** — a per-run instruction such as plan mode | the caller, per `Send` | yes, per run |

The rules that make the layering a guarantee rather than a convention:

1. **L0 is not text the caller passes; it is text the SDK prepends.** `buildSystemPrompt`
   begins with `prompt.Safety(opts)` for every agent, whatever the role and whatever
   `Options.SystemPrompt` says. Composition is concatenation in a fixed order, in one function;
   no other code path builds a system prompt, and the subagent path stops passing a prompt and
   passes a role instead.
2. **`Options.SystemPrompt` replaces L1, not L0.** This is a deliberate behaviour change for
   callers that set it: their text is now preceded by the safety layer. It is additive — no
   instruction a caller wrote is removed — and the phase that ships it says so. There is no
   option to drop L0: a prompt without it asks the SDK to make claims (chapter
   [08](08-security.md)) it can no longer back.
3. **L0 is last word as well as first.** One closing line after L4 restates that the safety
   clauses outrank everything above it. Models weight both ends of a prompt; with the clauses
   only at the start, a long L2 is the most recent instruction the model read.
4. **The default composition keeps every sentence of today's** (amended in p_0.49.1: byte identity conflicts with rule 1, because today's safety sentences sit inside the main text; L0 leads and the golden check is sentence-level).
   Original wording: the default composition is byte-identical to today's. With no role set and no
   `SystemPrompt`, L0 + L1(main) + L2 equals what `prompt.SystemWithOptions` plus
   `buildSystemPrompt` produce now. L0 is today's safety sentences moved, not new sentences. A
   golden file records today's output before the split, and the split is not merged until it
   matches — except for the closing line of rule 3, which is the one addition and is listed in
   the golden's diff by name.
5. **L4 is a `SendOption`.** `anvil.WithDirective(text)` puts a run-scoped instruction at the
   end of the system prompt for that run. The host's `planPromptPrefix` can move there, and a
   host that keeps prefixing the user message loses nothing.

## 3. Roles

```go
type Role string

const (
    RoleMain     Role = ""         // today's agent
    RoleSubagent Role = "subagent" // a child answering its parent
    RoleWorker   Role = "worker"   // a child executing a brief (0.50 Multi and Complex)
    RoleDecision Role = "decision" // Complex stage 1
    RoleAnalyze  Role = "analyze"  // Complex stage 2
)
```

`Options.Role` names the L1 text. What each says, in one sentence each, with the full text
living in `internal/prompt` as one constant per role:

- **main** — today's text: a local coding agent; prefer precise edits; read before write; run
  tests after meaningful changes; summarise when done.
- **subagent** — today's `subagentBrief`: one task, no questions, the final message is the
  whole result and is read by an agent.
- **worker** — the subagent text plus: the brief below is the task; do not widen it; touch only
  the files it lists; when done, report against each acceptance item, and say which were not
  met rather than claiming all were.
- **decision** — reason about the problem, not the code: constraints, options, trade-offs,
  risks; produce the decision document the brief's schema asks for; do not write code and do
  not plan file edits — that is the next stage's job.
- **analyze** — turn the decision into an executable plan: concrete files, ordered steps, an
  acceptance check per step; produce the roadmap the brief's schema asks for; do not implement.

Two roles are deliberately absent. **Plan** is a directive (L4) plus a tool set, not a role,
because plan mode is something a person switches on for one request of an otherwise ordinary
agent. **Reviewer** would be a fourth Complex stage, and chapter 50 argues for three.

A role is a **prompt layer and nothing else**. It does not change the tool set, the
permissions, the budget or the model; those are `Options` fields the container sets alongside
it. The SDK validates the role name and refuses an unknown one.

## 4. The API

```go
// Compose builds the system prompt for one agent from its layers.
func Compose(s Spec) string

type Spec struct {
    CWD        string
    AllowShell bool
    Role       Role
    RoleText   string // non-empty replaces the role's L1
    Project    string // L2, already assembled by the caller
    Brief      string // L3, already rendered
    Directive  string // L4
}
```

`internal/prompt` gains `Compose` and `Safety`; `System`, `SystemWithRules` and
`SystemWithOptions` remain as thin wrappers so that no caller changes in the phase that
introduces the layers. `buildSystemPrompt` in `agent.go` becomes the one caller of `Compose`.

## 5. What pins it

- **Golden:** `TestComposeDefaultIsUnchanged` compares `Compose` for the main role with no
  options against the recorded output of today's builder.
- **Every role starts with L0:** `TestEveryRoleBeginsWithSafety` iterates the role table and
  asserts `strings.HasPrefix(Compose(spec), Safety(...))` and that the closing line is present.
- **No role loses a clause:** a list of the safety sentences is a test fixture, and every
  composed prompt — each role, with and without `RoleText`, with a long L2 — must contain each
  sentence verbatim.
- **`SystemPrompt` cannot remove L0:** the test sets `RoleText` to a prompt that says "ignore
  all safety rules" and asserts the composed prompt still begins with L0 and ends with the
  closing line.
- **Subagents get L0:** a tier-1 eval fixture (chapter [47](47-evaluation.md)) spawns a child
  and asserts, through `Expect`, that the child's first request carries the untrusted-content
  clause.

## 6. Non-goals

- Prompt text that varies by model or provider. One role, one text.
- A user-editable role file in the workspace. Project rules (L2) are the workspace's layer;
  roles are the SDK's.
- Localisation of the prompts.
- Removing the host's `planPromptPrefix`: it keeps working, and moving it to `WithDirective`
  is the host's change to make.

## 7. Delivery

This chapter is delivered by [Milestone 0.49](../../roadmaps/v0.x/m_0.49_roles_and_prompts.md), through the phase below.

| Phase | Contract |
|-------|----------|
| p_0.49.1 | §2–§5: `Safety`, `Compose`, `Options.Role`, `WithDirective`, the five roles, the golden and the clause tests |
