# 65 — Production hardening and release confidence

**Milestone focus:** 0.65. **Design status:** proposed. **Risk:** high.

## 1. Problem

Feature-complete local flows are not the same as an all-day production client. Anvil needs proof
across platforms, large repositories, long sessions, process crashes, network faults, provider
variance, and upgrade/rollback boundaries. Diagnostics must help operators without exporting
source, prompts, secrets, or private paths by default.

## 2. Goals

- Establish Windows, macOS, and Linux release gates for SDK, Host, and VSIX.
- Add deterministic fault injection, crash recovery, soak, scale, and compatibility suites.
- Define privacy-preserving diagnostics and opt-in telemetry.
- Measure latency, resource use, context growth, store size, and recovery time.
- Automate release qualification, artifact provenance, rollback, and migration checks.

## 3. Validation matrix

The required matrix covers:

- operating system and architecture;
- Git and VS Code supported ranges;
- JSONL and SQLite stores;
- direct API and gateway model modes;
- trusted/untrusted workspace;
- clean, dirty, large, Unicode, long-path, symlink/junction, submodule, and sparse repositories
  according to advertised capability;
- network loss, rate limit, provider truncation, Host crash, editor crash, reboot, and disk-full
  boundaries.

Unsupported combinations are reported explicitly and excluded from capability advertisement.

## 4. Fault injection

Named failpoints are available only in test builds. They cover:

- before/after durable append;
- before/after filesystem or Git mutation;
- process kill during stream, checkpoint, Worktree transaction, cleanup, and provider write;
- partial/truncated stdout, protocol frames, and store records;
- timeout, cancellation, lock contention, and clock changes.

Each test asserts one of two outcomes: provably completed or durably recoverable/blocked. Silent
partial success is a failure.

## 5. Soak and scale

Scenarios include:

- multi-hour streaming and compaction;
- many sessions/tabs and large JSONL stores;
- parallel Workers, tasks, MCP servers, browser sessions, and sandboxes;
- large diffs and repositories;
- repeated reload, reconnect, cancel, retry, archive, and cleanup;
- bounded memory, handles, goroutines, child processes, temp files, and disk growth.

Performance budgets are versioned and regressions require an explicit waiver.

## 6. Diagnostics

Local diagnostics expose:

- component/product/protocol versions;
- capability and configuration summary with secret values removed;
- Host/process lifecycle, recent structured error codes, queue depth, and store health;
- resource and latency aggregates;
- artifact/package integrity.

A support bundle is previewed before export. It excludes transcript content, source, file bodies,
API keys, tokens, cookies, environment values, and absolute user paths unless the user selects a
specific attachment.

## 7. Telemetry

Telemetry is off by default and opt-in. Allowed events are schema-reviewed aggregates such as
feature success/failure code, latency bucket, platform, and version. No prompt, response, path,
command argument, model content, repository identity, or provider credential is collected.

Events have a local preview, retention limit, and delete switch.

## 8. Release and migration

- Reproducible per-platform Host and VSIX builds with hashes and provenance.
- Clean-install and N-2 upgrade tests for configuration, JSONL/SQLite, sessions, checkpoints, and
  lifecycle records.
- Forward migrations are explicit and restart-safe; downgrade refusal is clear when formats are
  incompatible.
- Rollback retains user data and never silently runs an older binary against newer mutable state.
- Marketplace/package metadata and bundled binary versions are checked before publication.

## 9. Acceptance

- Required platform matrix passes from clean environments.
- Fault tests prove transaction/recovery invariants at every durable boundary.
- Soak suites remain within declared resource and growth budgets.
- Support bundles and telemetry fixtures contain no forbidden content.
- Upgrade/rollback tests preserve or explicitly refuse every persisted schema.
- Build provenance and version synchronization are machine-verified.
- Release cannot proceed when a required gate is missing, stale, or waived without an audit record.

## 10. Non-goals

- Collecting user content for convenience.
- Claiming zero defects or using telemetry as a substitute for deterministic tests.
- Supporting unbounded historical platform/tool versions.
- Auto-uploading diagnostics after a crash.
