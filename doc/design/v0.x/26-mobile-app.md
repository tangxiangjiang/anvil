# 26 — Mobile app (Flutter) + web session on scheduler

**Product version focus:** **0.26** ([m_0.26_mobile_app.md](../../roadmaps/v0.x/m_0.26_mobile_app.md))  
**Status:** done

## Intent

Ship an in-tree **Flutter** client under `app/` for Anvil job work. Fleet ops stay on **anvil-web**; the phone app logs into web for a user session Bearer, then calls **anvil-scheduler** app HTTP with that same token for models and jobs.

## Topology

```text
Flutter ──POST /api/v1/auth/login──▶ anvil-web (SQLite users/sessions)
    │                                    │
    │ Bearer session_token               │ same sessions.db (read)
    ▼                                    ▼
    └──── GET/POST /v1/* ──────────▶ anvil-scheduler (app listener)
                                           │ cluster.assign / done
                                           ▼
                                        agentd
```

- Browser / Flutter never receive `ANVIL_SCHEDULER_TOKEN`.
- Scheduler **app** listener accepts: static scheduler token **or** a valid web session token.
- Scheduler **cluster** edge accepts **static token only** (agentd join).

## Auth bridge

- Web and scheduler share the web sessions SQLite path (`web_sessions_db` on scheduler).
- `LookupSession(plaintext)` validates Bearer on scheduler app routes.
- No JWT, no token exchange endpoint in 0.26.

## App product surface (0.26)

| Feature | Behavior |
|---------|----------|
| Login | Username/password → anvil-web; store web + scheduler base URLs + token |
| Job chat | `POST /v1/jobs` with `repo_id`, `prompt`, optional model/resume; wait `JobDoneBody` |
| Models | `GET /v1/models` read-only picker |
| Fleet | **Not in app** — use anvil-web |

## Explicit non-goals

- Milestone / workflow DAG / diff / review / project sync / WebSocket event streams
- Exposing scheduler static token in the mobile app
- Proxying jobs through anvil-web BFF
- Per-user RBAC beyond “any valid web session may call scheduler app HTTP”

These are 0.26 scope boundaries, not a permanent product boundary. After 0.26, Flutter continues
as the pure-client alternative for database-backed Workspace workflows. It will consume the same
versioned HTTP-bootstrap/WebSocket contract as the VS Code extension; it must not create a second
workflow state model. VS Code is expected to deliver workflow UX faster because code, diffs and
tools are already local to the editor, while Flutter remains the portable choice.

The web client does not compete with either active-work client. Its long-term role is
review/audit, administration and launching hosted VS Code. See
[32 — Product direction](32-sandbox-code-review.md#product-direction-locked-2026-10-23).

## Placement

- Flutter tree: repository root `app/` (outside `source/`).
- Go auth/session code remains under `source/`.

## Roadmap mapping

[m_0.26_mobile_app.md](../../roadmaps/v0.x/m_0.26_mobile_app.md) phases `p_0.26.1` … `p_0.26.4`.
