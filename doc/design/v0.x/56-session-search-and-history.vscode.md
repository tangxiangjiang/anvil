# 56 — Session search and history in VS Code

**Milestone focus:** 0.56. **Design status:** planned. This chapter adds local content search
without turning `session.list` into a transcript API or removing transcript bounds.

## 1. Today and the problem

Stored-session discovery is metadata-only:

- `anvil.List` loads `store.Summary` into `AgentInfo`: id, cwd, provider/model, title, timestamps,
  status, run count, usage, and subagent identity
  ([session_list.go](../../../source/session_list.go)).
- `session.list` projects that metadata into `SessionInfo`; it does not return message bodies
  ([proto.go](../../../source/internal/editorhost/proto.go)).
- `editors/vscode/src/sessionSearch.ts` filters only `title` and `agent_id`, because those are
  the only honest searchable fields in the listing.
- `anvil.LoadTranscript` and `session.transcript` keep a run-aligned **tail**, with
  `DefaultTranscriptLimit == 200` and an explicit `Dropped` count
  ([session_transcript.go](../../../source/session_transcript.go)).
- The session store is selected through `store.Backend`: JSONL is the default, SQLite is
  optional. The SQLite backend currently stores the same JSON record payloads in insertion
  order, not a separate normalized transcript schema.

Users remember words from a conversation, a tool name, an error, or a changed path—not the
first 80-rune title. Searching those words today requires opening sessions one by one, and a
tail-limited replay may not contain the remembered turn.

The data is highly sensitive: prompts, source snippets, paths, commands, and model output are
local workspace data. Search must stay local, scoped to the current workspace/store, bounded,
and explicit about what it returns.

## 2. Goals

- Search persisted session content locally across titles, user/assistant/tool messages, tool
  names/arguments, run errors, and structured plan/workflow text.
- Give JSONL and SQLite the same query language, ranking, limits, redaction assumptions, and
  result shape.
- Keep `session.list` metadata-only and cheap.
- Keep `session.transcript` tail-limited; search results link to bounded context rather than
  changing transcript replay into an unbounded load.
- Support closed sessions and subagent transcripts without creating live agents.
- Bound CPU, memory, bytes read, result count, snippet size, and cancellation latency.
- Make local sensitivity visible in UX and avoid network/telemetry paths.

## 3. Search model and protocol

### 3.1 Public SDK

```go
type SessionSearchOptions struct {
    Local       LocalOptions
    Query       string
    AgentID     string // optional exact scope
    Roles       []string
    Kinds       []string
    After       time.Time
    Before      time.Time
    Limit       int
    Cursor      string
    IncludeArgs bool
}

type SessionSearchHit struct {
    AgentID   string
    RunID     string
    RecordPos uint64
    Kind      string
    Role      string
    Name      string
    Timestamp time.Time
    Title     string
    Snippet   string
    Match     []TextRange
}

type SessionSearchResult struct {
    Hits       []SessionSearchHit
    NextCursor string
    Scanned    int
    Partial    bool
}

func SearchSessions(ctx context.Context, opts SessionSearchOptions) (SessionSearchResult, error)
```

`RecordPos` is a stable logical ordinal within one agent's ordered records for the current store
contents. It is a navigation anchor, not a byte offset, SQLite row id, or public persistence key.
Deleting or rewriting a session invalidates its anchors.

### 3.2 Editor-host methods

```text
session.search {
  query,
  agent_id?,
  roles?,
  kinds?,
  after?,
  before?,
  limit?,
  cursor?,
  include_args?,
  store?
} -> {
  hits: SessionSearchHit[],
  next_cursor?,
  scanned,
  partial
}

session.context {
  agent_id,
  record_pos,
  before?,
  after?,
  store?
} -> {
  entries: TranscriptEntry[],
  target_index,
  clipped_before,
  clipped_after
}
```

The existing `session.context` name is already used for context-budget information, so the
implementation uses the unambiguous wire name **`session.historyContext`**. The conceptual
operation above is named “context”; the protocol constant must not collide.

`session.historyContext` returns at most 41 transcript entries: default 10 before and 10 after,
hard cap 20 each side. It aligns outward to message/tool-result boundaries where possible but
never loads a whole long session into the webview.

### 3.3 Cursor contract

`cursor` is an opaque, versioned, authenticated envelope owned by the host. It binds:

```text
version + workspace identity + backend kind + normalized query/filter hash
+ snapshot id + last (score, timestamp, agent_id, record_pos) + expiry
```

The first page creates a snapshot boundary. For JSONL it records the candidate agent ids and
valid record count/file identity visible at start; for SQLite it records candidate ids and the
maximum record row id. Both become the same logical per-agent record high-water marks before
matching. Inserts after the boundary do not enter that traversal. A deleted session yields no
later hit for that session but does not reorder or duplicate surviving hits.

