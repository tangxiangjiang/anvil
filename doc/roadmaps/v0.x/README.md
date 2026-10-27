# Roadmaps v0.x (cutoff)

Versioned delivery for Anvil through milestone **0.50**. Design cutoff: [`doc/design/v0.x/`](../../design/v0.x/).
Milestone 0.46 closes the VS Code polish line; 0.47–0.50 develop and polish agent capabilities.
The shipped VSIX and host version is **0.46.54**; it is not the milestone status.

## Version index

| Version | Milestone | Status | Doc | Design chapter |
|---------|-----------|--------|-----|----------------|
| 0.1 | MVP | done | [m_0.1_mvp.md](m_0.1_mvp.md) | 00–04 |
| 0.2 | Resume & observability | done | [m_0.2_resume.md](m_0.2_resume.md) | 06 |
| 0.3 | Extensibility | done | [m_0.3_extensibility.md](m_0.3_extensibility.md) | 09 (initial) |
| 0.4 | Coding UX | done | [m_0.4_coding_ux.md](m_0.4_coding_ux.md) | 05 stream / 04 edits |
| 0.5 | Operator surface | done | [m_0.5_operator.md](m_0.5_operator.md) | 02 / 06 |
| 0.6 | Scale | done | [m_0.6_scale.md](m_0.6_scale.md) | — |
| 0.7 | Streaming & errors | done | [m_0.7_streaming_errors.md](m_0.7_streaming_errors.md) | [07](../../design/v0.x/07-streaming-and-errors.md) |
| 0.8 | Security hardening | done | [m_0.8_security.md](m_0.8_security.md) | [08](../../design/v0.x/08-security.md) |
| 0.9 | Extensibility depth | done | [m_0.9_extensibility.md](m_0.9_extensibility.md) | [09](../../design/v0.x/09-extensibility.md) |
| 0.10 | Cursor SDK local parity | done | [m_0.10_sdk_parity.md](m_0.10_sdk_parity.md) | [10](../../design/v0.x/10-sdk-parity.md) |
| 0.11 | Worker daemon & agent-worker | done | [m_0.11_worker_daemon.md](m_0.11_worker_daemon.md) | [11](../../design/v0.x/11-worker-daemon.md) |
| 0.12 | Model gateway | done | [m_0.12_model_gateway.md](m_0.12_model_gateway.md) | [12](../../design/v0.x/12-model-gateway.md) |
| 0.13 | Retrieval quality | done | [m_0.13_retrieval.md](m_0.13_retrieval.md) | [13](../../design/v0.x/13-retrieval.md) |
| 0.14 | Hooks & skills deepen | done | [m_0.14_hooks_skills.md](m_0.14_hooks_skills.md) | [14](../../design/v0.x/14-hooks-skills.md) |
| 0.15 | Web search | done | [m_0.15_web_search.md](m_0.15_web_search.md) | [15](../../design/v0.x/15-web-search.md) |
| 0.16 | LSP & language intelligence | done | [m_0.16_lsp.md](m_0.16_lsp.md) | [16](../../design/v0.x/16-lsp.md) |
| 0.17 | Git tools | done | [m_0.17_git_tools.md](m_0.17_git_tools.md) | [17](../../design/v0.x/17-git-tools.md) |
| 0.18 | Test & build tools | done | [m_0.18_test_build.md](m_0.18_test_build.md) | [18](../../design/v0.x/18-test-build.md) |
| 0.19 | Vision (image / screenshot) | done | [m_0.19_vision.md](m_0.19_vision.md) | [19](../../design/v0.x/19-vision.md) |
| 0.20 | Agent plugins (worker-side) | done | [m_0.20_plugins.md](m_0.20_plugins.md) | [20](../../design/v0.x/20-plugins.md) |
| 0.21 | Dev memory (plugin) | done | [m_0.21_dev_memory.md](m_0.21_dev_memory.md) | [21](../../design/v0.x/21-dev-memory.md) |
| 0.22 | Project search (SQLite+VDB) | done | [m_0.22_project_search.md](m_0.22_project_search.md) | [22](../../design/v0.x/22-project-search.md) |
| 0.23 | YAML ops config (agentd + gateway) | done | [m_0.23_yaml_config.md](m_0.23_yaml_config.md) | [23](../../design/v0.x/23-yaml-config.md) |
| 0.24 | Scheduler hub (controller + catalog) | done | [m_0.24_scheduler.md](m_0.24_scheduler.md) | [24](../../design/v0.x/24-scheduler.md) |
| 0.25 | Web fleet ops (anvil-web) | done | [m_0.25_web_admin.md](m_0.25_web_admin.md) | [25](../../design/v0.x/25-web-admin.md) |
| 0.26 | Flutter mobile + auth bridge | done | [m_0.26_mobile_app.md](m_0.26_mobile_app.md) | [26](../../design/v0.x/26-mobile-app.md) |
| 0.27 | App fleets + cluster ops | done | [m_0.27_app_fleets.md](m_0.27_app_fleets.md) | [27](../../design/v0.x/27-app-fleets.md) |
| 0.28 | Web Clusters / Nodes / Workers | done | [m_0.28_clusters_workers.md](m_0.28_clusters_workers.md) | [28](../../design/v0.x/28-clusters-workers.md) |
| 0.29 | Sandbox workers + web FS browser | done | [m_0.29_sandbox_workers.md](m_0.29_sandbox_workers.md) | [29](../../design/v0.x/29-sandbox-workers.md) |
| 0.30 | Loop guard (unlimited steps) | done | [m_0.30_loop_guard.md](m_0.30_loop_guard.md) | [30](../../design/v0.x/30-loop-guard.md) |
| 0.31 | Job event stream (app chat) | done | [m_0.31_job_stream.md](m_0.31_job_stream.md) | [31](../../design/v0.x/31-job-event-stream.md) |
| 0.32 | Sandbox code review + VS Code Server | in_progress | [m_0.32_sandbox_code_review.md](m_0.32_sandbox_code_review.md) | [32](../../design/v0.x/32-sandbox-code-review.md) |
| 0.33 | Delivery UI + schema | done | [m_0.33_delivery_ui.md](m_0.33_delivery_ui.md) | [33](../../design/v0.x/33-delivery-ui.md) |
| 0.34 | Workspace domain (Clusters hierarchy) | done | [m_0.34_workspace_domain.md](m_0.34_workspace_domain.md) | [34](../../design/v0.x/34-workspace-domain.md) |
| 0.35 | VS Code extension + editor host | done | [m_0.35_vscode_extension.vscode.md](m_0.35_vscode_extension.vscode.md) | [35](../../design/v0.x/35-vscode-extension.vscode.md) |
| 0.36 | Tool permissions (rules + analyzer) | done | [m_0.36_tool_permissions.vscode.md](m_0.36_tool_permissions.vscode.md) | [36](../../design/v0.x/36-tool-permissions.vscode.md) |
| 0.37 | Permission modes (a complete ladder) | done | [m_0.37_permission_modes.vscode.md](m_0.37_permission_modes.vscode.md) | [37](../../design/v0.x/37-permission-modes.vscode.md) |
| 0.38 | Session identity & transcript replay | done | [m_0.38_session_identity.vscode.md](m_0.38_session_identity.vscode.md) | [38](../../design/v0.x/38-session-identity-and-replay.vscode.md) |
| 0.39 | Execute + Test campaign loop | done | [m_0.39_execute.md](m_0.39_execute.md) | [33](../../design/v0.x/33-delivery-ui.md) + vision §2 / §4.4 |
| 0.40 | Editor alignment (Claude-class VS Code client) | done | [m_0.40_editor_alignment.vscode.md](m_0.40_editor_alignment.vscode.md) | [40](../../design/v0.x/40-editor-alignment.vscode.md) |
| 0.41 | Auto mode + permissions manager | done | [m_0.41_permission_auto_and_manager.vscode.md](m_0.41_permission_auto_and_manager.vscode.md) | [41](../../design/v0.x/41-permission-auto-and-manager.vscode.md) |
| 0.42 | Subagents, background tasks, and workflows | done | [m_0.42_subagents_and_tasks.vscode.md](m_0.42_subagents_and_tasks.vscode.md) | [42](../../design/v0.x/42-subagents-and-tasks.vscode.md) |
| 0.43 | Context management | done | [m_0.43_context_management.vscode.md](m_0.43_context_management.vscode.md) | [43](../../design/v0.x/43-context-management.vscode.md) |
| 0.44 | MCP management | done | [m_0.44_mcp_management.vscode.md](m_0.44_mcp_management.vscode.md) | [44](../../design/v0.x/44-mcp-management.vscode.md) |
| 0.45 | Protocol smoke + web polish | done | [m_0.45_protocol_smoke.md](m_0.45_protocol_smoke.md) | [35](../../design/v0.x/35-vscode-extension.vscode.md) + [45](../../design/v0.x/45-web-editor-polish.md) |
| 0.46 | Editor polish (58 phases) | done | [m_0.46_editor_polish.vscode.md](m_0.46_editor_polish.vscode.md) | [46](../../design/v0.x/46-editor-polish.vscode.md) |
| 0.47 | Evaluation baseline and audit | done | [m_0.47_evaluation_and_audit.md](m_0.47_evaluation_and_audit.md) | [47](../../design/v0.x/47-evaluation.md) |
| 0.48 | LLM calls: effort, retry, stop reasons | planned | [m_0.48_llm_calls.md](m_0.48_llm_calls.md) | [48](../../design/v0.x/48-llm-effort-and-retry.md) |
| 0.49 | Roles and prompts | planned | [m_0.49_roles_and_prompts.md](m_0.49_roles_and_prompts.md) | [49](../../design/v0.x/49-roles-and-prompts.md) |
| 0.50 | Agent modes | planned | [m_0.50_agent_modes.md](m_0.50_agent_modes.md) | [50](../../design/v0.x/50-agent-modes.md) |

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
| 1 | Model gateway (catalog + auth) | **0.12** | [12](../../design/v0.x/12-model-gateway.md) |
| 2 | Retrieval quality | **0.13** | [13](../../design/v0.x/13-retrieval.md) |
| 3 | Hooks / skills deepen | **0.14** | [14](../../design/v0.x/14-hooks-skills.md) |
| 4 | agentd + agent-worker | **0.11** | [11](../../design/v0.x/11-worker-daemon.md) |
| 5 | Web search (+ fetch_url) | **0.15** | [15](../../design/v0.x/15-web-search.md) |
| 6 | LSP / diagnostics / rename | **0.16** | [16](../../design/v0.x/16-lsp.md) |
| 7 | Git tools (read) | **0.17** | [17](../../design/v0.x/17-git-tools.md) |
| 8 | Test & build tools | **0.18** | [18](../../design/v0.x/18-test-build.md) |
| 9 | Vision / images | **0.19** | [19](../../design/v0.x/19-vision.md) |
| 10 | Worker-side plugins | **0.20** | [20](../../design/v0.x/20-plugins.md) |
| 11 | Dev memory (plugin) | **0.21** | [21](../../design/v0.x/21-dev-memory.md) |
| 12 | Project search (SQLite+VDB) | **0.22** | [22](../../design/v0.x/22-project-search.md) |

Suggested implementation order: **0.11** ∥ **0.12** → **0.22** engine → **0.13** `semantic_search` wire-up → **0.17**/**0.18** → **0.16** → **0.20** → **0.21** memory → **0.15** → **0.14** → **0.19**.

Explicitly **not** in this lock: Cursor Cloud/Bridge, in-process multi-agent graph, full Cursor hooks/plugins/team MDM, OS sandbox clone, notebook cell editor, DAP debugger, public plugin marketplace.

**First-party IDE plugin UI** was previously on this exclusion list. It is now in scope as **0.35** — a VS Code extension over a new editor-host protocol ([35](../../design/v0.x/35-vscode-extension.vscode.md)), which supersedes that non-goal for the client only. Chapter [32](../../design/v0.x/32-sandbox-code-review.md) (hosted VS Code Server for sandbox review) is a separate, complementary line.

Parent index: [`doc/roadmaps/README.md`](../README.md).
