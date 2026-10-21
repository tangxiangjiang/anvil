# 11 — Worker daemon & agent-worker

**Product version focus:** **0.11** ([m_0.11_worker_daemon.md](../../roadmaps/v1.0/m_0.11_worker_daemon.md))  
**Status:** done (implementation through p_0.11.1–p_0.11.5)

## Intent

Ship a **local worker runtime** beside the in-process Anvil SDK:

- **`agentd` (worker-daemon)** — long-lived process on one machine: owns worker lifecycle and is the only cluster edge for that host.
- **`agent-worker`** — child process that runs a **restricted Anvil Local agent** against an assigned `cwd`.

Orchestration hosts (CLI, CI, or products like cloud-agent Gateway) talk to **daemon**, never spawn naked workers into a cluster. The public Go package `anvil` stays an in-process library; daemon/worker are additive binaries and a small control protocol.

This is **not** Cursor sub-agent graph parity and **not** a model gateway. LLM traffic still goes OpenAI-compatible / Anthropic (optionally via a separate model gateway in front of Anvil).

## Glossary

| Term | Meaning |
|------|---------|
| **agentd / worker-daemon** | Per-machine supervisor: start/stop/health/quota workers; optional cluster join + job forward. |
| **agent-worker** | One execution slot: Anvil Local agent in a child process (or equivalent isolation unit). |
| **Controller / host** | Caller that assigns work (local CLI, cloud-agent Slave, CI). Speaks daemon control protocol. |
| **Job** | One assignable unit: prompt (or follow-up), `cwd`/repo binding, limits, cancel token. |
| **Cluster edge** | Daemon’s optional face that registers with a cluster control plane and relays jobs to local workers. |
| **Scheduler / coordinator** | Cluster control plane. In-tree **`anvil-scheduler`** (0.24+) is the app-facing hub: HTTP for apps, cluster edge for agentd, LLM provision + job assign. Not required for 0.11 local-only path. |

Naming note: prefer **worker** / **daemon**, not slave/master.

## Topology

```text
                    ┌─────────────────────┐
                    │ Scheduler (optional)│  cluster control plane
                    └──────────┬──────────┘
                               │ join / assign / result
                               ▼
┌──────────────┐      ┌────────────────────┐
│ Controller   │─────▶│ agentd (this host) │  local control + cluster edge
│ CLI / host   │      └─────────┬──────────┘
└──────────────┘                │ spawn / IPC
                     ┌──────────┼──────────┐
                     ▼          ▼          ▼
               agent-worker  agent-worker  …
                     │
                     ▼
               anvil.Local (Create/Prompt/Send/Stream/Wait)
                     │
                     ▼
               model gateway or direct LLM HTTP
```

**Rules**

1. **One agentd per machine** (or per strong isolation domain). Workers do not register with the cluster themselves.
2. **Daemon manages processes; it does not run the LLM tool loop.** Cognition stays inside agent-worker → Anvil.
3. **Local-first.** Unix domain socket (POSIX) / named pipe (Windows) + length-prefixed JSON (or similar) RPC. Cluster is an extra path on the same daemon, not a second supervisor.
4. **In-process SDK unchanged.** Embedding `anvil` in a single binary remains valid; daemon is for multi-job / host / fleet cases.

## agentd responsibilities

### Local plane (required in 0.11)

| Concern | Behavior |
|---------|----------|
| Lifecycle | Spawn/kill workers; restart policy; drain on shutdown |
| Capacity | Max concurrent workers; queue or reject when full |
| Health | Heartbeat / liveness; mark unhealthy; reclaim slots |
| Binding | Map job → allowlisted `cwd` / repo id (host policy); never trust client-supplied absolute paths alone |
| Assign | Hand job to an idle worker; stream events back to controller |
| Cancel | Propagate cancel to worker run; best-effort then force-kill after grace |
| Observability | Structured logs; job/worker ids; no secrets in logs |

### Cluster plane (designed in 0.11; implement after local works)

| Concern | Behavior |
|---------|----------|
| Join | Register node id, capacity, labels with scheduler |
| Auth | Token / mTLS as configured; keys only from env / secret files |
| Forward | Accept cluster assign → same local assign path |
| Report | Heartbeat capacity + job terminal status / errors upstream |
| Non-goal here | Global scheduling policy, multi-tenant billing, cross-region placement |

Cluster scheduling **strategy** lives in the scheduler; agentd only **advertises capacity** and **executes/forwards** to local workers.

## agent-worker responsibilities

