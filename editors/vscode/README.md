# Anvil for VS Code

The editor surface for the Anvil agent. The conversation is an **editor tab**
that streams a turn, typesets the model's Markdown, shows its reasoning in a
collapsed row above the answer, and gives every tool call a card with what went
in and what came back; it stops for your approval before anything writes to the
workspace. The **activity bar** lists this workspace's past sessions, one click
to continue, and the composer floats over the transcript so the conversation
scrolls underneath it.

This extension holds no agent logic. It spawns the `anvil-host` sidecar — a
stdio program built from the Anvil Go SDK — and renders what that process
reports. The protocol between the two is chapter 35 of the design docs
(`doc/design/v1.0/35-vscode-extension.md`); the TypeScript mirror of it is
`src/protocol.ts`.

## Installing a packaged build

Download or build the VSIX for your platform and install it:

```bash
code --install-extension anvil-vscode-0.41.2-win32-x64.vsix
```

Nothing else is needed: the `anvil-host` sidecar for that platform travels
inside the package, so a machine with no Go toolchain runs it. Reload the
window, open a folder you trust, run **Anvil: Set API Key**, and then
**Anvil: Open Chat** — or click a session in the Anvil view in the activity bar,
which opens the tab onto it.

## Running it from source

```bash
npm install
npm run compile
npm run build:host   # builds source/cmd/anvil-host into bin/
```

`media/vendor/katex/` is committed rather than installed. The formula renderer
ships inside the VSIX and the webview fetches nothing, so `package:vsix` needs no
`node_modules` for it; `npm run vendor:katex` re-copies `katex.min.js` and its
licence from the `katex` devDependency when the version changes, and refuses a
bundle that is not the pinned version or that assigns `innerHTML`.

Then press <kbd>F5</kbd> (or the `Run Extension` launch config) to open a second
VS Code window with the extension loaded, and run **Anvil: Open Chat**. `Run
Extension (trusted workspace, no reload)` opens that window straight onto this
repository, which is the quicker loop if you are working on both sides at once.

The sidecar is found in this order, and the first hit wins:

1. **`anvil.hostPath`**, when it is set — a path you chose, checked against
   nothing, so a host you built yourself always works.
2. **The bundled binary**, `bin/anvil-host[.exe]` inside the extension. This is
   the packaged case and the F5 case, and it is the only source that is verified
   before it runs (see *Provenance*).
3. **`anvil-host` on `PATH`** — for a build you installed yourself. A missing
   binary fails with the spawn error and a note about which of the three was
   being looked for.

## Packaging

```bash
npm run package:vsix                 # this machine's platform
npm run package:vsix -- --target linux-x64
npm run package:all                  # one VSIX per target, in sequence
npm run smoke:vsix -- --vsix anvil-vscode-0.41.2-win32-x64.vsix
```

Targets: `win32-x64`, `win32-arm64`, `linux-x64`, `linux-arm64`, `darwin-x64`,
`darwin-arm64` — the same list `src/hostBundle.ts` resolves against, so a
package can only be built for a platform the extension knows how to find a host
on. Each VSIX carries exactly one sidecar, cross-compiled with `CGO_ENABLED=0`
by `scripts/build-host.mjs`; the platform has to be chosen at package time
because nothing in an installed extension says which machine it will run on.

A packaged VSIX contains the compiled JavaScript, the media, the README, and
`bin/` — never `src/`, the tests, the scripts, or the workspace's own `.anvil/`
state.

### Provenance

Building the sidecar writes `bin/hosts.json` beside it: the file name, its
sha256, its size, the Go toolchain, the protocol revision, and when it was
built. At spawn the extension hashes the bundled binary and refuses to run it if
the digest is not the one recorded — the check is skipped for a configured path
or a `PATH` lookup, where there is nothing truthful to compare against.

That is a check on the package, not a signature: it catches a package that was
damaged, half-replaced, or assembled from a stale `bin/`, which are the ways
this actually goes wrong. It is not a defense against someone who can write to
the installed extension directory — they can rewrite the manifest too. What it
does mean is that the binary that runs is the binary that was built with the
JavaScript that is running it.

