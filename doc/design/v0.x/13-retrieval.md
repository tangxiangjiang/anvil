# 13 — Retrieval quality (index / semantic search)

**Product version focus:** **0.13** ([m_0.13_retrieval.md](../../roadmaps/v0.x/m_0.13_retrieval.md))  
**Status:** done

## Intent

Close the largest **feel** gap versus Cursor Local: beyond `grep` / `glob` / `read_file`, give the agent a **codebase retrieval** path — local index + semantic (embedding) search — without embedding Cursor’s proprietary harness.

Two delivery modes (prefer A, allow B):

| Mode | Mechanism |
|------|-----------|
| **A — Builtin** | Anvil tool(s) + on-disk index under `{cwd}/.anvil/` |
| **B — MCP** | Documented MCP server that exposes the same job; Anvil only registers MCP |

0.13 ships **A** as default; B is an integration note / optional example, not a hard dependency.

## Glossary

| Term | Meaning |
|------|---------|
| **Index** | Durable artifacts for a cwd: file inventory, chunks, embeddings (or BM25 postings). |
| **Chunk** | Addressable snippet: path + start/end line + text. |
| **Embedder** | HTTP service that returns vectors (typically the **model gateway** or compatible `/v1/embeddings`). |
| **semantic_search** | Builtin tool: query → top-k chunks with paths and excerpts. |

## Architecture

```text
                    index build / refresh
┌─────────┐       ┌──────────────────┐
│  files  │──────▶│  internal/index  │──▶ .anvil/index/
└─────────┘       └────────┬─────────┘
                           │ embeddings (optional)
                           ▼
                    model gateway /embeddings
                           │
agent tool loop ──semantic_search──▶ query embed + ANN/BM25 ──▶ chunks
```

## Index layout

**Concrete MVP storage** is defined in **[22-project-search.md](22-project-search.md)**: single SQLite `search.db` (tables + **FTS5** + embedded vectors / sqlite-vec). The earlier jsonl sketch is retired for implementation.

Respect existing path sandbox ([04](04-tools-and-sandbox.md), [08](08-security.md)): index only files inside cwd; skip secrets patterns (`.env`, keys) via denylist.

## Builtin tool

```text
semantic_search
  query: string
  path_glob?: string
  top_k?: number   # default 8, max bounded
```

Returns structured text (or JSON-in-text) listing path, line range, score, excerpt. Truncate per 0.4/tool limits.

Optional later: `index_status`, `reindex` as operator tools or CLI-only (`anvil index build`).

## Build & refresh

| Trigger | Behavior |
|---------|----------|
| CLI / Prewarm | `anvil index build <cwd>` or Prewarm hook option |
| Stale detect | Compare file hash/mtime; incremental re-chunk changed files |
| Agent start | Do **not** block Create on full rebuild; use last good index or empty + warn |

Embedding batch calls go through configured embed endpoint (`Options.EmbedBaseURL` or same as gateway). If unset, **BM25 / lexical fallback** still required so retrieval works offline.

## Ranking

0.13 minimum:

1. Lexical (BM25 or equivalent) always available.
2. Vector similarity when embeddings present.
3. Simple hybrid: merge + dedupe by chunk_id (weights in config).

No claim of Cursor Composer-quality recall; acceptance is “materially better than grep-only on medium repos” with fixtures.

## Embeddings route (gateway)

When hybrid embeddings are enabled, Anvil calls OpenAI-compatible:

`POST {EmbedBaseURL}/embeddings` with body `{"model":"<id>","input":["…"]}` and `Authorization: Bearer …`.

`EmbedBaseURL` is typically the same as chat `BaseURL` (gateway `/v1`). Gateway may proxy `/v1/embeddings` to an upstream that supports it; if unset, index build and `semantic_search` stay **FTS-only**.

## MCP path (B)

Optional alternative to the builtin index (not required for 0.13 acceptance):

| Tool | Args | Notes |
|------|------|--------|
| `semantic_search` | `query`, `path_glob?`, `top_k?` | Same semantics as builtin; return path/line/excerpt text |
| `reindex` | optional `full?: bool` | Operator refresh; Anvil may keep this CLI-only |

Register via `Options.MCPServers` / `.cursor/mcp.json`. Builtin `semantic_search` remains the default; deny it with `DisallowedTools: ["semantic_search"]` if MCP replaces it.

## Security

- Index artifacts must not copy file contents into world-readable global caches outside cwd (unless explicit user cache dir in config).
- Redact high-entropy secrets from chunks when detected (best-effort; align redact package).
- `semantic_search` is read-only; no shell.

## Explicit non-goals

- Whole-internet web search — see **[15-web-search.md](15-web-search.md)** / **0.15**.
- Exact Cursor semantic-search parity.
- Cross-repo global index in 0.13 (single cwd).
- Training embed models.

## Roadmap mapping

- Tool / product slice: [m_0.13_retrieval.md](../../roadmaps/v0.x/m_0.13_retrieval.md)
- Engine (SQLite + VDB): [m_0.22_project_search.md](../../roadmaps/v0.x/m_0.22_project_search.md) — implement storage here; wire `semantic_search` in 0.13 and/or p_0.22.4.