- Run **one active Anvil agent/run** at a time (same concurrency rule as SDK MVP: one Run per Agent).
- Receive: job id, cwd (from daemon allowlist), model/base URL env, prompt / resume id, limits (`MaxSteps`, timeouts).
- Emit: stream events compatible with Anvil run events (assistant / tool_call / tool_result / status / done / error) so hosts can map to their own schemas (e.g. cloud-agent `task.event`).
- Exit or return to idle pool when job completes; daemon decides reuse vs recycle.
- Sandbox = Anvil cwd tools + existing 0.8 security posture; daemon may add OS-level limits later (cgroups / job objects) without changing the SDK API.

Workers are **not** a second public API surface for app authors. Authors either use `anvil` in-process or speak **agentd**.

**Plugins (0.20):** optional capability packs **Init inside the worker** (or in-process SDK), not inside agentd. Daemon config `plugins.available` / `deny` / `search_paths` intersects `job.assign.plugins`; denied ids never Init. `hello_ok` and cluster heartbeat advertise `plugin.<id>` labels — see [20-plugins.md](20-plugins.md).

## Control protocol (sketch)

Transport: local IPC only for the controller↔daemon path in 0.11 core.

Suggested message kinds (names illustrative):

| Kind | Direction | Purpose |
|------|-----------|---------|
| `hello` / `hello_ok` | both | Version, auth, capabilities |
| `worker.list` | C→D | Capacity and worker states |
| `job.assign` | C→D | Job payload + cwd binding |
| `job.event` | D→C | Streamed run events |
| `job.cancel` | C→D | Cancel |
| `job.done` | D→C | Terminal status + result summary |
| `cluster.*` | D↔scheduler | Join / heartbeat / assign / result (phase after local) |

Daemon↔worker IPC may be the same framing or a narrower stdin/stdout JSONL protocol; keep it internal.

Version the protocol (`proto: 1`). Breaking changes bump proto or product minor with a design note.

## Relation to public SDK & packages

```text
source/
  … existing anvil library …
  cmd/
    anvil/           # existing operator CLI
    agentd/          # worker-daemon (0.11)
    agent-worker/    # worker entry (0.11; may be invoked only by agentd)
  internal/
    daemon/          # listen, registry, assign, cluster client
    worker/          # job runner wrapping anvil.Create/…
```

- Library import path stays `github.com/anvil-dev/anvil`.
- Daemon must not require Cursor SDK or Cursor runtime.
- Model catalog / routing stays outside: agentd `llm.base_url` (0.23+), scheduler-provisioned llm (0.24+), or `ANVIL_BASE_URL` / worker env points at a **model gateway** or provider. agentd does not proxy LLM HTTP — the worker Completer calls that URL. Fleet apps prefer **anvil-scheduler** HTTP ([24-scheduler.md](24-scheduler.md)); ops config is JSON or YAML ([23-yaml-config.md](23-yaml-config.md)).

## Security

- Controller auth on local socket (token file / SO_PEERCRED where available); do not expose agentd on public TCP by default.
- Cluster listen/dial only when explicitly configured.
- cwd allowlist owned by daemon config (same idea as cloud-agent Slave `projects[].cwd`).
- Redact API keys in logs (align with 0.8).
- Cancel + force-kill deadline to avoid stuck workers holding slots.

## Explicit non-goals (0.11)

- Replacing cloud-agent **Gateway / App / workflow DAG** (those stay host-side; they may *consume* agentd later).
- In-process multi-agent graph / planner handoff inside `anvil.Create`.
- Cursor Bridge, Cloud agents, IDE/LSP.
- Model gateway implementation (adjacent product; listed in post-0.10 scope lock, not this chapter).
- Retrieval index / skills catalog (separate upcoming chapters).
- Full Kubernetes operator / autoscaler.
- Pixel-compatible `@cursor/sdk` event schema (map at the host).

## Migration note (cloud-agent)

cloud-agent Slave today calls `@cursor/sdk` Local. A later integration can:

1. Keep Gateway + contracts.
2. Point Slave (or a Go host) at **agentd** instead of Cursor Local.
3. Map `job.event` → existing `task.event`.

That integration is **out of Anvil 0.11 acceptance** unless a phase explicitly lists a minimal adapter; design only guarantees a stable local assign/stream/cancel path.

## Roadmap mapping

Delivered via [m_0.11_worker_daemon.md](../../roadmaps/v1.0/m_0.11_worker_daemon.md) phases `p_0.11.1` … `p_0.11.5`.
