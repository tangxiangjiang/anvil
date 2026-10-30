# 53 — Model capability profiles and run signals

**Milestone focus:** 0.53. **Design status:** planned.

## 1. Today / problem

Milestone 0.48 delivered the SDK primitives:

- `Options.Effort`, `Options.MaxOutputTokens`, and `Options.Retry` in
  [`source/agent_options.go`](../../../source/agent_options.go);
- `llm.Request.MaxOutputTokens`, `Response.StopReason`, and `StopLength` in
  [`source/internal/llm/types.go`](../../../source/internal/llm/types.go);
- runtime `retry` and `truncated` events plus `Result.Truncated` in
  [`source/internal/runtime/loop.go`](../../../source/internal/runtime/loop.go).

There are three integration gaps.

First, Anthropic thinking is currently one legacy mapping: `thinkingFor` emits
`{"type":"enabled","budget_tokens":...}` and `anthropicMaxTokens` couples the thinking budget
to `max_tokens`. New Anthropic model families require adaptive thinking:
`{"type":"adaptive"}` with `output_config.effort`. The SDK intentionally has no model-name
table, so it cannot safely guess which wire a gateway model id needs.

Second, the editor-host drops both run signals. `projectEvent` in
[`source/internal/editorhost/session.go`](../../../source/internal/editorhost/session.go)
does not admit `retry` or `truncated`; `EventParams` has no retry fields; `projectDone` does
not project public `RunResult.Truncated`. The VS Code `EventType` union therefore cannot show
the behavior already implemented by the SDK.

Third, there is no editor control for `MaxOutputTokens` and no explicit continuation action.
Automatic continuation remains prohibited: it spends another completion without consent and
can repeat output or work.

## 2. Goals

1. Describe model behavior with an explicit capability profile supplied by the container,
   never inferred from a model id in the SDK.
2. Support Anthropic legacy budget thinking and adaptive thinking without leaking reasoning
   text or silently changing effort.
3. Carry retry and truncation signals losslessly over editor-host protocol.
4. Let the user set an output ceiling and explicitly continue a truncated answer.
5. Never continue automatically.
6. Preserve request bytes and behavior when no capability profile is supplied.

## 3. Capability profile

The public SDK model becomes:

```go
type ThinkingProtocol string

const (
    ThinkingUnspecified      ThinkingProtocol = ""
    ThinkingNone             ThinkingProtocol = "none"
    ThinkingOpenAIEffort     ThinkingProtocol = "openai_effort"
    ThinkingAnthropicBudget  ThinkingProtocol = "anthropic_budget"
    ThinkingAnthropicAdaptive ThinkingProtocol = "anthropic_adaptive"
)

type ModelCapabilities struct {
    Thinking        ThinkingProtocol
    EffortLevels    []Effort
    MaxOutputTokens bool
    Vision          bool
}

type Model struct {
    ID           string
    Capabilities ModelCapabilities
}
```

`Stage` in `source/mode_complex.go` gains `Capabilities ModelCapabilities` because a Complex
stage may select a model different from the session model. Empty stage capabilities inherit
the session model's profile only when the stage model id is also empty. A non-empty stage model
without a profile is unspecified; the SDK does not pretend two ids share capabilities.

The editor-host wire mirrors the shape:

```text
model_capabilities: {
  thinking: "none" | "openai_effort" | "anthropic_budget" | "anthropic_adaptive",
  effort_levels: ["low", "medium", "high"],
  max_output_tokens: true | false,
  vision: true | false
}
max_output_tokens: <positive integer>
```

Both fields are optional in `SessionParams`. `ModelEntry` from `models.list` may carry the
same optional `capabilities` object when the configured catalog provides it. The existing
fields (`id`, `owned_by`, `provider`, `upstream_model`, `vision`) remain valid; a plain
OpenAI-compatible `/models` response that declares no capabilities stays unknown.

### 3.1 Ownership and precedence

The container owns profiles because it owns the provider route and exact model selection.
VS Code resolves, in order:

1. an explicit per-model profile saved with its curated model entry;
2. capabilities returned by the configured gateway catalog;
3. no profile.

Conflicting sources are not merged field by field. The first complete profile wins and its
source is shown in model settings. The SDK has no hard-coded model-name prefixes, dates, or
aliases.

