# 09 — Extensibility

**Milestone focus:** 0.3 (stdio MCP / custom tools / Permission / compression **implemented**); 0.6 adds HTTP/SSE MCP; depth in **[m_0.9_extensibility.md](../../roadmaps/v1.0/m_0.9_extensibility.md)** (**0.9 done**). SDK config parity → **[10-sdk-parity.md](10-sdk-parity.md)** / **0.10**.

## Custom tools

- Register via `Options.Tools` (`ToolSpec` + `ToolHandler`).
- Merge with built-ins; name collisions → create-time error.

## MCP

- Integrate MCP clients into the tool registry.
- Config shape:

```text
MCPServers: map[name] => {
  Transport: stdio | sse | http   // empty + Command => stdio
  Command, Args, Env              // stdio
  URL                             // sse / http
  Headers                         // http/sse only (0.9+); ignored for stdio
}
```

- On Resume: re-pass MCP configs (not persisted with secrets).
- Static `Headers` (e.g. `Authorization`) are supported for remote MCP; OAuth / browser auth remains out of scope.

## Permission hooks

- Optional callbacks before mutating tools (`write_file`, `edit_file`, `shell`):
  - Approve / deny / modify args.
- Default: allow all enabled tools (current MVP behavior).

## Context compression (0.9+)

- When history exceeds a char budget (`HistoryMaxChars`), summarize older turns; keep recent N (`HistoryKeepRecent`, default 6).
- Persist a store record `type=summary` (message Name `anvil.summary`).
- On Resume: load **last summary + messages after it** only — do not rebuild the full pre-compression transcript.

## Parallel tools (0.9+)

- `Options.ParallelTools` (default off): same-turn tool_calls may run concurrently.
- Batches that include mutating tools (`write_file` / `edit_file` / `apply_patch` / `shell`) stay sequential.
- Event order: all `tool_call` in assistant order, then all `tool_result` in the same start order (not completion order).
- `ParallelToolsMax` bounds concurrency (default 4).

## Sub-agents

- Out of scope until a future milestone explicitly redesigns architecture.
- Do not invent hidden multi-agent routing in 0.x without an explicit redesign milestone.

## Compatibility promise

- Extensibility features must not break the Create/Prompt/Send/Stream/Wait core.
- Prefer additive options; avoid renaming public types without a major version.
