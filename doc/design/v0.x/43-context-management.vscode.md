# 42 — Context management: a budget, a compact, and a cache clock

**Milestone focus:** 0.43. Statuses live in
[m_0.43_context_management.vscode.md](../../roadmaps/v0.x/m_0.43_context_management.vscode.md).

## 1. Today

The SDK has had compression since 0.3 ([p_0.3.4](../../roadmaps/v0.x/m_0.3/p_0.3.4.md)) and has
persisted its marker since 0.9 ([p_0.9.1](../../roadmaps/v0.x/m_0.9/p_0.9.1.md)). What it does is
narrow:

- `Options.HistoryMaxChars` (a **character** budget) and `HistoryKeepRecent`
  ([options.go](../../../source/agent_options.go), [compress.go](../../../source/internal/compress/compress.go)).
- The runtime applies it **once**, before the first model call of a run
  ([loop.go](../../../source/internal/runtime/loop.go)); a run with many tool calls grows afterwards
  unchecked.
- `keep` counts messages, not turns: the tail can begin with a tool result whose call was
  summarized away.
- The summary is a `user` message named `anvil.summary`, persisted as a `summary` record; Resume
  loads the last marker plus the messages after it ([agent.go](../../../source/agent.go),
  [transcript.go](../../../source/session_transcript.go)).

The editor never enables any of it: `buildOptions` sets no `HistoryMaxChars`, and `SessionParams`
has no field for one ([server.go](../../../source/internal/editorhost/server.go),
[proto.go](../../../source/internal/editorhost/proto.go)). The 0.40.8 meter reports what a run
cost against `anvil.contextWindow`, but nothing reports what room is left, and a person cannot
ask for a compaction. Provider cache usage is not parsed at all: usage carries only
input/output/total ([types.go](../../../source/agent_types.go)), and the Anthropic client sends no
`cache_control` ([anthropic.go](../../../source/internal/llm/anthropic.go)).

So a long editor session has one outcome: it reaches the window and the provider refuses the
next request. This chapter closes that gap.

## 2. The budget

### 2.1 Estimation, not a tokenizer

There is no tokenizer in the tree and this chapter does not add one: a per-model tokenizer is a
large dependency that is wrong the moment the provider changes its chat format. The budget is an
**estimate**, computed by one function:

```text
tokens(message) = ceil(chars/4) + 4                 per text message
                + 85                                when the message carries tool calls
                + imageCost                        per image part (a declared constant)
```

Chars means the message's content plus the name and arguments of every tool call. The function is
internal (`contextbudget.Estimate`), documented as an estimate, and used for exactly two
decisions: whether to compact, and what to show in the meter. It is never presented as the
provider's count — the provider's own `usage` remains the only figure called "tokens used". What
the SDK exposes is the resulting `ContextUsage`, not the estimator.

### 2.2 The window is declared

`Options.Context`:

```go
type ContextOptions struct {
    WindowTokens  int     // 0 = unknown; auto-compaction is off
    AutoCompactAt float64 // fraction of the window; default 0.8
    KeepRecent    int     // messages kept verbatim after a summary; default 8
}
```

The window is a **caller declaration**, not a lookup: `ModelInfo` has no window field, and the
0.40.8 meter already refused to guess one. The editor passes `anvil.contextWindow`; the SDK
accepts whatever the caller knows. `AutoCompactAt` is clamped to `(0, 1]`; `KeepRecent` has a
floor that keeps at least one turn. `HistoryMaxChars` stays supported and composes: the runtime
applies whichever budget triggers, so an existing embedder changes nothing.

### 2.3 Where compaction runs

Two places, one function:

1. **Before a run**, as today, when the estimate (history + the new prompt) crosses
   `AutoCompactAt × WindowTokens`. The system message is never summarized.
2. **Inside a run**, at the top of each step, before the next model call, under the same rule.
   This is what a long tool-calling run needs; it is also why the summary can appear between two
   assistant turns rather than only at a run boundary.

The cut is a **turn boundary**: the tail begins at the earliest `user` message at or after the
`KeepRecent` mark, so a tool result always keeps the assistant call that produced it. If no such
boundary exists (a pathological history), the whole non-system history is summarized and the
tail is the last user turn. The summary request itself is bounded: only the older messages, with
tool arguments elided to a fixed size, and a deterministic fallback if the summarizer fails — the
same contract 0.3 established.

### 2.4 Manual compaction

```go
func (a *Agent) Compact(ctx context.Context) (CompactResult, error)
func (a *Agent) ContextUsage() ContextUsage
```

- `Compact` works on an idle agent only. With a run in flight it returns an error
  (`a run is in progress`) rather than racing the loop's message slice.
- It applies the same cut and summarizer with `force` semantics: it compacts when there is more
  than one turn, regardless of the budget, because a person who asked meant it. When there is
  nothing to compact it returns `Compacted: false` and no error.
- It persists the summary marker and the tail through the same sink path the loop uses, so the
  store looks exactly like an automatic compaction's.
