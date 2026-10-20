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
source/internal/editorhost/*.go` finds nothing — and the extension has no setting. A
workspace that writes `.anvil/lsp.json` does get language tools today, because the SDK reads
that file whenever the session loads the project setting source; what has never existed is
the user's way to declare a server once, in settings, and the wire path that carries it. The
gap is narrower than "LSP is absent", and the milestone page says so.

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
| Both | fork first, then put this turn's files back | merge the two undo stacks |

The UI offers the three deliberately, labels the granularity ("this turn"), and decides
availability by asking `checkpoint.list` rather than assuming a checkpoint exists — the
same rule the current restore button already follows. A first message has no previous run;
the fork option then means "start a new conversation" (what `/clear` does) and says so.

**Where the combined restore is asked from.** A fork copies stored conversation records; it
does not copy checkpoint directories, which live under the agent id
(`<cwd>/.anvil/checkpoints/<agent>/<run>`). The restore in the combined action is therefore
asked of the conversation that owns the checkpoint, not of the new one, and "in the new
session" above means "on the same workspace root the new session opens in", not "through the
fork's id". The files land where they were; the new tab opens on the history before the
turn.

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

**What "the assistant's text" means.** There is no single message to quote: a plan-mode turn
may speak, call a read-only tool, and speak again. The client keeps the turn's own `assistant`
events — a child agent's steps arrive with a `parent_tool_call_id` and are not the plan —
splits them into paragraphs at each tool call, and joins them with blank lines. A card under
the turn then offers *Open plan*, *Approve and run* and *Keep planning*, and the same three
choices are palette commands, so a plan can still be acted on when the document is the active
editor. The plan is held together with the conversation that produced it: a tab can move to
another agent, and approving a plan into a conversation it did not come from would carry out a
decision about somewhere else.

The host is unchanged. A structured plan (steps, files, acceptance) would need a new turn
contract and is deliberately not attempted here; the plan is prose because the model produces
prose.

## 6. Reading: focus view, formulas, export

Three client-only changes, all of which leave the protocol alone:

- **Focus view** filters the transcript to narrative: tool cards and thinking blocks
  collapse, the answer stays. It is also the view an approval card must survive: the card is
  a question to the reader, and hiding it would leave a turn waiting behind a wall the reader
  cannot see through. The state lives in the webview (`vscode.getState()`) and never enters
  settings or the store. The document owns the flag — the toolbar button, `Ctrl+Alt+F` and
  the palette command all ask the document to flip it, and nothing else writes it — while
  the host only re-seeds the page from the `focus` the serializer saved when VS Code restores
  the tab. The saved object is merged rather than replaced, because the agent id and the view
  are written at different moments.
- **Formulas** render `$…$` and `$$…$$` with KaTeX 0.19.0, vendored into
  `media/vendor/katex/` by `npm run vendor:katex`: one `katex.min.js` of about 273 KB, no
  stylesheet, no font file, no network request. It is used in `output: 'mathml'` mode. The
  HTML output writes inline `style` attributes for its layout, which the page's CSP blocks;
  the ways past that are `'unsafe-inline'` or serving a stylesheet, and both are worse than
  MathML, which writes no styles and needs no CSS. The renderer builds elements through
  `textContent`, `trust: false` keeps `\href` and `\htmlClass` from naming a link or a class
  the model chose, and a formula it refuses is drawn as its source in monospace. `$5 and $6`
  stays money — no space before the closer, no digit after it — and an unclosed delimiter
  stays the characters it is while the model is still typing.
  If the bundle size becomes unacceptable, the fallback is still to show the source in a
  monospace block, not to load a script from a CDN.
- **`/export`** writes the current transcript as Markdown through `showSaveDialog`, defaulting
  to `anvil-conversation-<date>.md` in the workspace folder. The export carries the
  conversation and a summary of tool calls — the name, and for `write_file`, `edit_file` and
  `apply_patch`, the paths the call named — not raw tool output, not arguments, not internal
  ids, not anything from the host's environment. The store is the source rather than the
  screen, and a transcript the store truncated says how many records came before it.

## 7. Accessibility is a contract, not a pass

The webview is the product's only surface, so "the screen reader can follow a turn" is a
functional requirement, not decoration. The milestone fixes:

- live regions: `polite` for "answer finished", `assertive` only for approval requests and
  errors, with the streaming updates throttled so a screen reader is not asked to read every
  token. The transcript keeps its `log` role but is not itself live: the announced text is a
  finished block, and the log is what the reader browses afterwards;
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
repository. 0.47 adds `$ANVIL_HOME/permissions.json`, or `~/.anvil/permissions.json` when the
variable is unset — the same `.anvil` directory the MCP token store uses, and the override the
tests need so that `go test` never reads the developer's machine-wide policy. The two files do
not share a shape: the user file may carry `protected`, the workspace file keeps `defaultMode`,
and each parser refuses the other's key, so a repository cannot widen what a person must be
asked about and a machine-wide file cannot pick the mode of every session.

Merging follows severity, not file order: **deny > ask > allow**. A workspace rule cannot
weaken a user-level deny — otherwise cloning a repository could re-enable something the person
turned off globally. Reports keep provenance (`user permissions.json rule 2` versus
`.anvil/permissions.json rule 1`) so the manager can show where a decision came from.

**Protected paths** are paths that an `allow` rule never matches, regardless of how specific
it is: the repository's `.git/`, Anvil's own `~/.anvil/`, and the workspace's
`.anvil/checkpoints/`. Writes there still follow the mode (ask, accept-edits, bypass); what
changes is that a remembered "always allow" cannot silently include them. The decision always
carries the paths — on the approval card and on the tool result — so in `bypass` and `dontAsk`
the record says after the fact why no allow could have covered the call. The user file may add
globs of its own; they are anchored exactly as a rule's path pattern is.

The permissions manager shows the two layers separately. Editing the user file is allowed
only when it exists or when the person asks to create it; a malformed file remains read-only,
the same contract as 0.41.

## 9. One root per window

Seven places in the extension resolve the workspace as `workspaceFolders?.[0]`
(`hub.ts`, `hostManager.ts`, `editorContext.ts`, `changes.ts`, `chatPanel.ts`). In a
multi-root window that silently binds a conversation, its checkpoints, and its MCP file to
the first folder, with no indication that the other folders are not part of it.

0.47 replaces them with one resolver (`src/workspaceRoot.ts`) and makes the root explicit:

- one root per window (a conversation, its host process and its files share one `cwd`);
- on the first conversation in a multi-root window, a picker chooses the root; dismissing it
  creates nothing — no host process, no session. The choice is stored in `workspaceState`, so
  it is remembered for the workspace: a reloaded window reuses it rather than asking the same
  question again, and the header says which folder it is;
- the conversation header shows the root, and a started conversation cannot switch it.

No other module reads the folder list. A call site that needs the root asks the resolver for
the folder or its URI, which is what makes "one root per window" a property of the code rather
than a convention.

Multiple hosts per window is a different design (process per root, per-root panels) and is
out of scope; the honest limitation is stated in the README.

## 10. Version alignment

When this chapter was written the product line was 0.44, the VSIX reported 0.41.2, and
`anvil-host` reported 0.41.0-dev — three numbers for one product, and the two the extension
compares were released by different milestones. The last phase raised the VSIX and the host's
`initialize` version to 0.47.0, updated the cutoffs (`doc/design/README.md`,
`doc/roadmaps/README.md`, the two v1.0 indexes) to 0.47, and added "superseded by 0.47" notes
to sentences this chapter overturns — history is annotated, never rewritten.

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
| [p_0.47.9](../../roadmaps/v1.0/m_0.47/p_0.47.9.md) | Acceptance fixes: vision on by default, a refused turn visible, the plan block closable |
| [p_0.47.10](../../roadmaps/v1.0/m_0.47/p_0.47.10.md) | The transcript's visual grammar: rows instead of cards, calmer type, a side-by-side edit diff |
| [p_0.47.11](../../roadmaps/v1.0/m_0.47/p_0.47.11.md) | One visual grammar for all five webviews: shared tokens and panel chrome |
| [p_0.47.12](../../roadmaps/v1.0/m_0.47/p_0.47.12.md) | Dot markers, more air, a configurable chat text size |

0.47.9–0.47.12 were written after the milestone was accepted. Their findings came from the
first real use of the panel, not from a test: a capability that existed and was never turned
on, an error that only reached the log, a block with no way out — and then a transcript that
was tiring to read, followed by four manager panels that each declared their own copy of the
same document chrome, and finally a row marker, a spacing and a text size that the reader
named directly. The lesson is recorded here rather than in a commit message — the manual
passes the earlier phases wrote down as "still worth doing" are the ones that would have found
all of these.

## 15. The visual grammar after use

The first phase of the polish line designed each surface on its own; real use showed that the
surfaces did not read as one product. §15 records the grammar p_0.47.10, p_0.47.11 and
p_0.47.12 settle on, so a later change to one panel has something to agree with.

- **A tool call is a row.** The bordered card with a per-state left edge made a run of work
  read as a wall of frames, and the green of a succeeded call made the normal case look like a
  notification. A call is now one line — dot, name, hint, state — that opens onto inset
  output behind a single hairline. Thinking uses the same row so reasoning is not a different
  species. Failure keeps a colour; success and "stopped" do not.
- **The dot carries the row's state.** The caret the first pass kept said "there is more to
  read" before every call, which is true of a row whose body is usually empty. p_0.47.12
  replaced it with a 6px round dot: filled while a call runs, muted when it is done, in the
  error colour when it failed, a ring when the result never arrived, and a ring while the row
  is open. The shape is the same in the Thinking row, so the two grammars start on one line.
- **Spacing is a rhythm, not a per-rule accident.** Rows pad vertically, the pane under a row
  indents to the name rather than to the dot, and the gap between transcript items comes from
  `--ui-gap`, which p_0.47.12 raised for every webview. A row that merely fits is a row that
  is tiring to scan.
- **The constants live in one file.** `media/tokens.css` declares the gap, radius, two line
  heights, the mono family, the hairline and the muted colour as `--ui-*`. The chat stylesheet
  keeps its older `--anvil-*` names as aliases because its tests read them, and no other
  stylesheet may redeclare a `--ui-*` value.
- **The four manager panels share their chrome.** Permissions, Tasks, MCP and Review each used
  to declare `body`, `#root`, the title, the path line, the banner, inputs and buttons. That
  document shell is now `media/panel.css`; each panel styles only its own sections. Sections
  are separated by hairlines and hover, not by nested frames, and a finished status is muted —
  only running and failed carry colour.
- **An edit is compared old beside new.** Inside an `edit_file` call the short comparison is
  two columns with the editor's own inserted/removed line backgrounds, each column scrolling
  its own long lines, under a path line that carries the added and removed counts. The fallback
  is a body class (`diff-narrow`) toggled from one throttled resize listener measuring the
  transcript's content width, because `chat.css` is parsed by splitting on `}` in its test and
  may not contain an at-rule; the markup does not change when the shape does. The width at
  which the fallback fires grows with the reader's text size, because two columns stop paying
  for themselves sooner at 20px than at 15.
- **The reader sizes the text.** `anvil.chatFontSize` (default 15, range 10–24) is the chat
  document's base size, applied through `--anvil-font-size`. It is display-only: the
  configuration listener compares readings with the size removed and posts the new value to
  every open tab, so a size change never restarts the host or interrupts a run.
- **Approval previews stay unified.** The surface that decides what is applied keeps the
  host's unified diff. Only the call's own short comparison — read, not decided — is paired.
- **High contrast gets a non-colour cue.** A tinted line background is not guaranteed there,
  so added and removed rows also carry a 2px left edge, and the quiet rows switch to the
  normal foreground. The focus rings the accessibility phase pinned are untouched.