`initialize` then asserts the protocol revision on the wire, and the host's
version banner is compared against the extension's release line; a mismatch
names both sides and stops rather than failing later inside a tool call.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| `could not start the Anvil host: … (no bundled host for this platform…)` | The VSIX is for another platform. Install the one matching your OS and arch, or build `source/cmd/anvil-host` and set `anvil.hostPath`. |
| `the bundled Anvil host … does not match the checksum recorded when it was built` | The installed package changed after it was built. Reinstall it, or point `anvil.hostPath` at a binary you built. |
| `… speaks editor-host protocol N; this extension speaks M` | The sidecar and the extension are from different revisions. Reinstall the extension; if you set `anvil.hostPath`, point it at a matching build. |
| `Anvil refuses to run in an untrusted workspace.` | Expected. Trust the folder (**Workspaces: Manage Workspace Trust**) — the extension will not start an agent that can edit and execute in a folder you have not trusted. |
| The chat tab opens but nothing streams | The provider refused the run; the tab says which and why. Check **Anvil: Set API Key** and `anvil.baseUrl` / `anvil.model`. |
| The session list says it could not list sessions | The sidebar reports the spawn failure in the row's tooltip. Refresh it after fixing the cause; the list is cached, so a failure is not retried on its own. |
| A session row shows an agent id instead of a title | The session was never prompted, so there is no first message to name it by. The extension falls back to the id rather than inventing a title; the tooltip has the workspace, model, timestamps and run count either way. |
| Nothing happens in a folder with no files opened | The host's working directory is the workspace folder, so a single-file window has none. Open a folder. |

## Configuring a model

| Setting | Meaning |
| --- | --- |
| `anvil.provider` | `openai` or `anthropic` |
| `anvil.model` | Model id; empty uses the host's default |
| `anvil.baseUrl` | OpenAI-compatible base URL; empty uses the provider default |
| `anvil.maxSteps` | Step limit per turn; `0` uses the host's default |
| `anvil.contextWindow` | The window the token meter measures against, in tokens, and the window the session auto-compacts within. `0` (the default) means unknown, and the composer then shows counts only — Anvil does not guess a window from a model name |
| `anvil.autoCompactAt` | How full that window may get before the host summarizes the conversation on its own, as a fraction (default `0.8`). The meter marks this point and counts down to it; ignored while the window is `0` |
| `anvil.contextKeepRecent` | Messages a compaction keeps verbatim (default `8`, minimum `2`) |
| `anvil.cacheTtlSeconds` | How long the provider keeps its prompt cache, for the meter's countdown. `0` (the default) means the provider's default: five minutes for Anthropic, and no countdown for a provider that never states one |
| `anvil.store` | `jsonl`, `sqlite`, or empty for the host's default |
| `anvil.vision` | Accept pasted images; off by default |
| `anvil.rules` | Load `AGENTS.md` and `.anvil/rules/*.md`; on by default |
| `anvil.cursorRules` | Also load `.cursor/rules/*.md|.mdc`; on by default |
| `anvil.permissions` | Read the workspace's `.anvil/permissions.json`; on by default. Off means the session ignores the file — the rules **and** the `defaultMode` it may declare — and an approval card has nothing to offer under "always allow", because a rule written into a file this session does not read would not be in force |
| `anvil.mcp.project` | Load `.cursor/mcp.json` from the workspace; on by default. The file can start processes, which is why it is read only from a folder you trust |
| `anvil.mcp.user` | Also load `~/.cursor/mcp.json`; off by default. It is shared by every project, so reading it is opted into; project entries win over user entries with the same name |
| `anvil.webSearch.enabled` | Give the session `web_search` and `fetch_url`; off by default. Off means the tools are not registered at all, so the model never sees one it cannot use. Calls stay behind the `web` permission group |
| `anvil.webSearch.provider` | `http` (default) calls the endpoint in `anvil.webSearch.baseUrl`; `mcp` registers no built-ins and expects a search server from `.cursor/mcp.json` |
| `anvil.webSearch.baseUrl` | The JSON search endpoint for the `http` provider. Required with that provider — the session refuses to open without it, naming the setting |
| `anvil.webSearch.maxResults` | Results per search, 1–10; `0` uses the provider default of 5 |
| `anvil.lsp.enabled` | Give the session the language tools (`definition`, `references`, `hover`, `rename_symbol`). Off by default. A workspace's `.anvil/lsp.json` is read independently when the project layer is loaded, so this switch is for servers the editor declares |
| `anvil.lsp.servers` | Language servers, each `{language, pattern, command, args}`. The command is an executable, never a shell string; it runs with the host's privileges in a trusted workspace |
| `anvil.testCommand` | The argv of the test command, for example `["go", "test", "./..."]`; setting it registers `run_tests` |
| `anvil.buildCommand` | The argv of the build command; setting it registers `build` |
| `anvil.hostPath` | The sidecar to spawn; empty uses the bundled one, then `PATH` |

