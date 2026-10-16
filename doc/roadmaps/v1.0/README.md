# Roadmaps v1.0 (cutoff)

Versioned delivery for Anvil through product **0.37**. Design cutoff: [`doc/design/v1.0/`](../../design/v1.0/).

## Version index

| Version | Milestone | Status | Doc | Design chapter |
|---------|-----------|--------|-----|----------------|
| 0.1 | MVP | done | [m_0.1_mvp.md](m_0.1_mvp.md) | 00–04 |
| 0.2 | Resume & observability | done | [m_0.2_resume.md](m_0.2_resume.md) | 06 |
| 0.3 | Extensibility | done | [m_0.3_extensibility.md](m_0.3_extensibility.md) | 09 (initial) |
| 0.4 | Coding UX | done | [m_0.4_coding_ux.md](m_0.4_coding_ux.md) | 05 stream / 04 edits |
| 0.5 | Operator surface | done | [m_0.5_operator.md](m_0.5_operator.md) | 02 / 06 |
| 0.6 | Scale | done | [m_0.6_scale.md](m_0.6_scale.md) | — |
| 0.7 | Streaming & errors | done | [m_0.7_streaming_errors.md](m_0.7_streaming_errors.md) | [07](../../design/v1.0/07-streaming-and-errors.md) |
| 0.8 | Security hardening | done | [m_0.8_security.md](m_0.8_security.md) | [08](../../design/v1.0/08-security.md) |
| 0.9 | Extensibility depth | done | [m_0.9_extensibility.md](m_0.9_extensibility.md) | [09](../../design/v1.0/09-extensibility.md) |
| 0.10 | Cursor SDK local parity | done | [m_0.10_sdk_parity.md](m_0.10_sdk_parity.md) | [10](../../design/v1.0/10-sdk-parity.md) |
| 0.11 | Worker daemon & agent-worker | done | [m_0.11_worker_daemon.md](m_0.11_worker_daemon.md) | [11](../../design/v1.0/11-worker-daemon.md) |
| 0.12 | Model gateway | done | [m_0.12_model_gateway.md](m_0.12_model_gateway.md) | [12](../../design/v1.0/12-model-gateway.md) |
| 0.13 | Retrieval quality | done | [m_0.13_retrieval.md](m_0.13_retrieval.md) | [13](../../design/v1.0/13-retrieval.md) |
| 0.14 | Hooks & skills deepen | done | [m_0.14_hooks_skills.md](m_0.14_hooks_skills.md) | [14](../../design/v1.0/14-hooks-skills.md) |
| 0.15 | Web search | done | [m_0.15_web_search.md](m_0.15_web_search.md) | [15](../../design/v1.0/15-web-search.md) |
| 0.16 | LSP & language intelligence | done | [m_0.16_lsp.md](m_0.16_lsp.md) | [16](../../design/v1.0/16-lsp.md) |
| 0.17 | Git tools | done | [m_0.17_git_tools.md](m_0.17_git_tools.md) | [17](../../design/v1.0/17-git-tools.md) |
| 0.18 | Test & build tools | done | [m_0.18_test_build.md](m_0.18_test_build.md) | [18](../../design/v1.0/18-test-build.md) |
| 0.19 | Vision (image / screenshot) | done | [m_0.19_vision.md](m_0.19_vision.md) | [19](../../design/v1.0/19-vision.md) |
| 0.20 | Agent plugins (worker-side) | done | [m_0.20_plugins.md](m_0.20_plugins.md) | [20](../../design/v1.0/20-plugins.md) |
| 0.21 | Dev memory (plugin) | done | [m_0.21_dev_memory.md](m_0.21_dev_memory.md) | [21](../../design/v1.0/21-dev-memory.md) |
| 0.22 | Project search (SQLite+VDB) | done | [m_0.22_project_search.md](m_0.22_project_search.md) | [22](../../design/v1.0/22-project-search.md) |
| 0.23 | YAML ops config (agentd + gateway) | done | [m_0.23_yaml_config.md](m_0.23_yaml_config.md) | [23](../../design/v1.0/23-yaml-config.md) |
| 0.24 | Scheduler hub (controller + catalog) | done | [m_0.24_scheduler.md](m_0.24_scheduler.md) | [24](../../design/v1.0/24-scheduler.md) |
| 0.25 | Web fleet ops (anvil-web) | done | [m_0.25_web_admin.md](m_0.25_web_admin.md) | [25](../../design/v1.0/25-web-admin.md) |
| 0.26 | Flutter mobile + auth bridge | done | [m_0.26_mobile_app.md](m_0.26_mobile_app.md) | [26](../../design/v1.0/26-mobile-app.md) |
| 0.27 | App fleets + cluster ops | done | [m_0.27_app_fleets.md](m_0.27_app_fleets.md) | [27](../../design/v1.0/27-app-fleets.md) |
| 0.28 | Web Clusters / Nodes / Workers | done | [m_0.28_clusters_workers.md](m_0.28_clusters_workers.md) | [28](../../design/v1.0/28-clusters-workers.md) |
| 0.29 | Sandbox workers + web FS browser | done | [m_0.29_sandbox_workers.md](m_0.29_sandbox_workers.md) | [29](../../design/v1.0/29-sandbox-workers.md) |
| 0.30 | Loop guard (unlimited steps) | done | [m_0.30_loop_guard.md](m_0.30_loop_guard.md) | [30](../../design/v1.0/30-loop-guard.md) |
| 0.31 | Job event stream (app chat) | done | [m_0.31_job_stream.md](m_0.31_job_stream.md) | [31](../../design/v1.0/31-job-event-stream.md) |
| 0.32 | Sandbox code review + VS Code Server | planned | [m_0.32_sandbox_code_review.md](m_0.32_sandbox_code_review.md) | [32](../../design/v1.0/32-sandbox-code-review.md) |
| 0.33 | Delivery UI + schema | done | [m_0.33_delivery_ui.md](m_0.33_delivery_ui.md) | [33](../../design/v1.0/33-delivery-ui.md) |
| 0.34 | Workspace domain (Clusters hierarchy) | done | [m_0.34_workspace_domain.md](m_0.34_workspace_domain.md) | [34](../../design/v1.0/34-workspace-domain.md) |
| 0.37 | Execute (Worker bind + card controls + Polish) | in_progress | [m_0.37_execute.md](m_0.37_execute.md) | [33](../../design/v1.0/33-delivery-ui.md) (Roadmap) + vision §9 |

