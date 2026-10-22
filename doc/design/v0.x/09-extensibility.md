# 09 — Extensibility

**Milestone focus:** 0.3 (stdio MCP / custom tools / Permission / compression **implemented**); 0.6 adds HTTP/SSE MCP; depth in **[m_0.9_extensibility.md](../../roadmaps/v0.x/m_0.9_extensibility.md)** (**0.9 done**). SDK config parity → **[10-sdk-parity.md](10-sdk-parity.md)** / **0.10**.

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
  *(Superseded by 0.44: the host now owns the OAuth flow and the tokens, see [44-mcp-management.md](44-mcp-management.md) §6. Static headers still work.)*

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

## Sub-agents / workers

- **In-process** multi-agent graph / hidden routing inside `anvil.Create` remains out of scope.
- **Process-level** workers are designed in **[11-worker-daemon.md](11-worker-daemon.md)** / product **0.11** (`agentd` + `agent-worker`), not as Cursor-style nested sub-agents in the library.

## Hooks & skills (deepen)

- 0.10 ships minimal file hooks; deeper hooks + **skills** → **[14-hooks-skills.md](14-hooks-skills.md)** / **0.14**.

## Retrieval

- Builtin index / `semantic_search` → **[13-retrieval.md](13-retrieval.md)** / **0.13**.
- Web / `fetch_url` → **[15-web-search.md](15-web-search.md)** / **0.15**.
- LSP / git / test-build / vision → **[16](16-lsp.md)**–**[19](19-vision.md)**.
- Worker-side **plugins** (capability packs) → **[20-plugins.md](20-plugins.md)** / **0.20** (distinct from MCP and skills).
- **Dev memory** as first-party plugin → **[21-dev-memory.md](21-dev-memory.md)** / **0.21** (not session Resume).

## Compatibility promise

- Extensibility features must not break the Create/Prompt/Send/Stream/Wait core.
- Prefer additive options; avoid renaming public types without a major version.
