# Anvil Delivery Pipeline Vision

> Status: design notes (product direction). Not yet a versioned `doc/design/v1.0/` chapter.
> Scope: requirement → design → roadmap (node graph) → agent execute, on top of the existing Anvil stack.
> Date: 2026-10-14

---

## 1. Goal

Turn Anvil from “agent runtime + fleet jobs” into a **human + AI delivery system**:

```
Requirement → Design → Roadmap → Develop (app) → Test → Complete
                                         └─ test fail → Buglist → Roadmap (bugfix) ─┘
```

Text roadmaps (`doc/roadmaps/…`) remain the **exported artifact**. The **working surface** for planning is a material-editor-style **node graph**, polished by people and AI together.

Existing strengths to reuse (do not rebuild):

| Layer | Already in Anvil |
|-------|------------------|
| Design / roadmap docs (Anvil’s own delivery) | `doc/design/`, `doc/roadmaps/`, Cursor rules |
| Agent execute | SDK, `agentd` / `agent-worker`, scheduler `/v1/jobs`, SSE |
| Search (code-oriented) | 0.13 retrieval, 0.22 project search |

Gaps today: no first-class **requirement** product artifacts; jobs are free-form prompts (not bound to req/design/phase); no **roadmap graph** UI; no **delivery-domain** retrieval.

---

## 2. End-to-end pipeline

### 2.1 Milestone lifecycle status (locked)

Lifecycle is **per milestone** (`major.minor`). Status is **not** free-picked at create — create always starts at `awaiting_design`. Later stages advance it:

| Status | Meaning |
|--------|---------|
| `awaiting_design` | Locked at create; waiting for Design |
| `awaiting_roadmap` | Design approved; waiting for Roadmap polish / ready |
| `awaiting_development` | Roadmap ready; app may start development |
| `in_development` | App / agent executing |
| `testing` | Test stage after development hand-off |
| `complete` | Tests passed |

**Test fail path:** produce a **buglist** → status returns to `awaiting_roadmap` → Roadmap adds bugfix phases → later `awaiting_development` again.

```
Create ──► awaiting_design ──Design approve──► awaiting_roadmap
                                                      │
                                            Roadmap ready
                                                      ▼
              complete ◄──test pass── testing ◄── in_development ◄── awaiting_development
                                   │
                              test fail
                                   ▼
                          Buglist → awaiting_roadmap (bugfix phases)
```

Full execute / test runner / buglist entity automation lands in **0.39+**. Early UI only locks create status and advances Design → Roadmap → awaiting development.

### 2.2 Stage surfaces

```
┌─────────────┐            ┌─────────────┐            ┌──────────────────────────┐
│ Requirement │ ──────────►│   Design    │ ──────────►│ Roadmap                  │
│  (spec)     │            │ (options +  │            │  · Graph (milestones)    │
│             │            │  + Versions)│            │  · phases from Versions   │
└─────────────┘            └─────────────┘            └────────────┬─────────────┘
                                                                   │ ready
                                                                   ▼
                                                            App develop
                                                                   │
                                                                   ▼
                                                                 Test
                                                          pass / fail (buglist)
```

**Gates** constrain execution (entry/exit, DoD, human check). They are authored on each **Design Version**, then carried into roadmap phases — not a separate Roadmap tab.

Agents do not skip gates.

---

## 3. Roadmap model: two layers

### 3.1 Milestone = large node graph (planning topology)

- Nodes = capability / delivery chunks (may depend on each other).  
- Edges = dependencies (`depends_on` / `blocks`).  
- Primary place for **human + AI polish** of the plan.  
- Owns **acceptance criteria** and **merge-to-main**.  

### 3.2 Phase = sequential landing stages (inside a milestone)

- Ordered chain: `p1 → p2 → p3` (little branching).  
- Unit of **agent execution** and day-to-day commits.  
- Merges into the **milestone branch**, not directly into main.  
- Has implementation checklists / self-checks, **not** formal product acceptance.

