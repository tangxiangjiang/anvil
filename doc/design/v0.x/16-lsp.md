# 16 — LSP & language intelligence

**Product version focus:** **0.16** ([m_0.16_lsp.md](../../roadmaps/v1.0/m_0.16_lsp.md))  
**Status:** done

## Intent

Add **language-server–backed** tools so the agent can use diagnostics, navigation, and safe renames instead of guessing from grep. This is the largest “correctness” lever after local retrieval ([13](13-retrieval.md)).

Anvil does **not** become an IDE: no UI, no deep editor plugin. It spawns or attaches to **LSP servers** for languages configured under cwd, exposes a small tool surface, and tears them down with the agent (or a shared pool under agentd later).

## Architecture

```text
anvil tools
    │
    ▼
internal/lsp  (client + supervisor)
    │  stdio JSON-RPC
    ▼
gopls / rust-analyzer / pyright / typescript-language-server / …
```

- One supervisor per agent (MVP); optional shared supervisor in agent-worker (0.11+) later.
- Config maps `glob / languageId → server command` (project `.anvil/lsp.json` or `Options.LSP`).
- Missing server binary → tool error string, not Create failure (unless `LSP.Required=true`).

## Builtin tools

| Tool | Args (conceptual) | Behavior |
|------|-------------------|----------|
| `diagnostics` | path? / glob? | Pull publishDiagnostics (or `textDocument/diagnostic`); filter by severity; truncate |
| `definition` | path, line, character | `textDocument/definition` → locations |
| `references` | path, line, character, includeDeclaration? | `textDocument/references` |
| `hover` | path, line, character | Optional; docs/signature snippet |
| `rename_symbol` | path, line, character, newName | `textDocument/rename` → apply **WorkspaceEdit** via existing file tools / patch apply |

### Multi-file edit

- Prefer **LSP WorkspaceEdit** from rename (and later `codeAction` if added).
- Non-LSP bulk edit remains `apply_patch` / multiple `edit_file` (already shipped).
- Notebook (`.ipynb`) **out of 0.16 core**; optional follow-on: cell-aware edit via dedicated tool or MCP — note only.

### Tool group

`lsp` for `BuiltinTools` / `DisallowedTools`. Default **on** when any server is configured; else tools absent or return “LSP not configured”.

## Client requirements (MVP)

- `initialize` / `initialized`, `textDocument/didOpen` (and didChange on edit if we write files mid-run — best-effort sync after Anvil writes).
- Request: definition, references, rename, diagnostic.
- Sync file content Anvil just wrote before issuing nav/rename on that path.
- Timeouts per request; cancel on agent Cancel.

## Config (sketch)

```text
Options.LSP = {
  Enabled: true
  Servers: [
    { Language: "go", Pattern: "**/*.go", Command: "gopls", Args: [] }
  ]
  InitTimeout, RequestTimeout
}
```

Project file `.anvil/lsp.json` when `settingSources` includes project (align 0.10 loading style).

## Security

- Server cwd = agent cwd; path sandbox on all locations returned (drop outside cwd).
- Do not pass secrets into server env beyond caller allowlist.
- Rename applies only sandbox-checked paths.

## Explicit non-goals

- Full IDE feature set (completion UI, debug adapter protocol as product).
- Bundling language server binaries in Anvil releases.
- Claiming equal to Cursor’s proprietary code intelligence.
- DAP debugger control in 0.16.

## Roadmap mapping

[m_0.16_lsp.md](../../roadmaps/v1.0/m_0.16_lsp.md) phases `p_0.16.1` … `p_0.16.4`.
