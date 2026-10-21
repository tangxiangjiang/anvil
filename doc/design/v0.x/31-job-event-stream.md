# 31 — Job event stream (app chat)

**Product version focus:** **0.31** ([m_0.31_job_stream.md](../../roadmaps/v1.0/m_0.31_job_stream.md))  
**Status:** done

## Intent

Work Platform chat should show assistant tokens and tool activity as they happen (Cursor-like), not only a spinner until `job.done`.

## Behavior

1. **agentd → scheduler:** forward run events as `cluster.event` (`JobEventBody`) while a job runs; still finish with `cluster.done`.
2. **scheduler → app:** `POST /v1/jobs` with `"stream": true` (or `Accept: text/event-stream`) responds as SSE:
   - `event: job.event` + JSON `JobEventBody` (`assistant` text deltas, `tool_call`, `tool_result`, `progress`, …)
   - `event: job.done` + JSON `JobDoneBody` (terminal)
3. **Flutter:** stream consumer appends `assistant` text into the live bubble; tool lines as muted status; finalize from `job.done`.

Non-streaming `POST /v1/jobs` remains JSON terminal response (compat).

## Non-goals

- WebSocket hub / seq replay store
- Full tool-call cards / diffs UI
- Changing gateway LLM SSE contract