```
[Milestone A] ──depends──► [Milestone B]
     │                          │
     │ phases (ordered)         │ phases (ordered)
     ▼                          ▼
  p1 → p2 → p3               p1 → p2
```

**One-liner:** Milestone is strategic topology; Phase is the construction crew order inside each big node.

---

## 4. Delivery UI surfaces (lock before data model)

**Principle:** Settle **screens and interactions** on anvil-web first; schema / GraphPatch / export follow the UI, not the other way around.

Host: **anvil-web** nav entry **Delivery** (alongside Clusters). Flutter app is **not** the authoring surface — it later opens polish/execute against the same IDs.

### 4.0a Shared AI Chat Component

One dock implementation for all polish surfaces — do **not** fork chat UIs per stage:

| Path | Role |
|------|------|
| `source/internal/web/static/js/components/ai_chat.js` | Shell: title, cite nav, `@` chips, drag-drop cite, input, send, optional log |
| Host feature (e.g. `delivery.js`) | Domain pickers (Detail / Version / Option / Node…), scroll targets, `onSend` → field / GraphPatch apply |

**Consumers (same component):** Requirements polish · Design dock · Roadmap graph/node chat · later **Test → Polish** loop.

Cite chips stay above the textarea (not inlined). View mode may drag `data-cite-key` / `data-cite-drag` onto the dock.

### 4.0 Information architecture

**Clusters (ops)** — product hierarchy (0.34+):

```
Clusters
└── Scheduler
      └── Workspace          # Token; Dev Nodes join here
            ├── Workspace View (Graph)  # Projects + Workers topology
            └── Project      # logical eng unit; Delivery binds here
                  └── Worker # checkout (path + branch) on a Dev Node
```

**Delivery** — planning surfaces. Top hop is always **Generation (major)**; then Project / Design / Milestone. Execute still picks a Worker:

```
Delivery
├── Requirements          # Generations → major → Project/milestones → versions
├── Designs               # Generations → major → Design detail (ADR + Versions)
├── Roadmap               # Generations → major → cards (Execute + 提交测试)
└── Test                  # Generations → major → testing queue (Pass / Fail)
```

Primary entry: **Requirements home = Generation plan + Project picker**. Click a major to scope; open a Project for the full milestone edit flow. Designs / Roadmap / Test open on the same Generation plan, then drill into that major. Clusters “Open in Delivery” remains an optional deep link.

Cross-links: Project ↔ Req versions ↔ Design ↔ Milestone/Phase; Worker is the execute checkout. Export on Design / Roadmap toolbars.

In Requirements UI, `patch` rows are **versions** (capability slices). Roadmap still uses **phases** as the execute/landing order under a milestone (same `major.minor.patch` id).

### 4.1 Requirements UI (Project-scoped target; Worker execute)

#### Binding model

| Concept | Meaning |
|---------|---------|
| **Engineering requirement** | Target: top-level on a **Project** (`project_key`); 0.33 still may bind `worker_id` until migrate |
| **Reqs directory pointer** | Sandbox-relative subdirectory under a Worker checkout of that Project (e.g. `docs/requirements`) |
| **Versioned req group** | Inside one eng-req: managed as **`major.minor.patch`** |

#### Version semantics (locked)

| Segment | Product meaning | Delivery mapping |
|---------|-----------------|------------------|
| **major** | Product generation / era | Generational bucket (e.g. `1` = v1 line) |
| **minor** | Milestone within that generation | ↔ roadmap **milestone** |
| **patch** | Phase within that milestone | ↔ roadmap **phase** |

Example: `1.33.1` → major `1` (世代) · minor `33` (milestone 0.33) · patch `1` (phase p_0.33.1).

#### Screens

