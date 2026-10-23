# 32 — Sandbox code review + VS Code Server

**Product version focus:** **0.32** ([m_0.32_sandbox_code_review.md](../../roadmaps/v0.x/m_0.32_sandbox_code_review.md))  
**Status:** in_progress

## Intent

1. **anvil-web read-only workbench** on a sandbox / worker project: file tree, open file, git status + diff.  
2. **agentd can launch VS Code Server** (`code-server` or compatible) against a project directory configured **the same way as workers** (sandbox-relative path, optional git clone rules).  

Operators review in Anvil; for real editing they open the node-hosted VS Code Server URL. Anvil web never becomes a code editor.

Builds on sandbox list from [29](29-sandbox-workers.md). Complements Flutter execute/review later; **0.32 ships web + agentd code-server control**.

## Product direction (locked 2026-10-23)

The Web workflow surface is frozen. New workflow interaction belongs in clients built for active
work: the Anvil VS Code extension first, and the Flutter client in parallel as its slower,
portable alternative.

`Workspace` is a durable workflow/domain definition stored in the service database. It is not the
VS Code folder and it is not client-owned state. A checkout opened by desktop VS Code or a hosted
VS Code Server is an execution projection of that Workspace. The service remains the source of
truth for Workspace structure, workflow runs, approvals and audit history.

The resulting product split is:

| Surface | Responsibility |
|---------|----------------|
| Workflow service + database | Workspace definitions, run state, scheduling, permissions, approvals and event history |
| VS Code extension | Primary workflow authoring/execution UI; code context, live progress and human decisions |
| Flutter client | Alternative pure-client workflow UI over the same service contract |
| anvil-web | Review, audit, administration and **Open in VS Code**; no new workflow authoring UI |
| Hosted VS Code Server | Cloud editing and workflow execution using the same Anvil extension as desktop VS Code |

The active clients connect to the workflow service over a versioned WebSocket event protocol.
HTTP bootstrap supplies identity, Workspace metadata and a short-lived connection ticket.
Commands carry idempotency ids; events carry monotonic sequence numbers; reconnect resumes from
the last sequence and falls back to a database snapshot when a gap cannot be replayed. The
extension and Flutter may cache projections, but neither becomes an independent workflow state
machine.

This decision keeps two **client choices**, not two product implementations: desktop/hosted VS
Code for the fastest code-centric path, and Flutter for portable pure-client work. Both observe
and mutate the same database-backed Workspace.

## Product split (locked)

| Surface | Role |
|---------|------|
| anvil-web | Read-only review/audit UI; start/stop control; **owns IDE access auth** (session) |
| nginx | Fixed reverse proxy **to the node**; optional `auth_request` to web |
| agentd ide-proxy | Dynamic HTTP/WS proxy `sid` → loopback; enforce/trust web auth |
| VS Code Server | Cloud edit/workflow UI through the Anvil extension (`127.0.0.1`, `--auth none`) |
| Flutter app | Alternative execute/review/workflow client (iterated separately); not required in 0.32 |

**Never in Anvil web:** save file, apply patch, `git add` / `commit` / `push` / branch
switch UI, embedded Monaco editor for editing, or a second workflow authoring/execution UI.

## Scope

### In — read-only review

- Browse sandbox (existing list) and **open a worker project root** as the workbench cwd.  
- **Read file** by sandbox-relative path (size cap; text + binary refusal).  
- **Git status** / **git diff** (caps + truncated flag).  
- Web UI: tree | read-only viewer | Changes + diff.  
- All paths confined to `sandbox_dir` (same resolve rules as 0.29).

### In — VS Code Server (agentd)

- Configure a **code-server instance** with the **same project directory UX as add-worker**:
  - sandbox FS browser + mkdir (reuse 0.29)
  - `project_path` absolute or relative to `sandbox_dir`
  - optional `git_url` with the same clone / refuse-nonempty-non-git rules as workers ([28](28-clusters-workers.md) / [29](29-sandbox-workers.md))
  - optional `name` / `description` for the IDE session list  
- agentd **starts** VS Code Server with that folder as workspace; **stops** / **status**; returns public URL.  
- **IDE access auth is owned by anvil-web** (same login session as the console); code-server itself runs **unauthenticated on loopback** behind ide-proxy.
- Persist IDE session registry under sandbox (e.g. `.anvil/code_servers.json`), similar spirit to `workers.json`.  
- Web: per node / worker — **Edit in VS Code** (start if needed → open URL).

