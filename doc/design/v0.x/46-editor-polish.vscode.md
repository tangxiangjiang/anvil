# 44 — Editor polish: wiring, review, reading, and boundaries

**Milestone focus:** 0.46. Statuses live in
[m_0.46_editor_polish.vscode.md](../../roadmaps/v0.x/m_0.46_editor_polish.vscode.md).

## 1. What this chapter is

Product 0.44 closed the capability line that began at 0.35: the editor host, permission
modes, subagents, context management and MCP are all delivered and observable. What remains
between the product and a client that people can use all day is not another engine. It is:

- **wiring**: capabilities the SDK has designed and built, which the editor host never turned
  on;
- **interaction**: data the client already receives, which has no affordance;
- **boundaries**: places where the product says more than it does, or less than it should.

This is the VS Code line, numbered 0.46. The number is one of a deliberate gap in the 0.35–0.46 run:
the web and Go back-end work (0.45) and the agent line (0.47–0.50) were numbered around it so that
parallel IDE work would not collide. The milestone page says the same thing.

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

0.46 adds the missing path:

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

0.46 gives the prose a surface without inventing a protocol object:

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
repository. 0.46 adds `$ANVIL_HOME/permissions.json`, or `~/.anvil/permissions.json` when the
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

0.46 replaces them with one resolver (`src/workspaceRoot.ts`) and makes the root explicit:

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
`doc/roadmaps/README.md`, the two v0.x indexes) to 0.46, and added "superseded by 0.46" notes
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
| [p_0.46.1](../../roadmaps/v0.x/m_0.46/p_0.46.1.vscode.md) | LSP and test/build wiring end to end |
| [p_0.46.2](../../roadmaps/v0.x/m_0.46/p_0.46.2.vscode.md) | "Rewind to here", fork and restore as separate actions |
| [p_0.46.3](../../roadmaps/v0.x/m_0.46/p_0.46.3.vscode.md) | Plan as a read-only document with approve/reject |
| [p_0.46.4](../../roadmaps/v0.x/m_0.46/p_0.46.4.vscode.md) | Focus view, formulas, `/export` |
| [p_0.46.5](../../roadmaps/v0.x/m_0.46/p_0.46.5.vscode.md) | Accessibility: live regions, roles, keyboard |
| [p_0.46.6](../../roadmaps/v0.x/m_0.46/p_0.46.6.vscode.md) | User-scope rules and protected paths |
| [p_0.46.7](../../roadmaps/v0.x/m_0.46/p_0.46.7.vscode.md) | One root per window, explicit in multi-root |
| [p_0.46.8](../../roadmaps/v0.x/m_0.46/p_0.46.8.vscode.md) | Version alignment and documentation |
| [p_0.46.9](../../roadmaps/v0.x/m_0.46/p_0.46.9.vscode.md) | Acceptance fixes: vision on by default, a refused turn visible, the plan block closable |
| [p_0.46.10](../../roadmaps/v0.x/m_0.46/p_0.46.10.vscode.md) | The transcript's visual grammar: rows instead of cards, calmer type, a side-by-side edit diff |
| [p_0.46.11](../../roadmaps/v0.x/m_0.46/p_0.46.11.vscode.md) | One visual grammar for all five webviews: shared tokens and panel chrome |
| [p_0.46.12](../../roadmaps/v0.x/m_0.46/p_0.46.12.vscode.md) | Dot markers, more air, a configurable chat text size |
| [p_0.46.13](../../roadmaps/v0.x/m_0.46/p_0.46.13.vscode.md) | A step rail, coloured operations, human tool titles and file links |
| [p_0.46.14](../../roadmaps/v0.x/m_0.46/p_0.46.14.vscode.md) | One rail per reply, a folded Thought, tool output in a read-only tab |
| [p_0.46.15](../../roadmaps/v0.x/m_0.46/p_0.46.15.vscode.md) | A replay window that starts at a turn, and a result that draws its own row |
| [p_0.46.16](../../roadmaps/v0.x/m_0.46/p_0.46.16.vscode.md) | Moderate density, coloured state words, larger dots, a framed pane, a summary ending |
| [p_0.46.17](../../roadmaps/v0.x/m_0.46/p_0.46.17.vscode.md) | The summary endings marked by the live path as well as the replay |
| [p_0.46.18](../../roadmaps/v0.x/m_0.46/p_0.46.18.vscode.md) | A reply's start marker, one fold per turn, history closed and the newest open |
| [p_0.46.19](../../roadmaps/v0.x/m_0.46/p_0.46.19.vscode.md) | One scrollbar per side of an edit diff, revealed on hover and moving both sides together |
| [p_0.46.20](../../roadmaps/v0.x/m_0.46/p_0.46.20.vscode.md) | An animated anvil-and-hammer working line on the status strip |
| [p_0.46.21](../../roadmaps/v0.x/m_0.46/p_0.46.21.vscode.md) | An 18px orange-hammered mark and a word that turns through the forge |
| [p_0.46.22](../../roadmaps/v0.x/m_0.46/p_0.46.22.vscode.md) | An anvil and hammer drawn to fill the view box, with pixel-wide spark strokes |
| [p_0.46.23](../../roadmaps/v0.x/m_0.46/p_0.46.23.vscode.md) | The working line moved below the root and meter, which stay together |
| [p_0.46.24](../../roadmaps/v0.x/m_0.46/p_0.46.24.vscode.md) | The root and run lines moved into the transcript as a footprint; the session total into the composer |
| [p_0.46.25](../../roadmaps/v0.x/m_0.46/p_0.46.25.vscode.md) | A status line of one height, with a truncation box per half, and buttons that cannot shrink |
| [p_0.46.26](../../roadmaps/v0.x/m_0.46/p_0.46.26.vscode.md) | Session counts that land and pulse, a first turn that says “not yet”, and a rail that stops at its own reply |

