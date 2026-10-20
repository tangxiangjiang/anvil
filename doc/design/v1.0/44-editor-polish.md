# 44 — Editor polish: wiring, review, reading, and boundaries

**Milestone focus:** 0.47. Statuses live in
[m_0.47_editor_polish.md](../../roadmaps/v1.0/m_0.47_editor_polish.md).

## 1. What this chapter is

Product 0.44 closed the capability line that began at 0.35: the editor host, permission
modes, subagents, context management and MCP are all delivered and observable. What remains
between the product and a client that people can use all day is not another engine. It is:

- **wiring**: capabilities the SDK has designed and built, which the editor host never turned
  on;
- **interaction**: data the client already receives, which has no affordance;
- **boundaries**: places where the product says more than it does, or less than it should.

0.45 and 0.46 are not planned in this repository. This chapter deliberately leaves the gap
in the numbering and starts at 0.47, as requested; the milestone page says the same thing so
a reader does not have to guess whether a file is missing.

The design rule for this milestone is a filter, not a theme. A candidate is in scope only if
it meets one of:

1. the SDK or the host protocol already carries it (wiring);
2. the client already holds the data (interaction);
3. the product is currently inconsistent or overstates what it does (correctness).

Anything that needs a new external dependency, a new deployment shape, or a new product
boundary is out, even when it is on a parity list. §13 names the deferred candidates.

## 2. Language intelligence is designed; it is not wired

Chapter [16](16-lsp.md) designed `anvil.LSPOptions` and `anvil.LSPServerConfig`:
a language server is a stdio command mapped from a language or a glob, the SDK registers
`lsp_*` tools, and `Required` decides whether a failed start aborts `Create`.

The SDK has had this since 0.16. The editor host has never set `opts.LSP` — `grep -n LSP
source/internal/editorhost/*.go` finds nothing — so no VS Code session has ever had a
language server, and no setting in the extension could have changed that.

0.47 adds the missing path:

- `session.new` gains `lsp: {enabled, servers: [{language, pattern, command, args}]}`.
- The host maps it to `LSPOptions`, with `Required` always `false`: a language server that
  cannot start is a missing tool, not a broken conversation. (A client that one day wants
  fail-fast can add a field; the default must not be fail-fast.)
- The extension exposes `anvil.lsp.enabled` and `anvil.lsp.servers`.

**Trust and commands.** The extension already refuses untrusted and virtual workspaces. A
workspace's settings therefore arrive from a workspace the person has trusted, which is the
same bar the SDK's own `opts.LSP` callers already meet. Commands still travel as
`command: string` plus `args: []string` and are never joined into a shell string, so a path
with a space is an argument rather than two.

## 3. Test and build are already in the protocol

`SessionParams` has accepted `test_command` and `build_command` since 0.18, and
`server.go` maps them to `opts.Test` and `opts.Build`. The extension has never sent either,
so `run_tests` and `build` have never been available in VS Code.

This milestone adds `anvil.testCommand` and `anvil.buildCommand` (arrays), sends them with
`session.new`, and stops there. An empty array means "not configured"; the tools are absent,
which is exactly today's behaviour.

## 4. Rewind is a run, and the UI should say so

The SDK captures a checkpoint **before each run** (`CheckpointRun` in
[checkpoints.go](../../../source/checkpoints.go)) and `session.fork` copies a conversation
**up to a finished run**. The client already exposes both, but only as two buttons on a run.

A run is one turn, and one turn starts from one user message. So "rewind to this message" is
not a new granularity; it is a composition:

| Action | What it does | What it does not |
|---|---|---|
| Fork from here | `session.fork` at the **previous** run | touch any file |
| Restore files | `checkpoint.restore` for **this** message's run | change the conversation |
| Both | fork first, then restore in the new session | merge the two undo stacks |

The UI offers the three deliberately, labels the granularity ("this turn"), and decides
availability by asking `checkpoint.list` rather than assuming a checkpoint exists — the
same rule the current restore button already follows. A first message has no previous run;
the fork option then means "start a new conversation" and says so.