## Naming

See [VERSIONING.md](VERSIONING.md). Enforced by `.cursor/rules/doc-structure.mdc` and `doc-versioning.mdc`.

## Lessons

Operational notes from real runs: [LESSONS.md](LESSONS.md).

## How to use

1. Pick the active milestone phase (`status: planned` → set `in_progress`).
2. Implement only that phase's tasks and acceptance criteria.
3. Mark phase `done`; refresh this index if milestone status changes.

## Post-0.10 scope lock (designs landed)

| Priority | Track | Milestone | Design |
|----------|--------|-----------|--------|
| 1 | Model gateway (catalog + auth) | **0.12** | [12](../../design/v1.0/12-model-gateway.md) |
| 2 | Retrieval quality | **0.13** | [13](../../design/v1.0/13-retrieval.md) |
| 3 | Hooks / skills deepen | **0.14** | [14](../../design/v1.0/14-hooks-skills.md) |
| 4 | agentd + agent-worker | **0.11** | [11](../../design/v1.0/11-worker-daemon.md) |
| 5 | Web search (+ fetch_url) | **0.15** | [15](../../design/v1.0/15-web-search.md) |
| 6 | LSP / diagnostics / rename | **0.16** | [16](../../design/v1.0/16-lsp.md) |
| 7 | Git tools (read) | **0.17** | [17](../../design/v1.0/17-git-tools.md) |
| 8 | Test & build tools | **0.18** | [18](../../design/v1.0/18-test-build.md) |
| 9 | Vision / images | **0.19** | [19](../../design/v1.0/19-vision.md) |
| 10 | Worker-side plugins | **0.20** | [20](../../design/v1.0/20-plugins.md) |
| 11 | Dev memory (plugin) | **0.21** | [21](../../design/v1.0/21-dev-memory.md) |
| 12 | Project search (SQLite+VDB) | **0.22** | [22](../../design/v1.0/22-project-search.md) |

Suggested implementation order: **0.11** ∥ **0.12** → **0.22** engine → **0.13** `semantic_search` wire-up → **0.17**/**0.18** → **0.16** → **0.20** → **0.21** memory → **0.15** → **0.14** → **0.19**.

Explicitly **not** in this lock: IDE plugin UI, Cursor Cloud/Bridge, in-process multi-agent graph, full Cursor hooks/plugins/team MDM, OS sandbox clone, notebook cell editor, DAP debugger, public plugin marketplace.

Parent index: [`doc/roadmaps/README.md`](../README.md).
