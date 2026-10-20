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

import { DoneParams, HostEvent, TodoItem, TranscriptEntry } from './protocol';

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
  // The calls already emitted. A result whose call is not here — the window
  // opened mid-run, or a call was cut — gets the row it should have had, so the
  // renderer never has to show a result with nothing to attach it to.
  const seenCalls = new Set<string>();

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

    if (entry.kind === 'summary') {
      // A compaction boundary. The live path draws the same row from a
      // `compact` event; the store keeps no counts, so the replayed one says
      // only that it happened — which is all a reader can be told honestly.
      steps.push({
        kind: 'event',
        event: { session_id: sessionId, run_id: runId, type: 'compact' },
      });
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
        if (call.id) seenCalls.add(call.id);
        // A checklist has no record of its own: it is an ordinary tool call,
        // and the arguments are the whole list. Synthesising the live `todo`
        // event here gives the webview one renderer for both paths. A call
        // whose arguments the tool would have refused yields nothing, because
        // the same validation is applied below — so a failed call does not
        // replay as a list that was never accepted.
        if (call.name === 'todo_write') {
          const items = todoItemsOf(call.args);
          if (items) {
            steps.push({
              kind: 'event',
              event: { session_id: sessionId, run_id: runId, type: 'todo', items },
            });
          }
        }
      }
      continue;
    }

    if (entry.role === 'tool') {
      // The store keeps the result's tool name but not the call's arguments, so
      // a synthesized row says what ran and leaves IN empty. Emitting the call
      // here rather than in the renderer keeps one path: a replayed result is
      // drawn by the same code as a live one.
      if (entry.tool_call_id && !seenCalls.has(entry.tool_call_id)) {
        steps.push({
          kind: 'event',
          event: {
            session_id: sessionId,
            run_id: runId,
            type: 'tool_call',
            tool_call_id: entry.tool_call_id,
            name: entry.name ?? '',
          },
        });
        seenCalls.add(entry.tool_call_id);
      }
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

const TODO_STATUSES = new Set(['pending', 'in_progress', 'done']);

/**
 * The task list a stored `todo_write` call carries, or undefined when it could
 * not have been accepted.
 *
 * The check mirrors the tool's: every row needs text and one of the three
 * statuses, and text is trimmed because that is what the tool stored. Reading
 * the arguments leniently would let a call the host refused replay as a list
 * the panel never showed live, which is the one thing a replay must not do.
 */
function todoItemsOf(args: unknown): TodoItem[] | undefined {
  if (!args || typeof args !== 'object') return undefined;
  const rows = (args as { items?: unknown }).items;
  if (!Array.isArray(rows)) return undefined;
  const items: TodoItem[] = [];
  for (const row of rows) {
    if (!row || typeof row !== 'object') return undefined;
    const { text, status } = row as { text?: unknown; status?: unknown };
    if (typeof text !== 'string' || text.trim() === '') return undefined;
    if (typeof status !== 'string' || !TODO_STATUSES.has(status)) return undefined;
    items.push({ text: text.trim(), status: status as TodoItem['status'] });
  }
  return items;
}
