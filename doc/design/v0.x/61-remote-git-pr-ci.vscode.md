# 61 — Remote Git, pull requests, and CI

**Milestone focus:** 0.61. **Design status:** proposed. **Risk:** high.

## 1. Problem

Anvil can inspect and mutate local Git state, but delivery stops at the local repository boundary.
A completed Worktree cannot yet publish a reviewed branch, open or update a pull request, observe
required checks, or route a failed check back into an explicitly approved repair attempt.

Remote delivery introduces credentials, repository policy, third-party state, rate limits, and
irreversible writes. It must be a host-owned integration, not a generic shell or model-supplied URL.

## 2. Goals

- Add a provider-neutral remote repository and pull-request contract.
- Ship GitHub as the first adapter without baking GitHub response shapes into Agent state.
- Push only a reviewed, exact commit to an approved remote/ref.
- Create, inspect, update, and comment on pull requests through explicit actions.
- Observe CI checks durably and offer bounded, reviewable fix attempts.
- Preserve local Worktree, review, and permission guarantees.

## 3. Provider and identity model

```text
RemoteRepository {
  provider, host, owner, name, remote_id, default_branch
}

DeliveryTarget {
  repository_id, base_ref, head_ref, reviewed_commit, review_digest
}

PullRequestRecord {
  provider_id, url, base_ref, head_ref, head_commit, state, revision
}

CheckRunRecord {
  provider_check_id, name, status, conclusion, url, observed_at
}
```

Repository ids come from trusted workspace configuration and provider discovery. The model may
propose a title or body, but cannot choose credentials, arbitrary hosts, remotes, or refspecs.

## 4. Authentication and secret boundary

- OAuth/device authorization and tokens are host-owned and stored in SecretStorage or the
  existing credential service.
- Tokens never enter prompts, JSONL transcripts, shell environments, Git config, or Worker
  Worktrees.
- Provider requests use an allow-listed HTTPS host and repository identity.
- Git push uses a short-lived credential channel owned by the host; no persistent credential
  helper is written.
- Forks, organization SSO, protected branches, and insufficient scopes produce explicit setup or
  policy states, never fallback behavior.

## 5. Publication flow

1. Require an integrated or explicitly publishable reviewed commit.
2. Rebuild and match the immutable review/delivery binding.
3. Resolve the configured remote repository and base branch.
4. Fetch remote metadata without changing the checked-out branch.
5. Refuse non-fast-forward head updates unless the user explicitly creates a new branch.
6. Push the exact commit to a generated or confirmed head ref.
7. Record the remote ref and commit before PR mutation.
8. Show a final modal before creating or updating a pull request.

No force push is available in 0.61.

## 6. Pull-request contract

The initial actions are:

- **Open pull request** from a reviewed delivery target;
- **Update pull request** only when its recorded head ref and expected provider revision still
  match;
- **Open in browser**;
- **Post reviewed summary/comment** after showing the exact content;
- **Close** only through a separate destructive confirmation.

Title/body generation is a proposal. The user can edit it before the host sends one provider
request. Repeated calls use idempotency keys and provider revision checks.

## 7. CI observation and repair loop

CI is event-driven when webhooks/subscriptions exist and polling with bounded backoff otherwise.
Check state is appended to the durable delivery record.

For a failed check:

1. fetch bounded logs/artifacts through provider APIs;
2. redact secrets and persist provenance;
3. attach the evidence to a new structured work item;
4. require approval before starting a fix Worker;
5. publish a new reviewed commit only through the normal delivery flow.

There is no infinite “fix until green” loop. Attempt count, elapsed time, token budget, and changed
scope are bounded. Each attempt remains separately reviewable.

## 8. VS Code surface

Add a Pull Request/CI group under Workflow:

- repository and branch identity;
- PR title, number, URL, head commit, review state;
- required and optional checks with live status;
- bounded logs in read-only documents;
- explicit Publish, Open PR, Update PR, Retry checks, and Create fix attempt actions;
- actionable authentication, policy, rate-limit, and stale-state errors.

The panel is read-first. Viewing remote state never pushes, comments, reruns, or creates a PR.

## 9. Failure and recovery

- A successful push followed by PR failure is recorded as `published_unlinked`; retry may create
  the PR without pushing again.
- Unknown provider outcomes trigger a read-after-write lookup by idempotency key.
- Rate limits persist `retry_after` and never busy-loop.
- Remote head drift blocks updates and preserves the local reviewed commit.
- Provider outages do not delete local Worktrees or delivery evidence.

## 10. Acceptance

- Credentials and provider headers are absent from prompts, logs, Git config, and stored events.
- Only the exact reviewed commit can be pushed.
- PR create/update/comment operations require explicit confirmation and are idempotent.
- Remote drift, protected branches, missing scopes, and non-fast-forward updates fail closed.
- CI state survives restart; failed-check evidence creates no fix attempt without approval.
- Fix attempts are bounded and use the 0.60 isolated execution path.

## 11. Non-goals

- Force push, automatic merge, bypassing branch protection, or approving a PR on behalf of a user.
- Supporting every Git host in the first adapter.
- Treating CI success as permission to integrate or publish unrelated changes.
- Remote Worker execution; that is designed in chapter 66.
