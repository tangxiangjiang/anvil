# 06 — Session store

## Goals

- Survive process restart: `Resume(agentID)` restores conversation and options needed to continue.
- Keep format debuggable: **JSONL**, one record per line.

## Layout

```text
{cwd}/.anvil/
  agents/
    agent-<ulid>.jsonl
```

- Agent ID format: `agent-<ulid>` (or ULID without prefix if we prefer; pick one and stick to it in 0.2).
- Optional: `runs/` index later; MVP of 0.2 can embed run markers in the same JSONL.

## Record kinds (conceptual)

| Type | Fields |
|------|--------|
| `meta` | agent_id, created_at, cwd, model, options snapshot |
| `message` | role, content, tool_call_id, tool_calls, timestamp |
| `summary` | compression marker (0.9+); Resume keeps this + later messages only |
| `run_start` / `run_end` | run_id, status, steps, usage |

Exact JSON schema is finalized in [m_0.2/p_0.2.1.md](../../roadmaps/v1.0/m_0.2/p_0.2.1.md). Design requires append-only writes and crash-safe "last valid line" reads.

Agent ID format in code: `agent-<hex>` (random), file name `{agentID}.jsonl`.

## Resume behavior

1. Resolve store from `opts.Local.CWD` (required).
2. Open `agents/{id}.jsonl`; if missing → error (not retryable).
3. Rebuild message history; re-create tool registry from options.
4. Inline MCP/custom tools are **not** restored from disk (pass again on Resume) — same rule as Cursor SDK.

## 0.1 vs 0.2

- **0.1:** in-memory history only for the process lifetime; `Resume` returned not-implemented.
- **0.2.1:** JSONL under `{cwd}/.anvil/agents/` + working `Resume`.

## Backends (0.6+)

| Backend | Layout | Notes |
|---------|--------|--------|
| `jsonl` (default) | `{cwd}/.anvil/agents/{id}.jsonl` | Debuggable, append-only |
| `sqlite` | `{cwd}/.anvil/agents.db` | Optional; selected via `LocalOptions.Store` |

No automatic migration between backends; pick one per cwd (or migrate offline yourself).

## Non-goals

- Multi-machine sync, encryption at rest (caller can encrypt the disk).
- Automatic JSONL ↔ SQLite migration tooling.
