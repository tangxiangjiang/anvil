# 66 — Remote Worker execution

**Milestone focus:** 0.66. **Design status:** proposed. **Risk:** critical.

## 1. Problem

Local Worktrees and containers are bounded to one machine. Large builds, platform-specific tests,
team infrastructure, and unattended CI repair need remote execution. Existing scheduler/fleet
components do not yet provide the editor-grade identity, evidence, approval, checkpoint, and
repository boundaries established by the 0.60–0.65 requirement-to-release workflow.

## 2. Goals

- Run an approved structured work item on an authenticated remote Worker.
- Reuse durable run, sandbox, Worktree, review, and delivery contracts.
- Bind source snapshot, environment, model profile, policy, and result to one execution identity.
- Stream progress and evidence to VS Code while tolerating disconnects.
- Return changes through reviewed commits/artifacts, never an untrusted filesystem sync.

## 3. Control-plane identity

```text
RemoteWorker {
  worker_node_id, pool_id, platform, architecture,
  capabilities, attestation?, state, revision
}

RemoteExecution {
  execution_id, artifact_id, work_item_id, attempt,
  source_commit, environment_digest, policy_digest,
  remote_worker_id, run_id, state, revision
}
```

The scheduler chooses only among Workers satisfying a host-authored capability requirement.
Model output cannot select a node, endpoint, image, credential, or trust level.

## 4. Source and result transport

- Source is an immutable commit plus an optional reviewed patch bundle with digest.
- The remote side creates its own isolated checkout and 0.62 sandbox.
- Git credentials are scoped to fetch the approved source and, where policy allows, publish only a
  generated result ref.
- Results are signed manifests referencing commit/tree, patch digest, test evidence, logs, and
  artifacts.
- Local integration rebuilds and reviews the result binding before any primary mutation.

No bidirectional arbitrary directory synchronization is part of the protocol.

## 5. Authentication and transport

- Mutual authentication with rotated host/node identities.
- Encrypted transport with replay protection and request idempotency.
- Node enrollment, revocation, and pool policy are operator actions.
- Secrets use workload-scoped handles and expire with the execution.
- Logs and artifacts are encrypted at rest according to pool policy.

The editor never connects directly to an arbitrary Worker address; it talks to the configured
control plane.

## 6. Scheduling and quotas

Placement considers platform, architecture, sandbox runtime, toolchain, network profile, data
residency, concurrency, and resource limits. Quotas cover queue time, runtime, CPU/memory, disk,
artifact size, model budget, and retries.

Scheduling is durable:

```text
queued -> assigned -> preparing -> running -> uploading -> review_ready
              |             |          |
              v             v          v
           expired       failed     interrupted
```

Reassignment always creates a new attempt. Split-brain attempts cannot both publish as the same
execution.

## 7. Disconnect and cancellation

- VS Code disconnect does not cancel remote work unless policy says so.
- The control plane journals events; reconnect resumes from sequence.
- Cancellation is an acknowledged state transition, not a dropped connection.
- A lost node lease becomes interrupted/unknown and blocks result acceptance until attested facts
  reconcile.
- The system never claims control of a process whose node identity/lease was lost.

## 8. Evidence and review

The result review shows:

- source and result commits/trees;
- node/pool capability and environment digest;
- sandbox/image/policy digest;
- acceptance and verification results;
- truncation, interruption, and artifact provenance;
- remote publication state.

Remote success is not integration authorization. The same explicit 0.57 integration, 0.64
review/PR publication, and 0.65 release gates apply.

## 9. VS Code surface

- Local vs remote execution selector with policy/default explanation.
- Pool capability, estimated queue class, resource/budget, and data-boundary preflight.
- Durable queue/run progress, logs, artifacts, cancellation, retry, and reconnect.
- Clear unknown/interrupted states and operator recovery links.
- Result review integrated under the original structured work item.

## 10. Acceptance

- Unauthenticated, revoked, stale-lease, wrong-pool, or capability-mismatched Workers cannot run or
  publish a result.
- Source/result/environment/policy digests are verified end to end.
- Disconnect, duplicate delivery, reassignment, and node loss do not create two accepted results.
- Credentials and secrets are scoped, expiring, and absent from model/log/artifact content.
- Cancellation and quota enforcement stop the remote sandbox and persist terminal evidence.
- Remote results cannot mutate the local primary checkout without normal explicit review.

## 11. Non-goals

- Arbitrary SSH command execution or trusting a user-supplied host.
- Shared mutable network filesystems as the source/result protocol.
- Live migration of a running Agent process.
- Bypassing local review because a remote node is “trusted”.
