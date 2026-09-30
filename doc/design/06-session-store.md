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
| `run_start` / `run_end` | run_id, status, steps, usage |

Exact JSON schema is finalized in milestone 0.2 phase docs; design requires append-only writes and crash-safe "last valid line" reads.

## Resume behavior

1. Resolve store from `opts.Local.CWD` (required).
2. Open `agents/{id}.jsonl`; if missing → error (not retryable).
3. Rebuild message history; re-create tool registry from options.
4. Inline MCP/custom tools are **not** restored from disk (pass again on Resume) — same rule as Cursor SDK.

## 0.1 scope

- Agent IDs may be allocated in memory only.
- Persistence and `Resume` ship in **0.2**.
- Design this chapter so 0.1 code leaves extension points (e.g. optional `Store` interface no-op).

## Non-goals

- Multi-machine sync, encryption at rest (caller can encrypt the disk).
- SQLite (deferred unless JSONL proves insufficient).