0.47.9–0.47.26 were written after the milestone was accepted. Their findings came from the
first real use of the panel, not from a test: a capability that existed and was never turned
on, an error that only reached the log, a block with no way out — and then a transcript that
was tiring to read, followed by four manager panels that each declared their own copy of the
same document chrome, and finally a row marker, a spacing and a text size that the reader
named directly, and then the rail, operation colour and human names that the same reader
recognised from another editor, and then the reply-wide rail, the folded Thought and the
output tab that the same reader asked of the first rail, and then a stored conversation that
opened on a result whose call the window had cut, and finally a long conversation that read as
too sparse, whose state words said nothing, whose dots sat under the line, whose opened call
had no edge, and whose final answer had no ending — and then that ending missing from the live
turn because the live path asked for a failure that is never absent, then a turn with no
beginning and no way to close the ones already read, then an edit comparison that drew a
scrollbar under every line it contained, then a panel that said nothing at all while a turn
was in flight, then a working mark too quiet to notice and too still to read, then that mark
drawn into three quarters of its box with a spark under a pixel wide, and finally a working
line inserted between the two facts about the session it did not belong to, and finally those
lines floating over the conversation, where a measured reserve could not stop the transcript
from being painted across them. The lesson is recorded here rather
than in a commit message — the manual passes the earlier phases wrote down as "still worth
doing" are the ones that would have found all of these.

## 15. The visual grammar after use

The first phase of the polish line designed each surface on its own; real use showed that the
surfaces did not read as one product. §15 records the grammar p_0.46.10, p_0.46.11 and
p_0.46.12 settle on, so a later change to one panel has something to agree with.

- **A tool call is a row.** The bordered card with a per-state left edge made a run of work
  read as a wall of frames, and the green of a succeeded call made the normal case look like a
  notification. A call is now one line — dot, name, hint, state — that opens onto inset
  output behind a single hairline. Thinking uses the same row so reasoning is not a different
  species. Failure keeps a colour; success and "stopped" do not.
- **The dot carries the row's state.** The caret the first pass kept said "there is more to
  read" before every call, which is true of a row whose body is usually empty. p_0.46.12
  replaced it with a 6px round dot: filled while a call runs, muted when it is done, in the
  error colour when it failed, a ring when the result never arrived, and a ring while the row
  is open. The shape is the same in the Thinking row, so the two grammars start on one line.
- **Spacing is a rhythm, not a per-rule accident.** Rows pad vertically, the pane under a row
  indents to the name rather than to the dot, and the gap between transcript items comes from
  `--ui-gap`, which p_0.46.12 raised for every webview. A row that merely fits is a row that
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

## 16. The rail, the operation, and the file link

p_0.46.13 answers one more reading of the same transcript. §15 made a step a row; §16 makes a
run of steps a sequence, and makes the row say what the step was.

- **A rail belongs to a run.** A tool row and a Thinking row are steps in a turn; a user
  message and an assistant paragraph are not. The stylesheet draws one 1px segment per step
  at the dot's centre, and `syncRail` gives a step `rail-up` / `rail-down` only where another
  step follows it in the same container. A run of one draws nothing, so the line begins and
  ends on a dot; prose flushes the run, so the line never crosses an answer. The segment
  reaches 10px into the gap on a side where the run continues, which meets across the 20px a
  message's margin and the transcript's gap add up to.
- **An operation is not a success state.** The tool dot is green — the editor's
  `testing-iconPassed` with `charts-green` behind it — before, during and after a call.
  Finishing dims it slightly rather than fading it to muted; failure is the error colour and
  the open/stopped ring is green. Only Thinking stays grey, because reasoning is not an
  operation.