An absent profile is compatibility mode, not a claim that the model has no capabilities:
OpenAI keeps the current `reasoning_effort` behavior when effort is set; Anthropic keeps the
current budget-thinking behavior. An explicit `ThinkingNone` rejects non-default effort before
the HTTP request. An explicit profile that omits the selected effort from `EffortLevels` also
fails locally; no silent downgrade is allowed.

## 4. Provider request semantics

`llm.Request` carries `Capabilities ModelCapabilities` from the selected `Model`. Adapters
validate only explicit profiles.

### 4.1 OpenAI-compatible

`ThinkingOpenAIEffort` sends the existing top-level
`reasoning_effort: "<low|medium|high>"`. `ThinkingNone` plus non-empty effort is rejected.
Unspecified preserves today's body. `MaxOutputTokens` continues to map to
`max_completion_tokens`; an explicit `MaxOutputTokens: false` profile rejects a non-zero
ceiling.

### 4.2 Anthropic budget

`ThinkingAnthropicBudget` keeps today's mapping:

```json
{
  "thinking": {"type": "enabled", "budget_tokens": 4096},
  "max_tokens": 12288
}
```

The existing low/medium/high budgets and `anthropicMaxTokens` floor remain the compatibility
contract. Opaque signed thinking blocks in `Message.ProviderState` retain their current
run-scoped behavior.

### 4.3 Anthropic adaptive

`ThinkingAnthropicAdaptive` sends:

```json
{
  "thinking": {"type": "adaptive"},
  "output_config": {"effort": "medium"},
  "max_tokens": 8192
}
```

`output_config` is omitted only when effort is empty; `thinking.type` remains `adaptive`
because the profile selected the protocol. Adaptive mode has no `budget_tokens`, and
`MaxOutputTokens` is not raised by a legacy thinking floor. A configured positive ceiling is
sent as `max_tokens`; zero keeps the adapter default. Provider-state preservation for tool use
is unchanged.

The request structs use separate legacy/adaptive representations so an impossible body cannot
contain both `budget_tokens` and `output_config.effort`.

## 5. Retry and truncation wire

The public event names already exist. The host admits them and adds fields:

```go
type EventParams struct {
    // existing fields...
    Attempt     int   `json:"attempt,omitempty"`
    MaxAttempts int   `json:"max_attempts,omitempty"`
    DelayMS     int64 `json:"delay_ms,omitempty"`
}

type DoneParams struct {
    // existing fields...
    Truncated bool `json:"truncated,omitempty"`
}
```

The public retry event and its internal retry callback gain `MaxAttempts`, populated from the
resolved `RetryPolicy`; the editor host must not guess the denominator from a default.
TypeScript adds `retry | truncated` to `EventType`, the same fields to `HostEvent`, and
`truncated?: boolean` to `DoneParams`.

Projection rules:

- `retry` carries the failed attempt number, resolved maximum attempts, bounded delay, and
  provider message in `text`. It is emitted before waiting and is not terminal.
- `truncated` is a signal only. Although the current runtime event carries the partial answer
  in `Text`, VS Code must not append it: assistant deltas already rendered that text. The event
  draws a notice.
- `done.truncated` is authoritative for terminal UI and reconnect races. It is projected from
  `RunResult.Truncated`.
- A final answer is truncated only when its final no-tool completion stopped for length, as
  already enforced by `runtime.Loop`. A length stop on an intermediate tool-call round does
  not mark the run.

## 6. VS Code controls and data flow

The model control adds:

- an output-token limit: Off or a positive integer, stored per conversation and sent as
  `max_output_tokens`;
- capability badges and source in model settings;
- effort choices filtered by an explicit profile. Unknown profiles keep all current choices
  but label support as unverified.

During a retry, the active response shows “Provider busy — retrying in 2.0s (attempt 2/3).”
The row updates in place and disappears after the next provider delta. The exact provider
message remains available in details. Cancellation during backoff uses the existing Cancel
path and removes the row.

When `done.truncated` is true, the partial answer remains a normal assistant answer followed
by a warning and a **Continue generating** button. Nothing is sent until the user activates
that button.

### 6.1 User-controlled continuation

Continuation uses the existing `prompt` method with two additive fields:

```text
continue_from: <run_id>
text: "Continue the previous truncated answer from exactly where it stopped. Do not repeat completed content."
```

