# 03 — Runtime loop

## Purpose

Execute one user message as a **tool-calling loop** until the model stops requesting tools, the **loop guard** stops a runaway, an optional step ceiling is hit, errors, or is cancelled.

## Algorithm

1. Append user message to the in-memory conversation.
2. Build request: system prompt + history + tool schemas.
3. Call LLM (`internal/llm`).
4. If response has tool calls:
   - Emit `tool_call` events in assistant order.
   - Execute via registry: sequential by default; `Options.ParallelTools` (0.9+) may run a non-mutating batch concurrently.
   - Emit `tool_result` events in the same start order; append tool results to history.
   - **Loop guard** (0.30+): detect repeated identical `(tool,args)`, stalled identical results, or short tool-name cycles. First hit injects a corrective user message; second hit ends with `loop_detected`.
   - Go to 3.
5. If response is assistant text only:
   - Emit `assistant` (and deltas if streaming tokens).
   - Emit `done`; set `RunResult.Status = finished`.
6. Persist run metadata when store is enabled (0.2+).

## Limits

| Limit | Default | Notes |
|-------|---------|--------|
| Loop guard | on | Repeat / stall / ping-pong — primary convergence for long jobs |
| `MaxSteps` | **0 (unlimited)** | Optional hard ceiling only; do not set for normal coding jobs |
| `MaxWallTime` | none unless set | Added in 0.2; cancel semantics |
| Per-tool timeout | shell: 60s | Others bound by ctx |

Each LLM round emits a `progress` event with current step (`MaxSteps` is 0 when unlimited).

## Completion conditions

| Condition | Status |
|-----------|--------|
| Model returns no tool calls | `finished` |
| Context cancelled / Cancel | `cancelled` (0.2) |
| Loop guard stop | `error` (`loop_detected`) |
| Optional MaxSteps exceeded | `error` (`max_steps`) |
| LLM or tool hard failure | `error` |

## System prompt responsibilities

Owned by `internal/prompt`:

- Role: coding agent in `cwd`.
- Tool usage guidance (prefer edit over rewrite; run tests after changes).
- Safety: stay within workspace; do not exfiltrate secrets.
- Optional short workspace listing (file tree snippet) — keep small.

## Single-flight

If `Send` is called while a Run is active → return `*anvil.Error` (not retryable).

## Out of scope for 0.1

- Planner / multi-agent handoff.
- Speculative / planner-driven tool fan-out (opt-in same-turn parallel is 0.9).
- Automatic context summarization (0.3; durable markers in 0.9).
