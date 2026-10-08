# 09 — Extensibility

**Milestone focus:** 0.3 (stdio MCP / tools / hooks **implemented**); 0.6 adds HTTP/SSE MCP.

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
}
```

- On Resume: re-pass MCP configs (not persisted with secrets).
- OAuth / browser auth for remote MCP is out of scope.

## Permission hooks

- Optional callbacks before mutating tools (`write_file`, `edit_file`, `shell`):
  - Approve / deny / modify args.
- Default: allow all enabled tools (current MVP behavior).

## Context compression

- When history exceeds a token/char budget, summarize older turns into a single system/context message.
- Preserve recent N turns verbatim.
- Store summary markers in JSONL for Resume consistency.

## Sub-agents

- Out of scope until a future milestone explicitly redesigns architecture.
- Do not invent hidden multi-agent routing in 0.x without an explicit redesign milestone.

## Compatibility promise

- Extensibility features must not break the Create/Prompt/Send/Stream/Wait core.
- Prefer additive options; avoid renaming public types without a major version.