- **The row names the act, not the implementation.** The webview keeps a display map from
  the SDK's names (`read_file`, `shell`) to titles (`Read`, `Bash`); an unknown name is
  de-slugged instead of shown raw. The title is the body's size, weight 600, in the
  foreground, and the approval card's heading and announcement use the same map. The host,
  the rules and the wire keep the SDK's names.
- **The argument completes the sentence.** `Read` carries the file and, when the call had
  one, its range (`lines 113–130`, from the 1-based `offset` and `limit` the host accepts);
  `Edit` carries its path; `Bash` its command; `Grep` its pattern. The path is the link
  colour and a link in fact: `openPath` opens the file at the call's first line. A relative
  path is joined to the window's chosen root and an absolute one is accepted only when it is
  inside it, so the webview cannot make the extension open anything the row did not draw.
- **The edit's counts move out of the diff.** The path is in the row, where a reader looks
  first, so the comparison starts with `Added 1 line` / `Removed 2 lines` and keeps its two
  columns, its own line numbers and the narrow fallback. The old path line inside the diff is
  gone rather than duplicated.

## 17. The reply, the thought, and the output

p_0.46.13 read a run of calls as a sequence of adjacent steps. The same reader then asked for
the line to be the reply's first-level boundary instead: one answer, one column, prose
included. §17 records that unit and the two things that hang from it.

- **The rail belongs to the reply.** A run is every consecutive `.tool` and
  `.msg.assistant` child of a transcript container, not just the runs where a paragraph
  happens to be absent. Prose is part of the answer, so the line crosses it; the line stops
  where the conversation changes hands — a user message, a notice, an approval. Every block
  in the run carries its own full-height segment and reaches into the gap only where the run
  continues, so a one-block reply still has a line and a reply with prose is still one line.
  Prose is inset 22px, which is where a row's name starts; a Thinking dot stays at 9px and
  the line passes through it.
- **A thought is a folded row.** The summary is a grey dot, the word, and a chevron that
  turns when the block opens. The reasoning sits under it, at the same inset, with no second
  vertical bar: the rail is the one line, and the body's old hairline was a duplicate at the
  wrong distance. Thinking is collapsed by default and is not announced; opening it is the
  reader's choice.
- **A result is a document when it does not fit a row.** A finished tool row with text shows
  `N line(s) of output` in small grey under the summary. Clicking it opens the full result in
  a read-only tab titled `<Tool> output (<suffix>)`, supplied by a
  `TextDocumentContentProvider` under `anvil-tool-output:` — no file is written and nothing
  can edit it. The suffix is a hash of the tool call id and the URI carries the id itself, so
  a second click reveals the same tab. The row's OUT pane keeps its display cap and its
  "show all" button; the tab is where the rest is read.

## 18. A replay starts at a turn

p_0.46.15 is the first correction to the replay itself rather than to how a block looks. Two
properties of a stored conversation met a reader at once.

- **A window is moved to a turn, not to a count.** The host keeps the newest 200 entries and
  moves forward to the first user turn in that window. A window with no user turn at all — a
  single long run — used to stay where the cap fell, so the transcript opened with an OUT
  whose call was never drawn. The window now moves back to the user turn that opened the run.
  A run longer than the cap is shown whole: the cap bounds a render, and a render that starts
  mid-run is wrong rather than merely large.
- **A result is information, not an error.** The renderer's fallback for a result with no
  card was `addNotice`, which prints the text in the failure colour. A result the model
  already acted on did not fail. The replay now synthesizes the call that the window cut,
  from the one part of it the store keeps — the tool's name — and the live renderer draws
  the same row before settling it. The synthesized call carries no arguments because the
  store has none; an empty IN is what is known. A result with no id at all gets one muted
  line and the usual output caption.

The shape that makes this work is the replay's own: it emits events, never markup, so a
synthesized call and a stored one are drawn by one renderer, and a fix here is a fix for both
paths.

## 19. Density, an ending, and a framed pane

p_0.46.16 is the first phase that changes the transcript's *rhythm* after the rail settled it,
and the first that gives a reply a shape of its own. Five decisions hold it together.

- **Density is bought with margins, not with the gap.** The 12px between transcript children
  separates one step from the next and stays. The air came from 8px gaps and margins stacked
  around every message, and from a caption that sat a full gap below the row it described.
  `.msg` becomes `gap: 4px; margin-block: 4px`, the summaries lose 2px of vertical padding,
  and the caption is pulled up 8px. A message and a row are 16px apart, two messages 20px, a
  row and its caption 4px; the rail's extensions grow to 10px on every block kind, so the
  line still meets itself across both distances.
