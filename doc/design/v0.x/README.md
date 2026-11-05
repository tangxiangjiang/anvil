# Design v0.x (cutoff)

Anvil Agent SDK and VS Code design delivered through product milestone **0.57** and proposed
through **0.66**. Chapters
51–57 bridge the agent capabilities delivered through 0.50 into the editor, close the shell
protected-path gap, and add reliable runs, structured plans, session search, and isolated
workers. Chapters 60–66 extend that base into end-to-end execution, remote delivery,
browser/computer use, stronger isolation, autonomous review, production hardening, and remote
Workers. 0.39 is the Delivery **execute / test campaign loop**, designed by
[39](39-execute.md) on top of the surfaces in [33](33-delivery-ui.md) and the vision in
[DELIVERY_PIPELINE_VISION.md](../../../DELIVERY_PIPELINE_VISION.md). Read chapters in order
before implementing the matching roadmap phase.

This tree is the **design cutoff** for 0.1–0.66 delivery planning. Later product lines get a new
`doc/design/v…/` directory.

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
| 27 | [27-app-fleets.md](27-app-fleets.md) | App fleet picker + cluster ops → **0.27** |
| 28 | [28-clusters-workers.md](28-clusters-workers.md) | Web Clusters / Nodes / Workers → **0.28** |
| 29 | [29-sandbox-workers.md](29-sandbox-workers.md) | Sandbox workers + web FS browser → **0.29** |
| 30 | [30-loop-guard.md](30-loop-guard.md) | Loop guard / unlimited steps → **0.30** |
| 31 | [31-job-event-stream.md](31-job-event-stream.md) | Job SSE stream for app chat → **0.31** |
| 32 | [32-sandbox-code-review.md](32-sandbox-code-review.md) | Sandbox read-only review + VS Code Server → **0.32** |
| 33 | [33-delivery-ui.md](33-delivery-ui.md) | Delivery UI + SQLite schema → **0.33** |
| 34 | [34-workspace-domain.md](34-workspace-domain.md) | Workspace → Project → Workers (Clusters) → **0.34** |
| 35 | [35-vscode-extension.vscode.md](35-vscode-extension.vscode.md) | VS Code extension + editor host → **0.35** |
| 36 | [36-tool-permissions.vscode.md](36-tool-permissions.vscode.md) | Rule set + shell analyzer, and the rule shapes the approval card remembers → **0.36** |
| 37 | [37-permission-modes.vscode.md](37-permission-modes.vscode.md) | The complete mode ladder, and where a session's starting mode comes from → **0.37** |
| 38 | [38-session-identity-and-replay.vscode.md](38-session-identity-and-replay.vscode.md) | A session's title, a reviewable change on every tool card, and a transcript that survives a reload → **0.38** |
| 39 | [39-execute.md](39-execute.md) | Execute from a ready roadmap to a tested milestone: the lifecycle state machine, phase Jobs, the test campaign loop, and accept and merge → **0.39** |
| 40 | [40-editor-alignment.vscode.md](40-editor-alignment.vscode.md) | Parallel tabs, session administration, partial acceptance, checkpoints, fork, and the SDK capabilities the client never turned on → **0.40** |
| 41 | [41-permission-auto-and-manager.vscode.md](41-permission-auto-and-manager.vscode.md) | A deterministic `auto` rung, and a structured permissions manager → **0.41** |
| 42 | [42-subagents-and-tasks.vscode.md](42-subagents-and-tasks.vscode.md) | Agent-in-agent, background tasks and `/tasks`, and declarative workflows → **0.42** |
| 43 | [43-context-management.vscode.md](43-context-management.vscode.md) | A token budget, safe compaction at run start and mid-run, `/compact`, and the cache clock → **0.43** |
| 44 | [44-mcp-management.vscode.md](44-mcp-management.vscode.md) | A host-owned MCP runtime with observable state, safe config edits, reconnect, Streamable HTTP and OAuth → **0.44** |
| 45 | [45-web-editor-polish.md](45-web-editor-polish.md) | anvil-web polish, the web drafting assistant, and the editor-host protocol smoke tests → **0.45** |
| 46 | [46-editor-polish.vscode.md](46-editor-polish.vscode.md) | Wiring LSP and test/build, rewind, plan review, reading, accessibility, rule scopes, and one root per window → **0.46** |
| 47 | [47-evaluation.md](47-evaluation.md) | A three-tier eval harness, a scorer, a baseline file, and the invariants suite that guards Anvil's own capabilities → **0.47** |
| 48 | [48-llm-effort-and-retry.md](48-llm-effort-and-retry.md) | Reasoning effort on both wires, retry with backoff, stop reasons and error kinds; effort vs. reasoning content → **0.48** |
| 49 | [49-roles-and-prompts.md](49-roles-and-prompts.md) | A layered system prompt with an unremovable safety layer, and five roles → **0.49** |
| 50 | [50-agent-modes.md](50-agent-modes.md) | Single, Multi, Complex as a ladder; `delegate` and the brief; Complex as a workflow → **0.50** |
| 51 | [51-protected-shell-paths.vscode.md](51-protected-shell-paths.vscode.md) | Fail-closed shell path analysis keeps remembered allows away from protected paths → **0.51** |
| 52 | [52-agent-mode-bridge.vscode.md](52-agent-mode-bridge.vscode.md) | Single, Multi, and Complex across editor-host and VS Code, with typed Brief and Acceptance views → **0.52** |
| 53 | [53-model-capabilities-and-run-signals.vscode.md](53-model-capabilities-and-run-signals.vscode.md) | Explicit model capabilities, adaptive thinking, retry/truncation signals, and controlled continuation → **0.53** |
| 54 | [54-durable-agent-runs.vscode.md](54-durable-agent-runs.vscode.md) | Sequenced replayable events and honest recovery of background task state → **0.54** |
| 55 | [55-structured-plans-and-workflow-control.vscode.md](55-structured-plans-and-workflow-control.vscode.md) | One structured plan contract across Plan, todo, and Complex workflow control → **0.55** |
| 56 | [56-session-search-and-history.vscode.md](56-session-search-and-history.vscode.md) | Local full-text session search and cursor history for JSONL and SQLite → **0.56** |
| 57 | [57-worktree-workers.vscode.md](57-worktree-workers.vscode.md) | Isolated worktree Workers, attributed checkpoints, conflict review, integration, and cleanup → **0.57** |
| 60 | [60-product-requirements-workbench.vscode.md](60-product-requirements-workbench.vscode.md) | VS Code requirement lifecycle, artifact-aware Chat, and first Web audit-only cutover → **0.60** |
| 61 | [61-planning-and-policy.vscode.md](61-planning-and-policy.vscode.md) | Design, roadmap, work graph, execution policy, and planning cutover → **0.61** |
| 62 | [62-isolated-implementation.vscode.md](62-isolated-implementation.vscode.md) | Worktree execution, container isolation, recovery, and execution cutover → **0.62** |
| 63 | [63-verification-and-evidence.vscode.md](63-verification-and-evidence.vscode.md) | Verification campaigns plus supervised browser/desktop evidence → **0.63** |
| 64 | [64-review-and-delivery.vscode.md](64-review-and-delivery.vscode.md) | Evidence-bound review, bounded repair, exact-commit PR/CI delivery → **0.64** |
| 65 | [65-release-and-hardening.vscode.md](65-release-and-hardening.vscode.md) | Explicit release, production gates, and final audit-only Web enforcement → **0.65** |
| 66 | [66-remote-workers.vscode.md](66-remote-workers.vscode.md) | Authenticated remote sandbox execution with verifiable results → **0.66** |

Chapters 58–59 are intentionally unassigned. Chapters 60–65 move the complete interactive
requirement-to-release workflow to VS Code; chapter 66 adds remote execution.

Parent index: [`doc/design/README.md`](../README.md).

## Relation to roadmaps

- Design describes **what** and **why** for this cutoff.
- [`doc/roadmaps/v0.x/`](../../roadmaps/v0.x/) describes **when**.
- Chapters 11–57 map to delivered [m_0.11](../../roadmaps/v0.x/m_0.11_worker_daemon.md) …
  [m_0.57](../../roadmaps/v0.x/m_0.57_worktree_workers.vscode.md). Chapters 60–66 map to the
  proposed 0.60–0.66 milestone files in the same roadmap tree.
  [m_0.39](../../roadmaps/v0.x/m_0.39_execute.md) maps to chapter 39; chapter 33 holds its
  surfaces and schema.