**Language tools.** A configured language server is started by the host, and its tools are
registered for the session: a server that cannot start removes its tools and never refuses
the conversation. The workspace can also describe servers in `.anvil/lsp.json`; the list in
settings is appended to that file. `testCommand` and `buildCommand` are argv arrays, so a
path with a space is one argument and nothing is handed to a shell. All three are omitted
from `session.new` when unset, which is why an unconfigured session is exactly the request
it was before they existed.

The API key is **not** a setting. If the VS Code process already has
`ANVIL_API_KEY`, that is enough and **Anvil: Set API Key** is unnecessary. A
key saved with that command overrides the variable and is kept for later
launches (VS Code's secret storage, and a local copy in the extension's own
state when that store comes back empty); **Anvil: Clear API Key** removes both
copies and falls back to `ANVIL_API_KEY` when the process has one. The
web-search key follows the same rule: **Anvil: Set Web Search Key** stores it
and it reaches the host as `ANVIL_WEB_SEARCH_API_KEY` — the only place that key
is ever read from, and **Anvil: Clear Web Search Key** removes it. Neither key
is written to a settings file, an output channel, or a log line. Every one of
those commands restarts the host, because a child keeps the environment it was
spawned with.

## What it will and will not do

- **Refuses untrusted workspaces.** The extension declares
  `untrustedWorkspaces.supported: false`, and `HostManager.assertTrusted()`
  throws before any spawn — so a folder you have not trusted cannot run the
  agent, whatever the UI does.
- **Asks before mutating calls.** `write`, `edit`, `shell` and `verify` raise an
  approval prompt by default; so does any tool the host adds from outside the
  SDK (MCP, plugins). The prompt shows a preview the host computed — the argv
  for a command, the diff for a file, a summary otherwise. (Rendering the diff
  inside the prompt is not the same as the diff-review surface, which is
  p_0.35.5.)
- **Remembers a pattern, not a tool.** "Always allow" offers the shapes that
  would actually decide the call in front of you — this exact command, commands
  starting with these tokens, anything the analyzer can prove read-only, this one
  file — and each one writes a rule into `.anvil/permissions.json`. The host
  re-reads that file and settles the call itself, so the choice takes effect
  immediately and is visible in the file afterwards. When the call is one no rule
  shape can describe (a `&&` line, say), the card offers a **session-only**
  entry instead, marked as such. A call that runs without a prompt says
  `allowed by rule N: …` on its card, and a refused one shows the rule's reason.
  A rules file that will not compile is opened where it is broken, with a
  diagnostic on the line, rather than failing the session with a bare error —
  and **Anvil: Open Permission Rules** is the manager that edits it without the
  JSON; **Anvil: Open Permission Rules File** opens the file itself.
- **Kills its child.** One host per window, stopped on deactivate, on a folder
  change, and on a settings change (the host reads its configuration at spawn).
- **Tells you what it read.** The tab reports which rule files this workspace
  loaded — `AGENTS.md`, `.anvil/rules/*.md`, and optionally `.cursor/rules/*` —
  and says so plainly when one was skipped or cut short by the size budget.
  It does the same for MCP: a card lists the servers the enabled `mcp.json`
  layers declare — name, transport, source file — with a state dot and word for
  each one and, once connected, how many tools it offers; a failure shows the
  error instead of breaking the session, and a layer whose file is not there is
  named. The dot is only drawn when the host can actually observe a connection:
  a host from before this milestone reports configuration alone, and the card
  then stays the plain list it was rather than inventing a status. A file that
  will not parse is reported as an error, since an empty list would hide the one
  thing you can fix. `/mcp` is where any of it is changed. Calls through MCP
  tools stay behind the `mcp` permission group. When the web tools
  are on, the tab adds one row saying so and whether a key is stored — present
  or absent, never the value; when they are off there is no row at all. With
  the `mcp` provider it says what that really means: no built-in tools are
  registered, and search has to come from a server the workspace declares.
- **Counts what the session spent.** A line under the composer adds up the input
  and output tokens of every finished run — live, or replayed from a stored
  `run_end` — and it resets when the tab moves to a new conversation. With
  `anvil.contextWindow` set it draws the conversation's estimated size against
  that window, what is left, and where auto-compaction will fire
  (`18k of 200k (9%) · 182k left · compact at 160k`); the estimate comes from the
  host, so it is re-read after every run and when a compaction finishes. When a
  provider reports cached input, the line adds the cached share and — with a
  known TTL — counts the cache's remaining life down to **cache cold**. With no
  window there is no percentage: no catalogue in the repository defines one, and
  a guess from a model name would make the fraction a number about Anvil rather
  than about the model. `/compact` folds the conversation on demand; the
  transcript marks the boundary and says how much was summarized.