Snapshot state is host-local, bounded, and expires after 15 minutes or host restart. The cursor
contains only an opaque snapshot id and signed traversal state, not client-editable offsets or
query text. Invalid encoding/signature, wrong workspace/store/query, unknown version, or expired
snapshot returns a typed cursor error and **no partial page**; the client restarts from page one
only after explicit refresh.

### 3.4 Query language

Version 1 is deliberately small:

- whitespace-separated terms are ANDed, matching current `filterSessions`;
- quoted text is an exact case-insensitive phrase;
- `-term` excludes a term;
- filters: `role:user|assistant|tool`, `kind:message|error|plan|todo`,
  `status:finished|error|cancelled|interrupted`, `after:YYYY-MM-DD`,
  `before:YYYY-MM-DD`;
- all other punctuation is literal Unicode text after NFKC normalization and case folding.

There is no regex, glob, fuzzy model call, embedding, or query sent to a provider. Invalid
filters return a position-aware validation error before scanning.

## 4. Data sources and indexing

### 4.1 Canonical searchable documents

Search is defined over a backend-independent projection of `store.Record`:

| Record/source | Searchable fields |
|---|---|
| `meta` / `store.Summary` | title, task title, agent id |
| `message` | role, content, tool name |
| assistant tool calls | tool name; arguments only when `IncludeArgs` |
| `run_end` / durable run state | status, code, error, cause |
| plan/workflow records from 0.55 | title, goal, plan steps, acceptance, item id |

Reasoning is absent because it was never persisted. Checkpoint file contents are not searched;
checkpoint metadata/path search is deferred because it belongs to a separate retention boundary.

Tool arguments default to excluded. They often contain full file bodies, commands, or secrets a
model was asked to write. The VS Code command exposes “Include tool arguments” as an explicit
toggle with a local-data warning.

### 4.2 Backend parity before optimization

The authoritative implementation iterates `Backend.ListIDs`, calls `Backend.Load`, projects
documents, and applies one matcher/ranker in Go. Both JSONL and SQLite therefore execute the same
semantics. SQLite FTS and a JSONL side index may be added only as transparent accelerators whose
results pass the same contract suite byte-for-byte.

An accelerator is disposable derived state under `.anvil/search/`, versioned by:

```text
index_version + backend_kind + workspace_fingerprint + source_generation
```

It contains normalized searchable text and anchors, so it is sensitive. It uses owner-only
permissions where the platform supports them, is never committed, and can be deleted without
losing sessions. If stale, corrupt, locked, or version-mismatched, search falls back to a bounded
canonical scan and reports `partial` if limits stop it.

SQLite's `agents.db` is not itself used as a privileged faster semantic path in version 1.
“SQLite optional” means storage choice, not different search behavior.

### 4.3 Ranking

Ranking is deterministic:

1. exact title or agent id;
2. title contains all positive terms;
3. exact phrase in message/plan content;
4. all terms in one record;
5. terms spread across records in one run.

Within a score, newer record timestamp wins, then agent id and record position break ties.
No usage data, model, provider, or archived state changes relevance.

Snippets contain at most 240 Unicode code points centered on the first match, with control
characters removed and whitespace folded. Match ranges index the returned snippet, not the
original content.

Pages are slices of this total order. A traversal never returns the same
`{agent_id,record_pos,kind}` twice. `limit` defaults to 50 and is capped at 200; an empty page
with `next_cursor` is permitted when snapshot members were deleted after the first page.

## 5. Data flow

### 5.1 Searching

1. VS Code opens a Quick Pick and waits for at least two non-filter characters or explicit
   Enter; an empty query continues to use metadata-only `session.list`.
2. Input is debounced. A new query cancels the previous request through its context.
3. The host validates and compiles the query.
4. The searcher enumerates sessions in the selected backend, applying cheap metadata/date
   filters before loading records.
5. It scans projected documents up to configured byte/record/time budgets, ranks hits, and
   returns a bounded page.
6. Selecting a hit opens the session, loads the normal transcript tail, then asks
   `session.historyContext` only when the target is outside that tail.

### 5.2 History navigation

The chat remains a tail renderer. A search hit outside the tail opens a labelled, read-only
“History context” block containing the bounded context window and an action to return to the
latest transcript. It does not splice old entries into the live transcript, because that would
make ordering and event deduplication ambiguous.

The target anchor may have become stale after deletion or backend replacement. The context call
then returns `history anchor expired`; VS Code refreshes the search instead of opening the
nearest record and pretending it is the target.

### 5.3 Subagents and workflow artifacts

Subagent sessions are searchable records with their own `agent_id`; hits show the parent/task
metadata already present in `SessionInfo`. The searcher does not inject child messages into a
parent result. A 0.55 work item can link to its child execution, but each hit still names the
store that actually contains the text.