| Surface | Purpose |
|---------|---------|
| **Generation index** | First screen: `delivery_majors` cards (0.x, 1.x, …); open one → work under that major |
| **Major home** | Projects / milestones that have versions in this major; link to Project picker |
| **Create eng-req** | Pick **Project/Worker** (inventory) → set **reqs subdirectory** → title; milestone attaches to developing major |
| **Version browser** | After open Project: milestones (`major.minor`) with version list (`patch`); create version nodes |
| **Version detail** | Goals, delivery note, **completion criteria** (capabilities to ship) — **fields drive export** |
| **Req chat dock** | Polish current version slice → **field patches** (not blind whole-doc rewrite) |
| **Lifecycle status** | Read-only on milestone; starts `awaiting_design`; advanced by Design / Roadmap / later execute |

Empty state: “Bind Worker & set reqs folder” before any version exists.

### 4.2 Designs UI

| Surface | Purpose |
|---------|---------|
| **Generation index** | Same majors as Requirements; open → designs for that major |
| **Design list** | Under major: filter by status |
| **Design detail** | Bound `req_id` + `major.minor`; **structured ADR** + ordered **Versions** (execution units); links to roadmap |
| **ADR** | Context · Decision · Consequences · Spec · Architecture (mermaid) · Sequence (mermaid) · Sample & capacity — Markdown Edit/Preview |
| **Version cards** | Per Version: title + **Depends on** (sibling DAG) + **Gate** + **Sub-architecture** + **Class design** (Markdown Edit/Preview; mermaid OK on Sub-architecture) |
| **Design chat dock** | Cite ADR sections or Version fields; mock polish appends to Decision / Gate |
| **View** | Compose full Design Markdown (ADR + Versions) for review (Preview / raw) |
| **Approve gate** | After View; ≥1 Version with Gate / Sub-architecture / Class design; sets `approved` |
| **Bootstrap** | **Approved only** → topo-order Versions by deps, then seed eng-req patches + roadmap phases (idempotent) |

Toolbar flow: **Save** (draft) → **View** → **Approve** → **Bootstrap roadmap**.

### 4.3 Roadmap UI

Primary planning canvas — see §5 interactions below (graph chat, per-node chat, export).

| Chrome | Behavior |
|--------|----------|
| **Generation index** | Majors first; open → milestones for that major only |
| **Graph + cards** | Milestone nodes for **this major** (drag); rich cards: Worker, Start/Pause/Stop, Design/Req links, Polish list |
| **Polish** | On complete: modal + AI assist → save into Version + card ▶ run (mock until Jobs) |
| **提交测试** | From `in_development` → lifecycle `testing` (explicit; not auto-on-Stop) |
| **Phase strip** | Phases under the milestone (seeded from Design Versions; shown as phase count on cards) |

### 4.4 Test

| Surface | Purpose |
|---------|---------|
| **Generation index** | Same majors; open → milestones **in `testing` only** for that major |
| **Campaign** | AI-draft cases → Worker mock write/run → buglist → Worker fix → regress → Pass |
| **Test card** | Acceptance excerpt; cases list; Run / Fix / Regress; Pass gated on clean campaign |
| **Buglist** | Rows bound to `eng_req` + `major.minor`; status `open|fixing|verified|closed` |
| **Fail** | Escape hatch → stub + `awaiting_roadmap` (no auto bugfix phases yet) |

Test queue is **not** the `complete` list — `complete` means tests already passed (Pass after clean campaign).
| **Graph chat** | Floating/docked: whole-graph polish → `GraphPatch` preview → Apply / Discard |
| **Node chat** | From inspector: context = node + neighbors + design/req slices → local patch |
| **Export** | Preview markdown tabs (`m_*.md`, `p_*.md`) → write to configured export root |

**Gates** for execute are authored on each **Design Version** (`Gate` field: entry/exit, DoD, human check) — not a Roadmap sub-tab.

Human always confirms Apply on AI patches (no silent overwrite).

### 4.4 What App UI will consume later (not built here)

| App surface | Needs from Delivery UI IDs |
|-------------|----------------------------|
| Execute phase | `phase_id`, export md slice, worker |
| Review / polish chat | `req_id` / `design_id` / `milestone_id` + read model |
| Milestone accept | gate checklist from milestone node |