- **Shows the plan as a checklist.** The built-in `todo_write` tool lets the
  model publish its task list, and the panel draws it below the composer — one
  row per item, with the current one in the normal text colour and finished ones
  struck through. Each call carries the whole list and replaces the block; an
  empty list removes it. The call is an ordinary tool card too, so a reopened
  conversation shows the checklist it ended with, rebuilt from the stored
  arguments.
- **Lets one agent hand work to another.** With `anvil.subagents` on (the
  default) the session has `spawn_agent`: the model writes a brief, and a child
  agent runs it in its own conversation with the parent's tools and permission
  callback. The child's steps stream under the spawn card — assistant text,
  reasoning and tool calls, drawn with the same renderer and indented rather
  than redrawn. A child that needs approval stops the same person the parent
  would, and its card names the child. Afterwards **Open subagent transcript**
  opens the child's stored conversation, which is a session like any other;
  replay shows the spawn call's brief and result, not steps the store never
  wrote. With `background: true` the call returns a task id instead of waiting,
  and the same table that lists shell jobs lists the child. `run_workflow` is
  the same child agent, declared as a small DAG: steps, dependencies, literal
  `{{step.result}}` substitution and one level of `for_each`, with one result
  block listing every step's status. It is orchestration, not a scripting
  language; a background workflow is one row in the Tasks panel.
- **Shows work that outlived the call.** A background `shell` or subagent is a
  row in the Tasks panel: kind, title, status, elapsed, and the actions that
  apply — **View output** for a shell task, **Open transcript** for an agent
  task, **Cancel** while it runs. `/tasks` opens it, and so does the status-bar
  item that appears while anything is running; the item counts the window's
  live sessions, because the panel is about the window rather than about one
  tab. Closing a session stops its tasks, and the panel then says what they
  became instead of showing a job that vanished. Output is text, read from the
  host's ring on demand, with a note when older bytes were dropped.
- **Takes the keyboard and the files.** <kbd>Escape</kbd> interrupts a running
  turn from anywhere in the panel (an idle Escape keeps its other meanings and
  never touches the draft). Files dropped on the panel become `@` references —
  resolved against the workspace by the extension, so a file outside it is
  refused with a reason — and dropped images become attachments. The **Image**
  button next to `@` opens a picker for the same attachments a paste produces.
  <kbd>↑</kbd>/<kbd>↓</kbd> at the edges of the input walk the prompts this
  conversation has shown, draft included: leaving the list restores what you
  were typing, and the list is cleared with the transcript rather than outliving
  the conversation it describes.
- **Copies an answer, and shows the run from anywhere.** A finished answer has a
  quiet **Copy** that puts the raw Markdown on the editor's clipboard. One
  status-bar item describes the window rather than a tab: idle, working, or
  waiting for an approval — which outranks working, so a question asked in a
  tab you are not looking at still reaches you. Clicking it goes to the tab that
  is asking, else the one in front, else any open tab, and only opens a new one
  when there is none.
- **Typesets the reply, and shows the thinking when the provider sends it.** A
  reply is rendered as Markdown — paragraphs, headings, lists, quotes, fenced
  code — by a parser that returns a tree of plain objects and nothing else: the
  panel builds the nodes, so nothing the model writes can add markup or script
  to the page. Reasoning that an OpenAI-compatible provider streams
  (`reasoning_content`, or `reasoning`) appears in a collapsed **Thinking** row
  above the answer, which closes itself once the answer starts. It is
  display-only — never stored, never replayed, never sent back. A link is only
  clickable if it is `http` or `https`, and the extension checks that again
  before asking the operating system to open it; anything else stays the literal
  text it was written as. The Thinking row is provider-dependent: Anthropic's
  extended thinking must be echoed back with a signature the SDK does not carry
  yet, so it does not appear there. Formulas are typeset too: `$…$` and `$$…$$`
  are drawn by a KaTeX bundle that ships with the extension, in MathML mode, so
  the page still loads nothing from the network and needs no stylesheet. `$5 and
  $6` stays money rather than becoming a formula, an unclosed `$` stays the
  character it is while the model is still typing, and a formula the renderer
  refuses appears as its source in monospace.
