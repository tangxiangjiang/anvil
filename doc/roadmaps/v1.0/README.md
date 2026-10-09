# Roadmaps v1.0 (cutoff)

Versioned delivery for Anvil through product **0.10**. Design cutoff: [`doc/design/v1.0/`](../../design/v1.0/).

## Version index

| Version | Milestone | Status | Doc | Design chapter |
|---------|-----------|--------|-----|----------------|
| 0.1 | MVP | done | [m_0.1_mvp.md](m_0.1_mvp.md) | 00–04 |
| 0.2 | Resume & observability | done | [m_0.2_resume.md](m_0.2_resume.md) | 06 |
| 0.3 | Extensibility | done | [m_0.3_extensibility.md](m_0.3_extensibility.md) | 09 (initial) |
| 0.4 | Coding UX | done | [m_0.4_coding_ux.md](m_0.4_coding_ux.md) | 05 stream / 04 edits |
| 0.5 | Operator surface | done | [m_0.5_operator.md](m_0.5_operator.md) | 02 / 06 |
| 0.6 | Scale | done | [m_0.6_scale.md](m_0.6_scale.md) | — |
| 0.7 | Streaming & errors | planned | [m_0.7_streaming_errors.md](m_0.7_streaming_errors.md) | [07](../../design/v1.0/07-streaming-and-errors.md) |
| 0.8 | Security hardening | planned | [m_0.8_security.md](m_0.8_security.md) | [08](../../design/v1.0/08-security.md) |
| 0.9 | Extensibility depth | planned | [m_0.9_extensibility.md](m_0.9_extensibility.md) | [09](../../design/v1.0/09-extensibility.md) |
| 0.10 | Cursor SDK local parity | planned | [m_0.10_sdk_parity.md](m_0.10_sdk_parity.md) | [10](../../design/v1.0/10-sdk-parity.md) |

## Naming

See [VERSIONING.md](VERSIONING.md). Enforced by `.cursor/rules/doc-structure.mdc` and `doc-versioning.mdc`.

## Lessons

Operational notes from real runs: [LESSONS.md](LESSONS.md).

## How to use

1. Pick the active milestone phase (`status: planned` → set `in_progress`).
2. Implement only that phase's tasks and acceptance criteria.
3. Mark phase `done`; refresh this index if milestone status changes.

## Suggested order after 0.6

1. **0.7** design 07 — streaming/errors hardening  
2. **0.8** design 08 — security hardening  
3. **0.9** design 09 — extensibility depth (not sub-agents; not 0.10 settingSources)  
4. **0.10** design 10 — Cursor SDK *local* API/config parity  

Still deferred without new design: IDE plugin, cloud, Bridge, multi-agent runtime clone.

Parent index: [`doc/roadmaps/README.md`](../README.md).
