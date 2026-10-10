# Design v1.0 (cutoff)

Anvil **local-only** Go Agent SDK design through product milestone **0.26** (Flutter mobile + auth bridge). Read chapters in order before implementing the matching roadmap phase.

This tree is the **design cutoff** for 0.1–0.26 delivery. Later product lines get a new `doc/design/v…/` directory.

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
| 10 | [10-sdk-parity.md](10-sdk-parity.md) | Cursor SDK **local** parity → roadmap **0.10** (done) |
| 11 | [11-worker-daemon.md](11-worker-daemon.md) | agentd + agent-worker → **0.11** (done) |
| 12 | [12-model-gateway.md](12-model-gateway.md) | Model gateway → **0.12** (done) |
| 13 | [13-retrieval.md](13-retrieval.md) | Index / semantic search → **0.13** |
| 14 | [14-hooks-skills.md](14-hooks-skills.md) | Hooks & skills → **0.14** |
| 15 | [15-web-search.md](15-web-search.md) | Web search / fetch_url → **0.15** |
| 16 | [16-lsp.md](16-lsp.md) | LSP diagnostics / nav / rename → **0.16** |
| 17 | [17-git-tools.md](17-git-tools.md) | git_status / diff / log → **0.17** |
| 18 | [18-test-build.md](18-test-build.md) | run_tests / build → **0.18** |
| 19 | [19-vision.md](19-vision.md) | Image attachments / read_image → **0.19** |
| 20 | [20-plugins.md](20-plugins.md) | Worker-side agent plugins → **0.20** |
| 21 | [21-dev-memory.md](21-dev-memory.md) | Dev memory **plugin** → **0.21** |
| 22 | [22-project-search.md](22-project-search.md) | Project search SQLite+VDB → **0.22** |
| 23 | [23-yaml-config.md](23-yaml-config.md) | YAML ops config (agentd + gateway) → **0.23** |
| 24 | [24-scheduler.md](24-scheduler.md) | Scheduler hub (app HTTP + cluster) → **0.24** |
| 25 | [25-web-admin.md](25-web-admin.md) | anvil-web fleet ops console → **0.25** |
| 26 | [26-mobile-app.md](26-mobile-app.md) | Flutter app + web session on scheduler → **0.26** |

Parent index: [`doc/design/README.md`](../README.md).

## Relation to roadmaps

- Design describes **what** and **why** for this cutoff.
- [`doc/roadmaps/v1.0/`](../../roadmaps/v1.0/) describes **when**.
- Chapters 11–26 map to [m_0.11](../../roadmaps/v1.0/m_0.11_worker_daemon.md) … [m_0.26](../../roadmaps/v1.0/m_0.26_mobile_app.md).
