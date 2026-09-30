# 07 — Streaming and errors

## Event model

Stream delivers `Event` values until closed.

| Type | When | Payload highlights |
|------|------|--------------------|
| `assistant` | Model text (full or delta) | `Text` |
| `tool_call` | Before/as tool executes | `ToolCallID`, `Name`, `Args` JSON |
| `tool_result` | After tool returns | `ToolCallID`, `Content`, `IsError` |
| `done` | Terminal success path | mirrors status finished |
| `error` | Terminal failure on stream | message / code |

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