Defining these deep links now keeps web field names stable when schema lands.

### 4.5 UI → data abstraction order

1. Freeze §4 screens + primary actions (this section).  
2. Derive **entities / statuses / patch ops** from visible controls.  
3. Then GraphPatch JSON + markdown export templates.  
4. Only then persistence paths and APIs.

---

## 5. Visual roadmap editor (detail)

### 5.1 UX

| Interaction | Behavior |
|-------------|----------|
| AI bootstrap | From approved Design, generate **initial milestone graph** + default phase chains |
| Graph-level chat | “Compress to 3 milestones”, “Reorder dependencies” → structured `GraphPatch` |
| **Per-node AI chat** | Context = node + neighbors + bound design slices; patch only that subgraph |
| Human edit | Drag nodes/edges, edit fields, undo; AI proposes, human applies |
| Export | Graph → existing roadmap markdown (`m_*.md`, `p_*.md`) |

AI must emit **structured graph patches** (add/update/remove node/edge), not only free markdown paste.

### 5.2 Suggested node types

| Type | Role |
|------|------|
| `Milestone` | Large graph node; acceptance + merge gate |
| `Phase` | Ordered step under a milestone |
| `Task` | Optional checklist item under a phase |
| `Gate` | Human/CI approval checkpoint |
| `Artifact` | Link to design section, API, CL, etc. |

### 5.3 Persistence vs export

| Artifact | Role |
|----------|------|
| `.anvil/delivery/` structured store | Working state while editing (shape derived from §4 UI) |
| `doc/roadmaps/…` / `doc/design/…` markdown | Exported, reviewable, versionable text |
| Job metadata | `req_id` / `design_id` / `milestone_id` / `phase_id` |

Prefer tool → markdown export fidelity; round-trip markdown → graph is optional later.

### 5.4 UI host

- **anvil-web** owns Delivery authoring (§4).  
- Flutter app: execute / review / polish chat against IDs (later).  
- Execution stays on scheduler + workers.

---

## 6. Git / branch workflow

Aligned with “accept and merge at **milestone**”:

```
main
  └── milestone/<id>          ← integration branch; MERGE TARGET FOR PHASES
        ├── phase work / commits
        └── … when milestone acceptance passes → merge to main
```

| Level | Develop | Merge into | Formal acceptance |
|-------|---------|------------|-------------------|
| Phase | Yes (agent jobs) | Milestone branch | No |
| Milestone | Integration + polish | **main** | **Yes** |

Notes:

- Keep phases short; merge them into the milestone branch often.  
- Avoid long-lived stacks of many milestones unmerged.  
- On huge monorepos (e.g. Perforce), the same policy applies with **CL streams** instead of Git branches: phase = CL group; milestone = accepted integrate to mainline.

---

## 7. Agent execution

1. Milestone branch exists (from main).  
2. Scheduler runs **phase jobs in order** (`phase_id` bound).  
3. Each phase: implement → self-check → commit on milestone branch.  
4. When all phases for a milestone are done → **milestone acceptance** (human + automated checklist).  
5. Pass → merge milestone → main; fail → fix phases or reopen graph polish.

Do **not** auto-merge to main without the milestone Gate.

---

## 8. Delivery retrieval engine

Systematized text (req / design / roadmap md / graph / job traces) needs a **dedicated retrieval domain**, not only generic code search.

### 8.1 Why

| Corpus | Needs |
|--------|--------|
| Design / Req | Section chunks, glossary, constraints |
| Roadmap md | Structured filters (`status`, `milestone`, `phase`) |
| Graph JSON | Neighbor queries (upstream/downstream) |
| Jobs / chats | Lookup by `phase_id` / `milestone_id` |

### 8.2 Capabilities

