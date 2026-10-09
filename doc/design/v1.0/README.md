# Design v1.0 (cutoff)

Anvil **local-only** Go Agent SDK design through product milestone **0.10** (Cursor SDK *local* surface parity). Read chapters in order before implementing the matching roadmap phase.

This tree is the **design freeze line** for 0.1–0.10 delivery. Later product lines get a new `doc/design/v…/` directory.

## Read order

| # | Doc | Topic |
|---|-----|--------|
| 00 | [00-overview.md](00-overview.md) | Goals, non-goals, glossary, version strategy |
| 01 | [01-architecture.md](01-architecture.md) | Layers, packages, data flow |
| 02 | [02-public-api.md](02-public-api.md) | Create / Prompt / Resume / Send / Stream / Wait |
| 03 | [03-runtime-loop.md](03-runtime-loop.md) | Tool-calling loop, limits, completion |
| 04 | [04-tools-and-sandbox.md](04-tools-and-sandbox.md) | Built-in tools and cwd sandbox |
| 05 | [05-llm-provider.md](05-llm-provider.md) | OpenAI-compatible + Anthropic clients |
| 06 | [06-session-store.md](06-session-store.md) | JSONL / store backends and Resume |
| 07 | [07-streaming-and-errors.md](07-streaming-and-errors.md) | Events and error model → roadmap 0.7 |
| 08 | [08-security.md](08-security.md) | Path escape, shell, secrets → roadmap 0.8 |
| 09 | [09-extensibility.md](09-extensibility.md) | Custom tools, MCP, hooks → roadmap 0.3 / 0.9 |
| 10 | [10-sdk-parity.md](10-sdk-parity.md) | Cursor SDK **local** parity → roadmap **0.10** |

Parent index: [`doc/design/README.md`](../README.md).

## Relation to roadmaps

- Design describes **what** and **why** for this cutoff.
- [`doc/roadmaps/v1.0/`](../../roadmaps/v1.0/) describes **when**.
- Chapter 10 only via [m_0.10_sdk_parity.md](../../roadmaps/v1.0/m_0.10_sdk_parity.md).
