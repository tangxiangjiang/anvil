# 28 — Web Clusters / Nodes / Workers

**Product version focus:** **0.28** ([m_0.28_clusters_workers.md](../../roadmaps/v1.0/m_0.28_clusters_workers.md))  
**Status:** done

## Intent

Ops console models the fleet as **Schedulers → Nodes → Workers**. Nodes join via agentd (web never adds nodes). Adding a worker runs on agentd: validate path, clone git when needed, refuse nonempty non-git dirs.

## Information architecture

```text
Nav Clusters
  → Scheduler fold (name, description, listen IP, online)
    → Node fold (hostname, name, description, status)
      → Workers (project path, git remote, status) + Add / Remove
```

| Layer | Source | Web can add? |
|-------|--------|--------------|
| Scheduler (Cluster) | web `fleets[]` + scheduler `GET /v1/meta` | config only |
| Node | agentd `cluster.join` / heartbeat | no |
| Worker | project binding on that agentd | yes |

## Protocol

### Scheduler

- Config: `name`, `description` (optional).
- `GET /v1/meta` → `{ name, description, listen_app, listen_cluster }`.
- Join/heartbeat carry `hostname`, `name`, `description` on nodes (`GET /v1/nodes`).
- `POST /v1/nodes/{id}/workers` `{ project_path, git_url? }`
- `DELETE /v1/nodes/{id}/workers/{worker_id}` (idle only; disk kept)

### Agentd (cluster RPC)

- `cluster.worker_add` / `cluster.worker_remove`
- Git rules: missing path + `git_url` → clone; existing git → read origin; nonempty non-git → error.

### Web BFF (multi-fleet)

- `GET /api/v1/clusters` — poll each `fleets[]` scheduler (shared token); best-effort per fleet.
- `POST|DELETE /api/v1/clusters/{fleet_id}/nodes/{node_id}/workers…`
- `PATCH …/nodes/{node_id}` drain/enable

## UI

- Nav **Clusters** (`#/clusters`); foldable scheduler/node rows; add-worker modal.
- No “Add node”.

## Non-goals

- Per-fleet scheduler tokens (shared `ANVIL_SCHEDULER_TOKEN` for 0.28)
- Flutter worker CRUD (consume richer node fields; add/delete follow-up)
- Deleting project files on worker remove

## Follow-on

Sandbox root + web FS browser + worker name/description: [29-sandbox-workers.md](29-sandbox-workers.md).