### Out

- Any write to file contents or delete/rename via Anvil HTTP.  
- Interactive git mutations from Anvil UI.  
- Bundling or shipping VS Code bits inside Anvil releases (operator installs `code-server` / binary on the node).  
- Separate code-server password UI / second login for IDE (web session is enough).  
- Delivery pipeline graph tool layer (`DELIVERY_PIPELINE_VISION.md` — separate track).

`sandbox_mkdir` remains for folder creation when adding workers / IDE sessions; not a general edit API.

## Ingress (locked): nginx → node → dynamic IDE proxy

**Do not** put code-server traffic through anvil-web, and **do not** reload nginx per session.

```
Browser ──► nginx (fixed per node) ──► agentd ide-proxy ──► 127.0.0.1:{session_port}
              /ide/... or host-based       (by sid)           code-server
```

| Layer | Responsibility |
|-------|----------------|
| **nginx** | Stable reverse proxy to the **node**; optionally `auth_request` → anvil-web before IDE traffic. |
| **agentd ide-proxy** | Routes `/ide/{sid}/…` → loopback code-server; auth via web session (or trust nginx auth_request). |
| **code-server** | `127.0.0.1` + `--auth none`; never a public URL. |
| **anvil-web** | Login/session store; control APIs; IDE gate; **Open** → public URL (not raw `:187xx`). |

Illustrative nginx (static + web auth):

```nginx
location = /api/v1/auth/session {
    proxy_pass http://anvil-web;
    proxy_pass_request_body off;
    proxy_set_header Content-Length "";
    proxy_set_header X-Original-URI $request_uri;
}

location /nodes/node-a/ide/ {
    auth_request /api/v1/auth/session;
    proxy_pass http://agentd-host:IDE_PROXY_PORT/ide/;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_set_header Cookie $http_cookie;
    proxy_read_timeout 86400;
}
```

agentd returns `url` like `https://anvil.example/nodes/node-a/ide/{sid}/` (from `public_base` + sid), never `http://127.0.0.1:18701`.

## Agentd config

Extend agentd YAML (illustrative):

```yaml
sandbox_dir: /data/anvil-sandbox
code_server:
  enabled: true
  # Absolute path to code-server (or openvscode-server) binary; required if enabled.
  bin: code-server
  # Local-only bind for each code-server child (always loopback).
  bind: 127.0.0.1
  base_port: 18700
  # ide-proxy listen (what nginx points at). Separate from control/cluster ports.
  proxy_listen: 127.0.0.1:18600
  # Public URL prefix nginx exposes for this node (normalize in code).
  # Example: https://anvil.example/nodes/node-a/ide
  public_base: https://anvil.example/nodes/node-a/ide
  # code-server child: always --auth none (loopback). Access control = web session.
  # URL of anvil-web used by ide-proxy to validate sessions (auth_request / introspect).
  web_auth_url: https://anvil.example/api/v1/auth/session
```

If `code_server.enabled` is false or `bin` missing → start RPC returns clear error; review UI still works.

## Auth (IDE data plane) — anvil-web owns it

**Rule:** Anyone who can use the IDE must already be logged into anvil-web. No second password prompt from code-server.

```
Browser (cookie / Bearer from web login)
   → nginx
   → agentd ide-proxy
        → check session with anvil-web (fail → 401)
        → proxy to 127.0.0.1:code-server (--auth none)
```

Recommended MVP mechanisms (pick one in implementation; both are “web owns auth”):

1. **nginx `auth_request`** to anvil-web (e.g. `GET /api/v1/auth/session`) before proxying to ide-proxy — simplest ops story.  
2. **ide-proxy introspect**: forward `Cookie` / `Authorization` to `web_auth_url`; 2xx → allow proxy. Needed if nginx cannot auth_request.

Web responsibilities:

- Existing login/session store remains the only identity.  
- Optional: on **Open**, issue a short-lived **IDE ticket** (query or cookie scoped to `/ide/`) so WebSocket upgrades still carry proof without brittle header forwarding — still minted and validated by web.  
- Control APIs (`POST …/code-servers`) already require web/scheduler Bearer; unchanged.

code-server: **`--auth none`**, bind loopback only. Do not configure code-server password as the product auth path.

## Agentd RPC

### Read / git

