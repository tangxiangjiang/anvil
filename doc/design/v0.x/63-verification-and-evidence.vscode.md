# 63 — Verification, browser/computer use, and evidence

**Status:** planned
**Product version focus:** 0.63

## 1. Goal

Verify a 0.62 implementation against the approved acceptance and policy revision. Verification is
a durable campaign of bounded checks and evidence, not a chat claim. VS Code owns campaigns and
decisions; Web displays their audit trail only.

## 2. Verification campaign

A campaign binds exact requirement, plan, work item, execution, Worker, base/head commit, patch,
environment, model/profile, policy, and acceptance revisions. It contains ordered checks:

- configured build, test, lint, type, LSP, and project-specific commands;
- deterministic file/diff and checkpoint evidence;
- browser observations and interactions when policy requires them;
- platform/device/manual evidence requests;
- result, duration, bounded output, provenance, and evidence digest.

Results are `passed`, `failed`, `blocked`, `inconclusive`, or `cancelled`. Missing, stale,
truncated, unsupported, or unverifiable required evidence cannot become `passed`.

## 3. Browser capability ladder

Browser and desktop capabilities remain distinct:

1. `browser_read` — navigate allowed origins and collect DOM/accessibility/screenshot/console/network
   evidence without interaction.
2. `browser_interact` — click/type/select against a fresh observed target.
3. `browser_external_write` — submit/upload or otherwise mutate an external system after a fresh,
   action-specific approval.
4. `desktop_interact` — separately enabled input within one explicitly selected window/process.

No model-generated JavaScript runs. Selectors are hints resolved to a current target fingerprint.
Stale, hidden, ambiguous, cross-origin, or policy-blocked targets fail without action. Page content
is untrusted evidence and cannot change permissions, origins, tools, secrets, or approval policy.

## 4. Browser and desktop isolation

The supervised browser uses a generated profile, origin/network allow-list, bounded tabs, storage,
downloads, resources, time, and deterministic shutdown. Downloads remain quarantined. Uploads bind
an exact approved file and origin.

Profiles are ephemeral by default; any retention duration, storage class, and deletion action are
explicit policy. The ordered browser trace is exportable only after secret redaction and preserves
action/evidence provenance.

Cookies, authorization headers, passwords, OTPs, payment fields, and protected inputs never become
model-visible. When policy permits credential entry, an opaque secret handle is approved for the
exact destination origin and field; the model and trace observe only the operation outcome, never
the value. User takeover supports login or ambiguity without recording secrets.

Desktop interaction is confined to the selected current window/process, stops on focus loss, has an
emergency stop, masks protected regions, and blocks terminals, password managers, security
settings, and elevation. Unsupported platforms report unavailable capability with no fallback.

## 5. Repair boundary

A failed campaign can propose a structured repair work item citing failed check and evidence ids.
Creation requires approval and routes back through 0.61 planning and 0.62 isolated implementation.
It never edits the original Worker, silently reruns checks, or loops until green. Attempt, time,
token, and changed-scope budgets are enforced.

## 6. VS Code verification surface

The Product/Workflow views provide:

- campaign plan and requirement-to-check coverage;
- live and restored check rows with logs, screenshots, artifacts, and provenance;
- browser trace timeline and explicit takeover/approval cards;
- Run selected, Run failed, Stop, Attach manual evidence, and Create repair item;
- stale/missing/inconclusive markers that cannot be hidden by summary prose;
- a verification manifest handed to 0.64 review.

Opening, refreshing, filtering, or reading evidence starts no check or browser.

## 7. Web cutover

Web test/fix/regression controls, browser workbench, and acceptance mutations are removed after VS
Code parity. Web keeps read-only campaign, trace, evidence metadata, and exported audit records.
Sensitive screenshots, logs, and artifacts remain authorization-checked and redacted.

## 8. Acceptance

- Every required acceptance criterion maps to current attributable evidence or an explicit block.
- Read-only browser sessions cannot interact; external writes cannot inherit broad permission.
- Secrets and protected regions are absent from traces, transcripts, and exports.
- Profile retention is explicit and exported traces are ordered, attributable, and secret-redacted.
- Failed verification creates no repair execution without approval and bounded policy.
- Live/reconnected campaign views agree and passive viewing performs no mutation.
- Web cannot run tests, regressions, browser actions, repairs, or acceptance decisions.

## 9. Non-goals

- Autonomous code review, Git publication, PR, or merge (0.64).
- General remote desktop, CAPTCHA bypass, elevation, or unattended high-impact workflows.
- Remote Worker/browser fleet scheduling (0.66).

## 10. Dependencies

- [62 — Isolated implementation](62-isolated-implementation.vscode.md).
- Delivered test/build, vision, Web search, durable run, and permission capabilities.

## 11. Roadmap

- [Milestone 0.63](../../roadmaps/v0.x/m_0.63_verification_and_evidence.vscode.md).
