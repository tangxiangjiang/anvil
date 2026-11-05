# 61 — Design, roadmap, planning, and execution policy

**Status:** planned
**Product version focus:** 0.61

## 1. Goal

Turn an approved 0.60 requirement into an approved, dependency-ordered delivery plan without
starting work. VS Code owns design, roadmap, milestone, phase, work-item, test, review, delivery,
and rollback authoring. The service owns their revisions and decisions; Web shows audit only.

## 2. Artifact graph

The planning graph is:

`Requirement → Design revision → Milestone → Phase → Work item`

Every edge names exact identity and revision. A plan cannot silently follow a newer requirement or
design. Superseding an upstream artifact marks dependent approvals stale and produces an impact
diff; it does not rewrite approved descendants.

Existing 0.55 `PlanArtifact` and work-item contracts remain the execution-facing representation.
0.61 adds a durable bridge from Workspace/Project delivery entities to those artifacts:

- project, requirement, design, milestone, phase, and work-item ids;
- repository paths/commits/digests for versioned documents;
- dependency and acceptance mappings;
- expected revisions and approval decisions;
- release target and policy digest.

## 3. Planning content

An executable plan declares:

- ordered work items, dependencies, ownership, acceptance criteria, and completion evidence;
- repository/base revision, Worktree strategy, integration and delivery targets;
- test/build/LSP, browser, review, CI, and release requirements;
- model/profile, token/time/attempt/change-scope budgets;
- tool, permission, sandbox, network, secret, and desktop-interaction policies;
- cancellation, retry, repair, rollback, and unsupported-capability behavior.

Missing capability is a planning error, not permission to choose a weaker fallback. A model may
propose the graph and policies, but cannot approve them or choose credentials, remote nodes, policy
exceptions, or release authority.

## 4. VS Code planning surface

The Product group provides:

- Design and Roadmap document editors with outline, dependency, validation, and audit views;
- milestone/phase/work-item trees with readiness and stale markers;
- structured plan diff for changed dependencies, acceptance, policy, and release target;
- explicit Submit, Approve, Reject, Supersede, and Re-open commands;
- Chat references to exact requirement/design/plan revisions;
- diagnostics that link cycles, missing criteria, unsupported capabilities, or invalid versions to
  the relevant field/document.

Opening, expanding, filtering, comparing, or approving a plan starts no Worker. The Run affordance
does not appear until 0.62 has proved an approved dependency-ready item.

## 5. Version and document rules

Product versions map generation → milestone → phase using the repository's versioned design and
roadmap layout. The service stores lifecycle identity, revisions, approvals, and audit; Git stores
the reviewable document bodies when configured. A transaction binds both so neither a database row
nor an uncommitted file can impersonate an approved plan.

Imports validate schema and links before creating a draft. Exports are deterministic. Manual edits
outside the client are detected by digest/commit drift and require a new revision before approval.

## 6. Web cutover

After parity gates pass, Web design, roadmap, gate, approval, and workflow-authoring controls are
removed. Existing URLs resolve to read-only audit/detail pages with **Open in VS Code**. Mutation
requests from the Web client are rejected even if a stale page still contains an old control.

## 7. Acceptance

- An approved requirement reaches an approved dependency-ready work item entirely in VS Code.
- Cycles, stale revisions, missing acceptance, and unsupported policy fail before approval.
- Impact diffs show every invalidated downstream approval.
- Reconnect restores the same graph, decisions, and selected artifact.
- Planning starts no execution and performs no Git/provider/release mutation.
- Web can inspect/export planning history but cannot author or decide it.

## 8. Non-goals

- Worktree or sandbox execution (0.62).
- Verification/review/PR/release execution (0.63–0.65).
- Letting natural-language Chat bypass structured artifacts.

## 9. Dependencies

- [60 — Product requirements workbench](60-product-requirements-workbench.vscode.md).
- [55 — Structured plans and workflow control](55-structured-plans-and-workflow-control.vscode.md).
- Repository document-versioning rules.

## 10. Roadmap

- [Milestone 0.61](../../roadmaps/v0.x/m_0.61_planning_and_policy.vscode.md).
