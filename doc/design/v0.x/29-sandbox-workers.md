# 29 — Sandbox workers + web FS browser

**Product version focus:** **0.29** ([m_0.29_sandbox_workers.md](../../roadmaps/v1.0/m_0.29_sandbox_workers.md))  
**Status:** done

## Intent

Every project worker on agentd runs under a configured **sandbox directory**. Operators pick or create folders inside that sandbox from the web Clusters UI (filesystem browser), and give each worker a **name** and **description** so lists stay readable.

## Agentd

- Config: `sandbox_dir` (absolute after normalize; created if missing).
- Worker `project_path` must resolve inside `sandbox_dir` (relative paths preferred from UI).
- Worker state: `name`, `description`, `project_rel` (sandbox-relative).
- Cluster RPC:
  - `cluster.sandbox_list` / `cluster.sandbox_mkdir` → `cluster.sandbox_result`
  - `cluster.worker_add` accepts `name`, `description`, sandbox-relative `project_path`
- Heartbeat advertises `sandbox_dir`.

## Scheduler / web

- `GET /v1/nodes/{id}/sandbox?path=`
- `POST /v1/nodes/{id}/sandbox/mkdir` `{ path }`
- Web BFF mirrors under `/api/v1/clusters/{fleet}/nodes/{id}/sandbox…`
- Clusters modal: name, description, sandbox FS browser (list / mkdir / select), git URL.

## Non-goals

- Editing files in the sandbox from web
- Deleting sandbox folders from web
- Per-worker sandbox roots

Read-only file view + git review + agentd VS Code Server: [32-sandbox-code-review.md](32-sandbox-code-review.md).
