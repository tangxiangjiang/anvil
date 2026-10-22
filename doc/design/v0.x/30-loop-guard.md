# 30 — Loop guard (replace MaxSteps default)

**Product version focus:** **0.30** ([m_0.30_loop_guard.md](../../roadmaps/v0.x/m_0.30_loop_guard.md))  
**Status:** done

## Intent

Long coding jobs may need thousands of LLM rounds. A small `max_steps` is the wrong control. Convergence comes from detecting **no-progress tool loops**, not from capping step count.

## Behavior

In `runtime.Loop` after each tool batch:

| Pattern | Threshold (default) | First hit | Second hit |
|---------|---------------------|-----------|------------|
| Identical `(tool, args)` | 3 consecutive | Inject corrective user message | `error` / `loop_detected` |
| Identical tool result hash | 3 consecutive | same | same |
| Tool-name cycle length 2 | 3 full repeats | same | same |

`Options.MaxSteps` / job `max_steps`: **0 = unlimited** (default). Optional hard ceiling only.

## Non-goals

- Semantic / embedding loop detection
- Removing the MaxSteps field from the public API (kept as optional escape hatch)