No tool-call-level rewind is offered. A checkpoint records the workspace before the turn,
not before each tool call; presenting per-tool undo would be a lie the code cannot back.

## 5. Plan review is a document, not a method

Plan mode today is two things in the host: the write-capable tools are withheld, and a
paragraph is prepended to the user's message. The plan itself is the model's prose in the
stream. There is no "plan finished" event and no approval method, so a person approves by
switching modes by hand and typing.

0.47 gives the prose a surface without inventing a protocol object:

- When a plan-mode turn ends, the client renders the assistant's text into a read-only
  virtual document (`anvil-plan:` scheme, `TextDocumentContentProvider`).
- **Approve** switches the session back to the mode it had before plan mode
  (`applyOptions`) and sends one fixed instruction to begin. **Reject** stays in plan mode
  and returns focus to the composer.
- The document cannot trigger execution; it is text plus client-side commands.

The host is unchanged. A structured plan (steps, files, acceptance) would need a new turn
contract and is deliberately not attempted here; the plan is prose because the model produces
prose.

## 6. Reading: focus view, formulas, export

Three client-only changes, all of which leave the protocol alone:

- **Focus view** filters the transcript to narrative: tool cards and thinking blocks
  collapse, the answer stays. The state lives in the webview (`vscode.getState()`), so it
  survives a reload and never enters settings or the store.
- **Formulas** render `$…$` and `$$…$$` with a locally bundled renderer (KaTeX or an
  equivalent). The page's CSP stays `default-src 'none'`; nothing is fetched. Unclosed `$`
  stays literal, and the renderer builds DOM rather than assigning `innerHTML` to host text.
  If the bundle size becomes unacceptable, the fallback is to show the source in a monospace
  block, not to load a script from a CDN.
- **`/export`** writes the current transcript as Markdown through `showSaveDialog`. The
  export carries the conversation and a summary of tool calls, not raw tool output, internal
  ids, or anything from the host's environment.

## 7. Accessibility is a contract, not a pass

The webview is the product's only surface, so "the screen reader can follow a turn" is a
functional requirement, not decoration. The milestone fixes:

- live regions: `polite` for "answer finished", `assertive` only for approval requests and
  errors, with the streaming updates throttled so a screen reader is not asked to read every
  token;
- roles and labels: the transcript is `role="log"`, every icon-only control has an
  `aria-label`, approval controls are a named group;
- keyboard: the composer, send, and toolbar are reachable in that order; `Esc` closes panels
  and dialogs; the slash menu is operable with arrows and Enter;
- visibility: focus rings are checked against the dark and high-contrast themes.

The first task of the phase is an inventory (`grep` for `aria-`, `role=`, and keyboard
handlers), because the honest starting point is not known yet.

## 8. Rules get a second scope, and protected paths

Rules are read from `{cwd}/.anvil/permissions.json` and from `opts.PermissionRules`; there is
no user-level file, so a preference like "never run `rm -rf`" has to be copied into every
repository. 0.47 adds `~/.anvil/permissions.json`.

Merging follows severity, not file order: **deny > ask > allow**. A workspace rule cannot
weaken a user-level deny — otherwise cloning a repository could re-enable something the person
turned off globally. Reports keep provenance (`user permissions.json rule 2` versus
`.anvil/permissions.json rule 1`) so the manager can show where a decision came from.

**Protected paths** are paths that an `allow` rule never matches, regardless of how specific
it is: the repository's `.git/`, Anvil's own `~/.anvil/`, and the workspace's
`.anvil/checkpoints/`. Writes there still follow the mode (ask, accept-edits, bypass); what
changes is that a remembered "always allow" cannot silently include them. In `bypass` and
`dontAsk`, the approval record marks the path as protected so the decision is visible after
the fact.

The permissions manager shows the two layers separately. Editing the user file is allowed
only when it exists or when the person asks to create it; a malformed file remains read-only,
the same contract as 0.41.

## 9. One root per window

Seven places in the extension resolve the workspace as `workspaceFolders?.[0]`
(`hub.ts`, `hostManager.ts`, `editorContext.ts`, `changes.ts`, `chatPanel.ts`). In a
multi-root window that silently binds a conversation, its checkpoints, and its MCP file to
the first folder, with no indication that the other folders are not part of it.

