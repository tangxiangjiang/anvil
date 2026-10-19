# 33 — Delivery UI + schema (anvil-web)

**Product version focus:** **0.33** ([m_0.33_delivery_ui.md](../../roadmaps/v1.0/m_0.33_delivery_ui.md))  
**Status:** done

## Intent

Ship **anvil-web Delivery** with frozen UI surfaces **and** the durable store / HTTP API those surfaces own — so schema matches the Worker-bound, `major.minor.patch` model before markdown export and GraphPatch land.

Vision: [`DELIVERY_PIPELINE_VISION.md`](../../../DELIVERY_PIPELINE_VISION.md) §4.

## Product split

| Surface | 0.33+ |
|---------|------|
| anvil-web **Delivery** | Requirements / Designs / Roadmap / **Test** (0.39.3) |
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
| PATCH | `/api/v1/delivery/designs/{id}/versions/{vid}` | Update Gate / Sub-architecture / Class design / `depends_on` |
| POST | `/api/v1/delivery/designs/{id}/versions/{vid}/gate-check` | Toggle Gate checklist item `{index, done}` |
| DELETE | `/api/v1/delivery/designs/{id}/versions/{vid}` | Remove Version (draft) |
| POST | `/api/v1/delivery/designs/{id}/approve` | Approve when Versions complete |
| POST | `/api/v1/delivery/designs/{id}/bootstrap` | Seed roadmap phases from Versions |
| PATCH | `/api/v1/delivery/roadmap/milestones/{id}` | Canvas `x`/`y`; Worker / `run_state`; `execute` / `submit_test` / `pass_test` / `fail_test` / `ready_dev` / `accept_milestone` |
| POST | `/api/v1/delivery/roadmap/milestones/{id}/acceptance-check` | Toggle Acceptance checklist `{index, done}` |
| POST | `/api/v1/delivery/buglist` | Optional create-only path for a buglist stub (Fail usually goes through milestone PATCH) |
| POST | `/api/v1/delivery/test-cases` | Batch create cases `{ eng_req_id, major, minor, cases:[{title,body}] }` |
| POST | `/api/v1/delivery/test-cases/run` | Mock Worker run for bind; failed cases spawn open buglist items |
| POST | `/api/v1/delivery/buglist/{id}/fix` | Mock Worker fix: `open` → `verified` |
| POST | `/api/v1/delivery/buglist/regress` | Mock regression for bind; `verified` → `closed` (or reopen) |
| POST | `/api/v1/delivery/jobs` | Create scheduler job for a phase (bind metadata + composed prompt); Start may call this internally |
| GET | `/api/v1/delivery/jobs/{id}/events` | Proxy scheduler job SSE (`job.event` / `job.done`) |

**Job bind (0.39.5+):** scheduler `JobAssignBody` accepts optional pass-through `milestone_id`, `phase_id`, `eng_req_id`, `major`, `minor`. Phases store `job_id` / `job_status` (`queued|running|finished|error|cancelled`). Roadmap **Start** enqueues the next unfinished phase; finished auto-advances while `run_state=running`.

**Campaign / Polish Jobs (0.39.8–9):** Test **Run** / **Regress** store `campaign_job_*` on the milestone; **Fix** stores `job_*` on the buglist row; Polish ▶ run stores `job_*` on the polish row. `phase_id` pass-through uses kind tags (`campaign:run`, `campaign:regress`, `campaign:fix:{id}`, `polish:{id}`).

**Gates hard check (0.39.10):** Design Version `Gate` markdown checkboxes (`- [ ]` / `- [x]`) must all be checked before phase Job enqueue (Start / auto-advance) and before Test **Run**. Text-only Gates (no boxes) do not block. Toggle via `POST .../versions/{vid}/gate-check`.

Workers for the create picker: cluster inventory when available; otherwise snapshot may include denormalized workers from existing eng-reqs plus seed helpers.

## Screens

Sidebar: **Delivery** (collapsible) → children. **Shared top layer:** each surface opens on **Generations** (`delivery_majors`) first — `major` / 大版本 — then drills into scoped work.

Routes: `#/delivery/{requirements|designs|roadmap|test}` → major index; `#/delivery/{section}/m/{M}/…` → scoped under that generation.

1. **Requirements** — Home keeps **Generation plan** (Edit / Start / Seal / Add) **and** Project picker. Click a major → `m/{M}` scoped milestones; open a Project → eng-req home (same Generation plan filter + milestones) → version detail + Approve.  

2. **Designs** — Generations → `m/{M}` design cards → detail (**structured ADR** + ordered **Versions**). New design defaults to current major.  

**Design flow:** **Save** (draft) → **View** (composed Markdown) → **Approve** (after View; Versions complete). After Approve the workspace is **view-only** (no Save / edit / chat); toolbar shows **View** + **Bootstrap roadmap** to publish phases.  

**ADR storage** (`delivery_designs`): `adr_*` section columns; composed `adr` kept for export/legacy. Old rows with only `adr` hydrate into **Decision**.  

**Design Versions** (`delivery_design_versions` + `delivery_design_version_deps`): execution units under one Design/ADR — each has **Gate**, **Sub-architecture**, **Class design**, and **Depends on** (DAG of sibling Versions). Approve requires complete fields and an acyclic graph; Bootstrap **topo-sorts** Versions then seeds eng-req patches + canvas phases 1:1 (renumbers `sort_order`).  

3. **Roadmap** — Generations → `m/{M}` **graph** (drag) + **control cards**: Worker, Start / Pause / Stop (`run_state` + **phase Jobs** via scheduler), live job snippet while running, **提交测试** (`in_development` → `testing`), Design / Requirements links, **Polish** ▶ run via Jobs (0.39.9). No separate execute page. No Gates tab — gates live on Design Versions.  

4. **Test** (0.39.3–0.39.4, Jobs in 0.39.8) — Generations → `m/{M}` **queue of milestones in lifecycle `testing` only**. Campaign: **AI assist** drafts cases → **Run** / **Fix** / **Regress** enqueue Jobs on the Roadmap Worker → SSE status on the card → **Pass** when campaign clean (`accept_status=pending`). **Fail** → buglist + **bugfix phases** + `awaiting_roadmap`; Roadmap **Ready** → `awaiting_development` then Start again (0.39.11). **Accept** on complete (0.39.12) records acceptance; merge-to-main not yet.

## Non-goals

- Deterministic markdown export  
- Real AI (chat may append a canned field patch)  
- GraphPatch apply / drag editor polish  
- VS Code Server / sandbox review (0.32)  
- Branch / milestone-accept merge policy (phase + campaign Jobs landed; accept→merge later)  

## Acceptance

- Logged-in operator: Delivery Requirements backed by SQLite API — create Worker-bound eng-req, browse `M.m.p`, edit and approve a version (survives reload).  
- Designs / Roadmap readable from the same store.  
- Design chapter + phases `p_0.33.1` (UI) / `p_0.33.2` (schema) documented.
