# 33 — Delivery UI + schema (anvil-web)

**Product version focus:** **0.33** ([m_0.33_delivery_ui.md](../../roadmaps/v1.0/m_0.33_delivery_ui.md))  
**Status:** done

## Intent

Ship **anvil-web Delivery** with frozen UI surfaces **and** the durable store / HTTP API those surfaces own — so schema matches the Worker-bound, `major.minor.patch` model before markdown export and GraphPatch land.

Vision: [`DELIVERY_PIPELINE_VISION.md`](../../../DELIVERY_PIPELINE_VISION.md) §4.

## Product split

| Surface | 0.33 |
|---------|------|
| anvil-web **Delivery** | Requirements / Designs / Roadmap |
| Persistence | SQLite tables in `anvil-web` `db_path` (same file as auth) |
| HTTP | `/api/v1/delivery/*` (session Bearer) |
| Markdown export / GraphPatch / agent jobs | **Out** (later milestones) |
| Flutter | Out |

## Requirements model (locked)

1. **Bind to Worker** — one engineering requirement per `worker_id` (denormalized label / fleet / node / `project_rel` at create).  
2. **Reqs subdirectory pointer** — `reqs_rel` under that project (e.g. `docs/requirements`).  
3. **Versioned groups** after open:

| Segment | Meaning |
|---------|---------|
| **major** | Product generation / era (planned in `delivery_majors`) |
| **minor** | ↔ **milestone** |
| **patch** | ↔ **phase** |

Display / id form: `major.minor.patch` (e.g. `0.33.1`). Milestone create uses `major.minor`. Approve gate is **per version** (patch row).

**Generation plan** (`delivery_majors`): status is a one-way flow **planning → developing → sealed** (new majors always Planning; each advance needs confirm). **Start** (→ developing) requires the previous major to be **Sealed** (major `0` has no predecessor). Sealed majors reject new milestones.  
Only the **last** major in **Planning** can be deleted → soft-delete to `delivery_trash` (milestones included). Restore may **overwrite** or **append** if the major number already exists.

## Schema (SQLite)

Tables (prefix `delivery_`):

| Table | Role |
|-------|------|
| `delivery_majors` | Generation plan: major + status + title/summary |
| `delivery_eng_reqs` | Worker-bound eng-req + `reqs_rel` |
| `delivery_req_versions` | PK `(eng_req_id, major, minor, patch)` + structured fields |
| `delivery_designs` | Design bound to eng-req + version triple |
| `delivery_design_options` | Option cards under a design |
| `delivery_milestones` | Roadmap nodes (canvas x/y) |
| `delivery_phases` | Ordered phases under a milestone |
| `delivery_edges` | Milestone dependency edges |
| `delivery_gates` | Pending human approvals + deep-link `href` |

Working-state on the worker project (`.anvil/delivery/`) and markdown export are **not** required in 0.33; console SQLite is the source of truth for the UI.

## API (MVP)

| Method | Path | Notes |
|--------|------|--------|
| GET | `/api/v1/delivery` | Full snapshot for UI hydrate |
| POST | `/api/v1/delivery/reset` | Clear + reseed demo rows |
| POST | `/api/v1/delivery/eng-reqs` | Create eng-req (+ initial `1.0.1` version) |
| POST | `/api/v1/delivery/eng-reqs/{id}/versions` | Add version |
| PATCH | `/api/v1/delivery/eng-reqs/{id}/versions/{M}/{m}/{p}` | Update fields |
| POST | `/api/v1/delivery/eng-reqs/{id}/versions/{M}/{m}/{p}/approve` | Set approved + gate |
| PATCH | `/api/v1/delivery/designs/{id}` | ADR sections |
| POST | `/api/v1/delivery/designs/{id}/versions` | Add Design Version |
| PATCH | `/api/v1/delivery/designs/{id}/versions/{vid}` | Update Gate / Sub-architecture / Class design |
| DELETE | `/api/v1/delivery/designs/{id}/versions/{vid}` | Remove Version (draft) |
| POST | `/api/v1/delivery/designs/{id}/approve` | Approve when Versions complete |
| POST | `/api/v1/delivery/designs/{id}/bootstrap` | Seed roadmap phases from Versions |

Workers for the create picker: cluster inventory when available; otherwise snapshot may include denormalized workers from existing eng-reqs plus seed helpers.

## Screens

Sidebar: **Delivery** (collapsible) → children:

1. **Requirements** — rounded card list + `+` create wizard (scheduler → node → worker → project FS pick/create reqs dir; auto `major.minor` milestone) → version browser → detail + Approve  
2. **Designs** — list + detail (**structured ADR** + ordered **Versions**; no Option cards)  

**Design flow:** **Save** (draft) → **View** (composed Markdown) → **Approve** (after View; Versions complete). After Approve the workspace is **view-only** (no Save / edit / chat); toolbar shows **View** + **Bootstrap roadmap** to publish phases.  

**ADR storage** (`delivery_designs`): `adr_*` section columns; composed `adr` kept for export/legacy. Old rows with only `adr` hydrate into **Decision**.  

**Design Versions** (`delivery_design_versions`): ordered execution units under one Design/ADR — each has **Gate**, **Sub-architecture**, **Class design** (Markdown + mermaid Edit/Preview). Approve requires ≥1 Version with all three fields filled. Approve / Bootstrap seed eng-req patches + canvas phases 1:1 by sort order.  

3. **Roadmap** — canvas + inspector phase chain; milestone cards are **draggable** (`PATCH …/roadmap/milestones/{id}` persists `x`/`y`). No Gates tab — gates live on Design Versions.  

## Non-goals

- Deterministic markdown export  
- Real AI (chat may append a canned field patch)  
- GraphPatch apply / drag editor polish  
- VS Code Server / sandbox review (0.32)  
- Job binding / branch policy  

## Acceptance

- Logged-in operator: Delivery Requirements backed by SQLite API — create Worker-bound eng-req, browse `M.m.p`, edit and approve a version (survives reload).  
- Designs / Roadmap readable from the same store.  
- Design chapter + phases `p_0.33.1` (UI) / `p_0.33.2` (schema) documented.
