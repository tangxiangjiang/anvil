# Lessons learned

Short notes that should influence how we drive Anvil milestones. Prefer linking from phase docs over duplicating long narratives.

## 2026-09-30 — oneshot asked to implement p_0.2.1

**Setup:** `./bin/oneshot.exe . "那你帮我做下p_0.2.1的功能"` after MVP worked for Q&A / file edits.

**What happened:** Agent explored docs/code, started `internal/store` + `Resume` wiring, then hit `max steps exceeded (32)` and exited with startup/run error. Working tree left a partial but directionally correct implementation.

**Takeaways for later phases**

| Lesson | Implication |
|--------|-------------|
| Oneshot ≠ milestone delivery | Use Cursor (or a future interactive CLI) for multi-file phases; oneshot for smoke / small prompts |
| Cap steps vs task size | Raise `MaxSteps` or split tasks when using the SDK on large changes; document defaults |
| Always review agent diffs | Check tests, phase Acceptance checkboxes, ID consistency, silent error swallows |
| Rules are not automatic for oneshot | If we want phase discipline inside Anvil runs, encode it in system prompt / tools, not only `.cursor/rules` |
| Partial agent code is salvageable | Prefer “clean up + test” over discard when structure matches design |

See also: [m_0.2/p_0.2.1.md](m_0.2/p_0.2.1.md) notes section.
