# 65 — Release, production hardening, and Web audit-only cutover

**Status:** planned
**Product version focus:** 0.65

## 1. Goal

Close the requirement-to-release workflow with an explicit release candidate, promotion,
publication, rollback intent, and production qualification. Complete the client migration: VS Code
is the only interactive product and administration surface; `anvil-web` is enforced read-only
audit.

## 2. Release candidate

An immutable candidate manifest binds:

- approved requirement, design, plan, work-item, and policy revisions;
- implementation execution, Worker, base/head commit, and patch;
- verification campaign, review verdict, finding decisions, PR, and CI evidence;
- release target, version, artifacts, hashes, signatures, provenance, and migration set;
- rollback intent, approver, expected revision, and idempotency key.

Candidate states are `draft`, `ready`, `approved`, `publishing`, `released`, `partially_released`,
`failed`, `revoked`, and `rolled_back`. “Release” means the configured target transition, not merely
a green check: merge, tag, package publication, deployment handoff, or an explicit combination.
Each adapter reports partial outcomes durably and supports read-after-write recovery.

## 3. VS Code release surface

The Releases group provides:

- candidate provenance graph from requirement through reviewed commit;
- missing/stale gate diagnostics and release-target preview;
- explicit Approve release, Publish/Promote, Recover, Revoke, and Start rollback;
- adapter-specific status without provider-specific state leaking into the common lifecycle;
- immutable report and audit export.

No release action is implied by opening, refreshing, CI success, review pass, or requirement
acceptance. Waivers bind gate, actor, reason, expiry, and candidate revision.

## 4. Qualification matrix

The complete 0.60–0.64 path is qualified across supported Windows/macOS/Linux, architectures, Git,
VS Code, store/schema, model modes, providers, and runtimes. Required campaigns include:

- failpoints around durable writes and external mutations;
- crash, restart, network/provider loss, disk/lock failure, truncation, and cancellation races;
- multi-hour soak and large repository/diff/session/workflow tests;
- bounded memory, handles, goroutines, child processes, temporary files, store growth, and latency;
- clean install, upgrade from N-2, migration restart, downgrade refusal, and rollback drills;
- reproducible Host/VSIX/service artifacts with hashes, signatures, SBOM/provenance, and gate
  freshness.

A required missing/stale/failed gate blocks release unless an explicit audited policy permits a
time-bounded waiver.

## 5. Diagnostics and telemetry

Local diagnostics remain useful when Host, store, package, provider, or runtime startup fails.
Support bundles are previewable and redact prompts, responses, source, secrets, credentials,
absolute user paths, and protected evidence by default.

Aggregate telemetry is off by default, contains no content, and has explicit retention, inspection,
export, and deletion controls. Automatic crash upload is not permitted.

## 6. Final Web boundary

At 0.65 completion:

- Web has audit search, filters, detail timelines, redacted evidence views, and export only.
- Web has no AI Chat or prompt submission.
- Web has no requirement/design/roadmap/workflow/test/review/release mutation.
- Web has no cluster, Worker, model, secret, permission, or organization configuration.
- Web has no approval, start, stop, retry, browser, Git, PR, CI, merge, or rollback action.

Administrative controls move to authenticated VS Code panels or CLI over the same service APIs.
The server still owns scheduling, authorization, policy, and audit. Web mutation routes are removed
or return a stable read-only error; hiding buttons is insufficient. Historical deep links remain
readable and offer **Open in VS Code**.

## 7. Acceptance

- One explicit VS Code flow reaches a durable configured release state from an approved 0.60
  requirement with complete traceability.
- Partial publication, restart, duplicate request, and rollback drills preserve one authoritative
  outcome.
- Required qualification, migration, provenance, package, and smoke gates pass.
- Diagnostics and telemetry leak none of the forbidden fixtures.
- Automated route/UI tests prove Web is read-only audit and contains no Chat/config/workflow
  mutation.
- 0.66 can reuse the same plan, sandbox, evidence, review, release, and reconnect contracts for
  remote Workers.

## 8. Non-goals

- Automatic release on green CI.
- Arbitrary downgrade or silent incompatible-state rewrite.
- Remote Worker execution, enrollment, pools, or placement (0.66).

## 9. Dependencies

- [60 — Product requirements workbench](60-product-requirements-workbench.vscode.md).
- [61 — Planning and policy](61-planning-and-policy.vscode.md).
- [62 — Isolated implementation](62-isolated-implementation.vscode.md).
- [63 — Verification and evidence](63-verification-and-evidence.vscode.md).
- [64 — Review and delivery](64-review-and-delivery.vscode.md).

## 10. Roadmap

- [Milestone 0.65](../../roadmaps/v0.x/m_0.65_release_and_hardening.vscode.md).
