# 09 — Extensibility

**Milestone focus:** 0.3 (design now, implement later).

## Custom tools

- Allow callers to register tools on `Options` or Agent:
  - Name, description, JSON schema, `Execute` func.
- Merge with built-ins; name collisions → create-time error.
- Custom tools still subject to cwd policy if they use shared path helpers.

## MCP

- Integrate MCP stdio clients (e.g. `mark3labs/mcp-go`) into the tool registry.
- Config shape conceptually:

```text
MCPServers: map[name] => { Command, Args, Env } or HTTP URL
```

- On Resume: re-pass MCP configs (not persisted with secrets).

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
- Do not invent hidden multi-agent routing in 0.1–0.3.

## Compatibility promise

- Extensibility features must not break the Create/Prompt/Send/Stream/Wait core.
- Prefer additive options; avoid renaming public types without a major version.
