# 39 — Editor alignment

**Milestone focus:** make the VS Code extension act like the client it is modelled on →
**[m_0.40_editor_alignment.md](../../roadmaps/v1.0/m_0.40_editor_alignment.md)**.

[35](35-vscode-extension.md) built the extension and the editor host; [36](36-tool-permissions.md)
and [37](37-permission-modes.md) gave a session a policy and a mode; [38](38-session-identity-and-replay.md)
gave it a name and a transcript. What none of them gave it is the shape of the client those
chapters used as the bar: **several conversations at once, a way to manage them, a way to accept
part of a change, a way to go back, and the handful of tools the SDK already knows how to run but
the host never turns on.**

This chapter is written as an alignment record. The first section is the inventory — what the
reference client does, what Anvil does today, and which phase closes the gap. The rest states the
contract for each phase, so the inventory does not have to be rediscovered from the code.

## The inventory

`[doc]` marks a capability the reference client documents; `[use]` marks one observed from using
it rather than read from a specification. Only `[doc]` items are acceptance criteria; `[use]`
items are listed so the list is honest about where a given item came from.

| Capability | Reference client | Anvil today | Phase |
|---|---|---|---|
| Parallel conversations | Several tabs, each a session `[doc]` | One panel; `openChat` reveals it or creates it once | **0.40.1** |
| Session administration | Rename, delete, search history `[doc]` | Derived read-only title; no rename, no delete, no search | **0.40.2** |
| Partial acceptance | Accept or reject a change hunk by hunk `[doc]` | Whole-call allow/deny; the rewritten-args channel exists but the webview never uses it | **0.40.3** |
| Checkpoints / rewind | Restore the workspace to an earlier point `[doc]` | None; the change tracker is in-memory and lives one run | **0.40.4** |
| Conversation branching | Fork from an earlier turn `[doc]` | None | **0.40.5** |
| MCP servers | Servers listed and usable `[doc]` | The SDK reads and connects them; the host passes no `SettingSources`, so only inline config runs | **0.40.6** |
| Web search and fetch | Built-in web tools `[doc]` | Implemented in 0.15; no setting, no key channel in the extension | **0.40.7** |
| Usage and context | Live context usage `[doc]` | `Usage` is terminal-only on `RunResult`; no event carries it | **0.40.8** |
| Task list | A visible checklist of the model's plan `[doc]` | None | **0.40.9** |
| Input affordances | Escape interrupts, drag-drop, image picker, prompt history, copy `[doc]`/`[use]` | Stop button, paste, `@` picker | **0.40.10** |
| Sub-agents | A coordinator delegating to workers `[doc]` | No concept in the SDK at all | not planned |
| Extended reasoning | Provider-specific thinking blocks | Provider line is OpenAI-compatible; `MaxTokens` is fixed | not planned |

The two unplanned rows are decisions, not omissions. A sub-agent needs a runtime that can run an
agent inside an agent with its own context and its own tool policy; the SDK has no such concept
and inventing one inside a client would be an architecture, not a feature. Thinking blocks are
Anthropic-shaped; the chosen provider line has no equivalent, and the fixed output cap is a
provider detail rather than an editor one.

**Superseded by 0.47.** The *Partial acceptance* and *Checkpoints / rewind* rows describe the
state before 0.40. Checkpoints arrived in 0.40.4 and 0.47.2 gives them the affordance this
table's row was asking for: every user message offers *Rewind to here*, with fork and
restore as separate actions at the one granularity a checkpoint has — the turn — as
[44](44-editor-polish.md) §4 describes. Partial acceptance is unchanged since 0.40.3; 0.47
does not touch it.

## Several conversations at once

The protocol already supports it: a session is opened by `session.new` or `session.resume`, every
event and every approval carries the `session_id` it belongs to, and `session.close` closes one
session without touching the process. **Nothing on the host side changes in 0.40.1.** What is
single-conversation is the client:

- `HostManager` holds one `session` field and installs one notification handler that routes every
  notification to it;
- `ChatPanel.open()` reveals the one panel instead of creating another;
- the serializer is registered with that one instance, so two restored tabs would be adopted into
  the same object;
- the catalogue calls (`models.list`, `skills.list`, …) need the process but not a conversation,
  and are already independent of the session.

The shape that follows from those four facts:

```
extension.ts
  └── Hub                       one per window
        ├── HostManager         one child process, one JSON-RPC connection
        │     └── sessions: Map<session_id, ChatSession>
        ├── catalogue           models, skills, rules — shared, cached
        └── panels: ChatPanel[] one per editor tab
```

- **The connection is shared; the session is not.** `Hub` owns the process, the notification
  router and the catalogue. A `ChatPanel` owns one `ChatSession`, its own listener bundle, its own
  pending approvals and its own change tracker. The router is keyed by `params.session_id`; a
  notification whose id is unknown is logged and dropped, which is what a late event from a closed
  tab should do.
- **The tab is the session.** The webview calls `vscode.setState({ agentId })` when it learns an
  id; the serializer restores a tab by resuming that agent and replaying it through the 0.38
  reader. A restored tab and a tab that was never closed therefore take the same path, and the
  replay code is not written twice.
- **The sidebar reflects the tabs.** A row whose agent id has a live panel is marked, and clicking
  it reveals that panel instead of opening a second one. `anvil.openChat` creates a new tab;
  `anvil.openSession` is the reveal-or-create one.

Mode is per session and fixed at open (chapter 37), so two tabs may run different modes. That is a
consequence worth stating rather than a bug to smooth over: the mode picker in a tab changes that
session, and a new tab starts from the settings again.

## Rename, delete, search

A title is derived today and therefore cannot be changed by a person. The store is append-only,
and that is worth keeping, so a rename is **one more `meta` record** carrying a `title` field:

- `Summarize` reads `meta.title` with the same rule the first user message uses — normalized to one
  line, cut at 80 runes — and a later `meta` record **replaces** an earlier one, while the
  first-user-message rule only fills an empty title. Records are chronological, so a rename lands
  after the message that would have named the session and wins.