## 6. Core invariants

1. **Listing is metadata-only.** `session.list` never gains message bodies, snippets, or a
   “search blob”.
2. **Transcript stays a tail.** `DefaultTranscriptLimit` and `Dropped` semantics remain; search
   context is a separate bounded read.
3. **One query, two stores, one answer.** Given equivalent records, JSONL and SQLite return the
   same ordered hits and snippets.
4. **Search is local.** No query, index, snippet, or hit leaves the editor host.
5. **Workspace and backend are explicit boundaries.** Search never silently merges workspaces,
   JSONL, and SQLite.
6. **No hidden content source.** Only persisted fields listed in §4.1 are searchable.
7. **Cancellation is prompt.** The scanner checks `ctx` between sessions and bounded record
   batches.
8. **Partial is visible.** Reaching a scan/time/byte budget sets `partial`; it never looks like a
   complete zero-result answer.
9. **Cursors are query- and snapshot-bound.** They are opaque, validated, expiring, and cannot
   be reused against a changed query, workspace, or backend.

## 7. Security, privacy, and failure semantics

- Search surfaces content that may be older and more sensitive than the visible transcript.
  The Quick Pick states “Searching local Anvil history in this workspace”.
- Results and snippets are plain text. They do not render Markdown, command URIs, HTML, or file
  links from stored model output.
- Search inherits persistence-time redaction for tool output. User/assistant text and tool
  arguments were not generally redacted; no second redaction pass can honestly make them safe.
  `IncludeArgs` is therefore off by default.
- The host resolves only the configured workspace root and backend. Symlinks in message text are
  text; search never follows them.
- Corrupt JSONL tails use the existing valid-prefix behavior. The result is `partial` with a
  per-session diagnostic. Corrupt SQLite payloads follow the same projection rule and are
  counted as skipped, not silently treated as no match.
- A query timeout returns accumulated hits with `partial: true`; cancellation due to a newer
  query returns no UI error.
- Index deletion is best-effort. Session deletion invalidates derived documents immediately in
  the canonical path; a stale accelerator must verify source generation before serving hits.

## 8. Migration and compatibility

- No session-record migration is required for canonical scan. Existing JSONL files and SQLite
  payloads are searchable as written.
- Derived indexes are rebuilt, never migrated in place.
- Existing title/id search in `sessionSearch.ts` remains the zero-query and old-host fallback.
- Older hosts answer `-32601`; the command continues to offer metadata matches and does not imply
  content search succeeded.
- `session.transcript` response and limit semantics do not change.
- Switching a workspace setting from JSONL to SQLite selects a different history. This milestone
  does not copy, merge, or deduplicate stores.

## 9. Test strategy

- A shared corpus is written through both backends; every query asserts identical hit ordering,
  snippets, ranges, pagination, partial flags, and history context.
- Query parser tests cover Unicode normalization, quotes, exclusions, filters, invalid dates,
  control characters, and stable error positions.
- Property tests compare accelerator results with canonical scan over generated record sets.
- Large-store tests enforce result, byte, time, memory, and cancellation bounds.
- Transcript tests prove search does not change `DefaultTranscriptLimit`, run alignment, or
  `Dropped`.
- Protocol tests prove `session.list` contains no content fields and
  `session.historyContext` cannot read another workspace/store.
- Security tests assert no network adapter/provider is invoked, snippets are plain text, tool
  arguments are excluded by default, and index files receive restrictive permissions.
- Corruption tests cover a JSONL partial tail, malformed SQLite payload, stale cursor, stale
  index, deleted session, and expired anchor.
- Cursor tests traverse page sizes 1, 2, and 50, covering tampering, changed query/store,
  expiry/restart, deterministic ties, concurrent inserts, deletion, empty pages, and no
  duplicate hit within one snapshot.
- VS Code tests cover debounce/cancel, partial-result notice, old-host fallback, subagent labels,
  and returning from bounded history context to the live tail.

## 10. Non-goals

- Cloud, team, cross-workspace, or cross-machine search.
- Embeddings, semantic search, model-generated queries, or fuzzy ranking.
- Searching reasoning, environment variables, checkpoint file bodies, or files outside stores.
- Returning full transcripts in `session.list` or search hits.
- Removing transcript limits, transcript pagination, or splicing old context into a live stream.
- Automatic migration or merged search across JSONL and SQLite.

## 11. Delivery

| Phase | Contract |
|-------|----------|
| p_0.56.1 | Backend-independent document projection, query parser/matcher/ranker, parity corpus |
| p_0.56.2 | Opaque snapshot cursor, `session.search`/`session.historyContext`, pagination, bounds and failures |
| p_0.56.3 | VS Code local-history Quick Pick, bounded context UX, cancellation and old-host fallback |
