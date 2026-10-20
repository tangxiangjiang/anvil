/**
 * A conversation as Markdown, for `/export`.
 *
 * The file is for a person — a colleague, a ticket, the reader's own notes —
 * so it carries what the conversation *said* and the smallest honest summary of
 * what the agent *did*. It never carries tool output, argument values beyond
 * the paths a call touched, ids, or anything from the host's environment: the
 * transcript itself is redacted, but a file that leaves the workspace is a
 * different audience from a card that stays in it.
 *
 * The fold is `replayOf`, the same one the transcript uses, so the export
 * inherits the rule that makes a replayed conversation honest: the first
 * `role:"user"` record of a run is the user's turn and later ones are the loop
 * talking to the model. Re-deriving that rule here would be a second copy of
 * it, and the copies would drift.
 */

import { TranscriptEntry } from './protocol';
import { toolCallPaths } from './mentions';
import { replayOf } from './transcript';

export interface ExportOptions {
  /** The conversation's name, used as the document's title. */
  title?: string;
  /**
   * How many records the host left out before the first one, by the cap and by
   * the run boundary it was moved back to. A count rather than a flag, so the
   * file can say how much it does not contain.
   */
  dropped?: number;
  /** The date line, already formatted. Omitted means no date line. */
  date?: string;
}

/**
 * Renders the conversation as Markdown.
 *
 * Turns are H3 headings under the title: `### You` and `### Anvil`. That is one
 * level below a heading the reader is likely to have above the paste, and it
 * gives a Markdown preview's outline one entry per side of each turn.
 */
export function exportMarkdown(entries: TranscriptEntry[], options: ExportOptions = {}): string {
  const title = (options.title ?? '').trim() || 'Conversation';
  const out: string[] = [`# ${title}`, ''];

  const dropped = options.dropped ?? 0;
  if (dropped > 0) {
    out.push(
      `_Earlier turns are not included: the store keeps the newest records of this conversation, and ${dropped} came before them._`,
      '',
    );
  }
  if (options.date) out.push(`_Exported ${options.date}._`, '');

  let prose: string[] = [];
  let tools: string[] = [];

  /** Closes the assistant side of a turn, if it has anything to say. */
  function flush(): void {
    if (prose.length > 0) out.push('### Anvil', '', prose.join('\n\n'), '');
    if (tools.length > 0) {
      out.push('**Tools**', '', ...tools.map((line) => `- ${line}`), '');
    }
    prose = [];
    tools = [];
  }

  for (const step of replayOf(entries)) {
    if (step.kind === 'user') {
      flush();
      out.push('### You', '', step.text.trim(), '');
      continue;
    }
    if (step.kind === 'runEnd') {
      flush();
      const status = step.done.status;
      if (status === 'cancelled') out.push('_The turn was stopped._', '');
      else if (status === 'error') out.push('_The turn failed._', '');
      continue;
    }
    if (step.kind !== 'event') continue;
    const event = step.event;
    if (event.type === 'assistant' && !event.parent_tool_call_id) {
      const text = (event.text ?? '').trim();
      if (text !== '') prose.push(text);
    } else if (event.type === 'tool_call' && !event.parent_tool_call_id) {
      const line = toolSummary(event.name, event.args);
      if (line !== '') tools.push(line);
    }
  }
  flush();

  // Exactly one blank line between blocks, and one newline at the end: a file
  // that ends with three blank lines reads as truncated when previewed.
  return `${out.join('\n').replace(/\n{3,}/g, '\n\n').trimEnd()}\n`;
}

/**
 * One tool call, as a name and the paths it named.
 *
 * The paths come from `toolCallPaths` — the same derivation the change tracker
 * and the review card use — so the summary cannot disagree with the card about
 * which file a call touched. Nothing else from the arguments travels: a
 * `run_tests` command line and a `write_file` body are both tool input, and
 * tool input is what the export is for not copying.
 */
function toolSummary(name: string | undefined, args: unknown): string {
  if (typeof name !== 'string' || name === '') return '';
  const paths = toolCallPaths(name, args);
  if (paths.length === 0) return `\`${name}\``;
  return `\`${name}\` — ${paths.map((path) => `\`${path}\``).join(', ')}`;
}
