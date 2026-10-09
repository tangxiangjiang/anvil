# 07 — Streaming and errors

**Milestone focus:** core events in 0.1–0.6; remaining hardening in **[m_0.7_streaming_errors.md](../../roadmaps/v1.0/m_0.7_streaming_errors.md)** (0.7).

## Event model

Stream delivers `Event` values until closed.

| Type | When | Payload highlights |
|------|------|--------------------|
| `progress` | Start of each LLM round (0.6+) | `Step` (1-based), `MaxSteps` |
| `assistant` | Model text (full or delta) | `Text` |
| `tool_call` | Before/as tool executes | `ToolCallID`, `Name`, `Args` JSON |
| `tool_result` | After tool returns | `ToolCallID`, `Content`, `IsError` |
| `done` | Terminal success path | mirrors status finished |
| `error` | Terminal failure on stream | message / code |

MaxSteps exceeded yields `RunResult.Status=error` with a stable message; callers may use `anvil.IsMaxStepsExceeded` (0.6+). Default MaxSteps when unset is **128**.

Callers must still use `Wait` for authoritative `RunResult`.

## RunResult status

| Status | Meaning |
|--------|---------|
| `finished` | Normal completion |
| `error` | Loop or tool/LLM failure after start |
| `cancelled` | Explicit cancel or ctx cancel (0.2+) |

## Error type

```go
type Error struct {
    Message   string
    Retryable bool
    Cause     error
}
```

| Situation | Mechanism |
|-----------|-----------|
| Missing API key, bad cwd, invalid options | `Create`/`Prompt` returns `*Error`, Retryable=false |
| 429 / transient network | `*Error`, Retryable=true |
| Tool path escape | Tool result `IsError` to model; run may continue |
| MaxSteps exceeded | `RunResult.Status=error` |
| Panic in tool | Recover, convert to tool error; do not crash process |

## Logging guidance

- Log `agentID` and `runID` as soon as known.
- Never log API keys or full shell env.
- Prefer structured fields over free-text only.

## CI exit code convention (examples)

1. Startup `*Error` → exit 1  
2. `RunResult.Status == error` → exit 2  
3. `finished` → exit 0  
