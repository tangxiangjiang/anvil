# 00 — Overview

**Status:** done — v0.x design cutoff (chapters 00–35 landed)  
**Product:** Anvil — independent local-only Go Agent SDK

## Goals

- Provide a Go library that scripts a **coding agent** against a local working directory (`cwd`).
- **Capability-align** with Cursor SDK’s *local agent* surface (not call Cursor): `Create` / `Prompt` / `Resume` / `Send` / `Stream` / `Wait` / `Cancel` / `Close`, tools, MCP (phased).
- Run entirely on the caller machine: read/edit files, search, shell, no cloud runtime.
- Talk to LLMs via **two** HTTP protocols only: **OpenAI-compatible** Chat Completions and **Anthropic** Messages API.
- Persist local sessions so a process can `Resume` an agent by ID.

## Non-goals

- Cloud / remote VM agents, auto-create PR, hosted Cursor pools.
- In-process multi-agent **graph** / planner handoff inside the library (worker pool under **agentd** is a separate process model — see [11](11-worker-daemon.md)).
- Cursor SDK Bridge / depending on Cursor runtime (capability alignment ≠ protocol compatibility with Cursor).
- Drop-in import replacement for `@cursor/sdk` / `cursor-sdk`.
- ~~First-party IDE plugin UI.~~ **Revised:** in scope from **0.35** as a thin VS Code client over a new editor-host protocol ([35](35-vscode-extension.md)). LSP is still exposed as **agent tools** only ([16](16-lsp.md)) — the extension does not become a language server, and the SDK does not gain an editor dependency.
- Training or **hosting** model weights inside Anvil. Catalog + auth live in the **model gateway** ([12](12-model-gateway.md)), not as Cursor `models.list` inside the agent library.
- Other LLM wire protocols beyond OpenAI-compatible and Anthropic (unless a new design chapter adds them).

## Post-0.10 scope lock

After local surface parity (0.10), only these tracks are in-scope (designs landed; see roadmaps):

| # | Track | Design | Milestone |
|---|--------|--------|-----------|
| 1 | Model gateway (catalog + auth) | [12](12-model-gateway.md) | **0.12** |
| 2 | Retrieval (index / semantic search) | [13](13-retrieval.md) | **0.13** |
| 3 | Hooks / skills deepen | [14](14-hooks-skills.md) | **0.14** |
| 4 | agentd + agent-worker | [11](11-worker-daemon.md) | **0.11** |
| 5 | Web search (+ fetch_url) | [15](15-web-search.md) | **0.15** |
| 6 | LSP / diagnostics / rename | [16](16-lsp.md) | **0.16** |
| 7 | Git tools (read) | [17](17-git-tools.md) | **0.17** |
| 8 | Test & build tools | [18](18-test-build.md) | **0.18** |
| 9 | Vision / images | [19](19-vision.md) | **0.19** |
| 10 | Worker-side plugins | [20](20-plugins.md) | **0.20** |
| 11 | Dev memory (plugin) | [21](21-dev-memory.md) | **0.21** |
| 12 | Project search (SQLite+VDB) | [22](22-project-search.md) | **0.22** |
| 13 | VS Code extension / editor host | [35](35-vscode-extension.md) | **0.35** |

Tracks **0.23–0.34** (YAML ops config, scheduler, web console, mobile app, clusters, sandbox workers, loop guard, job stream, code review, delivery UI, workspace domain) landed after this lock and are indexed in [`doc/roadmaps/v0.x/README.md`](../../roadmaps/v0.x/README.md).

## Glossary

| Term | Meaning |
|------|---------|
| **Agent** | Durable handle bound to a `cwd`, conversation history, and options. |
| **Run** | One `Send`/`Prompt` execution: LLM + tool loop until completion or limit. |
| **Tool** | Named function the model can call (read_file, shell, …). |
| **Event** | Streamed unit on a Run (`assistant`, `tool_call`, `tool_result`, `done`, …). |
| **Store** | Local persistence under `{cwd}/.anvil/` (JSONL). |
| **agentd** | Per-machine worker-daemon (lifecycle + optional cluster edge). See [11](11-worker-daemon.md). |
| **agent-worker** | Child process running one Anvil Local agent job. |
| **anvil-host** | Stdio sidecar (`cmd/anvil-host`) that drives in-process Anvil sessions for the VS Code extension. A **standalone** capability on the public SDK — not part of the tool-chain. See [35](35-vscode-extension.md). |
| **Milestone** | Versioned delivery unit (`0.1`, `0.2`, …) in `doc/roadmaps/`. |
| **Phase** | Ordered work package inside a milestone (`p_0.1.1`, …). |

## Version strategy

- Product versions use `0.x` while pre-release; **0.1 = MVP**; **0.10** = SDK local surface parity; **0.11+** = post-parity scope lock and platform track (still 0.x).
- Design lives in versioned trees under `doc/design/` (this file is **`v0.x`** cutoff). Roadmaps live under `doc/roadmaps/v0.x/`.
- New capability requires a phase doc before code (see `.cursor/rules/doc-versioning.mdc`).
- Chapters **00–09** core; **10**→0.10 … **35**→0.35.
- Forward map: see [`doc/roadmaps/v0.x/README.md`](../../roadmaps/v0.x/README.md).

## Locked product decisions

| Decision | Choice |
|----------|--------|
| Runtime | Local only (in-process SDK); optional **agentd** for multi-job hosts |
| Editor client | Out-of-process sidecar (`anvil-host`) on the **public SDK only** — independent of the tool-chain ([35](35-vscode-extension.md)) |
| API style | Cursor **capability** alignment (local), idiomatic Go |
| LLM wire | OpenAI-compatible **and** Anthropic Messages |
| Session store | JSONL under cwd (no SQLite in 0.1/0.2 default) |
| Orchestration | Single agent tool loop in-process; process pool via agentd (0.11) |
| Plugins | Optional/heavy/org packs on **worker**; **general** tools stay in agent core ([20](20-plugins.md)) |
