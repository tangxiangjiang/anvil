# 46 — LLM calls: reasoning effort, retries, and stop reasons

**Milestone focus:** 0.49. Statuses live in
[m_0.49_llm_calls.md](../../roadmaps/v1.0/m_0.49_llm_calls.md).

## 1. Today

The provider layer is two adapters behind one interface
([types.go](../../../source/internal/llm/types.go)):

- `llm.Request` carries `Model`, `Messages`, `Tools` and `PromptCache`. Nothing says how much a
  model should think, how long its answer may be, or what to do when a call fails.
- The OpenAI request body is `model`, `messages`, `tools`
  ([openai.go](../../../source/internal/llm/openai.go)). The Anthropic body fixes
  `max_tokens: 8192` ([anthropic.go](../../../source/internal/llm/anthropic.go)).
- Reasoning **output** is supported: `Delta.Reasoning` streams a reasoner's thinking for
  display, and it is never accumulated, stored or sent back.
- `HTTPError.Retryable` is set in nine places and read in none. `runtime.Loop` ends the run with
  `CodeProvider` on the first error ([loop.go](../../../source/internal/runtime/loop.go)).
- Neither adapter reads why a completion stopped (`finish_reason`, `stop_reason`). An answer cut
  off at the output limit is indistinguishable from a finished one.

This chapter adds three request-side capabilities — **effort**, **retry**, **stop reasons** —
as SDK interfaces. Which model gets which effort, and which provider understands which field,
is configuration: the host and the worker read it from their config files and pass it in. The
SDK holds no model table and makes no choice of model.

## 2. Two different things called "reasoning"

`TestOpenAIStreamRequestCarriesNoReasoning`
([reasoning_test.go](../../../source/internal/llm/reasoning_test.go)) asserts that the request
body carries none of `reasoning`, `reasoning_content`, `reasoning_effort`, `include_reasoning`,
`thinking`. Its comment states its purpose: the moment a reasoning field is added to a struct
shared with the message encoder, the model's chain of thought starts going back upstream.

That purpose is right and it stays. But the test bundles two unrelated keys:

| | Reasoning **content** | Reasoning **effort** |
|---|---|---|
| What it is | the model's output: its thinking | a request parameter: how much to think |
| Direction | provider → SDK | SDK → provider |
| Where it lives | `Delta.Reasoning`, per message | one top-level request field |
| Risk if sent | leaks thought text to a provider, a gateway log or another model | none; it is a word or a number |
| Rule | never in `messages[]`, never stored, never echoed | sent exactly when configured |

So the test splits into two:

1. **`TestRequestNeverEchoesReasoning`** keeps the leak guard and makes it stricter: no message
   in `messages[]` carries `reasoning`, `reasoning_content` or `thinking` text in any spelling,
   for either adapter, with or without effort configured; and no top-level key carries
   reasoning *text*. It runs against a request built from a history whose assistant messages
   had reasoning streamed during the run — the case where a leak would actually happen.
2. **`TestRequestEffortField`** asserts that `reasoning_effort` (OpenAI) or `thinking`
   (Anthropic) appears **if and only if** effort is configured, with the expected value, and
   that a request with no effort configured is byte-for-byte today's.

The one place the line between the two is genuinely thin is Anthropic tool use, §3.4.

## 3. Effort

### 3.1 The option

```go
// Effort is how hard a model is asked to think before answering.
type Effort string

const (
    EffortDefault Effort = ""       // send nothing; the provider's own default
    EffortLow     Effort = "low"
    EffortMedium  Effort = "medium"
    EffortHigh    Effort = "high"
)
```

`Options.Effort` sets it for an agent; `llm.Request.Effort` carries it to the adapter; a child
agent inherits its parent's unless a `delegate` brief or a Complex stage overrides it (chapter
[48](48-agent-modes.md)). Three named levels and an empty default, not a number: the two
providers express effort in different units (§3.2, §3.3), and a caller that passes a number
would be passing one provider's unit through an interface that has to serve both.

The default is **empty**. A session that says nothing sends what it sends today, and the
adapter tests pin that byte for byte. Effort is turned on by the container's configuration,
per model, because the container is the layer that knows which model is behind a gateway id.

