# 31 — Job event stream (app chat)

**Product version focus:** **0.31** ([m_0.31_job_stream.md](../../roadmaps/v0.x/m_0.31_job_stream.md))  
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

### Interactive approval extension (0.45.9)

Workbench jobs opt in by setting `approval_session_id` on assignment. agentd then emits
`job.event(type=approval_request)` with `approval.request_id`, arguments, preview, decisions and
expiry; `approval_result` records answered, expired, or cancelled. The controller answers through
`POST /v1/jobs/{job_id}/approval`.

The scheduler routes the response only to the node in its live job ownership map and waits for a
`job.approval_result` acknowledgement. agentd requires the job id, approval session id and request id
to match one pending callback. A request is removed atomically on first answer or expiry, so
duplicates and late answers return a conflict. Cancellation denies and releases all pending calls.
Jobs without `approval_session_id` preserve the existing unattended behaviour.

## Non-goals

- WebSocket hub / seq replay store
- Full tool-call cards / diffs UI
- Changing gateway LLM SSE contract
