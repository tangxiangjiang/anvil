# 64 — Autonomous review and controlled re-planning

**Milestone focus:** 0.64. **Design status:** proposed. **Risk:** high.

## 1. Problem

Anvil can produce changes, run verification, and present Worktree review, but quality assessment
still depends on the implementing Worker and the user. A reviewer Agent can find defects and
missing acceptance evidence, yet it must not become an unbounded second implementer or silently
approve, merge, resolve conflicts, or rewrite the plan.

## 2. Goals

- Create immutable review artifacts bound to exact code, plan, tests, and runtime evidence.
- Run a role-separated reviewer with read-only repository access by default.
- Turn findings into traceable decisions and optional approved repair work.
- Propose conflict resolutions and re-plans without applying them automatically.
- Bound reviewer recursion, attempts, scope, tokens, and elapsed time.

## 3. Review artifact

```text
ReviewArtifact {
  review_id, artifact_id, work_item_id, worker_id,
  base_commit, head_commit, patch_digest,
  acceptance_digest, verification_digest,
  reviewer_profile, findings, verdict, revision
}

Finding {
  finding_id, severity, category, path?, line?,
  claim, evidence, confidence, suggested_action
}
```

The artifact is append-only. A refreshed review creates a new revision bound to the new patch.
Findings are not free-form transcript conclusions; each must cite bounded evidence or explicitly
state that evidence is unavailable.

## 4. Reviewer boundary

- The reviewer has read-only tools, no lifecycle mutation, no integration, no push, and no secret
  access.
- It cannot review its own hidden reasoning or mark its own finding resolved.
- Model/profile identity and capability are recorded.
- Prompt instructions from changed files are untrusted content.
- Truncated context, missing files, failed verification, or unsupported languages lower confidence
  and prevent a passing verdict when evidence is required.

## 5. Verdict and policy

Verdicts are `pass`, `changes_requested`, `blocked`, or `inconclusive`.

Repository policy may define:

- required review categories;
- severity gates;
- required verification;
- maximum changed scope;
- whether a human approval is always required.

A reviewer verdict is evidence, not authorization. Integration and publication retain their
existing confirmations.

## 6. Repair loop

For accepted findings:

1. the user selects findings or a policy selects only pre-approved low-risk categories;
2. the host creates a new structured repair item referencing finding ids;
3. 0.60 starts a new isolated attempt;
4. the new patch is reviewed against both original acceptance and selected findings;
5. attempt/time/token/change limits stop the loop.

The implementing Worker never mutates the review artifact. A fresh reviewer revision decides
whether evidence changed.

## 7. Conflict proposals

For a merge conflict, the reviewer may produce:

- conflict classification;
- base/worker evidence;
- a proposed resolution patch in a disposable Worktree;
- risks and verification commands.

The proposal cannot update the primary branch or original Worker. Applying it creates a new
reviewable attempt and requires explicit confirmation.

## 8. Controlled re-planning

Re-plan is available when findings invalidate assumptions or dependencies:

- generate a proposed new artifact revision;
- preserve previous items and decisions;
- show dependency/acceptance changes as a structured diff;
- require approval for changed/new items;
- never cancel or rewrite running work implicitly.

The proposal is stale if source artifact, repository, finding set, or relevant session state
changes.

## 9. VS Code surface

The Workflow view adds:

- review status and reviewer identity under each attempt;
- grouped findings by severity/category with source links;
- Accept finding, Dismiss with reason, Create repair attempt, and Propose re-plan;
- comparison between review revisions;
- explicit “evidence incomplete” and truncation states.

Opening or refreshing review is read-only. A finding action never edits files directly.

## 10. Acceptance

- Reviewer tools cannot write, integrate, publish, or access secrets.
- Reviews are rejected as stale after any bound commit/digest/revision changes.
- Every blocking finding has source-labelled evidence or is marked inconclusive.
- Repair attempts and re-plans require durable identities and bounded policy.
- No loop can exceed configured attempts, elapsed time, token budget, or scope.
- Conflict proposals leave primary and original Worker trees unchanged.
- Human confirmation remains required for integration and external publication.

## 11. Non-goals

- Claiming model review replaces human/security review.
- Automatic conflict resolution, merge, push, or plan replacement.
- Recursive reviewer-of-reviewer chains.
- Unbounded “fix until perfect” execution.