- **Reads a conversation without the machinery.** **Focus** on the composer —
  <kbd>Ctrl</kbd>+<kbd>Alt</kbd>+<kbd>F</kbd>, or **Anvil: Toggle Focus View** —
  hides the tool cards and the Thinking rows and leaves the questions and the
  answers. An approval card stays: it is a question to you, and hiding it would
  leave a turn waiting where you cannot see it. The view belongs to the tab, not
  to your settings: it survives a reload and comes back with the restored tab,
  and reopening the conversation later starts in the ordinary view. Toggling it
  keeps your draft and the place you were reading.
- **Works without a mouse or a screen.** A turn announces itself to a screen
  reader: a finished answer and the end of the turn politely, an approval
  request and an error assertively, and never token by token — a block is said
  once, when it stops growing, and progress lines are throttled to at most two
  a second. An approval card takes the keyboard when it appears, because the
  composer is disabled while a turn runs, and answering it hands focus back to
  the stop button. Every icon-only control has a spoken name — send and stop, a
  chip's remove cross, the banner's dismiss, the `@` button — and focus rings
  are drawn from the theme's own focus colour, two pixels wide in the
  high-contrast themes. The transcript stays a log a reader can browse rather
  than a live region. Arrow keys and <kbd>Enter</kbd> drive the `/` menu,
  <kbd>Esc</kbd> closes it and stops a running turn, and <kbd>Enter</kbd> in
  the composer sends.
- **Reopens the change a call made.** Every finished card for a call that wrote
  or edited a file offers **Review changes**, which opens VS Code's own diff
  editor. It is deliberately not the same thing as the prompt's preview: a call
  that ran under `accept edits`, under `bypass`, by an `allow` rule, or with
  `anvil.permissions` off never showed you anything, and those are the calls a
  way back to matters most for. The diff is the file as the turn found it
  against the file on disk whenever the extension still holds that before-image
  — the turn that just finished. Every older card, and an `apply_patch` (which
  spans files and has no single "before"), opens what the call itself carried,
  and the tab's title says which it is: *what this call replaced*, *the body
  this call wrote*, or the patch's own text. A `shell` command that redirects
  into a file changed it without naming it, so its card offers nothing rather
  than guessing. A failed call offers nothing either: a change that did not
  apply is not a change. What it does **not** do is accept, revert or undo
  anything — it is a view of what happened.
- **Keeps the conversation across a mode change.** `ask`, `accept edits`,
  `plan`, `no asking`, and `bypass` are session options in the SDK, so switching
  one closes the session and re-opens it under the same agent: same history, new
  mode. The mode is remembered per workspace, and the select's **Workspace
  default** entry gives up that memory so the workspace's own file decides again.
  You are told which mode is in force and where it came from: one from
  `.anvil/permissions.json` is announced once per session, and a declaration the
  host would not honour — `bypass` may not be granted by a project file — is a
  warning carrying the host's own words. `no asking` runs only what the rules
  already allow and never prompts; `bypass` is flagged in the composer. Both stop
  at the *asking*, not the policy: a `deny` in `.anvil/permissions.json` still
  refuses the call, a written `ask` rule still raises the card even under
  `bypass`, and the editor loads that file — and `.anvil/hooks.json`, which no
  client loaded before 0.36 — for every new session. A mode change is refused
  while a turn is running. See
  [`doc/design/v1.0/37-permission-modes.md`](../../doc/design/v1.0/37-permission-modes.md).
- **Keeps the conversation when you close the tab.** The tab is a view of a
  session, not the session itself: closing it leaves the agent and the host
  running, and reopening it re-announces the session, the mode and any approval
  still waiting for an answer. Opening a session from the sidebar reuses the one
  tab and empties it first, so two conversations are never stacked in it.
- **Draws a stored conversation back into the tab.** Opening a session — or
  reopening the window onto one — fills the tab with what happened in it: your
  messages, the replies as Markdown, the tool cards (each still able to open the
  change it made), and a marker where each run ended, with its steps and tokens.
  Reasoning has no row, because it was streamed and never written down; a call's
  card does not claim whether it succeeded, because that was never written
  either. A conversation longer than the reader's cap shows its most recent part
  and says how many earlier turns were left out. The window also remembers which
  session it was in, so a reload comes back to that conversation rather than to
  an empty tab — and if the session is no longer in the list it opens fresh
  instead of trying to resume something that is gone. An older host that does not
  answer `session.transcript` leaves the tab usable with no replay and no error.
  See
  [`doc/design/v1.0/38-session-identity-and-replay.md`](../../doc/design/v1.0/38-session-identity-and-replay.md).