The text is fixed by the client and appears in the transcript as a user action; continuation
is a new run with normal usage, permissions, retries, cancellation, and output ceiling.
`continue_from` lets the host validate that the named run is this session's latest completed
run and was truncated. It does not splice provider state or bypass `Agent.Send`.

The button disables immediately after activation. A stale, non-truncated, foreign, running, or
already-continued run is refused with `invalid params`. The user may instead edit/send any
ordinary message; doing so dismisses the continuation affordance. Repeated truncation presents
a new button for the new run and still requires another explicit action.

## 7. Core invariants

- **No model guessing:** SDK behavior does not branch on model id text.
- **One thinking protocol:** an Anthropic request contains legacy budget fields or adaptive
  fields, never both.
- **No thought leakage:** adaptive support changes request controls only; reasoning text still
  is not persisted or echoed, except existing opaque signed provider blocks within a tool turn.
- **No silent downgrade:** explicit unsupported effort/output limits fail before transport.
- **Exactly one partial answer:** `truncated` never duplicates assistant text.
- **No automatic continuation:** a length stop performs exactly one provider completion until
  a user action starts another run.
- **Authoritative terminal state:** `done.truncated`, not timing of a transient event, controls
  the button.

## 8. Security and failure semantics

Malformed profiles, unknown protocol strings, duplicate effort levels, non-positive limits,
and explicit provider/protocol contradictions fail session creation with a field-specific
error. Profiles are configuration, not model output, and are never accepted from a chat
message or tool result.

Provider rejection remains `request_rejected`; the adapter does not retry after stripping
adaptive thinking or effort. Retry events expose no API key or headers and use the already
sanitized provider error text. Delay is clamped by `RetryPolicy`; negative or overflowing wire
values are impossible after duration conversion.

A continuation is ordinary model work, not permission to repeat tools. The host accepts it
only after a truncated final answer with no outstanding tool call. All existing tool
permissions remain active on the new run.

## 9. Migration and compatibility

- Empty capabilities preserve current OpenAI and Anthropic request bodies. This is essential
  for SDK callers and gateways that do not yet publish profiles.
- Existing curated model ids migrate with an unknown profile; no name-based adaptive profile
  is synthesized.
- Optional catalog capability fields are additive. Unknown/older catalogs remain usable.
- Older clients ignore new event fields, but the repository's synchronized VS Code client
  gains the new event types in the same delivery.
- `MaxOutputTokens == 0` remains no explicit ceiling. No default limit is introduced during
  migration.
- Existing runs and transcripts have no `truncated` terminal metadata to reconstruct; replay
  shows the stored partial text but does not invent a Continue button.

## 10. Test strategy

Adapter golden tests cover unspecified byte identity, all explicit protocols, every effort,
adaptive with/without output limit, impossible mixed fields, unsupported capabilities, plain
and streaming responses, and signed provider-state tool continuation.

Editor-host tests assert retry attempt/maximum/delay projection, ordering, cancellation during
backoff, truncated event projection, authoritative `done.truncated`, and no duplicate text.
Protocol tests reject malformed profiles and stale/foreign continuation ids.

VS Code tests cover profile precedence, unknown-profile wording, effort filtering, output
limit persistence/reopen, in-place retry status, truncation warning, one-click/keyboard
continuation, double-click suppression, ordinary-message dismissal, and repeated truncation.
An integration fixture counts provider calls: truncation alone must leave the count at one;
one explicit Continue must make it exactly two.

## 11. Non-goals

- A SDK-maintained model catalog or model-id heuristics.
- Automatic continuation, invisible continuation, or stitching two answers into one stored
  assistant message.
- Persisting chain-of-thought.
- Retrying mid-stream after visible output.
- Changing retry policy defaults or adding stage/workflow retries.
- Automatically choosing effort, output limit, model, or mode from task complexity.
- Claiming capabilities that a plain `/models` endpoint did not declare.

## 12. Delivery phase mapping

| Phase | Contract |
|---|---|
| p_0.53.1 | Capability profile types/validation, Anthropic adaptive request path, legacy/unspecified compatibility, stage inheritance, and model/catalog/session wire |
| p_0.53.2 | Retry/truncated editor-host wire, retry attempt/maximum/delay, `done.truncated`, live/replay ordering, and privacy tests |
| p_0.53.3 | VS Code output ceiling, capability/status presentation, explicit Continue action, and no-auto-continue acceptance |
