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
| Streaming | 0.1: non-streaming per step OK; SSE later per provider |
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
**Later:** Provider SSE with incremental deltas.

## Mocking

- Inject `Completer` in tests (no network).
- Examples: set `Provider`, `BaseURL`, `APIKey`, `Model.ID` from env.
