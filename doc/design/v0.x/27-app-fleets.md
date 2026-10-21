# 27 — Mobile fleet picker + cluster ops

**Product version focus:** **0.27** ([m_0.27_app_fleets.md](../../roadmaps/v1.0/m_0.27_app_fleets.md))  
**Status:** done

## Intent

Flutter app bakes the **anvil-web** URL at build time (`--dart-define=ANVIL_WEB_URL`). Login is username/password only. After login, web returns a **fleet catalog** (schedulers); the user picks one and talks to that scheduler with the web session Bearer for cluster + jobs + models.

## Flow

```text
App stores web URL
  → POST /api/v1/auth/login
  → GET /api/v1/fleets
  → user selects fleet (scheduler_url)
  → scheduler /v1/nodes|jobs|models (Bearer = web session)
```

## Web API

`GET /api/v1/fleets` (auth): `{ fleets: [{ id, name, scheduler_url }] }`

Config: optional `fleets:` list; default one fleet from `scheduler_url`.

## App surfaces inside a fleet

| Tab | Ops |
|-----|-----|
| Cluster | List nodes/workers; `+/- max_workers`; drain/enable |
| Jobs | `POST /v1/jobs` |
| Models | `GET /v1/models` |

## Non-goals

- Multi-scheduler token isolation beyond shared web sessions DB
- Fleet CRUD UI in anvil-web (config file only for 0.27)