- **Ships its own sidecar.** A platform VSIX carries the `anvil-host` binary it
  was built with, verified against the checksum recorded at build time before it
  is run. The API key is not in the package: it is in VS Code's SecretStorage,
  and from there in the child process environment.
- **Can go back, and can branch.** A run that edited files captures their
  pre-images, and restoring that run puts them back — deleting files the run
  created, because the manifest records that they were absent. A session can be
  forked at a finished run's boundary, and the fork opens in a new tab carrying
  the records up to that point. What is still not possible is editing or
  resuming *inside* a transcript: a replay is a record, not a cursor. Reasoning
  is the one part of a turn that does not come back — it was never persisted —
  and a round whose record had not landed when the tab re-attached mid-run is in
  neither the replay nor the stream. See `doc/roadmaps/v1.0/m_0.40/`.

## Sessions

The Anvil view in the activity bar is this workspace's session list: one row per
past session, grouped by when it was last used, newest first, with the model and
run count beside it. A click opens the chat tab onto that session. The run icon
in the view's title bar starts a new conversation and the refresh icon re-reads
the list; **Anvil: Refresh Sessions** does the same from the palette.

The rows are what `session.list` returns and nothing more. A row is labelled with
the session's title — the first thing you typed in it, one line and truncated —
and the tooltip carries the whole agent id along with the workspace, model,
timestamps, run count and tokens. A session nobody prompted has no first message,
so it falls back to the shortened id rather than to a title the extension made
up. A session that the store has no timestamp for is listed under **Undated**
rather than being filed under a day it may not belong to, and an empty list says
which of the three reasons it is: no folder open, an untrusted workspace, or a
host that could not be started (with the host's own error).

The list is cached and only re-read when something changes — a run starting or
finishing, a session opening, the host restarting. The view never spawns a host
just to draw itself.

A subagent is a session too, and it hangs under the session that spawned it:
collapsed, labelled with the description its spawn call gave the task, with the
parent id and task in the tooltip. Opening one resumes and replays it through
the same path as any other row. A child whose parent is no longer in the list is
a root rather than a disappearance, and deleting a parent says how many child
sessions go with it before anything is removed.

## Commands

Type `/` in the composer for the catalogue. Built-ins:

| Command | What it does |
| --- | --- |
| `/plan [request]` | Switch to plan mode; anything after it is sent as the request |
| `/model [id]` | Choose the model this conversation uses |
| `/resume [agent id]` | Continue a past session in this workspace |
| `/rewind` | Rewind the last turn: fork before it, put its files back, or both |
| `/export` | Save this conversation as Markdown; nothing after the command is read |
| `/clear` | Start a new conversation |
| `/compact` | Summarize the conversation so far; handled by the host, nothing is sent to the model. Refused while a turn is running |
| `/tasks` | Open the background tasks panel; sends nothing to the model |
| `/mcp` | Open the MCP manager; sends nothing to the model |
| `/help` | List the commands |

**Rewind to here** is on every user message; `/rewind` opens the same control on the last
one. A run is one turn, and a checkpoint is taken before the turn rather than before each
tool call, so the three choices are deliberate: fork before the turn (the conversation
continues from the run before it, files untouched), restore this turn's files (the
conversation is untouched), or both. The file action appears only when the host has a
checkpoint for the run; the first message has no run before it, so its fork action starts a
new conversation.

**`/export` writes a file, not a screenshot.** It reads the stored transcript rather than
what the tab has drawn, so a tab that attached in the middle still exports what the store
keeps — and a transcript the store truncated says how many records came before it. The
Markdown is the conversation — your message, the answer — plus one line per tool call naming
the tool and, for `write_file`, `edit_file` and `apply_patch`, the paths it touched. Tool
arguments, tool output, call and run ids and anything from the host's environment are not in
it. The save dialog defaults to `anvil-conversation-<date>.md` in the workspace folder, and
cancelling it writes nothing.

**A plan is a document.** A plan-mode answer ends with a card offering **Open plan**,
**Approve and run**, and **Keep planning**. *Open plan* (or **Anvil: Open Plan**) opens the
turn's text in a read-only `anvil-plan:` document beside the conversation — there is no file
system provider behind the scheme, so nothing in it can be edited, saved, or made to run.
*Approve and run* (or **Anvil: Approve Plan and Run**) switches back to the mode that was in
force before plan mode and sends one sentence, `The plan is approved. Carry it out.`;
*Keep planning* (or **Anvil: Keep Planning**) stays in plan mode, sends nothing, and returns
focus to the composer. The three commands are also in the palette, where they act on the plan
document in front of you or on the front tab's newest plan. The approval is a new turn, not a
continuation of the plan turn, so it appears in the transcript like any other.