- **The dot is bigger than its box.** An 8px dot would move the name to 24px and the rail to
  10px, and every inset already written would be wrong. One pixel of negative margin on each
  side makes the outer box 6px again: the centre stays at 9px, the name at 22px, and only the
  drawn size changes. The dot is `position: relative; z-index: 1`, so it paints above the
  block's generated line inside the same stacking context. The line is not split around it —
  that would need a second element and markup that knows where the dot is.
- **A state word is coloured by what happened.** `done` takes the operation green, `stopped`
  the warning colour; success and interruption are not the same grey. A stopped call keeps a
  2px ring, so its state is legible without colour.
- **The frame belongs to a call's own panes.** An opened call's IN and OUT sit in one hairline
  box, and the two sections are divided by a hairline instead of 12px of space; the inner
  `pre` loses the left rule the frame replaces. A spawn's pane holds the child's whole
  conversation, so it opts out: framing it would put a box around a transcript.
- **A finished answer ends in its own summary.** When the last block of a finished run is
  prose, it becomes the turn's summary: an end dot in the ending colour
  (`--vscode-charts-purple`, distinct from the operation green and the failure red), the word
  `Summary`, and the answer's own first line as the title, with the rest of the answer below.
  The title line is removed from the body so it is said once; the Copy action still carries
  the whole answer. The rail stops at the dot — a line beside the body would make the
  conclusion read as another step. A first line that is syntax (a fence, a rule, a table)
  gets no summary at all, and a failed or stopped run keeps the ordinary ending, because its
  last text is not a conclusion.

The summary is built from what the model wrote rather than from the prompt or the session: a
title taken from anywhere else would be a second opinion about the answer. That is also why
the phase adds no protocol field, no host code and no wrapper element — the ending is a
reading of the answer, not a record attached to it.

## 20. A summary for the run that finished

p_0.46.17 is one predicate. The ending p_0.46.16 designed appeared when a stored conversation
was reopened and not when the same turn finished live, because the two renderers ask "did the
run end normally?" in different vocabularies and one of them asked it of the wrong thing.

- **The live path tested the object, not the kind.** `finishRun` marked the summary only
  `if (!failure)`, on the assumption that a clean finish arrives without a failure. It does
  not: `classifyDone` always returns a `RunFailure`, and a clean finish is
  `{kind: 'none', message: '', isError: false}`. The test was never true, so a live turn never
  gained an ending — while the replay, which reads the stored status, gained one as designed.
- **Each path speaks its own grammar.** The live path has a failure kind whose clean value is
  `none`; the replay has a status whose clean word is `finished`. The fix is
  `!failure || failure.kind === 'none'` in `finishRun`; neither path learns the other's
  vocabulary, and the rendering, the colours and the rail stay exactly as p_0.46.16 wrote them.
- **The guard for a missing failure stays.** `finishRun` is fed a message field; a malformed or
  older message that omits it still means the run ended, and a summary is a better answer than
  a `TypeError` thrown in the middle of a turn's ending.

A source-level test now pins the kind test and refuses the object test. The defect was not a
rendering bug and could not have been caught by a stylesheet assertion; it lived exactly where
the two endings agreed on their words — which is also why reading them side by side found it
in a minute and a reader found it in a window.

## 21. A reply has a beginning, and it folds

p_0.46.18 closes the bookend p_0.46.16 opened: a turn now starts with a marker and can be
folded as a unit. It also reverses one of p_0.46.16's non-goals — "a wrapper element per
reply" — for a reason that did not exist then.

- **Folding needs the element.** The rail was built flat on purpose: each block drew its own
  segment, so no wrapper was needed and the replay, the spawn scopes and the focus view
  learned nothing. Folding cannot be done that way: hiding arbitrary siblings is not a CSS
  operation, and a script toggling `display` would be a second, weaker `<details>`. The
  wrapper is the browser's element, so the chevron, the keyboard and the accessibility tree
  are correct by construction. The flat rail is kept inside it — `syncRail` now runs over the
  reply body, whose column repeats the transcript's 12px gap.
- **The route is the turn, not the block type.** A reply that is open takes everything the
  agent adds until a user message closes it. The host sends the checkpoint, changes and fork
  cards after the run ends; routing by block type alone would place those after the
  `<details>` while the turn's next block went inside, and the transcript would read out of
  order. Only the *first* block of a turn decides whether a reply is created, and only the
  blocks that are the agent's own — a round, a tool row, an approval, a result note — can
  begin one. Notices, command cards and session lines stay in the transcript.
- **A user message is the boundary.** Closing the reply before the message is appended is
  what keeps the message outside every turn. A numbered turn could be tracked instead, but
  the message is already the boundary the rail and the rewind use; the fold reads it the same
  way.