- It is a no-op when the only thing before the tail is the previous summary: re-summarizing a
  lone marker costs a model call and changes nothing. A second `/compact` therefore answers
  `Compacted: false` rather than rewriting the same boundary.
- `ContextUsage()` returns the estimated tokens, the window, the mark, and the message count —
  the numbers the editor's meter needs for an idle session.

`CompactResult` carries `Compacted`, `Before`, `After` (estimated tokens), `Summarized` (message
count), and `Summary` (the text that was kept).

### 2.5 The event and the replay

`EventCompact` rides beside `EventTodo` as a non-tool-result event:

```go
Event{Type: EventCompact, Compact: &CompactInfo{
    Reason: "auto" | "manual", Before, After, Summarized, Window int,
}}
```

The editor draws one divider in the transcript. It is display state for a live reader; the
**replay** comes from the store: the transcript projection gains a `summary` entry kind carrying
the summary text and the run it belongs to. The model's history rule does not change — Resume
still loads the last marker plus what follows — and the reader still sees every turn, because
the pre-summary messages stay in the file.

### 2.6 What compaction cannot do

- It cannot shrink a single message: one tool result larger than the tail budget is summarized
  with the turn it belongs to, and if the surviving tail alone exceeds the window the runtime
  proceeds and lets the provider answer. Refusing to send is worse than a provider error that
  says the truth.
- It does not compact children. A child starts empty; the depth and budget of a subtree are
  p_0.42.1's rules, not a reason to touch its history.
- It never runs while a tool call is executing; the check is between steps.

## 3. The cache

### 3.1 What exists

Nothing is parsed and nothing is requested. Providers report cache activity in different shapes,
and this milestone reads all of them:

| Wire | Field | Meaning |
|------|-------|---------|
| OpenAI | `usage.prompt_tokens_details.cached_tokens` | input tokens served from cache |
| DeepSeek (OpenAI-compatible) | `usage.prompt_cache_hit_tokens` / `prompt_cache_miss_tokens` | hit/miss split |
| Anthropic | `usage.cache_read_input_tokens` / `cache_creation_input_tokens` | read / written to cache |

`Usage` gains `CachedTokens` and `CacheWriteTokens`. Both stay zero when the provider said
nothing; the client must keep treating "absent" as unknown and not as "no cache".

### 3.2 Anthropic prompt caching

Anthropic caches only what the request marks. With `Options.Context.PromptCache` on (default on
for the Anthropic provider), the client adds `cache_control: {"type": "ephemeral"}` to:

- the system text (sent as a block array instead of a string), and
- the last message of the request.

Two breakpoints bound the cached prefix: the system prompt and the growing conversation. The
request shape for messages already uses block arrays, so only the system field changes type; the
OpenAI-compatible path is untouched because those providers cache automatically.

The TTL is **5 minutes** — the documented Anthropic ephemeral TTL. The SDK reports facts, not a
clock: cached counts and `LastRequestAt` cross the wire, and the client applies the TTL it knows.
The editor's default is 300 seconds for the Anthropic provider and 0 elsewhere — DeepSeek's cache
is documented as lasting hours to days, and OpenAI publishes no figure, so a five-minute countdown
there would be a lie. A session may declare its own TTL (`anvil.cacheTtlSeconds`); zero means
"show cache activity, draw no countdown".

### 3.3 The clock

The runtime records the wall-clock time of the **last model request** in a run and returns it on
`RunResult.LastRequestAt`; the terminal event carries the same value. The remaining life is a
client computation:

```text
remaining = CachedTTL - (now - LastRequestAt)    when CachedTTL > 0
```

The editor ticks it once a second while a tab is visible. At zero it says **cache cold** rather
than hiding: a cold cache is information. Every run refreshes the anchor only when it actually
made a request, so an idle tab's countdown ages out on its own — which is what the cache does.

## 4. Non-goals

- An exact tokenizer, a per-model window table, or counting images by pixels.
- Hierarchical memory, transcript embeddings, or recalling summarized turns on demand.
- Compacting a stored transcript in place, or deleting the pre-summary records.
- Cache TTL discovery, cache warm-up requests, or retrying for a warm cache.
- Compaction inside a tool call, or automatic compaction for a run with no declared window.
- A per-session background compactor: compaction is a step the loop takes, not a service.

## 5. Delivery

| Phase | Contract |
|-------|----------|
| p_0.43.1 | §2: estimate, budget, run-start and mid-run compaction, safe cut, `Agent.Compact`, `ContextUsage`, `EventCompact` |
| p_0.43.2 | §3: cached tokens on every wire shape, Anthropic `cache_control`, `LastRequestAt`, TTL |
| p_0.43.3 | Host: `SessionParams.Context`, `session.compact`, `session.context`, compact event and cache fields on the wire |
| p_0.43.4 | Editor: `/compact`, the meter's remaining/auto-compact mark, the cache countdown, the compact divider, settings and tests |