**Anvil: Open Permission Rules** (also the **Permissions** button on the composer) opens the
rules manager: this workspace's rules as a list of effect, tool, pattern and reason rows, plus
the mode new sessions start in. Save sends the whole list to the host, which compiles it with
the same parser a session uses and writes the file atomically; a file that will not parse is
read-only in the manager rather than overwritten. **Open JSON** in the manager, and
**Anvil: Open Permission Rules File** in the palette, open `.anvil/permissions.json` itself —
creating it with an empty rule set if it is not there yet. That file is what decides whether a
tool call runs, stops to ask you, or is refused outright — see *Permission modes and rules*.

**Anvil: Open MCP Servers** (also `/mcp`) opens the MCP manager: every server of both layers
with its state dot and word — `connected` with its tool list, `connecting`, `failed` with the
error, `needs sign-in` — its transport, the file it came from, the command or URL it runs, and
the names (never the values) of the env vars and headers it declares. Each row has Enable or
Disable, Reconnect, Delete (which asks again in place), and Sign out; a server that wants a
sign-in also has Sign in, which opens the address the host answers with in your browser — the
token is stored by the host, never by the editor. The form adds or edits a server in the
project file or the user file: name, transport, and either a command, arguments and env vars or
a URL and headers, one `KEY=value` per line. A blank env or header section means *keep what
the file has*, which is why editing prefills key names rather than secrets. A write goes
through the host, which rewrites the file atomically and preserves fields this build does not
model; the manager then shows the host's own fresh reading, not the edit it sent. A file that
will not parse turns the manager read-only and shows the parser's message, and **Open
mcp.json** — also **Anvil: Open mcp.json** in the palette — opens the file itself, creating it
when it is not there. The manager lists both layers even when a setting excludes one, and says
so in a banner: those are the files it edits, and the setting only decides what a session
reads.

Skills in the workspace (`.anvil/skills/<id>/SKILL.md`) are listed as commands
of their own: running one turns that skill on for the conversation. `@`
references a file or folder for the next prompt.

## Permission modes and rules

The **mode** decides only whether a call stops to ask; the **rules** decide what it may do.
The select in the composer has six modes and one entry that is not a mode:

| Mode | What it does |
| --- | --- |
| **Ask** | Every gated call waits for you |
| **Accept edits** | In-workspace edits apply without asking; shell, verify, egress and external tools still ask |
| **Auto** | Runs what it can prove harmless — read-only commands, in-workspace edits, your configured test/build commands — and asks about everything else |
| **Plan** | The workspace-changing tools are withheld and the agent is told to plan |
| **No asking** | For a run with nobody watching: only calls the rules already allow run, and nothing ever prompts |
| **Bypass** | Nothing prompts — but a rule you wrote that says `ask` still does, and a `deny` still refuses |
| **Workspace default** | No opinion: the mode is whatever `.anvil/permissions.json` declares, or `ask` |

**Auto** classifies a call with the same facts a rule matches on and the same shell analyzer
behind the `readonly` rule pattern — it makes no model call and reads no file. A command the
analyzer cannot prove non-mutating asks, and so do egress, MCP and plugin tools. A rule you
wrote that says `ask` opens its card here too.

The mode is remembered per workspace; **Workspace default** gives that memory up, and the
entry's label then shows the mode actually in force (*Workspace default (Plan)*). A mode that
came from the file is announced once per session, so a repository cannot quietly decide how
much runs unattended. `.anvil/permissions.json` may declare one:

```json
{ "version": 1, "defaultMode": "accept_edits", "rules": [] }
```

`"defaultMode": "bypass"` is **refused** — a project file may not grant a mode that approves
everything without showing it — so the session starts in `ask` and the panel tells you why, in
the host's own words. Anything else the ladder does not have is refused the same way, and a
declaration that is not a bare lowercase word is a file error with a line number, like a
misspelled effect. Turning `anvil.permissions` off ignores the declaration along with the
rules.

## Tests

```bash
npm test        # compile, lint, build the host, run everything
npm run test:unit
npm run test:integration
npm run smoke:vsix -- --vsix <file>   # open a built VSIX and run the host it carries
```