- **A reply opens by default, and the next turn closes it.** That single rule produces
  "newest open, history closed" without a replay path of its own: the replay closes each turn
  as its next user event arrives, and the last one stays open. A click pins a reply, so the
  reader who opens an old turn to read it does not lose it to the next message; the pattern
  is Thinking's `thinkPinned`.
- **The start dot is hollow, in the ending's colour.** A filled dot would be a second ending;
  a hollow ring is the shape an open row already uses, and the same purple at the same 9px
  makes the pair read as brackets around the turn. A generated segment from the dot to the
  body makes the rail begin at the marker rather than below it.
- **The folded face is the conclusion.** A folded reply that said only `anvil` would turn a
  saved conversation into a column of identical rows. The header carries the answer's first
  line — the line the end Summary already chose — and a turn whose run did not conclude
  normally keeps an empty hint rather than a borrowed one.
- **The speaker is said once.** The header names the turn; a round inside the reply no
  longer repeats `anvil` above its prose. The Summary's `.who` is the exception: it is the
  ending's own header, not a speaker label.

Two costs are accepted and written down: a closed `<details>` is not searched by find-in-page,
and a spawn card's pane keeps the flat grammar because it already folds and because its rounds
need a name to tell parent from child.

## 22. A scrollbar under every line

p_0.46.10 made an edit's comparison two columns and let each cell scroll its own long line.
The cost only showed on a file whose lines overflow: every line of both sides grew a
horizontal scrollbar, so a twenty-four-row comparison drew up to forty-eight bars inside
itself. p_0.46.19 makes the side the scroll region instead.

- **The pane is the scroller.** One scroll position per side is what makes the overflow
  countable and, more importantly, what makes the two sides synchronizable. The rows keep
  the same classes and backgrounds; only the element that clips them changed.
- **Two panes, not one shared bar.** A single horizontal scroller would have to sit either
  outside the vertical one — putting the vertical bar at the right edge of the widest line,
  off screen — or inside it, putting the horizontal bar under the last row instead of at the
  foot of the box. A pane per side keeps the vertical bar on the visible edge and puts each
  horizontal bar at the foot of its own pane.
- **Equal offsets.** The panes copy `scrollLeft` and `scrollTop` to each other rather than
  scaling by their scroll ranges: the same offset is the same character column on both sides,
  which is the comparison the reader is making. A narrower side simply stops at its own end.
- **Hidden until hover.** The bar is a tool for reaching a long line, not a permanent piece of
  the diff's frame. A transparent track and thumb leave the rows as the only visible thing;
  the theme's `--vscode-scrollbarSlider-*` colours appear when the pointer is over the
  comparison.
- **The narrow shape is a repaint.** The p_0.46.10 fallback kept one DOM and hid cells with
  CSS. A pane is a whole side, so a unified column cannot be carved out of two of them; the
  comparison keeps its pairs and paints either shape from them when the body class flips.
  This is the one place where the diff's two shapes are different DOM.

## 23. A working line that forges

A run in flight had no face of its own: the composer locks, the send button becomes a stop
button, and the status line — the one element that describes the run — was hidden from the
prompt until the run ended. p_0.46.20 gives it a working state, in the shape Claude Code
gives one: a mark, a word, and a dim parenthetical of facts.

- **The mark is the product's.** An anvil and a hammer, drawn as an inline SVG with the
  head pivoting at the anvil's edge and a spark on the strike. A character has no hammer
  that renders the same in every font, and an image would be a file to ship for one glyph.
- **The animation is script, the pivot is stylesheet.** `chat.css` may not carry an
  `@keyframes` — the layout tests parse the file as text — so the head swings through the
  Web Animations API. The stylesheet contributes the two things that are geometry: the pivot
  and `transform-box: view-box`.
- **The line replaces itself, it does not stack.** The working half and the summary share
  `#status`: one describes the run while it lasts, the other after it ends. Building both
  once means the clock and the animation survive every second's re-render; a single
  `textContent` write would restart the swing.
- **The text follows the CLI.** `Forging…` then `(esc to interrupt · 12s · step 2/40)`. The
  escape comes first because it is the part a reader can act on; the step appears only when
  the host reported one; tokens do not appear because a live run's usage arrives with the
  terminal event, and a number that appears only at the end is not progress.
- **Motion is a preference, not a feature.** `prefers-reduced-motion` leaves the hammer
  still and the spark painted out. The line, the word and the clock remain: the information
  is not the animation.

## 24. A mark you can see, and a word that turns

The first working line was correct and quiet: 14px, in the description colour, with one
word for the whole run. Beside 0.85em text it read as another muted fact, and on a long step
it became scenery — the same failure the status line had before p_0.46.20, one layer down.

- **Size before colour.** A mark smaller than the text it labels is decoration; at 18px it is
  a glyph. The view box does not change, so the pivot and the arc need no new numbers.