- Nothing else reads `meta`. `historyFromRecords` skips every non-message record
  ([agent.go:797](../../../source/agent.go#L797)), so a rename cannot alter what the model is sent, and
  `Resume` reads its options from the first meta record, which a later one does not replace.
- A title a person wrote is a decision, so it is not re-derived: an empty title is rejected rather
  than stored, because "clear the title" and "nothing was ever typed" would otherwise be the same
  row label.

Deleting removes `agent-<id>.jsonl`. The panel holding that session must be closed first — a live
session whose file disappeared would keep appending to a deleted path on the next run — so the
command closes the tab, then deletes. Once checkpoints exist (0.40.4) the same command removes the
agent's checkpoint directory as well; the phase that introduces the directory is the phase that
extends the delete.

Search is a client concern: `session.list` already returns everything a filter needs, and the
store is small (one line per message, one file per session). A QuickPick over title and id costs
no host method.

## Accepting part of a change

The approval path can already carry a rewritten call: `approval.response.args` becomes
`PermissionDecision.Args`, and the tool runs with that instead of what the model asked for
([server.go:1085](../../../source/internal/editorhost/server.go#L1085)). The client has never used it.
Partial acceptance is the first honest use.

The reason it is possible at all is that the preview is not a rendering: for `edit_file` the host
reads the file with `os.ReadFile` and sends the **whole file** as `before`, and computes `after`
by replacing the first occurrence of `old_string` ([preview.go:69-100](../../../source/internal/tools/preview.go#L69-L100)).
`write_file` sends `after` as the whole new content. So the client holds both ends of the change
as bytes, and the unified `diff` in the same preview is enough to describe the middle.

The contract:

1. **The accepted subset is computed against `before`, never by editing the diff.** Selected hunks
   are applied in order to the before text; the result is the content the file should have.
2. **Line terminators belong to the line they end.** `core.autocrlf` is on for this repository and
   the preview was read as raw bytes, so a CRLF file has `\r\n` in `before` while the diff is
   joined with `\n`. Matching hunks by line content and keeping each source line's own terminator
   means an untouched line comes back byte-identical; a synthesized line takes the terminator its
   neighbours use.
3. **All or nothing when it cannot be proven.** If any selected hunk's old side does not match the
   before text, the whole partial mode is unavailable for that approval (the card still offers
   Allow/Deny), because a best-effort splice is a file nobody reviewed.
4. **The rewrite goes through the same fields the tool reads.** `write_file` →
   `{path, content}`; `edit_file` → `{path, old_string: <whole before>, new_string: <accepted>}`.
   The whole-file replacement is guaranteed to match exactly once, because the preview read the
   same bytes and the file has not changed since (a file that did change fails the tool's own
   single-occurrence check and reports it, rather than silently applying a stale review).
5. **`apply_patch` is not splittable.** Its preview carries the patch text and no before/after, so
   it stays whole-call. Inventing file contents for it would break rule 3.

Rejecting every hunk is a deny, not an allow with an empty change: the model is told the call was
refused, which is what happened.

## Checkpoints

A checkpoint is the workspace **before a run**, and restoring one puts those files back. It is not
a transcript cursor and it does not touch the store; chapter 38 says so, and this chapter keeps the
two separate.

The capture point is the single place every tool call goes through, immediately before the tool
executes: `tools.runner.run` has the effective arguments (including an approval rewrite) and the
sandbox resolver, and `pathsFor(name, args)` already names the files a mutating call touches —
the same function the rule matcher and the approval preview use, so a checkpoint covers exactly
what a rule can see. Each path is copied to
`{cwd}/.anvil/checkpoints/<agent>/<run>/<relative path>`, and a manifest records whether the file
existed. A file that did not exist is recorded as absent, so restoring deletes it instead of
restoring an empty file.

Retention is bounded: the newest 100 runs per agent and nothing older than 30 days, pruned when a
session opens. Restore is sandbox-checked like every other path resolution — a checkpoint is a
record of what a call was allowed to write, and replaying it must not become a new escape.

What cannot be captured is stated rather than worked around: a `shell` command that redirects into
a file never names the file in its arguments, so its write is not recorded, exactly as the change
tracker already cannot see it. Restoring a run that contained such a command restores the files it
can see and says nothing about the ones it cannot.

## Fork

A fork copies the records up to and including a chosen `run_end` into a new `agent-<new>.jsonl`
and rewrites the `agent_id` in the copied `meta`. It is the same operation as a file copy plus one
field, because the store is already one file per agent and the history reader only needs the
records.

Forks are taken at run boundaries. A fork mid-run would copy a `run_start` without its
`run_end`, and the resumed session would begin inside a turn nobody finished. The title gains a
short suffix so the fork is distinguishable in the sidebar; it is a label, and the row tooltip
still carries the id.

## Turning on MCP

The SDK can already read `.cursor/mcp.json` and connect what it finds: `LocalOptions.SettingSources`
with `SettingSourceProject` selects the project file, and the parser lives in
[`internal/settings/mcpjson.go`](../../../source/internal/settings/mcpjson.go). The host passes an empty
`SettingSources` because 0.35 chose inline-only, and that choice is now the gap.

Two settings, deliberately different defaults:

- `anvil.mcp.project` — **true**. The file lives in the workspace, is reviewed like any other file
  in it, and the extension already refuses untrusted workspaces.
- `anvil.mcp.user` — **false**. Reading a home-directory file a user may not remember writing is a
  larger act than reading the workspace, so it is opted into.

What the panel lists is what will be loaded — name, transport and source file. Whether a server
*connected* and which tools it brought is not shown unless the SDK exposes that state; a green dot
that means "the file mentioned it" would be a lie about the only thing a person cares about. Calls
through MCP stay behind the `mcp` permission group from 0.36.

The listing is an additive method, `mcp.list`, that takes the same `setting_sources` the session
was given and answers the servers those files declare, plus whether each file exists. It carries
no connection field, and that absence is the contract: the wire cannot say "running" because the
host cannot observe it. A malformed file fails the call rather than yielding an empty list — an
empty list would say "nothing is configured" about a file that says something. A layer that is
enabled with no file is reported as such, because "off" and "on with nothing there" are different
answers and only the second is worth explaining in the panel.

## Web tools

0.15 implemented `web_search` and `fetch_url` behind `Options.WebSearch`; 0.36 gave them the `web`
group. The missing piece is entirely in the client: a switch, the provider and endpoint settings,
and a key that follows the same discipline as the model key — VS Code SecretStorage, injected as
`ANVIL_WEB_SEARCH_API_KEY` into the child environment, never written to settings, logs or the
protocol. `AllowPrivateURLs` stays off; a client setting is not the place to relax a fetch policy.

The tools appear only when enabled. A tool that exists but cannot authenticate is worse than one
that is absent, because the model will find it and fail. The session field is therefore present or
absent, never `enabled: false`: a host that predates the field and a client that does not want the
tools agree about what an omitted field means. The host turns it into `Options.WebSearch` and
refuses an http provider with no endpoint, naming `web_search.base_url` — the SDK's own
"BaseURL is required" does not tell a person which setting to fill in.

The panel's row is the honest half. It is posted only when the setting is on, says whether a key is
stored (never the value), and for the `mcp` provider says what that provider actually does: register
no built-in tools and leave search to a server the workspace declares. A row claiming built-ins for
that provider would be the same false green light the MCP card refuses to draw.

## Usage

`RunResult.Usage` exists and is final; the event stream carries no usage at all, which is why the
panel can show tokens only after a run. 0.40.8 adds `Usage` to the terminal event — `done` or
`error`, and no other: a count is a fact about a completed exchange, not a running estimate — and
the host forwards it on the events it carries, so a failed run reports what it spent before it
failed. The wire field is additive and omitted when unknown, so `ProtocolVersion` does not move and
a client that ignores it reads the payload it always read.

The authoritative end-of-run number stays the `done` notification, which already carries the
`RunResult` usage: the SDK's `done` event is still deliberately not forwarded (chapter 38's rule),
and the client counts each run exactly once, from the terminal envelope it receives — a live `done`
or a replayed `run_end`. Counting events as well would count an errored run twice, because the
`done` that follows it carries the same total.

A percentage needs a context window, and nothing in the repo defines one: `ModelInfo` has no such
field and inventing a per-model table would be a guess that goes stale. `anvil.contextWindow` is a
number with a default of zero, meaning unknown. At zero the composer shows cumulative input and
output tokens; above zero it also shows the fraction of the window the last run consumed. The
cumulative figure is the sum of the session's finished runs and is cleared with the transcript,
which is also the session change — so a reload that replays stored runs reconstructs the meter
rather than starting it at zero, and a new conversation starts it at zero rather than inheriting
the previous one.

## The task list

The reference client shows the model's own plan as a checklist, and Anvil has no equivalent
because the SDK has no planning tool. `todo_write` is the single capability this milestone invents
rather than connects:

- arguments are `{items: [{text, status: pending | in_progress | done}]}`;
- it writes no file and returns no content beyond a count;
- it emits a `todo` event carrying the list, and is persisted as an ordinary tool message, so a
  replayed conversation shows the list it showed live;
- the panel renders it below the composer, and an empty list removes the block.

It is a small tool with a small contract on purpose. It exists because a visible plan is part of
the interaction being aligned, not because the runtime needs one.

Three details make the contract hold:

1. **The list is the whole list.** `Event.Items` carries every row on every call; the panel
   replaces its block rather than appending, so moving an item to `done` cannot leave the previous
   `pending` row on screen. An empty list is a statement — nothing is planned — and the wire sends
   it as `[]` rather than omitting the field, because an absent field would be indistinguishable
   from an event that carried no list at all.
2. **The tool is not gated.** It touches no path and runs no process, so it is exempt from the
   permission gate even when a permission group names it. A workspace rule that denies it
   explicitly still wins: the gate is what a mode controls, and a written rule is a decision.
3. **A replay validates like the tool did.** Failure is not stored, so the replay reads the stored
   arguments and applies the tool's own rule — non-empty text, one of the three statuses. A call
   the host refused therefore replays as a card with no list, which is what it was, instead of a
   plan the model never managed to send.
4. **The block can be closed — for that plan, in that turn.** A head above the scrolling list
   carries a labelled close control (p_0.47.9). Closing records the plan's row texts and the turn
   it was closed in and hides the block; the same plan with a changed status stays closed, a plan
   whose text changed or any plan in a later turn appears again, and an empty list clears the
   dismissal. The alternative — a permanent switch — is a preference nobody asked for, and the
   checklist exists to be visible while it is being worked.

## Input

The remaining items are small and independent, but each has one decision worth stating:

1. **Escape is heard on the window, not the composer.** The textarea is disabled while a turn
   runs — which is exactly when Escape matters — so a listener there would never fire. An idle
   Escape is left entirely alone: there is nothing to interrupt, and the composer's own handler
   still uses it to close the palette while the draft stays put.
2. **A drop is classified by type.** An image becomes an attachment, read through the same
   `FileReader` a paste uses; every other file becomes a mention. Which of the two the drop
   carries has to be decided on the client, but *whether a path is inside the workspace* is not
   a client question: the webview posts what the drag carried (plain paths and/or `file:` URIs)
   and the extension resolves them with the workspace root, refusing an outside file with a
   sentence rather than letting the host reject the prompt later. Electron no longer exposes
   a dropped `File`'s path uniformly, so both carriers are read and either is enough.
3. **The picker answers in the same representation a paste produces.** The webview cannot open a
   dialog, so the button asks the extension; the extension reads the chosen files and posts
   `data:` URLs. The composer therefore holds one kind of attachment however it arrived, and the
   size cap is the paste path's own — checked on the encoded length so a file is never
   attachable one way and refused the other.
4. **Prompt history is the conversation's, not the tab's.** The list is held in memory, cleared
   with the transcript, and repopulated by the replay, so a reloaded tab can recall the prompts
   its transcript shows and `/clear` cannot recall the ones it just removed. Up and Down act only
   at the input's edges, so the arrows still move the caret through a multi-line draft; leaving
   the list downwards returns the draft the walk borrowed; editing a recalled prompt ends the
   walk rather than being overwritten by the next press.
5. **Copy writes the source text, through the host.** What a reader pastes elsewhere is the
   Markdown that arrived, not a second rendering of it. The write goes through
   `vscode.env.clipboard`: a webview's `navigator.clipboard` access depends on permissions the
   document does not control, and a Copy that silently fails is worse than a round trip.
6. **One status-bar item, and a waiting approval outranks a running turn.** The item is
   window-wide, so it reports the most urgent state any tab is in; a run stopped on a question is
   the state that needs the user, wherever its tab is. Its click goes to the waiting tab, else
   the front tab, else any open one, and only then opens a new one — "go where the work is"
   should not produce a second conversation. It reads the panels' own state on each
   `onRunningChanged`, so it cannot disagree with them.

## Security

Nothing in this milestone changes the trust boundary:

- the API key and the web-search key reach the host only through the child environment;
- the webview keeps rendering with `textContent`, including the new review panel;
- checkpoints are written inside the workspace and restored through the sandbox resolver;
- MCP is opt-in per source and stays behind the `mcp` group;
- the extension still refuses untrusted workspaces, and a new tab does not change that check.

## What this changes for existing callers

- `anvil.Event` gains an optional `Usage`; a consumer that ignores it behaves as before.
- `anvil.Event` gains `EventTodo` and optional `Items`, and the built-in set gains `todo_write`;
  a caller that allow-lists built-in tools can name it, and one that does not never sees the
  event.
- `anvil.Record` gains an optional `Title`; an older file without one reads exactly as today.
- `store.Summarize` learns to read a title field; derivation from the first user message is
  unchanged when no meta title exists.
- New public functions (`RenameAgent`, `DeleteAgent`, `ForkAgent`, `ListCheckpoints`,
  `RestoreCheckpoint`) and new host methods; every wire change is additive, so `ProtocolVersion`
  stays at 1 and an old host answers `-32601`, which the client already reads as absence.
- The input layer is client-only: no SDK or host method, one new contributed command
  (`anvil.focusChat`, what the status-bar item runs), and webview messages that an older
  extension never sends.

## Explicit non-goals

- Sub-agents or a multi-agent graph. There is no such concept in the SDK.
- Anthropic thinking blocks and a configurable output cap. The provider line is OpenAI-compatible.
- Checkpoints for shell redirection.
- MCP OAuth, remote transports beyond what the SDK supports, and a marketplace listing.
  *(MCP OAuth and Streamable HTTP are no longer out of scope: 0.44 owns them, see chapter 43.
  The marketplace listing stays excluded.)*
- Remote / SSH / dev-container targets, and cross-window session sync.
- Automatic context compaction.
- Notebook cell editing and multi-root in one panel — unchanged from 35. *(0.47 does not
  reopen this: it makes the one root a window works in explicit and chosen rather than
  silently the first folder — [44](44-editor-polish.md) §9 — and a panel spanning several
  roots stays out.)*

## Roadmap mapping

[`m_0.40_editor_alignment.md`](../../roadmaps/v1.0/m_0.40_editor_alignment.md) is this chapter,
in order. `0.40.1` carries the architecture every later phase stands on; `0.40.2`–`0.40.5` are the
session and change surfaces; `0.40.6`–`0.40.9` are capabilities the SDK already had; `0.40.10` is
the input layer.
