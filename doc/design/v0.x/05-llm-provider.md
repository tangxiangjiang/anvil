# 05 — LLM provider

## Goal

Support **two** wire protocols only (not Cursor runtime):

1. **OpenAI-compatible** Chat Completions — `POST {BaseURL}/chat/completions`
2. **Anthropic** Messages API — `POST {BaseURL}/v1/messages` (Claude)

Runtime always speaks an internal `Completer` interface; each provider adapter maps tools ↔ provider-specific tool schemas.

## Provider selection

```go
type Provider string

const (
    ProviderOpenAI    Provider = "openai"    // default
    ProviderAnthropic Provider = "anthropic"
)

// Options.Provider selects the adapter.
// Options.BaseURL overrides the default endpoint root for that provider.
// Options.APIKey  (or ANVIL_API_KEY) used for auth.
```

| Provider | Default BaseURL | Auth | Tool calling |
|----------|-----------------|------|--------------|
| `openai` | `https://api.openai.com/v1` | `Authorization: Bearer` | `tools` / `tool_calls` on chat completions |
| `anthropic` | `https://api.anthropic.com` | `x-api-key` + `anthropic-version` | Messages `tools` / `tool_use` / `tool_result` |

OpenAI-compatible gateways (DeepSeek, private `/v1`, vLLM, etc.) use `ProviderOpenAI` with a custom `BaseURL`, e.g. model `private/deepseek-v4-pro`.

Anvil’s own **model gateway** (catalog + auth + proxy) is designed in **[12-model-gateway.md](12-model-gateway.md)** / product **0.12**; point `BaseURL` + API key at that service.

## Example requests

**OpenAI-compatible**

```http
POST /v1/chat/completions
```

```json
{
  "model": "private/deepseek-v4-pro",
  "messages": [{"role": "user", "content": "你好"}],
  "tools": []
}
```

**Anthropic**

```http
POST /v1/messages
```

```json
{
  "model": "claude-sonnet-4-5",
  "max_tokens": 8192,
  "messages": [{"role": "user", "content": "你好"}],
  "tools": []
}
```

## Responsibilities (`internal/llm`)

| Concern | Behavior |
|---------|----------|
| Adapters | `openai` and `anthropic` packages/files behind one `Completer` |
| Chat | Normalize to internal messages; map tool defs and tool results per provider |
| Streaming | 0.1: non-streaming per step OK; SSE later per provider. Each delta carries visible text and, where the provider has one, its reasoning step |
| Errors | Map 401/429/5xx (and Anthropic error bodies) to retryable flags |
| Usage | Parse token usage when present |

## Public vs internal types

- Public API uses Anvil message/event types only.
- Wire JSON stays inside `internal/llm`.
- Do not export official OpenAI/Anthropic SDK types from `package anvil`.
- Prefer stdlib `net/http` + JSON for MVP (no required vendor SDK).

## Model selection

- `Options.Model.ID` required.
- No hard-coded vendor allowlists beyond examples.
- Wrong `Provider` for a BaseURL is a caller config error.

## Streaming strategy

**0.1:** Reliable non-streaming request per loop step; coarse `assistant` events.  
**0.4:** `StreamCompleter.CompleteStream` for OpenAI-compatible and Anthropic SSE; runtime emits incremental `assistant` deltas when available, with non-stream Completer fallback.  
**0.35.9:** `llm.Delta` also carries the provider's **reasoning** where the wire format has one.

**Reasoning deltas (0.35.9).** A reasoning model on an OpenAI-compatible endpoint streams its
chain of thought in a *separate* delta field — `reasoning_content` on DeepSeek, vLLM and LM
Studio, `reasoning` on OpenRouter. `Delta.Reasoning` carries it and the runtime turns it into
`anvil.EventReasoning`, which is display-only: the UI shows it in a collapsed block and the
SDK does nothing else with it.

Three rules make this safe, and all three are about *not* letting the field travel back:

- **The wire JSON stays inside `internal/llm`.** The delta struct in `openai_stream.go` is
  decode-only and anonymous, and `llm.Message` gains no field — so reasoning has no path into
  `toOpenAIMessages`. This is not stylistic: `openAIMessage` is **shared** between the request
  encoder and the response decoder, and a decode field added there is exactly how private
  reasoning would later be echoed upstream. A test asserts the serialized request body
  contains no `reasoning` key.
- **It is never requested.** No `reasoning_effort`, `include_reasoning`, or Anthropic
  `thinking` parameter is sent. What the provider volunteers is what is shown.
- **Anthropic is out of scope.** Its extended thinking is not display-only: with tool use the
  signed `thinking` block must be echoed back in the next request, and nothing in
  `fromAnthropicContent` / `toAnthropicMessages` can carry a signed opaque block through
  history rewriting. A separate change, not a field.

## Mocking

- Inject `Completer` in tests (no network).
- Examples: set `Provider`, `BaseURL`, `APIKey`, `Model.ID` from env.
