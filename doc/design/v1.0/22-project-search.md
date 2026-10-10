# 22 — Project search engine (MVP: SQLite + VDB)

**Product version focus:** **0.22** ([m_0.22_project_search.md](../../roadmaps/v1.0/m_0.22_project_search.md))  
**Status:** done (BLOB + brute-force cosine MVP; sqlite-vec optional later)

## Verdict

**Yes — SQLite + vector store is a solid MVP** for Anvil’s per-cwd project search.

At single-repo scale (10³–10⁶ chunks), you do not need a networked vector cluster. One local DB keeps ops simple, matches Anvil’s local-only story, and implements the retrieval surface in [13](13-retrieval.md).

## Recommended MVP shape

**Prefer one file:** `{cwd}/.anvil/index/search.db` (SQLite)

| Concern | Mechanism |
|---------|-----------|
| Files / chunks / meta | Normal SQLite tables |
| Lexical | **FTS5** over chunk text (BM25-ish) |
| Vectors | **Embedded VDB in the same process** — preferred: [sqlite-vec](https://github.com/asg017/sqlite-vec) virtual tables **or** `BLOB` + brute-force cosine for tiny repos |
| Hybrid | Merge FTS rank + vector rank; dedupe `chunk_id` |

```text
files ──chunk──▶ chunks ──embed──▶ vectors
                   │
                   └── FTS5(chunks_fts)
```

**Why not “SQLite + remote Milvus/Qdrant” for MVP?** Extra daemon, auth, and failure modes; little gain until multi-repo / huge corpora. Those can be a later **plugin** backend implementing the same engine interface.

**Why SQLite at all?** Already used optionally for agent store (0.6); single-file copy/backup; good enough concurrent read for one agent-worker.

## Relation to 0.13

| Doc | Role |
|-----|------|
| [13-retrieval.md](13-retrieval.md) | Product tools (`semantic_search`), ranking policy, embed via gateway |
| **This chapter (22)** | **Concrete engine**: schema, FTS5 + VDB, incremental update, CLI |

0.13’s `files.jsonl` / `vectors.*` sketch is **superseded for implementation** by `search.db`; tool names and hybrid policy from 13 remain. Implement 0.13 phases against the 0.22 engine (or fold remaining 0.13 storage work into 0.22).

## Engine interface (sketch)

```go
type ProjectSearch interface {
    Build(ctx, cwd, opts) error          // full or incremental
    Status(ctx) (IndexStatus, error)
    Search(ctx, q SearchQuery) ([]Hit, error)  // lexical | vector | hybrid
    Close() error
}
```

Builtin `semantic_search` calls `Search` with hybrid when vectors exist, else FTS-only ([13](13-retrieval.md)).

## Schema (sketch)

```text
meta(key, value)           -- schema_version, embed_model, built_at
files(path PK, mtime, hash, lang)
chunks(id PK, path, start_line, end_line, hash, text)
chunks_fts                 -- FTS5(text, content=chunks, ...)
chunk_vec(chunk_id, embedding)  -- sqlite-vec or BLOB
```

- Denylist / sandbox: same as 13/08.
- Embed model id recorded in `meta`; model change → full re-embed.

## Incremental update

1. Walk cwd → hash files.
2. Unchanged hash → skip.
3. Changed/removed → delete old chunks + fts + vec rows; re-chunk; embed new only.
4. Build must be restart-safe (transaction per file or per batch).

## Ops

- `anvil index build|status|clear` (or plugin/CLI under agent-worker).
- Default gitignore `.anvil/index/`.
- Agent Create does not block on rebuild (13).

## Feasibility notes / risks

| Risk | Mitigation |
|------|------------|
| sqlite-vec / extension load on Windows | Vendor carefully; fallback BLOB brute-force in CI |
| Huge monorepo | Cap files/chunks; path_glob; later shard — out of MVP |
| Embed cost | Batch + skip unchanged; allow FTS-only mode |
| Writer lock | One builder at a time per cwd; readers OK |

## Explicit non-goals (MVP)

- Distributed VDB cluster.
- Replacing `grep` (keep both).
- Cross-cwd global search.
- Perfect Composer recall.

## Roadmap mapping

[m_0.22_project_search.md](../../roadmaps/v1.0/m_0.22_project_search.md) phases `p_0.22.1` … `p_0.22.4`.
