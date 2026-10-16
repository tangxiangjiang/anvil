/**
 * A stored conversation, folded into the messages the panel already dispatches.
 *
 * The replay has no renderers of its own. Everything it produces is one of the
 * shapes the live stream already produces — an assistant chunk, a tool call, a
 * tool result — so a replayed conversation is drawn by the same code as a live
 * one, and a replayed edit card gets its review button by the same rule. That is
 * the whole point of the shape, and it is why nothing here decides anything the
 * panel decides: `reviewable` is not computed here, because the panel computes
 * it in one place for both paths.
 *
 * Three things this has to get right, none of which is obvious from the records:
 *
 * 1. **A `role:"user"` record is not always the user.** The loop stores its own
 *    messages to the model with that role — the note that follows a `read_image`
 *    result, the loop guard's warning. A run opens with the one user message
 *    that started it, so the first user record of a run is the user's turn and
 *    any later one is the SDK talking to the model. Shown as a "you" bubble, the
 *    replay would put words in the user's mouth they never typed.
 * 2. **Tool output is only as good as what was stored.** The reader returns the
 *    output already redacted, and `is_error` was never written down at all — so
 *    a call that failed replays as one that completed. The alternative is
 *    guessing from the text, which is worse than saying so.
 * 3. **No reasoning.** It was streamed and dropped rather than stored, so there
 *    is nothing to replay from; a replayed turn has no Thinking block, and the
 *    design chapter says that is the one thing that does not come back.
 */

import { DoneParams, HostEvent, TranscriptEntry } from './protocol';

/**
 * One thing to put on the screen, in order. The panel turns each into the
 * message the tab already understands; nothing here builds a webview message
 * itself, so the wire shape stays in the panel.
 */
export type ReplayStep =
  | { kind: 'user'; text: string }
  | { kind: 'notice'; text: string }
  | { kind: 'event'; event: HostEvent }
  | { kind: 'runEnd'; done: DoneParams };

/**
 * Folds stored entries into steps, oldest first.
 *
 * `sessionId` is the session the replay is being shown under, and only travels
 * so the synthesized events look like the ones the host streams. An entry kind
 * this build does not know is skipped rather than guessed at, so a newer host
 * cannot make an older panel show something it has no renderer for.
 */
export function replayOf(entries: TranscriptEntry[], sessionId = ''): ReplayStep[] {
  const steps: ReplayStep[] = [];
  // Whether the run being read is still waiting for the message that started
  // it. A run end puts it back, which is what makes this work for a store whose
  // records carry no run id.
  let awaitingUser = true;

  for (const entry of entries) {
    const runId = entry.run_id ?? '';
    const content = entry.content ?? '';

    if (entry.kind === 'run_end') {
      steps.push({
        kind: 'runEnd',
        done: {
          session_id: sessionId,
          run_id: runId,
          status: entry.status ?? 'finished',
          steps: entry.steps,
          usage: entry.usage,
        },
      });
      awaitingUser = true;
      continue;
    }

    if (entry.kind !== 'message') continue;

    if (entry.role === 'user') {
      if (awaitingUser) {
        awaitingUser = false;
        // Emitted even when empty: a turn that carried only an image is still a
        // turn, and the images themselves were never written down.
        steps.push({ kind: 'user', text: content });
      } else if (content !== '') {
        steps.push({ kind: 'notice', text: content });
      }
      continue;
    }

    if (entry.role === 'assistant') {
      if (content !== '') {
        steps.push({
          kind: 'event',
          event: { session_id: sessionId, run_id: runId, type: 'assistant', text: content },
        });
      }
      for (const call of entry.tool_calls ?? []) {
        steps.push({
          kind: 'event',
          event: {
            session_id: sessionId,
            run_id: runId,
            type: 'tool_call',
            tool_call_id: call.id,
            name: call.name,
            args: call.args,
          },
        });
      }
      continue;
    }

    if (entry.role === 'tool') {
      steps.push({
        kind: 'event',
        event: {
          session_id: sessionId,
          run_id: runId,
          type: 'tool_result',
          tool_call_id: entry.tool_call_id,
          name: entry.name,
          content,
        },
      });
    }
  }

  return steps;
}