- **Orange is the one accent the transcript does not spend.** `done` is green and `stopped`
  the warning colour, so an orange hammer cannot be read as a state. The anvil stays in the
  description colour: the swing is the thing to catch, not the whole picture.
- **The verb rides the clock that exists.** A second timer would be a second lifetime to own;
  the renderer keeps one interval per fact. The verb is
  `WORKING_WORDS[floor(seconds / 3) % length]`, derived from the elapsed time the line
  already counts — deterministic, testable, and free.
- **A host word outranks the rotation.** A step text is a fact about the run; the rotation
  exists only for the absence of one. Once the host says what it is doing, it keeps saying it
  until the run ends.
- **Room, not a frame.** The root line and the working line gain a little space above the
  composer. Boxing them would make the dock a card, which the transcript stopped being in
  p_0.46.10.

## 25. A mark that fills its box

An icon's size is not its file's size. The 18px box of p_0.46.21 held a drawing that used
about three quarters of the box across and left the corners empty; its spark was 0.8 view-box
units, which is 0.90px on screen — a stroke the browser paints as a smudge. The mark looked
smaller than its box and its brightest moment was the faintest thing in it.

- **The drawing is the scale.** Enlarging the box again would scale the margins too. The
  anvil and hammer are re-laid inside the same 16-unit view box so their ink spans about 89%
  of both dimensions: a face, waist and base across the bottom, a horn to the left edge, a
  wider handle and a larger head raised at the top right.
- **Strokes are checked in screen pixels.** SVG strokes scale with the box, so "1px" is a
  number in units multiplied by a ratio the stylesheet chooses. The spark's 1.4 units are
  about 1.6px at 18px — the smallest thing in the mark sets the floor, not the largest.
- **The pivot is a coordinate of the drawing.** `transform-origin` is in user units, so a
  redrawn anvil moves it: `11.4px 9.8px`, the face's top-right corner, is where the hammer
  rests and where it turns.
- **The swing stays inside the box.** At `-72deg` every corner of the head remains within
  the view box, so no frame of the animation clips.
- **The silhouette does not change.** Filling the box is a change of proportions, not of
  picture: anvil below, head above right, spark where they meet.

## 26. The facts come before the run

The dock carries two kinds of line: facts about the session (the folder the conversation
belongs to, and what the session has spent) and facts about one run (the working line, and
the summary it becomes). The template ordered root, status, meter, so a turn in flight put
the run's line between the session's two facts — the meter drifted away from the root it
belongs with, and the dock's growth during a turn moved both.

- **Order is meaning.** Root and meter describe the session; the working line describes one
  run. The DOM now reads root, meter, status, and no styling can say that better than the
  order does.
- **The run's line belongs at the bottom.** It is replaced by the run's summary, and a
  summary is read just above the input. Putting it there also leaves the two session facts
  still while the dock grows during a turn.
- **Spacing follows the grouping.** p_0.46.21 gave the root line a 10px foot while the
  working line was its neighbour; with the meter between them that foot would separate the
  pair, so it returns to 6px. The working line keeps the room it was given, now above the
  composer where it matters.

## 27. The footprint

p_0.46.21–p_0.46.23 kept the root line and the run's line above the composer, and kept
reordering them so each fact sat beside the one it belonged with. Order was never the whole
problem: the lines float over a scrolling surface, and the space reserved for them is
measured by a `ResizeObserver` after layout. Between a turn growing and the observer's
callback, the transcript is painted under the lines or the lines over it. The dock can be
made taller, never timelier.

The layout follows one rule instead: **a fact that must never be overlapped lives inside an
opaque surface; a fact allowed to move with the conversation stays in the flow.**

- **The session's and the run's lines are one entity in the flow.** `#status` and `#root`
  move into `#footprint`, the last child of `#transcript`, with `order: 1` so later turns
  cannot sort above it. They scroll with the conversation, so nothing can be painted over
  them and no measurement is involved.
- **One entry, 4px apart.** Two transcript entries would sit 12px apart and read as unrelated
  facts; these are a label and its value, and 4px is the panel's label-to-value gap. The
  entry itself takes the transcript's 12px rhythm through `margin-block: 4px`, and its lines
  drop their horizontal padding because the transcript already insets its content.
- **The first line is reserved.** `.footprint .status` keeps `min-height: 1.5em` whether or
  not a run is in flight. A block that shrinks when a turn ends moves everything below it; an
  empty line is cheaper than a jump. `#status` is therefore hidden by the footprint, not by
  its own content.
- **The block appears with the session and leaves with it.** The footprint is hidden until
  either line has something to say, and `clearTranscript` re-appends it: emptying the
  transcript's text detaches it.
