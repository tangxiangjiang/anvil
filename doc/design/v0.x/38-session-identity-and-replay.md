# 38 — Session identity and replay

**Milestone focus:** a session a person can recognise, inspect and return to →
**[m_0.38_session_identity.md](../../roadmaps/v0.x/m_0.38_session_identity.md)**.

[35](35-vscode-extension.md) built the chat tab and the session list;
[06](06-session-store.md) is the store both of them stand on, and
[08](08-security.md) is what a reader over that store may hand back. This chapter is about three
gaps between them, all of which have the same shape: **the data is already on disk, and nothing
projects it.**

- The listing reads `meta` and `run_end` records, so a session has no name.
- The wire carries a before and an after only where the SDK was about to ask, so a call that
  ran unprompted left no way to see what it changed.
- The SDK reads messages back only to rebuild the model's context, so a client cannot show a
  conversation it did not watch — and after a window reload the panel does not know which
  conversation it was in.

Nothing here adds a record kind, a persisted field, or a model call. Everything here is a
projection, a reader, a renderer, and one key in the editor's own workspace state.

## A title is derived, not stored

`store.Summarize` is the single rollup over a session's records, and every consumer — the CLI
listing, the host, any future client — reads a session's metadata from it. The title is
metadata, so it belongs there: one place computes it, one place truncates it, and no client has
to agree with another about what a session is called.

The source is **the first `role: "user"` message**. It is the earliest thing a person wrote, it
is already persisted by `persistMessage` on every run, and it needs nothing new: no schema, no
table column, no second write, no model call, and no API key — an offline `anvil list` names a
session as well as the editor does.

The normalization is deliberately blunt, in one unexported helper:

- control characters are dropped (a prompt can carry them; a sidebar row cannot render them);
- every run of whitespace, **including newlines**, folds to one space — a prompt is written in
  paragraphs and a title is one line;
- the result is trimmed, then cut at **80 runes**, with an ellipsis only when something was
  actually dropped. Runes, not bytes: the failure mode of a byte cut is a title that ends in
  mojibake, and it appears first for the users who write the least Latin text.

**A missing title is not an error and is not invented.** No user message means no title, which
is the same condition as "this session was never prompted", and the client falls back to the
shortened agent id exactly as it does today. The id is never removed from the tooltip: a title
is a label, and a label is not what `resume` takes.

Truncating here rather than in the client is a wire decision as much as a presentation one. The
full prompt can be hundreds of kilobytes, and `session.list` returns every session in the
workspace; a title is the only part of a message that belongs on a listing.

## Where a change is — and where it is not

The request is "click the edit and see what it changed". The obvious implementation is to have
the host compute the change and put it on the tool call's result, and the SDK already computes
exactly that shape for the approval card (`tools.Preview`: a path, a unified diff, and both
bodies). It is the wrong tool here, for two reasons that both show up as user-visible wrongness:

1. **The preview is computed only where the SDK is about to ask.** It is built inside the
   permission branch of `Registry.Execute`. So under `accept_edits`, under `bypass`, when a
   rule allows the call, or when the workspace has no permissions file, there is **no preview at
   all** — and the card would therefore offer a diff precisely when the user had already been
   shown one, and nothing when the edit applied silently. That is the inverse of what was asked
   for.
2. **A replayed call has no preview.** The store keeps messages, not previews, so the replay
   path would need a second implementation — the arguments-derived one — and the two would
   drift the first time either changed.

So the change is derived, in the client, from **the call's own arguments**, which is the one
source both a live call and a replayed call have. `src/editDiff.ts` is that derivation, and it
is a pure function of `(name, args)` — no `vscode`, no host, unit-testable, and identical for a
card rendered from a live event and one rendered from a store record. That identity is the
whole reason p_0.38.2 lands before p_0.38.3.

What it can honestly say:

| Call | What the card can open |
|---|---|
| `edit_file` | The old text and the new text the call carried |
| `write_file` | The body the call wrote, against an empty side |
| `apply_patch` | The patch text as a document, named for the file its headers name |
| Anything else | Nothing — the card offers no review rather than an invented one |

