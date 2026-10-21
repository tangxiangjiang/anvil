# 24 — Scheduler (cluster controller + gateway catalog)

**Product version focus:** **0.24** ([m_0.24_scheduler.md](../../roadmaps/v1.0/m_0.24_scheduler.md))  
**Status:** done

## Intent

Ship an in-tree **`anvil-scheduler`**: the app-facing control plane for a fleet of **agentd** nodes. Apps configure gateway credentials and submit jobs over HTTP; agentd joins over the existing cluster edge and receives LLM defaults (including API key). Scheduler also lists models from **anvil-gateway**.

## Topology

```text
App ──HTTP Bearer──▶ anvil-scheduler ──GET /v1/models──▶ anvil-gateway
                           ▲  │
           cluster.join / HB │  │ cluster.join_ok + llm
           cluster.done      │  │ cluster.config (live llm)
                             │  │ cluster.assign
                           agentd ──▶ Embedded worker ──HTTP──▶ gateway
```

- Apps do **not** need agentd IPC for fleet ops.
- Scheduler does **not** proxy chat; workers call the gateway with provisioned Options.
- Local agentd `listen` remains for single-node debug.

## Cluster protocol additions

| Kind | Direction | Purpose |
|------|-----------|---------|
| `cluster.join_ok` | scheduler → agentd | Admission + optional `llm` body |
| `cluster.config` | scheduler → agentd | Live llm push after app `PUT /v1/llm` |
| (existing) join / heartbeat / assign / done | — | Capacity + job execution |

`llm` body: `base_url`, `api_key`, `model`, `provider`, `max_steps`. Never log `api_key`.

## App HTTP (MVP)

| Method | Path | Behavior |
|--------|------|----------|
| GET | `/v1/health` | Liveness |
| GET/PUT | `/v1/llm` | Get (redacted) / set credentials + defaults; fan-out `cluster.config` |
| GET | `/v1/nodes` | Joined nodes + last heartbeat |
| GET | `/v1/models` | Cached gateway catalog |
| POST | `/v1/jobs` | Pick idle node → assign → wait `cluster.done` |

Auth: `Authorization: Bearer <token>`.

## Scheduling policy (0.24)

Round-robin among nodes with `idle > 0`. No HA, no fairness queues, no event streaming to apps.

## Explicit non-goals

- Web admin UI
- `cluster.event` streaming
- Multi-scheduler HA
- Autoscaler beyond idle pick

## Roadmap mapping

[m_0.24_scheduler.md](../../roadmaps/v1.0/m_0.24_scheduler.md) phases `p_0.24.1` … `p_0.24.4`.
