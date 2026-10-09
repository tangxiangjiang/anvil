# 10 — Cursor SDK local parity

**Product version focus:** **0.10** ([m_0.10_sdk_parity.md](../../roadmaps/v1.0/m_0.10_sdk_parity.md))  
**Status:** planned (design frozen for roadmap; implement only via 0.10 phases)

## Intent

Anvil’s public local surface should stay **capability-aligned** with Cursor SDK local agents (`Agent.create` / `prompt` / `resume` / `send` / `stream` / `wait` / `cancel` / `close`, plus the local config knobs hosts rely on).

Alignment means **same jobs for the caller**, idiomatic Go — not protocol compatibility, not embedding the Cursor runtime, not matching Composer/IDE exploration quality.

Chapters **00–09** already define the core loop, tools, providers, store, streaming, security, and extensibility. This chapter only records what **still must land for 0.10** relative to today’s Cursor SDK *local* docs.

## Already covered in 00–09 / shipped ≤0.6

| Capability | Where |
|------------|--------|
| Create / Prompt / Resume / Send / Stream / Wait / Cancel / Close | 02, 03 |
| Local `cwd`, sandbox tools | 04, 08 |
| OpenAI-compatible + Anthropic | 05 |
| Session store + Resume | 06 |
| Events / errors / usage | 07 |
| Custom tools, MCP (stdio + HTTP/SSE), permission hook | 09, 0.3/0.6 |
| List / Get | 0.5 |
| Progress, store backends, CLI | 0.6 |

## 0.10 additions (design requirements)

### Config layers — `settingSources`

- Empty / omitted: **inline only** (current default): `Options.MCPServers`, explicit rules flags.
- `project`: load `.cursor/mcp.json` under cwd; continue supporting Anvil rules paths (`AGENTS.md`, `.anvil/rules`, optional `.cursor/rules` when enabled).
- `user`: load `~/.cursor/mcp.json` (and document any Anvil user-level path).
- Precedence (high → low): per-send inline (if added) → Create inline → project → user.
- Inline MCP still **not** restored on Resume; file sources may be re-read when sources are enabled.

### System prompt and tool filters

- Public **`SystemPrompt`**: replaces Anvil’s default main system text for the primary loop; tool schemas and MCP registration still apply.
- **Built-in allow/deny** lists (Cursor’s `tools` / `disallowedTools` idea): filter built-ins and/or groups (`shell`, `mcp`, …). Must not collide confusingly with custom `ToolSpec` registration — prefer distinct field names in Go if `Tools` is already taken.
- Not persisted across Resume unless passed again.

### Run control and inspection

- **`Force`**: if a run is in flight, cancel/expire it then accept the new `Send` (document exact status of the cancelled run).
- **`Reload`**: re-read file-based MCP / rules / hooks without disposing the agent.
- **`ListRuns` / get run**: expose `run_start` / `run_end` (and ids) from the store for operators.

### Hooks (minimal)

- File-based project hooks (`.cursor/hooks.json` and/or `.anvil/hooks.json`) when project sources are on.
- 0.10 minimum: gate **shell** (and optionally write/edit) before execution; deny → tool error to the model.
- Full Cursor hook catalog is **out of scope** for 0.10.

### Prewarm (optional but specified)

- Host helper to resolve rules / MCP / ignore-style scans ahead of first `Send` for large checkouts (Cursor’s `prewarmLocalWorkspace` role). Best-effort cache; must not require the Cursor app.

### MCP HTTP extras

- Request **headers** on HTTP/SSE MCP configs (prefer landed in 0.9; required for hosts by 0.10). OAuth browser login remains deferred.

## Explicit non-goals (remain)

- Cursor Bridge / calling Cursor agent runtime  
- Cloud agents and dashboard MCP  
- Drop-in import replacement for `@cursor/sdk`  
- Sub-agent framework parity  
- IDE / LSP deep integration  
- Claiming equal large-repo intelligence without separate quality work  

## Roadmap mapping

Implement only through [m_0.10_sdk_parity.md](../../roadmaps/v1.0/m_0.10_sdk_parity.md) phases `p_0.10.1` … `p_0.10.5`. Do not implement from this chapter alone.

## Checklist deliverable

Phase `p_0.10.5` publishes a living table: Cursor SDK local feature → Anvil status (`done` / `0.10` / `deferred`), kept in sync with this chapter.