And when the client has something better, it uses it: the change tracker snapshot the pre-image
of every file a turn touched, so for a file the **most recently finished** turn wrote, the review
is **that image against disk** — the same diff the approval card and the changed-files row open.
The arguments are the fallback, not the ceiling.

The pre-images are held with the run that took them, which is why "most recently finished" is the
honest bound rather than "the turn that made this change". A later turn's pre-image shown under an
older card's button would read as that call's edit — one turn's change attributed to another's
call — so a run id that does not match falls back to the arguments instead. That also settles the
mid-turn click honestly: a card from the turn still running has no pre-image yet, because the
tracker has not finished that turn, and shows what its arguments carried.

The fallbacks are labelled rather than dressed up. A `write_file` with no pre-image opens against
an empty side; the tab says *the body this call wrote*, which is what it is, and not that the file
was empty. Reading the file from disk and calling it the "after" would be worse — it would show
the change as nothing. An `apply_patch` is not a diff at all but a document: it opens as its own
text (`PreviewDocuments.openText`), because it spans files and there is no single "before" it
belongs to.

Two things the review is **not**:

- **Not a request.** The approval card's *Review in editor* belongs to a pending decision: it is
  tracked per request and closed when the run ends. A call's review is a **record** of something
  that already happened. It answers nothing, and nothing closes it.
- **Not a revert.** There is no revert, no undo, and no "apply anyway" — the change is on disk
  and the review is a window onto it.

The known gap is stated rather than papered over: a `shell` command that redirects into a file
is invisible to the change tracker and has no arguments that describe a change, so its card
offers nothing.

## What the transcript reader returns

`anvil.LoadTranscript` is a public projection over `store.Load` — the same load `Resume` does,
read for a different purpose. Per message it returns the role, the content, and for a tool call
the id, the name and **the arguments**; per run it returns a boundary with the status, the step
count and the usage. Oldest first.

**The arguments are load-bearing.** Without them a replayed tool card is a name and a state,
and the review affordance this milestone exists for cannot be offered for any past session.
They are also, note, the only part of a tool call the store keeps — a replayed card cannot show
a result that was never written.

**What it must never carry**, and why each is not a filter the reader applies but a fact about
the store:

| Not carried | Why |
|---|---|
| Reasoning | It is streamed as an event and never persisted. There is nothing to return, and no reader could recover it. |
| The API key | Never written to the store, by construction. |
| Image bytes | Attachments travel to the model; a message record holds the text, not the parts. Pinned by a test, because "the store happens to be clean here" is exactly the kind of assumption that stops being true. |
| Anything from another workspace | The reader resolves the agent under the workspace root and reads that workspace's store, the way `session.list` does. |

Redaction is **inherited, not re-applied**: tool output is scrubbed on its way out of the tool,
before both the stream and the store, so the reader returns text that was already redacted and
there is no unredacted copy to leak. User and assistant text was never redacted on the wire
either, so the replay shows exactly what the live stream showed. What is new is durability, not
exposure — which is worth one line in [08](08-security.md) rather than a mechanism here.

**`historyFromRecords` is not this.** It is tempting: it already folds records into messages.
But it folds them into `[]llm.Message` for the model, it discards run boundaries, and it
applies the compression rule — three behaviors that are each wrong for a renderer, and the
compression one silently so. The reader is a separate projection over the same load, and the
duplication is the price of the two having different jobs.

## Order, the cap, and the one race

The store is append-only, so record order is conversation order, and the reader preserves it.
Runs are grouped by their id, so a boundary is unambiguous.

The reader is capped, keeping the **most recent** entries and reporting how many it dropped. The
cap is a bound on what one render costs, not a paginator: this phase shows the tail and says so
rather than offering "load earlier". Per-entry content is already bounded by the output cap the
store has always applied.

Replaying beside a live stream is where this could go wrong, and it does not, for one reason:
**the runtime emits an event before it persists the message that event described.** A reader
that loads while a run is in flight therefore sees only messages whose events are already gone.
There is nothing to duplicate.

What that leaves:

- **After a window reload there is no race at all.** The host is a child of the window; the
  window reloading kills it, so every record on disk belongs to a finished run.
- **A tab re-opened mid-run** seeds from the finished history plus the completed part of the
  in-flight run, then continues live. Events that arrived while the tab was closed were already
  dropped — that is 0.35's behavior and it is unchanged.