### 3.2 OpenAI-compatible

`reasoning_effort: "<level>"` as a top-level request field, present only when `Effort` is
non-empty. The adapter sends the word unchanged; it does not know which models accept it.

A provider that does not accept the field answers 400. The adapter does **not** strip the field
and retry: a silent downgrade would make a configured effort a lie, and the container could
never tell that its configuration was wrong. Instead the error is classified (§5) as
`CodeRequestRejected` with the provider's message, which names the parameter, and the container
removes effort from that model's configuration. The gateway's existing advisory `params` list on
a model route ([config.go](../../../source/internal/gateway/config.go)) is where a deployment
records which ids accept `reasoning_effort`; the SDK reads nothing from it.

### 3.3 Anthropic

`thinking: {"type": "enabled", "budget_tokens": N}`, with `N` by level:

| Level | `budget_tokens` |
|---|---|
| low | 1024 |
| medium | 4096 |
| high | 16384 |

The numbers are the adapter's, not the caller's, and they are constants in one place. They sit
below the fixed `max_tokens` of 8192 only for `low` and `medium`; `high` raises `max_tokens` to
`budget_tokens + 8192`, because the provider requires the output limit to exceed the thinking
budget and an answer should keep the room it had. The adapter test pins all three bodies.

### 3.4 Anthropic tool use with thinking enabled

When thinking is enabled and the model calls a tool, the provider's protocol requires the
assistant turn — thinking block included, with its signature — to be sent back verbatim on the
next request of the same tool loop. Without it the provider refuses the continuation.

This is the one case where reasoning content crosses back to the provider, and the design keeps
it as narrow as the protocol allows:

- The Anthropic adapter keeps the signed thinking blocks of the **current run's** assistant
  turns as an opaque `[]byte` on `llm.Message`, field `ProviderState`, documented as
  provider-private. It is not text: the SDK never decodes it, `Delta.Reasoning` is still what is
  displayed, and the field is never written by the OpenAI adapter.
- `ProviderState` is **not persisted**. The store's message record has no field for it; Resume
  starts a new run and the provider accepts a tool loop that begins fresh.
- `ProviderState` is **not read by any other adapter**. A session that changes provider between
  runs sends none of it, because the OpenAI encoder has no field to put it in.
- `TestRequestNeverEchoesReasoning` keeps passing: it checks for reasoning *text* keys, and
  `ProviderState` is encoded as the provider's own block type, which the test asserts carries a
  signature and is only ever the provider's own bytes.

**待定.** The exact wire requirement is to be confirmed against the provider's current
documentation before p_0.49.1 is marked done. If the provider has dropped the requirement, the
`ProviderState` field is not added and this section is reduced to a note saying so. What is
needed: one request/response pair from the live tier (chapter [45](45-evaluation.md) §4.3)
showing a tool loop with thinking enabled.

### 3.5 What effort does not do

It does not pick a model, and it is not a mode. A "high effort" session with a model that does
not reason sends a field the provider ignores or rejects. That is the container's configuration
to get right, and §3.2 is what tells it when it has not.

## 4. Retry

### 4.1 Where

Retry is a wrapper in `internal/llm`, not a branch in the runtime loop:

```go
// WithRetry returns a Completer that retries c on retryable failures.
func WithRetry(c Completer, p RetryPolicy) Completer
```

`runtime` may not talk HTTP and does not know what a 429 is ([01](01-architecture.md)); the
`Completer` it is handed is the retrying one. The wrapper implements `StreamCompleter` when
its inner completer does, so streaming is unchanged for the loop.

### 4.2 The policy

```go
type RetryPolicy struct {
    MaxAttempts int           // 0 means 3; 1 means no retry
    BaseDelay   time.Duration // 0 means 500ms
    MaxDelay    time.Duration // 0 means 8s
}
```

`Options.Retry` carries it. The zero value means **retry is on**, three attempts, which is a
behaviour change for callers and is the point: a run that dies on one 429 is the kind of thing
Codex does not do. A caller that wants today's behaviour sets `MaxAttempts: 1`.

Backoff is exponential from `BaseDelay` with full jitter, capped at `MaxDelay`. A `Retry-After`
header, when present and parseable, replaces the computed delay for that attempt and is still
capped.

