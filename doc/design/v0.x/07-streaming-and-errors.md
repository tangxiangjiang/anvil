# 07 — Streaming and errors

**Milestone focus:** core events in 0.1–0.6; hardening in **[m_0.7_streaming_errors.md](../../roadmaps/v0.x/m_0.7_streaming_errors.md)** (**0.7 done**).

## Event model

Stream delivers `Event` values until closed.

| Type | When | Payload highlights |
|------|------|--------------------|
| `progress` | Start of each LLM round (0.6+) | `Step` (1-based), `MaxSteps` |
| `assistant` | Model text (full or delta) | `Text` |
| `tool_call` | Before/as tool executes | `ToolCallID`, `Name`, `Args` JSON |
| `tool_result` | After tool returns | `ToolCallID`, `Content`, `IsError` |
| `done` | Terminal success path | mirrors status finished |
| `error` | Terminal failure on stream | `Text` message + `Code` (0.7+) |

MaxSteps exceeded yields `RunResult.Status=error` with `Code=max_steps`; callers may use `anvil.IsMaxStepsExceeded` / `IsMaxStepsExceededResult`. Default MaxSteps when unset is **128**.

Callers must still use `Wait` for authoritative `RunResult` (includes `Code`).

## Stable codes (0.7+)

| Code | Typical Status | Meaning |
|------|----------------|---------|
| `max_steps` | `error` | Hit `Options.MaxSteps` |
| `provider` | `error` | LLM/transport failure after run start |
| `cancelled` | `cancelled` | Explicit `Run.Cancel` or bare context cancel |
| `max_wall_time` | `cancelled` | `Options.MaxWallTime` exceeded |
| `context_deadline` | `cancelled` | Parent `context` deadline (not MaxWallTime) |
| `tool_panic` | reserved | Tool panic recovered as tool error today; code reserved |

Helpers: `ResultCode`, `FormatRunStart`, `FormatRunEnd`. Prefer `Code` over parsing English `Err` text.

## RunResult status

| Status | Meaning |
|--------|---------|
| `finished` | Normal completion |
| `error` | Loop or tool/LLM failure after start |
| `cancelled` | Explicit cancel, MaxWallTime, or parent ctx cancel/deadline (0.2+; codes distinguish in 0.7+) |

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
| MaxSteps exceeded | `RunResult.Status=error`, `Code=max_steps` |
| Panic in tool | Recover, convert to tool error; do not crash process |

## Logging guidance

- Log `agentID` and `runID` as soon as known (`anvil.FormatRunStart` after `Send`).
- Never log API keys or full shell env.
- Prefer `Code` / structured fields over free-text only.

## CI exit code convention (examples)

1. Startup `*Error` → exit 1  
2. `RunResult.Status == error` → exit 2  
3. `finished` → exit 0  
4. `cancelled` is not necessarily exit 2 (host choice); inspect `Code` for `max_wall_time` vs `context_deadline` vs `cancelled`
