# Design index

Anvil is an independent **local-only** Go Agent SDK. Read design chapters in order before implementing any roadmap phase.

## Read order

| # | Doc | Topic |
|---|-----|--------|
| 00 | [00-overview.md](00-overview.md) | Goals, non-goals, glossary, version strategy |
| 01 | [01-architecture.md](01-architecture.md) | Layers, packages, data flow |
| 02 | [02-public-api.md](02-public-api.md) | Create / Prompt / Resume / Send / Stream / Wait |
| 03 | [03-runtime-loop.md](03-runtime-loop.md) | Tool-calling loop, limits, completion |
| 04 | [04-tools-and-sandbox.md](04-tools-and-sandbox.md) | Built-in tools and cwd sandbox |
| 05 | [05-llm-provider.md](05-llm-provider.md) | OpenAI-compatible + Anthropic clients |
| 06 | [06-session-store.md](06-session-store.md) | JSONL persistence and Resume |
| 07 | [07-streaming-and-errors.md](07-streaming-and-errors.md) | Events and error model |
| 08 | [08-security.md](08-security.md) | Path escape, shell, secrets, truncation |
| 09 | [09-extensibility.md](09-extensibility.md) | Custom tools, MCP, hooks (post-0.1) |

## Relation to roadmaps

- Design describes **what** and **why**.
- [doc/roadmaps/](../roadmaps/) describes **when** (milestones / phases).
- Do not put version plans under `design/`.