### 4.3 What is retried, and what is not

| Failure | Retried? | Why |
|---|---|---|
| transport error before a response (`Status: 0`) | yes | nothing was received |
| 429, 5xx | yes | the provider said so |
| 4xx other than 429 | no | the request is wrong; sending it again cannot help |
| a stream that failed **after** the first delta reached `onDelta` | **no** | the user has seen text; a retry would show the answer twice. The error is surfaced with `CodeProviderMidStream` |
| a stream that failed before any delta | yes | indistinguishable from a transport error from the user's side |
| `ctx` cancelled or past its deadline | no, immediately | the caller stopped |

Usage is counted once: the wrapper returns only the successful attempt's `Response`, and a
failed attempt that carried a usage block (some providers send one on 5xx) is dropped.

### 4.4 What the caller sees

Each retry emits `Event{Type: "retry", Attempt: n, Delay: d, Text: <provider message>}` on the
run's stream. The event exists so that a UI can show *"provider busy, retrying in 2s (2/3)"*
rather than a frozen spinner, and so that the scorer (chapter [45](45-evaluation.md) §5) can
count it. The final failure, after the last attempt, is today's `CodeProvider` error with the
attempt count in its message.

The wrapper takes the `Emitter` through the context the runtime already threads, so the
`Completer` interface does not change shape.

## 5. Stop reasons and the output limit

### 5.1 `Response.StopReason`

Both adapters read why the completion ended and project it onto one small set:

| Value | OpenAI `finish_reason` | Anthropic `stop_reason` |
|---|---|---|
| `end` | `stop` | `end_turn` |
| `tool` | `tool_calls` | `tool_use` |
| `length` | `length` | `max_tokens` |
| `other` | anything else, or absent | anything else, or absent |

The runtime treats `length` as an event, `Event{Type: "truncated"}`, and the run's `Result`
carries `Truncated: true` when its last assistant message was cut. It does **not** ask the model
to continue: an automatic continuation is a second completion the caller did not budget for,
and it can repeat a tool call that already ran. The container decides what to do with a
truncated answer, as it does with a cancelled one.

### 5.2 `Request.MaxOutputTokens`

An optional output ceiling. OpenAI sends `max_completion_tokens`; Anthropic replaces the fixed
8192 (and, with effort `high`, the raised value of §3.3 is the floor, not the ceiling — the
larger of the two is sent, so the thinking budget always fits). Zero means today's behaviour.

### 5.3 Error classification

`HTTPError` gains a `Kind`, and the runtime maps it to a code the container can switch on:

| Kind | Code | Meaning |
|---|---|---|
| `KindTransport` | `provider` | no response, retried and gave up |
| `KindRateLimited` | `provider_busy` | 429 after retries |
| `KindServer` | `provider` | 5xx after retries |
| `KindRejected` | `request_rejected` | 4xx: the request itself is wrong (an unknown parameter, a context overflow, an invalid tool schema) |
| `KindAuth` | `auth` | 401/403 |

`request_rejected` is what carries §3.2's "this model does not accept effort" to the container,
with the provider's text intact, and what tells chapter [42](42-context-management.md)'s
compaction that the window was not as declared.

## 6. Non-goals

- Choosing a model, a provider, or an effort per task inside the SDK. All three are
  configuration passed in `Options`.
- Automatic continuation after `length`.
- Retrying a tool call, a subagent, or a workflow step. Those are above the provider layer
  and chapter [41](41-subagents-and-tasks.md) lists them as non-goals.
- Sending reasoning text back to a provider in any form other than §3.4's opaque,
  run-scoped, provider-signed block.
- A per-request effort on `Send`. Effort is an agent property so that a run's cost is
  predictable from its options; a per-run override is a later chapter if a container asks
  for one.

## 7. Delivery

| Phase | Contract |
|-------|----------|
| p_0.49.1 | §2, §3: `Effort`, both wires, the test split, the `ProviderState` decision |
| p_0.49.2 | §4: `WithRetry`, the policy, the retry event, usage counted once |
| p_0.49.3 | §5: `StopReason`, `MaxOutputTokens`, `Kind` and the codes; eval re-baselined |
