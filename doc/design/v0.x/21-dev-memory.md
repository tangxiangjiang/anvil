# 21 — Development memory (plugin)

**Product version focus:** **0.21** ([m_0.21_dev_memory.md](../../roadmaps/v1.0/m_0.21_dev_memory.md))  
**Status:** done

## Intent

Add **development memory**: durable, queryable notes about a codebase (conventions, pitfalls, decisions) that survive beyond a single chat transcript.

**Delivery: first-party plugin** (`id: memory` / `anvil.memory`), loaded on **agent-worker** (or in-process via `Options.Plugins`). Not part of the zero-plugin core agent.

Rationale: memory is valuable but **optional** — storage shape, retention, and privacy policy vary by host; keeping it a plugin preserves a thin core ([20](20-plugins.md)) while still shipping a reference pack in-tree.

## Distinct from session context

| Layer | What | Where |
|-------|------|--------|
| **Chat / session** (core, already shipped) | Transcript, Resume, `HistoryMaxChars` summary | `.anvil/agents/*.jsonl` |
| **Rules / skills** | Static instructions | AGENTS.md, skills dirs |
| **Dev memory** (this chapter) | Explicit or agent-written facts, searchable across runs | plugin-owned store under cwd |

Compression summaries are **not** a substitute for memory (they are lossy chat rolls). Memory entries are intentional, addressable records.

## Placement

```text
agent-worker / anvil (with Plugins: ["memory"])
        │
        ▼
plugin memory
  tools: memory_write / memory_read / memory_search / memory_list
        │
        ▼
{cwd}/.anvil/memory/     # default; sandbox-bound
  entries.jsonl | sqlite
  (optional vectors via embed — reuse 0.13 / gateway)
```

- Plugin permissions: `fs.read`, `fs.write` under memory dir only (Host-enforced).
- No net unless search uses remote embed via existing gateway config (optional).

## Tools (sketch)

| Tool | Args | Behavior |
|------|------|----------|
| `memory_write` | key?, text, tags[]?, scope? | Upsert entry; return id |
| `memory_read` | id or key | Fetch one entry |
| `memory_search` | query, tags?, top_k? | Lexical (+ optional vector) over entries |
| `memory_list` | limit?, tag? | Recent / tagged ids |
| `memory_delete` | id | Optional; gated by policy |

`scope`: `project` (default, cwd) | later `user` (explicit path outside cwd — **not** in 0.21 MVP).

## Entry schema

```json
{
  "id": "mem_…",
  "key": "optional-stable-key",
  "text": "Prefer table-driven tests in package foo",
  "tags": ["testing", "foo"],
  "created_at": "…",
  "updated_at": "…",
  "source": "agent|user|import"
}
```

Size caps per entry and total store; redact secrets on write (align 0.8).

## When the model should use it

- Plugin may inject a short `sessionStart` note: “memory tools available; persist durable project facts.”
- Auto-write of every turn is **non-goal** (noisy); rely on model + skills, or host hooks later.

## Relation to core

- **Core keeps** Resume / compression / rules — always.
- **Memory plugin** does not replace Resume; hosts that omit the plugin simply have no cross-run fact store.
- Do not move session JSONL into the memory plugin.

## Security / privacy

- Default store only under `{cwd}/.anvil/memory/` (gitignore recommended in docs).
- No upload to cloud in 0.21.
- `memory_delete` + retention policy config (max entries / max age) in plugin config.

## Explicit non-goals

- Core builtin memory tools (rejected for 0.21 — plugin only).
- Global multi-repo brain without explicit design.
- Replacing 0.13 code index (memory is facts/notes, not source chunks).

## Roadmap mapping

[m_0.21_dev_memory.md](../../roadmaps/v1.0/m_0.21_dev_memory.md) phases `p_0.21.1` … `p_0.21.3`.