Two kinds of test live under `test/integration/`. Most drive
`test/fixtures/fake-host.mjs` — a scripted host that speaks the real protocol,
so the extension's client, session state machine, and rendering can be tested
without a key or a network. The rest drive the **real** `anvil-host` binary with
the model call routed at a port nothing listens on, which pins the Go side of the
wire; they are skipped when the sidecar has not been built.

`src/settings.ts`, `src/hostManager.ts`, `src/chatPanel.ts`, `src/sessionTree.ts`
and `src/extension.ts` import `vscode` and are therefore not covered by these
tests — they need an extension host. Keep them thin; the behaviour worth testing
belongs on this side of the `vscode` boundary. `src/commands.ts`,
`src/hostBundle.ts`, `src/sessionList.ts`, `src/rulesFile.ts`, `src/editDiff.ts`,
`src/transcript.ts`, `src/export.ts` and `src/reattach.ts` are split that way on
purpose: they decide what a typed line means, which binary to run, what the
session sidebar shows, what the rules file's skeleton and error positions are,
what change a finished tool call carried and how it should be opened, what a
stored conversation folds into, what a conversation exports as, and whether a
reloaded window comes back to its session or starts fresh — and the
`vscode`-bound modules above only carry those
or starts fresh — and the `vscode`-bound modules above only carry those
decisions out. `test/unit/chatLayout.test.ts` is the third kind of check: it reads
`media/chat.css` and `media/chat.js` as text and asserts the layout invariants
those two files have to agree on, since nothing here can render them.
`test/unit/markdown.test.ts` loads `media/markdown.js` the same way — by
absolute path, for its side effect — and pins the property the transcript's
incremental renderer rests on: that re-parsing a document from its last block's
start reproduces that block exactly. `test/unit/linkPolicy.test.ts` reads the
two files that implement the link policy and checks they still both exist and
still allow the same schemes, which is the only part of that policy this suite
can see. `test/unit/permissionKinds.test.ts` is the fourth: it reads
`media/chat.js`, `src/protocol.ts` and the two Go files behind them and asserts
that every "always allow" kind the SDK offers has a label, that the methods are
spelled alike on both sides, and that the fields the card reads are the ones the
host sends — three failures that are all silent at runtime.
`test/unit/toolCardReview.test.ts` is the fifth, over the one click in the panel
no test here can perform: it reads `media/chat.js`, `src/chatPanel.ts`,
`src/editDiff.ts` and `src/mentions.ts` and asserts the button sits beside the
card's `<details>` rather than inside it, that the renderer offers a review only
where the extension said there was one, that the click hands back the call's
facts, that a pre-image is used only for the turn it came from, and that the
tools the review covers are the tools the change tracker follows — a second list
of file-writing tools is a list that goes stale in silence.
`test/unit/replayWiring.test.ts` is the sixth, over the replay: it reads
`src/chatPanel.ts` and `media/chat.js` and asserts the replay goes through
`postEvent` (so a replayed card is offered its review by the one place that
decides reviews) and never through `onEvent` (whose change-tracker snapshot would
file today's file contents under a historical call's id), that the reset lands
before the marker and the replay after both, that a host too old to answer costs
the tab a replay and nothing else, and that the two lines a replay adds are built
rather than written. The Go side of the same feature has its own reader tests,
including the one that asserts no entry and no line of the store file contains
image bytes or the API key. `test/unit/export.test.ts` pins the export's
audience: a fixture carries a file body, a tool result and the ids, and the
output must contain none of them, only the name and the paths.
`test/unit/reading.test.ts` is over the reading view and the formulas, where a
browser would be needed to see them: it reads `chat.js`, `chat.css`,
`chatPanel.ts`, the serializer, the package manifest and the vendored bundle,
and asserts the CSP kept its one source per directive, the renderer was asked
for MathML with `trust: false`, no HTML is assigned anywhere, the focus view
hides the tool cards and thinking but not an approval, and the export writes
through `vscode.workspace.fs` only after the dialog returned a target.
`test/unit/accessibility.test.ts` reads the same three files for the
screen-reader path: the two live regions and their urgencies, the transcript
kept out of them, the 500 ms throttle, the accessible name of every icon-only
control, the approval group and the focus rings in the ordinary and the
high-contrast themes.

The packaging step has its own check, since it is the part no unit test can
reach: `npm run smoke:vsix` extracts a built VSIX to a temporary directory,
asserts what is and is not inside it, verifies the binary against the manifest
it shipped with, and — on a package for this machine — runs that binary out of
the extracted tree and completes the handshake. A package that only works from
the directory it was built in fails it.