- **One bounded hole.** If the tab attaches in the narrow window after a round's last event and
  before its record lands, that round is in neither the seed nor the stream. It is at most one
  round per run, only on a mid-run tab re-open, and it is no worse than the drop that already
  happens today. It is documented, not fixed.

The honest way to put the whole thing: replay cannot show what the user never saw, because
nothing is persisted before it is streamed. It can **fail to show** two things the user did
see — reasoning, always, and that one round, rarely.

## Coming back to a session

A reloaded window is a fresh extension host with no memory of the conversation it was in, so
the panel remembers the current agent id in `workspaceState` — the same place, and the same
lifetime, as the permission mode it already remembers per workspace. It is written when the
panel learns an agent id and cleared when the conversation is cleared.

Reading it back is a decision with three outcomes, so it is a pure function
(`shouldReattach(stored, listed)`) that an extension host is not needed to test:

- **nothing stored** — nothing to do;
- **stored and still listed** — re-attach, which means calling the same `resumeAgent` a sidebar
  click does, so a reused tab is emptied before the replay fills it and two conversations are
  never stacked;
- **stored and no longer listed** — the store was deleted, or the window opened a different
  workspace whose store has no such agent. The key is **cleared**, which is a different outcome
  from having no key at all, and the tab opens fresh.

The replay is the same call in both places it is needed — opening a session, and coming back to
one — because those are the same thing to the panel: a transcript to draw and a session to
continue.

## Failure modes

| Condition | What happens |
|---|---|
| A session has no user message | No title; the row falls back to the shortened agent id |
| The first user message is whitespace or one character | The normalized title is whatever survives; an empty one falls back to the id |
| A title exceeds 80 runes | Cut at 80, ellipsis appended |
| A call's arguments are malformed | No review affordance; nothing is thrown and nothing is logged as an error |
| A call changed no file | No review affordance |
| A call failed | No review affordance — a change that did not apply is not a change |
| The pre-image held is a different turn's | The review falls back to the call's own arguments |
| A `shell` command wrote a file | No review affordance — the change tracker cannot see it |
| The stored agent id is gone | The key is cleared and the tab opens fresh |
| The host is older than this phase | `session.transcript` answers `-32601`; the tab works without replay |
| The transcript is longer than the cap | The most recent entries are shown, and the drop is reported |
| A record is unreadable on disk | The store's existing tail handling applies; a reader is not a repair tool |

## What this changes for existing callers

- `store.Summary` and `anvil.AgentInfo` gain a field; both are additive and both are read-only
  projections, so a caller that ignores them is unaffected.
- `SessionInfo` on the wire gains an optional `title`. **`ProtocolVersion` does not move.** The
  p_0.36.2 rule is the precedent and it applies exactly: a field added to an existing message is
  optional, and a new method is a new method. `session.transcript` is also **not** announced as a
  capability — a host that does not implement it answers `-32601`, which the panel renders as no
  replay, the same degradation the Permissions button already relies on.
- The extension gains no new setting and no new command.
- Nothing about the model's context changes. `Resume` behaves as it always has; the reader is a
  second view of the same records.

## Explicit non-goals

- **A model-generated title.** Better prose, at the cost of a model call per session, a place to
  store the answer, and a fallback when it fails — to replace a string already on disk.
- **Persisting pre-images** so a replayed diff is byte-exact. It is a store schema change that
  writes copies of the user's files nobody asked to keep.
- **Replaying reasoning.** It was never persisted; this is 0.35's limit, restated because
  "replay" now exists and it is what replay does not include.
- **Checkpoints, rewind, forking or editing a replayed conversation.** A transcript is a record,
  not a cursor.
- **Paginating the transcript**, or listing across workspaces.

## Roadmap mapping

- **0.38** — [m_0.38](../../roadmaps/v0.x/m_0.38_session_identity.md): p_0.38.1 titles,
  p_0.38.2 reviewable cards, p_0.38.3 the reader, the replay and the re-attach.
- Reverses one non-goal of [35](35-vscode-extension.md): *"the SDK exposes no transcript reader,
  so the tab marks the resume instead of printing a history it does not have."* The reader now
  exists; 35's chapter says so where it used to say otherwise.