- **The meter splits by scope.** The conversation's context share — used, percentage, left,
  and the compact threshold when one is set — belongs to the root it describes, so it becomes
  a `.root-facts` span on that line. The session's in/out and cache belong to the session, and
  move into `#meter` inside `.composer .actions`, before the spacer, where they are painted
  on an opaque card rather than floated over the transcript. `renderMeter` still owns both
  halves and is the only place the meter's arithmetic is read.
- **The total cannot push the buttons.** It is `flex: 1 1 auto` with `min-width: 0` and
  ellipsises left-aligned; the row's right end belongs to `@`, Image and Send. The whole
  string stays reachable through `title` and `aria-label`.
- **A write to the block keeps the reader where they were.** Each of `renderRoot`,
  `setStatus`, `startWorking` and `stopWorking` records whether the reader was at the bottom
  before it writes and restores that after — the same `atBottom`/`scrollIfPinned` pair every
  appended turn uses.
- **The transcript's live region is still off.** The per-second clock now lives inside
  `#transcript`, which is `aria-live="off"`; nothing announces each tick, and the end of a
  turn is still said once through `#srStatus`.

## 28. One height, three boxes

p_0.46.24 reserved the footprint's first line with `1.5em` — the height of the text — and
put an 18px mark in it. A run starting grew the line by about a pixel and a half and moved
the end of the conversation; the reserve covered the empty-to-summary case and not the
mark's. Two narrower failures shared the same cause: a line built from inline boxes has no
box of its own to ellipsise in, and a flex row lets every child shrink.

- **Reserve the taller child, not a round number.** `.status` takes
  `min-height: max(1.5em, 18px)`. The text line is `0.85em × 1.5` and scales with
  `anvil.chatFontSize`; the mark is a fixed 18px. The working line's natural height is the
  same maximum, so the reserved box and the filled box agree at every size.
- **A flex row has no baseline to add.** Inline content's line box is the strut's height plus
  whatever an inline-flex box contributes at its baseline; that is a number the stylesheet
  cannot state. `display: flex; align-items: center` makes the line's height purely
  `max(min-height, tallest child)`.
- **Each half truncates in its own box.** The summary is a flex item with `min-width: 0` and
  an ellipsis; the working half keeps the mark and the word fixed and lets the parenthetical
  ellipsise. A mark that clips reads as a broken icon, so only text truncates.
- **The controls do not shrink.** The composer's select and buttons are `flex: 0 0 auto`; the
  meter remains the row's only flexible child, so a long session total truncates on the
  left of the buttons instead of squeezing their labels.

## 29. A count that says it landed, and a line that says "not yet"

The session total is three counts — what went in, what came out, and how much of the input the
provider served from its cache — and they arrive one run at a time. The step counter is the one
number on the line that moves during a run. Four things were wrong with the two lines that
carry them, and three of the four were absences rather than errors.

- **Reporting is not entitlement.** `in`, `out` and `cache` each render whenever the provider
  reported them. `renderMeter` used to draw `in` and `out` behind `if (!context)`, so a
  workspace that declared a context window showed a line saying `cache 742k` and nothing
  else. The counts are one sentence about the session; whether that session also has a window
  is a different question, and `meterMath.label` adds that window's share on top rather than
  instead.
- **An absence is information; say which absence.** A count is only known once a run has
  ended, so for a conversation's whole first turn all three were unknown and the line hid
  itself. That read as "this build has no meter", not as "nothing has been counted yet". While
  nothing has been reported, each count draws an em dash — the one spelling that cannot be
  mistaken for a count — so the line keeps its place, its height and its meaning.
- **The placeholder expires.** It is drawn only while *nothing* has been reported. A
  conversation that has finished one run shows real counts and no dashes, which is what keeps
  the dash meaning "not yet" rather than "unknown".
- **One box per count.** Each count is its own `inline-block`, because a transform does not
  apply to a non-replaced inline box. The separators are nodes too, so a dot with no number on
  one side of it can be hidden rather than printed — derived from the same nodes, so a count
  hidden by one condition cannot appear in the line through another.
- **A pulse means "this changed".** Numbers are written through `setNumber`, which compares
  before it writes; an unchanged value is not written at all. `renderMeter` runs on every cache
  tick and every root update, and almost none of those move a count — a pulse that fired on
  redraw would be noise, not information.
- **The step pulses on advance, not on restatement.** `workingStep` is set from the host's
  `progress` event, which may restate the current step on any event inside it. Only a step that
  differs from the one already on the line is a new fact, so only that one pulses. The step's
  spelling moved into `stepLabel` so the counter could be a node of its own while the
  parenthetical kept one truncation box around it.
- **A count is not a clock.** The cache countdown is written into a fourth node, appended to
  the cache count, so a per-second tick cannot re-write — and therefore cannot pulse — the
  number it sits beside.