| Kind | Body | Result |
|------|------|--------|
| `cluster.sandbox_read` | `{ path }` | `{ path, content?, encoding, size, truncated?, binary?, error? }` |
| `cluster.git_status` | `{ project_path }` | status JSON (branch, entries[], truncated?) |
| `cluster.git_diff` | `{ project_path, staged?, paths[]?, context? }` | `{ diff, truncated?, error? }` |

### Code-server lifecycle

| Kind | Body | Result |
|------|------|--------|
| `cluster.code_server_list` | `{}` | `{ sessions: [...] }` |
| `cluster.code_server_start` | same shape as worker add: `{ name?, description?, project_path, git_url? }` | `{ id, project_rel, url, status, error? }` |
| `cluster.code_server_stop` | `{ id }` | `{ id, status, error? }` |
| `cluster.code_server_status` | `{ id? }` | session(s) + **public** `url` if running |

Start rules:

1. Resolve `project_path` via existing sandbox join (+ optional git clone like `worker_add`).  
2. Allocate **loopback** port (`bind` + `base_port` + slot); spawn `bin` with workspace = resolved absolute path and **`--auth none`**.  
3. Register sid → local port in ide-proxy; persist session (`pid`, port, `project_rel`, name).  
4. Return **public** `url` = `{public_base}/{sid}/` (plus any folder query the binary needs).  
5. Idempotent: same `project_rel` already running → return existing public URL.

**ide-proxy** (same agentd process or sibling listener on `proxy_listen`):

- **Authenticate first** via anvil-web session (or trust nginx that already ran `auth_request` — document which mode).  
- Strip prefix, map `sid` → `http://127.0.0.1:{port}/…`.  
- Support WebSocket upgrade (required for VS Code Server); auth must apply to upgrade requests too.  
- Unknown / stopped sid → 404; unauthenticated → 401.

Stop: SIGTERM child; drop sid from proxy map; mark stopped.

Heartbeat may advertise `code_server: true` / running count / `proxy_listen` (optional).

## Scheduler / web BFF

| Method | Path |
|--------|------|
| `GET` | `/v1/nodes/{id}/sandbox/file?path=` |
| `GET` | `/v1/nodes/{id}/git/status?project=` |
| `GET` | `/v1/nodes/{id}/git/diff?project=&staged=&path=` |
| `GET` | `/v1/nodes/{id}/code-servers` |
| `POST` | `/v1/nodes/{id}/code-servers` body = start (worker-add-like) |
| `POST` | `/v1/nodes/{id}/code-servers/{sid}/stop` |
| `GET` | `/v1/nodes/{id}/code-servers/{sid}` |

Web BFF: `/api/v1/clusters/{fleet}/nodes/{id}/…`.

No `PUT` for file bodies.

## Web UI

### Review workbench

Entry: Clusters → Node → Worker **Review** (or sandbox “Open”).

```
┌──────────────┬────────────────────────────┬─────────────────┐
│ File tree    │ File viewer (read-only)    │ Changes         │
│              │ + [Edit in VS Code]        │ status → diff   │
└──────────────┴────────────────────────────┴─────────────────┘
```

Banner: “Read-only in Anvil · edit via VS Code Server on this node”.

### VS Code Server

- Same modal pattern as **Add worker**: name, description, sandbox browser, optional git URL → **Start VS Code Server**.  
- List running/stopped sessions on the node; **Open** (new tab to **public** URL via nginx), **Stop**.  
- From worker row: shortcut start/open for that worker’s `project_rel`.

## Security

- Sandbox path confinement (0.29).  
- code-server **loopback only** + `--auth none`; **anvil-web session** gates all IDE access.  
- nginx is static (per node); dynamic port map never leaves the node.  
- Control plane and IDE data plane both ultimately trust the **same web session store**.  
- Diff/file caps; secret redact when cheap.

## Acceptance (milestone)

1. Web: browse tree, view file, git status + diff for a sandbox project.  
2. No Anvil API/UI writes file contents or mutates git.  
3. Path outside sandbox rejected.  
4. With `code_server` configured: start session using **worker-equivalent project config**; **Open** uses public URL via nginx → ide-proxy (not raw loopback port); stop cleans up process + proxy map.  
5. Logged-out / invalid web session cannot load IDE (401); logged-in session can. No code-server password prompt.  
6. Missing binary → actionable error; review UI still usable.  
7. Starting/stopping sessions does **not** require changing nginx config.

## Phases

See roadmap [m_0.32](../../roadmaps/v0.x/m_0.32_sandbox_code_review.md).
