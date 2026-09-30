# 00 — Overview

**Status:** planned  
**Product:** Anvil — independent local-only Go Agent SDK

## Goals

- Provide a Go library that scripts a **coding agent** against a local working directory (`cwd`).
- **Capability-align** with Cursor SDK’s *local agent* surface (not call Cursor): `Create` / `Prompt` / `Resume` / `Send` / `Stream` / `Wait` / `Cancel` / `Close`, tools, MCP (phased).
- Run entirely on the caller machine: read/edit files, search, shell, no cloud runtime.
- Talk to LLMs via **two** HTTP protocols only: **OpenAI-compatible** Chat Completions and **Anthropic** Messages API.
- Persist local sessions so a process can `Resume` an agent by ID.

## Non-goals

- Cloud / remote VM agents, auto-create PR, hosted pools.
- Multi-agent graph orchestration or sub-agent frameworks.
- Cursor SDK Bridge / depending on Cursor runtime (capability alignment ≠ protocol compatibility with Cursor).
- Drop-in import replacement for `@cursor/sdk` / `cursor-sdk`.
- First-party IDE plugin or deep LSP integration.
- Training or hosting models.
- Other LLM wire protocols beyond OpenAI-compatible and Anthropic (unless a new design chapter adds them).

## Glossary

| Term | Meaning |
|------|---------|
| **Agent** | Durable handle bound to a `cwd`, conversation history, and options. |
| **Run** | One `Send`/`Prompt` execution: LLM + tool loop until completion or limit. |
| **Tool** | Named function the model can call (read_file, shell, …). |
| **Event** | Streamed unit on a Run (`assistant`, `tool_call`, `tool_result`, `done`, …). |
| **Store** | Local persistence under `{cwd}/.anvil/` (JSONL). |
| **Milestone** | Versioned delivery unit (`0.1`, `0.2`, …) in `doc/roadmaps/`. |
| **Phase** | Ordered work package inside a milestone (`p_0.1.1`, …). |

## Version strategy

- Product versions use `0.x` while pre-release; **0.1 = MVP**.
- Design is version-agnostic truth; roadmaps pin what ships per milestone.
- New capability requires a phase doc before code (see `.cursor/rules/doc-versioning.mdc`).
- Forward map (see `doc/roadmaps/README.md`): **0.2** resume/cancel, **0.3** extensibility, **0.4** coding UX, **0.5** operator/eval, **0.6** deferred themes.

## Locked product decisions

| Decision | Choice |
|----------|--------|
| Runtime | Local only |
| API style | Cursor **capability** alignment (local), idiomatic Go |
| LLM wire | OpenAI-compatible **and** Anthropic Messages |
| Session store | JSONL under cwd (no SQLite in 0.1/0.2 default) |
| Orchestration | Single agent tool loop |
