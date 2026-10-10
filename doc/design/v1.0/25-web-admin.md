# 25 — Web fleet ops console (anvil-web)

**Product version focus:** **0.25** ([m_0.25_web_admin.md](../../roadmaps/v1.0/m_0.25_web_admin.md))  
**Status:** done

## Intent

Ship **`anvil-web`**: an extensible ops console for cluster fleet management. First delivery is node/worker status, capacity scale, drain, and token usage stats — not the feature ceiling.

## Topology

```text
Browser ──Bearer session (7d)──▶ anvil-web ──Bearer scheduler──▶ anvil-scheduler
                │ SQLite users/sessions              │ cluster.control
                │                                    ▼
                │                                 agentd (slots)
```

- No separate auth microservice.
- Browser never receives `ANVIL_SCHEDULER_TOKEN`.
- UI: JSON API + Alpine/Chart shell (not Go template SSR).

## 0.25 features

1. Node + worker slot status  
2. Scale `max_workers` up/down  
3. Enable / drain node  
4. Token usage aggregates  

## Extensibility

- Backend: `/api/v1` handlers on web Server; `schedclient` for scheduler.  
- Frontend: app shell, hash router, `nav` registry, `features/*.js`.  
- Reserved later: LLM UI, jobs, RBAC, OIDC.

## Control plane additions

- Heartbeat includes `workers[]` and `accepting`.  
- `cluster.control`: `{set_max_workers?, accepting?}`.  
- `JobDoneBody.usage` → scheduler `/v1/usage`.  
- `PATCH /v1/nodes/{id}` on scheduler.

## Roadmap mapping

[m_0.25_web_admin.md](../../roadmaps/v1.0/m_0.25_web_admin.md) phases `p_0.25.1` … `p_0.25.4`.
