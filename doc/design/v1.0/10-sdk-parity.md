# 10 — Cursor SDK local parity

**Product version focus:** **0.10** ([m_0.10_sdk_parity.md](../../roadmaps/v1.0/m_0.10_sdk_parity.md))  
**Status:** done (surface parity through p_0.10.1–p_0.10.5)

## Intent

Anvil’s public local surface should stay **capability-aligned** with Cursor SDK local agents (`Agent.create` / `prompt` / `resume` / `send` / `stream` / `wait` / `cancel` / `close`, plus the local config knobs hosts rely on).

Alignment means **same jobs for the caller**, idiomatic Go — not protocol compatibility, not embedding the Cursor runtime, not matching Composer/IDE exploration quality.

## Checklist (Cursor SDK local → Anvil)

| Cursor SDK local capability | Anvil |
|-----------------------------|--------|
| Create / Prompt / Resume / Send / Stream / Wait / Cancel / Close | done (≤0.6) |
| Local `cwd` + sandbox tools | done |
| OpenAI-compatible + Anthropic | done |
| Session store + Resume | done |
| Custom tools / Permission | done (0.3) |
| MCP stdio + HTTP/SSE + headers | done (0.6 / 0.9) |
| List / Get agents | done (0.5) |
| Progress / MaxSteps / CLI | done (0.6) |
| Stable error codes / wall vs deadline | done (0.7) |
| `local.settingSources` (`project` / `user`) | done (0.10) |
| `.cursor/mcp.json` project/user load | done (0.10) |
| Public `SystemPrompt` override | done (0.10) |
| Builtin allow / deny (`BuiltinTools` / `DisallowedTools`) | done (0.10) |
| Force busy Send | done (0.10) |
| Reload file MCP/rules/hooks | done (0.10) |
| ListRuns / GetRun | done (0.10) |
| Project hooks.json (minimal shell/write gate) | done (0.10) |
| Prewarm local workspace helper | done (0.10) |
| Full Cursor hooks event catalog | deferred (subset deepen → [14](14-hooks-skills.md) / 0.14) |
| Skills / skill router | deferred to [14](14-hooks-skills.md) / 0.14 |
| Model catalog API | deferred to [12](12-model-gateway.md) / 0.12 |
| Semantic / index search | deferred to [13](13-retrieval.md) / 0.13 |
| Web search / fetch | deferred to [15](15-web-search.md) / 0.15 |
| Git / test / build structured tools | deferred to [17](17-git-tools.md)–[18](18-test-build.md) |
| Vision / image attachments | deferred to [19](19-vision.md) / 0.19 |
| MCP OAuth browser login | deferred |
| Cursor Bridge / embed Cursor runtime | deferred |
| Cloud agents / dashboard MCP | deferred |
| Sub-agent framework (Cursor-style in-process) | deferred (process workers → [11](11-worker-daemon.md) / 0.11) |
| IDE / LSP deep integration | deferred (LSP **tools** subset → [16](16-lsp.md) / 0.16; not IDE UI) |
| Composer / semantic-search quality parity | deferred (not surface) |

## 0.10 API notes

### SettingSources

- `LocalOptions.SettingSources`: `project`, `user`; empty = inline-only.
- Project: `{cwd}/.cursor/mcp.json`; also loads `.cursor/rules` into the system prompt.
- User: `~/.cursor/mcp.json`.
- Precedence: inline `MCPServers` → project → user.
- Hooks: `.anvil/hooks.json` or `.cursor/hooks.json` when `project` is enabled.

### SystemPrompt and tool filters

- `Options.SystemPrompt` replaces the default Anvil main prompt; rules still append when enabled.
- `BuiltinTools` / `DisallowedTools`: names or groups `shell|read|write|edit|search|mcp`. Custom `Tools` are never filtered out.

### Force / Reload / ListRuns

- `Send(..., WithForce())` cancels the in-flight run then accepts the new message.
- `Agent.Reload` rebuilds registry/prompt/hooks from files.
- `ListRuns` / `GetRun` / `Agent.ListRuns` read `run_start` / `run_end` from the store.

### Hooks (minimal)

```json
{
  "version": 1,
  "beforeShell": [{"denyContains": "rm -rf", "message": "blocked"}],
  "beforeWrite": [{"denyContains": "/etc/", "message": "blocked"}]
}
```

Deny → tool error to the model. Full Cursor hook catalog remains deferred.

### Prewarm

`Prewarm(ctx, cwd, sources) (release func(), err)` resolves MCP/rules/hooks paths without the Cursor app.

## Explicit non-goals (remain)

- Cursor Bridge / calling Cursor agent runtime  
- Cloud agents and dashboard MCP  
- Drop-in import replacement for `@cursor/sdk`  
- Sub-agent framework parity (in-process); see [11](11-worker-daemon.md) for agentd workers  

- IDE / LSP deep integration  
- Claiming equal large-repo intelligence without separate quality work  

## Roadmap mapping

Implemented via [m_0.10_sdk_parity.md](../../roadmaps/v1.0/m_0.10_sdk_parity.md) phases `p_0.10.1` … `p_0.10.5`.
