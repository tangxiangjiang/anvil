# Roadmaps

Versioned delivery for Anvil. Design lives in [`doc/design/`](../design/); this tree only tracks **when** work ships.

## Version index

| Version | Milestone | Status | Doc |
|---------|-----------|--------|-----|
| 0.1 | MVP | done | [m_0.1_mvp.md](m_0.1_mvp.md) |
| 0.2 | Resume & observability | in_progress | [m_0.2_resume.md](m_0.2_resume.md) |
| 0.3 | Extensibility | done | [m_0.3_extensibility.md](m_0.3_extensibility.md) |
| 0.4 | Coding UX | planned | [m_0.4_coding_ux.md](m_0.4_coding_ux.md) |
| 0.5 | Operator surface | planned | [m_0.5_operator.md](m_0.5_operator.md) |
| 0.6 | Future / deferred | planned | [m_0.6_future.md](m_0.6_future.md) |

## Naming

See [VERSIONING.md](VERSIONING.md). Enforced by `.cursor/rules/doc-structure.mdc` and `doc-versioning.mdc`.

## Lessons

Operational notes from real runs: [LESSONS.md](LESSONS.md).

## How to use

1. Pick the active milestone phase (`status: planned` → set `in_progress`).
2. Implement only that phase's tasks and acceptance criteria.
3. Mark phase `done`; refresh this index if milestone status changes.

## Suggested order after 0.3

1. Finish **0.2** (`p_0.2.2` Cancel/limits → `p_0.2.3` interactive).
2. **0.4** coding UX (stream, edits, rules).
3. **0.5** operator/eval.
4. Promote items from **0.6** only with new design + phases.
