# 34 — Workspace domain (Clusters: Scheduler → Workspace → Project → Workers)

**Product version focus:** **0.34** ([m_0.34_workspace_domain.md](../../roadmaps/v1.0/m_0.34_workspace_domain.md))  
**Status:** done

## Intent

Reshape **Clusters** around a product hierarchy:

**Scheduler → Workspace → Project → Workers**

- **Workspace** — collaboration domain; one **Workspace Token**; Dev Nodes join with that token.  
- **Project** — logical engineering unit; **Delivery** (Req → Design → Roadmap → Test) binds here.  
- **Worker** — one sandbox checkout (path + branch) on a Dev Node; many Workers may share one Project (parallel branches).  
- **Dev Node** — physical machine (agentd); not a product browse layer; shown on Worker cards / Machines tab.

Vision: [`DELIVERY_PIPELINE_VISION.md`](../../../DELIVERY_PIPELINE_VISION.md) §4.0.

## Clusters UI

| Page | Content |
|------|---------|
| Scheduler cards | Config fleets; open → workspaces |
| Workspace cards | + create (shows token); body / **View** → **Workspace View (Graph)** |
| Workspace View | Topology: Project nodes + Workers under each; Project `depends_on` edges; Worker deps as secondary; tabs → Projects list / Machines |
| Workspace Projects | Project cards + + slot; Token; Dev Node count |
| Project detail | Worker cards + + slot; Start/Shutdown; × Remove + confirm; hover tip |
| Add Worker | Existing dir / new folder (`git init` + README + AI Chat) / worktree branch; Worker `depends_on`; Project deps on create |

## Rules (locked)

1. **1 Worker = 1 working copy** — distinct `project_path`; prefer `git worktree` for extra branches.  
2. **Delivery follows Project**, not Worker. Execute picks a Worker checkout.  
3. **depends_on** — declare only outgoing edges (Project→Project and/or Worker→Worker for debug).  
4. **Remove Worker** keeps disk files by default.

## Persistence / API

| Entity | Where |
|--------|-------|
| Workspace + token | anvil-web SQLite + mirrored on scheduler for join auth |
| Project + project_deps | anvil-web SQLite |
| Worker runtime | agentd (existing); binding `project_id` on web |
| Join | agentd `workspace_token` → scheduler tags `workspace_id` on node |

Web BFF:

- `/api/v1/workspaces` CRUD (scoped by `fleet_id`)  
- `/api/v1/workspaces/{id}/projects` CRUD + deps  
- `/api/v1/workspaces/{id}/graph` — projects + edges for View  
- Existing `/api/v1/clusters…` workers start/shutdown/remove unchanged  

## Delivery entry (follow-up to 0.34)

- Delivery home = **Project picker** (filter by name/key), grouped by Workspace.  
- eng-req binds `project_id` (+ optional default Worker for execute/path).  
- Clusters Project page **Open in Delivery** is a convenience deep link, not the primary find path.

## Out of scope (0.34)

- UI to add Dev Nodes; multi-workspace per node.  
- Incoming “depended-by” editor.