1. **Schema-aware chunking** (by heading / phase / node boundary).  
2. **Filters:** version, status, type, ids.  
3. **Chain retrieval:** phase → design slice → req → recent failed job.  
4. **Stable IDs first** (`m_0.32`, `p_0.32.1`, node UUID), then semantic.  
5. **Hybrid:** keyword/BM25 (ids, API names) + vectors (intent questions).  
6. **Citations:** path + line / `node_id` for editor and job prompts.

### 8.3 Placement

Extend 0.22-style index with a **`delivery` namespace** (or design chapter “delivery retrieval”):

```
.anvil/search/
  code.*
  delivery.*    # req, design, roadmap, graph
```

Agent tools (illustrative): `delivery_search`, `delivery_get`, `delivery_neighbors`.

Re-index on graph save / markdown export.

---

## 9. Suggested milestone cut (Anvil product)

Illustrative numbering after current cutoff (**0.32** = sandbox code review + VS Code Server, separate track):

| Step | Content |
|------|---------|
| **0.33** | Delivery **UI + formal schema** on anvil-web (Req / Design / Roadmap, SQLite + `/api/v1/delivery`) — design ch.33 |
| **0.34** | Markdown export + `.anvil/delivery/` project mirror derived from UI/store |
| **0.35** | Roadmap graph editor + GraphPatch apply |
| **0.36** | AI bootstrap / per-node chat → patches; delivery retrieval |
| **0.39** | Execute: **0.39.1** Worker bind + Execute → `in_development`; then phase jobs, Gates constraints, **test stage**, buglist → Roadmap loop, milestone accept → merge main |

The graph editor and the per-node chat above shipped as part of **0.39**'s work rather than under numbers of their own, and **0.35–0.38** belong to the editor line (VS Code extension, tool permissions, permission modes, session identity) — see [roadmaps/v1.0/README.md](doc/roadmaps/v1.0/README.md).

Exact numbers follow Anvil’s normal `doc/design` + `doc/roadmaps` process when promoted from this vision doc.

---

## 10. Non-goals (for this line)

- In-process multi-agent “committee” graphs inside the SDK library.  
- Auto-merge to main without milestone acceptance.  
- Replacing Anvil’s own `doc/design` / `doc/roadmaps` authoring for building Anvil (that process stays).  
- Full IDE material-editor parity on day one.  
- Treating every phase as a merge-to-main event.

---

## 11. Design contracts to write next (when implementing)

Promote into versioned docs under `doc/design/v1.0/` (and matching roadmaps), **in this order**:

1. **UI surfaces** — §4 screens, statuses, primary actions (wireframe-level).  
2. **Entities / statuses / patch ops** — derived from UI controls.  
3. **Export mapping** — structured store → req/design/roadmap markdown.  
4. **Graph schema + GraphPatch** — for Roadmap canvas.  
5. **Branch/CL policy** — milestone integration + main merge rules.  
6. **Delivery retrieval API** — index fields, tools, chunk rules.  
7. **Job binding** — scheduler fields for `milestone_id` / `phase_id`.

---

## 12. Summary

| Decision | Choice |
|----------|--------|
| Authoring host | **anvil-web Delivery** |
| Order of work | **UI surfaces → data model → export/AI** |
| Req / Design UI | Structured forms + chat dock → field patches |
| Planning UI | Milestone **node graph** + phase strip |
| Landing unit | **Phase** sequential chain under each milestone |
| Lifecycle | Per milestone; create → `awaiting_design` → … → `testing` → `complete` (or buglist loop) |
| Acceptance | Completion criteria on req; Gates constrain execute; milestone accept before main |
| Merge to main | **After milestone acceptance** |
| Phase merge | Into **milestone branch** only |
| AI role | Bootstrap + polish via **structured patches**, not blind md |
| Text docs | **Export** from tool layer |
| App | Develop when `awaiting_development` / `in_development` |
| Runtime | Reuse scheduler / workers / SSE |
| Gates IA | On **Design Version** (`Gate` field), not a Roadmap tab |}

This document is the rolled-up product vision from planning discussions; implementation should follow Anvil’s normal design-chapter → roadmap-phase → code path once adopted.