- **The pulse is script.** `chat.css` may carry no at-rule (the layout tests parse it as text),
  so the pulse is Web Animations: a 520ms scale to 1.18 and back with an opacity dip at the
  trough. Nothing is left behind, so a second landing cancels the first rather than queueing
  it, and `prefers-reduced-motion` removes the pulse entirely while keeping the number.
- **A rail segment needs something to meet.** The vertical line through a reply is drawn one
  segment per block, and the segments overlap: each reaches 10px past its own box, because the
  reply body is a flex column with a 12px gap and a segment that stopped at its own edge would
  leave the gap blank. Every block with another block under it needs that reach; the reply's
  last block does not, and what sat 10px beneath it was the transcript's gap, then the next
  entry, then the foot the dock reserves. At the bottom of a scrolled conversation that painted
  a stub of line under the transcript.
- **The rail's overlap is not the defect.** Removing the 10px reach entirely would put a
  visible break in the rail at every block boundary inside a reply. Only the end of the reply
  is wrong, so only the end is fixed — and the marker was already right: `syncRail` gives
  `rail-down` only to a block with another block after it. The class reaches other producers —
  an interrupted run, a replayed turn — and a segment that stops at its own box is the correct
  drawing for all of them, so one selector covers every producer.

The first draft of this section was written from the four reports and shipped three
absences of its own. They are recorded here because each one is the same mistake in a
different costume: a fact that is drawn is not the same as a fact that is *stated*, and a
value is not the same as the text that happens to hold it.

- **Compare the value, not the node's text.** `setNumber` first compared
  `node.textContent`. The cache count owns the countdown node, so its text changes every
  second while its number does not — and the comparison therefore pulsed the cache count on
  every tick, which is exactly the noise the comparison existed to prevent. The value a node
  was last told is kept beside the node (a `WeakMap`), so "the count changed" and "the node
  was rewritten" stay two different questions.
- **The window's share is a count too, and it is drawn.** `meterMath.label`'s share was
  collected into the string behind `meter.title` and `aria-label` and never into a node, so
  the line did not show it at all; it was also gated on the host's context estimate, which
  dropped it in exactly the case where the composer line is the only place left to say it.
  The share is a node of its own, drawn whenever there is a last run to measure.
- **A dot belongs to the count before it.** Two separators between three counts cannot
  express four counts: hiding `out` left `5k in cache 4k` with no dot between the two visible
  numbers, because the middle count's dot was the one that mattered. Nor is "a visible count
  on either side" enough — with the two middle counts hidden it draws two dots in a row. A
  dot is shown only while its own count is visible and another count follows it, so each pair
  of visible counts gets exactly one.
- **The placeholder keeps its count's name.** `cache —` says which fact is not known yet;
  three bare dashes say only that something is unknown.
- **The pulse moves the counter, not its separator.** The step's `·` was inside the pulsing
  node, so the punctuation scaled with the number. The separator is a text node beside it.
- **A tick does not move the reader.** `renderMeter` pinned the transcript on every render,
  including the one-second cache tick. Only the root line, which lives in the transcript,
  needs the reader kept at the bottom — and only when its text actually changes, which the
  renderer now compares first.
- **The document order matches the drawn order.** The footprint is ordered last with
  `order: 1`, but entries were appended after it in the DOM: a copy or a screen reader read
  the root line and the run's summary before the conversation. Entries are inserted in
  front of the footprint, so the two orders agree.
- **An asset URL carries its content's digest.** `webview.asWebviewUri` is stable per path,
  so a reinstall that changes a script without changing the version leaves the webview
  fetching its cached copy — a shipped fix installs and appears to do nothing. All five
  documents now build their links through one fingerprinted builder, and a reinstall cannot
  be served from cache.

## 30. A dot that stays under the composer

The transcript's dots carry `z-index: 1` for a reason of their own: a tool row's state dot,
a reply's start dot and a summary's end dot all sit on the vertical rail, and the rail is
drawn by the block around them, so each dot has to be painted after that line. The blocks are
positioned but have no stacking context, which means that `z-index: 1` does not stay inside
the block — it joins the root stacking context.

The dock is positioned too, with `z-index: auto`, and a positioned element with no order is
painted below anything with a positive one. So a row that scrolled under the composer was
covered by the card everywhere except its dot: the dot floated on the card, above the input
and the placeholder.

The fix is on the surface that is supposed to be on top. The dock declares `z-index: 2`, so
the whole card — not merely its opaque background — is painted above every transcript dot.
The dots keep their `1`, because removing it would put each dot under its own rail, which is
the reason it is there.

A test reads the numbers rather than the intent: the dock's `z-index` must be greater than the
`z-index` of all three dot rules. Pinning `2` on the dock alone would pass a later change that
raised a dot to `5`.
