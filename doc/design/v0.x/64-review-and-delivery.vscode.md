# 64 — Autonomous review and reviewed delivery

**Status:** planned
**Product version focus:** 0.64

## 1. Goal

Review the exact 0.62 implementation with 0.63 evidence, resolve findings under bounded policy, and
deliver the reviewed commit through remote Git, pull request, and CI. Every mutation remains an
explicit VS Code action. Web is a read-only audit projection.

## 2. Review artifact

An append-only review revision binds:

- requirement, plan, work item, policy, Worker, execution, and attempt;
- base/head commits and patch digest;
- acceptance and verification manifests;
- model/profile, reviewer identity, tool policy, and evidence inventory.

Verdicts are `pass`, `changes_requested`, `blocked`, or `inconclusive`. Findings carry stable ids,
severity, location, rationale, evidence citations, and confidence. Changed commits, plans,
acceptance, policies, or required evidence invalidate the revision. Missing/truncated evidence
cannot be converted into pass.

## 3. Reviewer and repair

The reviewer Agent has immutable read-only permissions: no file writes, lifecycle mutation,
credentials, publication, or approval authority. Model/repository/provider content cannot inject
identity, severity gates, policy, or authorization.

Finding decisions are audited. Selected findings may create a bounded repair item that returns
through 0.62 and receives a fresh independent verification and review. Conflict handling produces a
disposable proposal Worktree. Re-planning produces a structured dependency/acceptance impact diff
for explicit approval; it never replaces the approved plan automatically.

## 4. Publication boundary

Provider-neutral records describe repository identity, approved delivery target, publication, PR,
and checks. GitHub is the first adapter, not the data model.

Only the exact reviewed commit may be pushed to an approved host/repository/ref. Credentials are
host-owned, short-lived, and allow-listed. Tokens are absent from prompts, stores, Worktrees, Git
config, command output, persistent credential helpers, and inherited process environments. Force
push is forbidden. Drift,
non-fast-forward, protection, SSO, scope, timeout, rate limit, and unknown outcome fail closed or
enter an explicit recoverable state.

Publication and provider writes bind expected revision and idempotency key. Read-after-write
recovery prevents duplicate branches, PRs, comments, or status transitions.

## 5. Pull request and CI

VS Code shows a mutation preview before create/update/comment/close. Cancel writes nothing; confirm
writes exactly once. Refresh and reconnect are read-only.

CI state is durable through webhook/subscription or bounded polling. Logs and artifacts are bounded,
redacted, and provenance-labelled. A failed check can propose a repair item only after approval;
the repaired commit must pass fresh verification and review before publication. Attempt, time,
token, and scope budgets prevent “fix until green.” Automatic merge is never inferred from green CI.
An explicit revision-bound **Retry checks** action is previewed and idempotent; reading a failed
check never invokes it.

The delivered 0.57 integration transaction remains available for the exact reviewed Worktree/base
revision when policy requires local integration. It is explicit and never implied by review pass,
publication, PR creation, or green CI.

## 6. VS Code review/delivery surface

The Workflow view adds:

- evidence-completeness, reviewer verdict, findings, decisions, and stale markers;
- Compare, Accept/Dismiss finding, Create repair, Propose re-plan, and Review conflict proposal;
- repository/ref/commit publication preview and explicit Publish;
- PR identity, head/base, review state, checks, bounded logs/artifacts, and provider diagnostics;
- links back to requirement, plan, implementation, verification, and forward to release candidate.

## 7. Web cutover

Web review, approval, Git/PR, CI-rerun, repair, and merge controls are removed. Web may render the
review artifact, provider events, check summaries, decisions, and audit export. It never receives
provider credentials or performs remote writes.

## 8. Acceptance

- A review verdict is reproducible from immutable labelled evidence.
- Reviewer and repair boundaries prevent writes or recursive unbounded fixing.
- Only the exact approved reviewed commit reaches the approved remote ref.
- PR writes are previewed, revision-bound, idempotent, and recoverable.
- CI restores after restart; viewing a failure starts no Agent or rerun.
- Web cannot decide findings, publish, mutate PRs, rerun CI, or merge.

## 9. Non-goals

- Automatic integration, merge, release, or deployment (0.65).
- Multi-provider feature parity in the first adapter.
- Trust-based review bypass for local or remote Workers.

## 10. Dependencies

- [63 — Verification and evidence](63-verification-and-evidence.vscode.md).
- Delivered 0.57 explicit Worktree integration contract.

## 11. Roadmap

- [Milestone 0.64](../../roadmaps/v0.x/m_0.64_review_and_delivery.vscode.md).
