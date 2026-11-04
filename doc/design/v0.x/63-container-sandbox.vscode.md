# 63 — Container sandbox workers

**Milestone focus:** 0.63. **Design status:** proposed. **Risk:** critical.

## 1. Problem

Worktrees isolate repository files and attribution, not processes, kernels, devices, networks, or
host credentials. Test/build commands and generated code can still consume host resources or
reach services available to the user. A stronger execution tier needs an OS-level boundary while
preserving the local SDK and VS Code contracts.

## 2. Goals

- Add a runtime-neutral sandbox contract with containers as the first implementation.
- Run selected Workers, tools, tests, and browser sessions inside a bounded environment.
- Make mounts, network, secrets, processes, resources, and outputs explicit policy.
- Preserve durable run, Worktree, checkpoint, permission, and review identity.
- Fail closed when the required runtime or isolation feature is unavailable.

## 3. Capability model

```text
SandboxRuntime {
  kind, version, platform, rootless, network_modes,
  mount_modes, resource_limits, snapshot_support
}

SandboxSpec {
  image_digest, workspace_mount, readonly_mounts,
  network_policy, secret_handles, cpu, memory, pids,
  timeout, user, environment_allowlist
}

SandboxInstance {
  sandbox_id, worker_id, execution_id, runtime,
  spec_digest, state, revision
}
```

Docker, Podman, dev containers, or a future VM backend implement the same host contract. Image
tags are resolved to immutable digests before approval.

## 4. Trust and mount boundary

- The Worker Worktree is the only writable project mount.
- Git common metadata, primary checkout, other Worktrees, host home, SSH config, credential stores,
  sockets, and editor process data are never mounted.
- Read-only dependency/cache mounts require configured roots and canonical path checks.
- Runtime control sockets are never exposed inside the sandbox.
- Symlink, junction, device, FIFO, and mount traversal are checked both before start and before
  checkpoint/restore.

Generated files return only through the reviewed Worktree boundary.

## 5. Network and secret policy

Default network is `none`. Other modes are explicit:

- `allowlist`: DNS and outbound destinations restricted to configured hosts/ports;
- `package_fetch`: package registries and source mirrors only;
- `browser`: browser-origin policy plus required infrastructure;
- `full`: unavailable in automatic permission modes.

Secrets are opaque handles materialized by the supervisor for one process or file descriptor.
They are not environment defaults, image layers, logs, checkpoints, or model-visible tool output.

## 6. Process and resource policy

The supervisor enforces:

- non-root user and no privilege escalation;
- dropped capabilities and restricted devices;
- CPU, memory, PID, disk, output, and wall-time limits;
- process-group cancellation and orphan cleanup;
- bounded stdout/stderr with truncation evidence;
- no nested container runtime or host namespace access.

Unsupported isolation primitives lower the advertised capability; they do not silently weaken the
requested profile.

## 7. Lifecycle and recovery

```text
requested -> preparing -> ready -> running -> stopped
                 |          |          |
                 v          v          v
               failed   interrupted   cleanup_failed
```

The signed lifecycle record binds runtime, image digest, policy digest, Worker/execution ids, and
mount fingerprints. Restart inspects runtime facts but never claims an unknown process. Cleanup
removes only the matching instance, generated profile, and temporary volumes.

## 8. VS Code surface

- Sandbox profile selector on eligible workflow execution.
- Preflight showing image digest, writable/read-only mounts, network mode, resources, and secret
  names.
- Live resource/status view, logs, stop, and cleanup.
- Clear distinction between Worktree isolation and container isolation.
- Setup diagnostics for runtime installation, rootless support, image pull, and policy refusal.

## 9. Checkpoints and review

Checkpoints remain workspace-scoped, not whole-container snapshots. Restore writes only reviewed
Worktree paths after identity checks. Container layers, package caches, secret mounts, and runtime
metadata are not checkpoint content.

Review records runtime/image/policy evidence and verification results so a produced commit can be
traced to its execution boundary.

## 10. Acceptance

- Adversarial Workers cannot read host home, primary checkout, sibling Worktrees, runtime socket,
  or secrets not granted to the process.
- Resource, timeout, cancellation, and output limits terminate the whole process group.
- Network fixtures prove default deny and destination enforcement.
- Image, mount, policy, Worker, and checkpoint mismatches fail before launch/restore.
- Runtime crash and editor restart preserve an interrupted/recoverable state.
- Cleanup never targets an instance or volume without the signed identity tuple.

## 11. Non-goals

- Claiming containers are equivalent to a hardened VM against kernel exploits.
- Kubernetes or remote fleet scheduling; chapter 66 owns remote execution.
- Mounting the entire host workspace or user home for compatibility.
- Persisting secrets or mutable image tags in lifecycle records.
