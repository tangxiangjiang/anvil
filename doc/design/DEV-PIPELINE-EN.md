# Software Development Pipeline — System Design

> An AI-executed software delivery pipeline, with humans as reviewers and fallback.
>
> Chinese: [DEV-PIPELINE-CN.md](./DEV-PIPELINE-CN.md)

---

## Contents

- [1. Positioning](#1-positioning)
- [2. Core concepts](#2-core-concepts)
- [3. Hierarchy](#3-hierarchy)
- [4. Authority chain](#4-authority-chain)
- [5. Task flow shape](#5-task-flow-shape)
- [6. Human role](#6-human-role)
- [7. AI boundaries](#7-ai-boundaries)
- [8. State machine](#8-state-machine)
- [9. Testing](#9-testing)
- [10. Graph design](#10-graph-design)
- [11. Iteration](#11-iteration)
- [12. Architecture analogy](#12-architecture-analogy)
- [13. Complexity](#13-complexity)
- [14. Bottlenecks and metrics](#14-bottlenecks-and-metrics)
- [15. One-line summary](#15-one-line-summary)

---

## 1. Positioning

This system is a **software development pipeline** where **AI does the executable work** and **humans review, decide, and catch what AI cannot**.

Ops domain (Clusters): **Scheduler → Workspace → Project → Workers**. A Workspace Token admits Dev Nodes; **Project** is the delivery unit (req → design → roadmap → test); **Worker** is a branch checkout (many per Project in parallel).

Core purpose:

> **Draw hard boundaries for AI** — what it may do, what it must not do, under which specs it works, when a human must take over, and what “done” means — all encoded in the Graph.

The Graph is not a casual diagram for humans. It is simultaneously:

- a **task flow** map
- an **ownership** map
- an **AI boundary** map
- a **spec** map

---

## 2. Core concepts

| Concept | Meaning |
|---|---|
| **ADR** | Architecture Decision Record — here: technical decision + execution spec + sample docs, scoped to a Milestone |
| **Graph** | Auto-generated, read-only Roadmap Graph used for task-state tracking |
| **AI assist** | AI participates at every stage as a collaborator, never as the final decision-maker |
| **status** | The only writable control on the Graph |

---

## 3. Hierarchy

```
Major version (top structure / master plan)
  ├── Real overall software architecture for this version
  ├── Version-level narrative
  └── Milestone × N
        ├── Summary: what the req needs, and what “done” looks like
        ├── ADR × N (decision + spec + sample + capacity / redundancy)
        └── Version × N (smallest execution unit; AI codes, human reviews)
                └── Graph: the development pipeline (the “blueprint”)
```

### Responsibilities by layer

| Layer | Content | Owner | Management level | AI role |
|---|---|---|---|---|
| **Major** | Master plan, overall architecture | Dept manager | Command | Propose architecture |
| **Milestone** | Req summary, definition of done | Director | Campaign | Help split work |
| **ADR** | Decision, spec, sample, capacity / redundancy | Team lead | Tactics | Draft decisions / samples |
| **Version** | Smallest execution unit | Engineer | Combat | Assist coding |

---

## 4. Authority chain

```
Dept manager: owns the Major (master plan + overall architecture)
  → Director: splits Milestones
      → Team lead: splits ADRs
          → Engineer: reviews AI-produced Versions + fallback
```

Core rule:

> **Document hierarchy = management hierarchy = decision rights = ownership.**  
> Align all four. Decompose top-down. Each layer only manages the layer below.

---

## 5. Task flow shape

The whole design process is a **task-flow form**:

```
Requirement
  → Dept manager: Major (master plan + architecture)
      → Director: Milestones
          → Team lead: ADRs
              → Engineer: review Versions + fallback
                  → Execute → ship code
```

Each layer treats the **output of the layer above as its task**, then hands work downward.

Flow properties:

1. **Directed, stepwise, no skipping levels**
2. Every layer is **receive → transform → deliver**
3. **State is trackable**
4. **Escalation / rewind**: if execution finds an upper-layer error, bubble up level by level — never skip

---

## 6. Human role

Humans keep three permanent jobs:

| Human work | Nature | AI involvement |
|---|---|---|
| **Review data** | Judgment | AI drafts; human approves |
| **Hard code** | Fallback | What AI cannot do, humans take |
| **Integration test** | Start + supervise + final call | AI writes scripts; humans run and decide |

Core:

> **AI assists, humans decide, outputs are traceable.**

The engineer’s role shifts from “person who writes all code” to **review + fallback for code AI cannot handle**.

---

## 7. AI boundaries

### Capability — what AI may do

- Generate: Major proposals, Milestone splits, ADRs, Version code, test cases, bugfix suggestions
- Execute: structure content, fill specs, judge unit/smoke results, classify bugs
- Write auto-test flow scripts

### Authority — what AI must not do

- Decide (all four layers of approval stay human)
- Close a node (close requires integration test pass)
- Edit Graph structure (read-only)
- Advance work past review gates
- Start integration tests on its own

### Fallback — what must stay human

- Code AI cannot finish
- Tests AI cannot judge
- AI candidates → human decision
- Integration test: start, supervise, judge, close

### Spec — how AI must work

- Per-node **input / output / correctness criteria**
- Structured deliverable formats at every layer
- Explicit acceptance conditions per step

---

## 8. State machine

### Node states

```
Not started → Opened → In progress → Pending review → Review
                                                       ↓
                                         Dev complete (not yet closed)
                                                       ↓
                                    ┌──── test + fix loop ────┐
                                    │  buglist → feedback     │
                                    │      ↓                  │
                                    │    fix → unit/smoke     │
                                    │      ↓                  │
                                    │   pass? no → loop again │
                                    └──── yes ────────────────┘
                                                       ↓
                                         Integration test ──fail──→ loop
                                                       ↓ pass
                                                 Node closed
```

### Critical constraint

> **A node may close only when integration tests pass.**

- Code finished ≠ closed
- Unit / smoke green ≠ closed
- Empty buglist ≠ closed
- Only integration pass → close

### `status` is the only write entry

| Writable | Not writable on the Graph |
|---|---|
| `status` (node state) | The node itself |
| Open / review / finish workflow steps | Node relationships |
| | Node owners |
| | Graph structure |

Legal transitions are defined by the process. Humans only choose among **allowed** options. **Close authority belongs to humans**; AI has none.

---

## 9. Testing

### Overview

```
unit / smoke       ← AI owns (generate, run, judge)     high volume
    ↓
Integration test   ← AI writes auto-test scripts
                       Human starts + supervises
                       Human judges + closes             low volume, high stakes
```

### Unit / smoke

**Highest volume** (many cases, much data), but specs are clear and decidable — AI owns the loop:

- Generate cases
- Run tests
- Judge pass / fail

**Three required elements** for every test:

| Element | Meaning |
|---|---|
| **Input** | Preconditions / state / dependencies |
| **Output** | Expected / observable results |
| **Correctness** | Pass / fail criteria |

### Integration test

**Lowest volume, highest stakes** — semi-automated:

```
AI writes auto-test flow scripts
    ↓
Human starts auto-test
    ↓
Human supervises the run
    ↓
Human judges result / decides close
```

Non-negotiables:

1. AI writes scripts; **humans review scripts** (coverage and assertion strength)
2. **Start authority is human** (start = enter final review)
3. **Supervise is active** (watch flow, anomalies, result trustworthiness)
4. **Judge and close are human** (AI may emit raw output; it may not conclude)

### Buglist → feedback

- Bugs are not a free-floating list — **attach each to the node that produced it**
- Engineers see on the Graph which nodes have feedback
- Feedback is the **return channel** from buglist to the Graph
- Each bug is a **new Graph node** flowing: bug → assign → fix → verify → close

---

## 10. Graph design

### Role

| Attribute | Meaning |
|---|---|
| **Auto-generated** | No manual graph authoring; derived from structured upstream artifacts |
| **Read-only** | Graph is a product / view, not the source of truth; truth lives in upstream layers |
| **Task-state tracking** | Reflects current state of every task node |

### How it is produced

```
Requirement → Major plan → Design → Milestone → Version
                                                  ↓
                                      auto-generate Roadmap Graph
```

Upstream deliverables must be **structured** so the system can extract relationships:

- **Ownership**: Version ∈ ADR ∈ Milestone ∈ Major
- **Flow**: who hands work to whom
- **Dependencies**: Version↔Version, ADR↔ADR ordering
- **State**: current flow state per node

**Delivery Design UI** authors Version boundaries for AI execute: under each structured ADR, ordered **Design Versions** carry **Gate** + **Sub-architecture** + **Class design**. Approve / Bootstrap roadmap seeds phases 1:1 from those Versions (same IDs later for execute).

### Why editing is disallowed

- Graph vs data diverge — which is truth?
- Editing the Graph bypasses the authority chain
- Dual-write of state drifts out of sync

> **Change the process → change the data → the Graph follows.**  
> The Graph always matches truth and cannot be sidestepped.

### The only Graph action

**`status`**: open / review / finish workflow nodes.

---

## 11. Iteration

Software differs from buildings in one critical way: **iteration**. Each Major evolves from the previous one.

Consequences:

1. **Major is a snapshot** of what the system looks like at that point
2. **ADRs have lifecycles** — prior ADRs may be superseded or retired
3. **Graph is incremental** — add nodes/edges, change edges on the previous base
4. **Architecture drifts** — the Major narrative must reflect the **current real architecture**

### Cross-version ADR hygiene

- Tag ADRs with **creation version** and **status**
- When superseded, **point to the replacement ADR**
- Major narrative lists **ADRs in force for this version**

### Two axes

- **Horizontal (structure):** Major → Milestone → ADR / Version
- **Vertical (time):** v1 → v2 → v3 … each evolves the previous

Graph expresses the horizontal structure; iteration expresses the vertical evolution.

---

## 12. Architecture analogy

| Building | This system |
|---|---|
| Client brief: convention center, functions, hall layout | Requirement |
| Design institute: structure, modules, materials, capacity, redundancy | Milestone → ADR → Version Graph |
| Construction drawings | Graph |
| Construction | Coding (AI executes, human reviews) |
| Cost estimate | **Dropped** (software cost is dominated by human decisions and rework; not quantity-takeoff from drawings) |
| Capacity ceiling, redundancy | **Kept** (perf limits, capacity, concurrency, fault tolerance, degrade, backup) |

> **Drop the “money” dimension, not the “constraint” dimension.**

---

## 13. Complexity

This system is complex — but **real complexity**, not accidental complexity. Each hard piece solves a real problem:

| Complexity | Real problem it solves |
|---|---|
| Layers | Clear authority; no skipping |
| Role mapping | Explicit decider per layer |
| Single `status` write entry | No dual-write / Graph drift |
| Auto-generated Graph | No manual drawing; always matches truth |
| Test + fix loop | Real development is iterative repair |
| Bug feedback onto Graph | Issues stay attached to source |
| Close = integration pass | Blocks “code done = done” |
| AI assist at every stage | Load shifts from humans alone to human + AI |

Watch outs:

1. Do not add more layers (four is enough)
2. Do not inflate states (keep only what flow needs)
3. Keep rewind paths convergent
4. Do not build the whole tool at once (stage the landing)

---

## 14. Bottlenecks and metrics

Coding is no longer the bottleneck. Pressure moves to:

| Old bottleneck | New bottleneck |
|---|---|
| Slow coding | **Slow review** |
| Not enough people | **Not enough review throughput** |
| Hard to implement | **Hard for AI — needs human fallback** |

Key metrics:

- **Review throughput** (items reviewed per hour)
- **AI first-pass rate** (share of AI output accepted without rework)
- **Fallback rate** (share of code humans must rewrite)
- **Rewind rate** (share of reviews that send work back)

---

## 15. One-line summary

> This system is a pipeline where **AI generates everything generatable**, and humans **review data**, **fallback on hard code**, and **finally judge via integration tests** (semi-automated: AI writes scripts; humans start, supervise, and close). The Graph is auto-generated and read-only — a task-state tracker and the boundary wall for AI. Humans keep three jobs: **review data, hard-code fallback, integration test**. A node closes **only** when integration tests pass.

**Core principle: AI assists, humans decide, outputs are traceable.**
