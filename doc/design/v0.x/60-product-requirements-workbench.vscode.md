# 60 — Product requirements workbench and client boundary

**Status:** planned
**Product version focus:** 0.60

## 1. Decision

VS Code is Anvil's only interactive product-delivery client. The supported path begins with a
product requirement and continues through design, planning, implementation, verification, review,
delivery, and release in milestones 0.60–0.65.

`anvil-web` becomes a read-only audit application. It may search, filter, display, and export
durable records, but it does not create, edit, approve, execute, cancel, retry, configure, or chat.
The service/control plane remains authoritative; moving the workflow to VS Code does not move
durable state into extension memory.

## 2. Ownership model

- **VS Code** owns every human workflow action and the mature AI Chat experience.
- **Service/control plane** owns identities, revisions, lifecycle transitions, approvals, event
  journals, idempotency, authorization, and audit.
- **Git** owns source and versioned document bodies where a project chooses repository-backed
  requirements, designs, and roadmaps; the service binds their commit and digest.
- **Web** owns read-only audit projection and export only.
- **CLI** may provide automation and emergency administration against the same service contracts,
  but it does not create a second workflow model.

Neither `workspaceState` nor a Web browser is a source of truth. Reconnect begins from a snapshot
plus a durable event cursor.

## 3. Requirement artifact

`RequirementArtifact` is the first delivery object. It contains:

- stable requirement, workspace, project, and author identities;
- title, problem, users/stakeholders, constraints, assumptions, non-goals, and release intent;
- measurable acceptance criteria and required evidence classes;
- source provenance and optional repository document path/commit/digest;
- monotonic revision, lifecycle state, supersession link, and audit metadata.

Lifecycle is `draft → in_review → approved → superseded`. Rejection creates a durable decision and
returns the same artifact to `draft`; approval never starts planning or execution. Every mutation
uses expected revision and idempotency key. Stale writes fail with the current revision and a
reviewable diff.

## 4. VS Code product surface

The Anvil navigation gains a top-level **Product** function group:

- Requirements, Designs, Roadmaps, Work, Verification, Reviews, Releases;
- project/workspace selector backed by the shared Workspace domain;
- status, owner, revision, dependency, and approval markers;
- native or custom read-only/edit documents with revision-aware Save and Compare;
- commands to create, import, revise, submit, approve, reject, supersede, and open audit history.

AI Chat operates beside the selected artifact. Explicit artifact references carry identity,
revision, bounded content, and provenance. The model may propose a patch; only a visible diff and
explicit user action mutate an artifact. Conversation JSONL is not the requirement store.

## 5. Session continuity

An audit record, notification, or legacy Web URL can open the same artifact or Session in VS Code
through a signed, expiring handoff. The handoff contains identifiers, never credentials or artifact
bodies. VS Code resolves current authorization and revision before display.

Web transcripts are read-only projections. Web has no prompt box, tool approval, retry, or Agent
configuration. **Open in VS Code** resumes the durable Session and event cursor; opening it twice
activates the same tab.

## 6. Migration contract

Migration is capability-by-capability, never dual-write:

1. deliver the authoritative API and VS Code surface;
2. prove parity, reconnect, authorization, and audit;
3. disable the corresponding Web mutation controls and reject their mutation routes;
4. retain read-only historical rendering and export.

0.60 migrates requirement intake, requirement review, AI Chat entry, and product navigation.
Milestones 0.61–0.65 repeat the cutover for their workflow stages. Final enforcement is a 0.65
release gate.

Existing Web-created records remain readable and acquire stable revision/digest bindings during
migration. Unknown or malformed records are quarantined for audit instead of guessed into a valid
state.

## 7. Security and audit

- Authorization is checked by the service for every read and transition.
- Approval records bind actor, role, artifact revision, decision, time, and request id.
- Model output, repository text, Web content, and imported documents are untrusted content.
- Secrets, cluster credentials, audit attribution internals, and unrelated project data never
  become prompt context.
- Web audit APIs use bounded pagination and redacted exports; audit rows are append-only.

## 8. Acceptance

- A requirement can be created, revised, reviewed, and approved entirely in VS Code.
- Reload/reconnect restores the same artifact revision, editor state, and audit history.
- Artifact Chat references are revision-bound and proposed edits require explicit diff approval.
- Web can view/export the requirement history but has no requirement mutation or AI Chat path.
- An approved requirement starts no plan, Worker, Worktree, test, PR, or release.

## 9. Non-goals

- Planning or execution (0.61–0.62).
- Making VS Code local storage authoritative.
- Deleting historical Web records or audit views.
- Hosted VS Code transport or remote Worker execution (0.66).

## 10. Dependencies

- [34 — Workspace domain](34-workspace-domain.md).
- [45 — Web/editor polish](45-web-editor-polish.md), as migration input rather than future UI
  ownership.
- [55 — Structured plans and workflow control](55-structured-plans-and-workflow-control.vscode.md).

## 11. Roadmap

- [Milestone 0.60](../../roadmaps/v0.x/m_0.60_product_requirements_workbench.vscode.md).