0.47 replaces them with one resolver and makes the root explicit:

- one root per window (a conversation, its host process and its files share one `cwd`);
- on a new conversation in a multi-root window, a picker chooses the root; the choice is
  remembered per window;
- the conversation header shows the root, and a started conversation cannot switch it.

Multiple hosts per window is a different design (process per root, per-root panels) and is
out of scope; the honest limitation is stated in the README.

## 10. Version alignment

The product line is 0.44, the VSIX is 0.41.2, and `anvil-host` reports 0.41.0-dev. The last
phase aligns them, updates the cutoffs (`doc/design/README.md`, `doc/roadmaps/README.md`,
the two v1.0 indexes) to 0.47, and adds "superseded by 0.47" notes to sentences this chapter
overturns — history is annotated, never rewritten.

## 11. Protocol changes

Additive only:

| Method | Field | Meaning |
|---|---|---|
| `session.new` | `lsp` | `{enabled, servers[]}`; absent means off |
| `session.new` | `test_command`, `build_command` | already accepted; now sent |
| `permissions.get` | `user_path`, `rules[].source` | where a rule came from |
| — | none | rewind, plan review, focus view, export and accessibility are client-only |

No method is removed, no field changes type, and an older host ignores what it does not know,
which is how the extension already degrades (`runtime`, `permission_mode_source`).

## 12. Tests

- **Go**: `session.new` with `lsp` maps to `opts.LSP` and an unstartable server does not fail
  the session; `test_command`/`build_command` register their tools; permission merging is a
  table test (user deny vs workspace allow, ask vs allow, provenance); protected paths are
  never matched by an allow rule.
- **Extension unit**: the wire params for `lsp`, test and build are pinned exactly; the root
  resolver replaces all seven first-folder uses; rewind sends fork then restore in that order;
  the plan document is read-only; the webview has live regions and no `innerHTML`.
- **Extension integration** (`fake-host.mjs`): a plan turn produces a plan event; rewind over
  a run with and without a checkpoint; multi-root selection.
- **Manual**: a real language server, a real test command, a screen reader pass, and one
  multi-root window. Each phase's notes say what was walked and what was not.

## 13. Non-goals

- `/btw` (an ephemeral prompt that does not enter the store) — it needs a new host mechanism,
  not wiring.
- Browser automation (Playwright/CDP), scheduled or looping runs, git worktrees, notebooks.
- A plugin marketplace, organization policy distribution, or a configuration service.
- Remote-SSH / container deployment of `anvil-host`, and the loopback OAuth callback problem
  that comes with it.
- Structured plans (steps, files, acceptance) and tool-call-level rewind.
- Multiple roots per window, cross-root checkpoints or search.
- Internationalization and RTL.
- Any capability that changes the SDK's public API shape; this milestone is a client wiring
  line.

## 14. Delivery

| Phase | Deliverable |
|---|---|
| [p_0.47.1](../../roadmaps/v1.0/m_0.47/p_0.47.1.md) | LSP and test/build wiring end to end |
| [p_0.47.2](../../roadmaps/v1.0/m_0.47/p_0.47.2.md) | "Rewind to here", fork and restore as separate actions |
| [p_0.47.3](../../roadmaps/v1.0/m_0.47/p_0.47.3.md) | Plan as a read-only document with approve/reject |
| [p_0.47.4](../../roadmaps/v1.0/m_0.47/p_0.47.4.md) | Focus view, formulas, `/export` |
| [p_0.47.5](../../roadmaps/v1.0/m_0.47/p_0.47.5.md) | Accessibility: live regions, roles, keyboard |
| [p_0.47.6](../../roadmaps/v1.0/m_0.47/p_0.47.6.md) | User-scope rules and protected paths |
| [p_0.47.7](../../roadmaps/v1.0/m_0.47/p_0.47.7.md) | One root per window, explicit in multi-root |
| [p_0.47.8](../../roadmaps/v1.0/m_0.47/p_0.47.8.md) | Version alignment and documentation |
