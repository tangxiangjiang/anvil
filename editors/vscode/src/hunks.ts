/**
 * A change accepted in parts.
 *
 * The preview the host sends for a gated edit is a unified diff plus both
 * bodies. Deciding per hunk cannot be done on the diff alone: the diff is
 * context, while the file is bytes. So a selection is applied to the preview's
 * whole `before` text and the result is sent back as rewritten arguments — the
 * call then executes exactly the text that was computed here.
 *
 * Two rules keep that honest:
 *
 *   1. **Lines come from the file, not from the diff.** A context line is
 *      emitted as the `before` line it matched, terminator included, so an
 *      untouched CRLF line stays CRLF. Only added lines are synthesized, and
 *      they take the file's own terminator.
 *   2. **A hunk that does not match aborts the whole operation.** Partial mode
 *      returns `undefined` rather than a best guess; the caller then offers the
 *      ordinary Allow/Deny card.
 *
 * No `vscode` import: this half is testable.
 */

import { ApprovalPreview } from './protocol';

/** One body line of a hunk. */
export interface DiffLine {
  kind: 'context' | 'add' | 'del';
  /** The content, without the leading `' '`, `'+'` or `'-'`. */
  text: string;
  /** The diff marked this side as having no newline after the line. */
  noNewline?: boolean;
}

/** One `@@` section, header and body together. */
export interface DiffHunk {
  header: string;
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  lines: DiffLine[];
}

/** A change whose hunks were all proven to apply. */
export interface PartialDiff {
  before: string;
  after: string;
  hunks: DiffHunk[];
}

/** A line of a file, with the terminator it had there. */
interface FileLine {
  text: string;
  /** `'\n'`, `'\r\n'`, or `''` for a last line with no terminator. */
  eol: string;
}

/** Splits text into lines, keeping each line's own terminator. */
function splitLines(text: string): FileLine[] {
  const lines: FileLine[] = [];
  let start = 0;
  for (let index = 0; index < text.length; index++) {
    if (text[index] !== '\n') continue;
    const contentEnd = index > start && text[index - 1] === '\r' ? index - 1 : index;
    lines.push({ text: text.slice(start, contentEnd), eol: text.slice(contentEnd, index + 1) });
    start = index + 1;
  }
  if (start < text.length) lines.push({ text: text.slice(start), eol: '' });
  return lines;
}

/**
 * The terminator a synthesized line takes. A file has one convention; a diff
 * that joins its lines with `\n` must not impose that convention on it.
 */
function terminatorOf(before: string): string {
  return before.includes('\r\n') ? '\r\n' : '\n';
}

/**
 * Reads a unified diff into hunks.
 *
 * File headers (`---` / `+++`) end the current hunk. A line this parser does
 * not recognise ends it too, rather than being folded into the body: a hunk
 * with an unreadable line is a hunk that cannot be applied, and reporting that
 * is better than applying something else.
 */
export function parseDiff(diff: string): DiffHunk[] {
  const hunks: DiffHunk[] = [];
  let current: DiffHunk | undefined;
  let previous: DiffLine | undefined;

  for (const line of splitLines(diff)) {
    const text = line.text;
    if (text.startsWith('@@')) {
      const header = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/.exec(text);
      if (!header) {
        current = undefined;
        previous = undefined;
        continue;
      }
      current = {
        header: text,
        oldStart: Number(header[1]),
        oldLines: header[2] === undefined ? 1 : Number(header[2]),
        newStart: Number(header[3]),
        newLines: header[4] === undefined ? 1 : Number(header[4]),
        lines: [],
      };
      hunks.push(current);
      previous = undefined;
      continue;
    }
    if (text.startsWith('--- ') || text.startsWith('+++ ')) {
      current = undefined;
      previous = undefined;
      continue;
    }
    if (text.startsWith('\\')) {
      // "\ No newline at end of file" belongs to the line above it.
      if (previous) previous.noNewline = true;
      continue;
    }
    if (!current || text === '') continue;
    const prefix = text[0];
    if (prefix !== ' ' && prefix !== '+' && prefix !== '-') {
      current = undefined;
      previous = undefined;
      continue;
    }
    previous = {
      kind: prefix === ' ' ? 'context' : prefix === '+' ? 'add' : 'del',
      text: text.slice(1),
    };
    current.lines.push(previous);
  }
  return hunks;
}

/**
 * The index at which a hunk's old side starts, or undefined.
 *
 * Content is compared, not position: line numbers are a hint the diff carries,
 * and a file that has shifted since the preview would otherwise take the change
 * in the wrong place. An insertion (`oldLines === 0`) has no old side, so it
 * applies where it was pointed at.
 */
function locate(lines: FileLine[], oldTexts: readonly string[], from: number): number | undefined {
  if (oldTexts.length === 0) return from;
  for (let at = from; at + oldTexts.length <= lines.length; at++) {
    let matches = true;
    for (let offset = 0; offset < oldTexts.length; offset++) {
      if (lines[at + offset].text !== oldTexts[offset]) {
        matches = false;
        break;
      }
    }
    if (matches) return at;
  }
  return undefined;
}

/**
 * Applies the selected hunks to `before`.
 *
 * `accepted` names the hunks to take (by index). Unselected hunks are emitted
 * as the file had them, so rejecting one is indistinguishable from never having
 * proposed it. `undefined` means a hunk could not be proven to apply and the
 * whole operation is refused.
 */
export function applyAccepted(
  before: string,
  hunks: readonly DiffHunk[],
  accepted: ReadonlySet<number> | readonly number[],
): string | undefined {
  const selected: ReadonlySet<number> = accepted instanceof Set ? accepted : new Set(accepted);
  const lines = splitLines(before);
  const eol = terminatorOf(before);
  const out: string[] = [];
  let cursor = 0;

  for (let index = 0; index < hunks.length; index++) {
    const hunk = hunks[index];
    const oldTexts = hunk.lines.filter((line) => line.kind !== 'add').map((line) => line.text);
    // A hunk with no old side inserts *after* `oldStart`; every other hunk
    // starts *at* it. That off-by-one is the unified diff's, not ours.
    const pointedAt = hunk.oldLines === 0 ? hunk.oldStart : hunk.oldStart - 1;
    const from = Math.min(lines.length, Math.max(cursor, pointedAt));
    const at = locate(lines, oldTexts, from);
    if (at === undefined) return undefined;

    for (; cursor < at; cursor++) out.push(lines[cursor].text + lines[cursor].eol);

    let next = at;
    if (selected.has(index)) {
      for (const line of hunk.lines) {
        if (line.kind === 'del') {
          next++;
        } else if (line.kind === 'context') {
          // A hunk that claims more lines than the file has is malformed; it
          // must not read past the end into a guessed file.
          if (next >= lines.length) return undefined;
          out.push(lines[next].text + lines[next].eol);
          next++;
        } else {
          // A diff that says the file ends without a newline is telling the
          // truth about the side it names; a synthesized last line follows it.
          out.push(line.noNewline ? line.text : line.text + eol);
        }
      }
    } else {
      for (const line of hunk.lines) {
        if (line.kind === 'add') continue;
        if (next >= lines.length) return undefined;
        out.push(lines[next].text + lines[next].eol);
        next++;
      }
    }
    cursor = next;
  }

  for (; cursor < lines.length; cursor++) out.push(lines[cursor].text + lines[cursor].eol);
  return out.join('');
}

/**
 * The parts a change can be accepted in, or undefined when it cannot.
 *
 * Availability is proven, not assumed: applying *every* hunk must reproduce
 * the preview's `after` exactly. A diff that disagrees with the bodies — a
 * truncated one, a hand-written one, a file that changed underneath — fails
 * that check, and the card keeps its ordinary Allow/Deny choice. `apply_patch`
 * is absent by construction: it can span files and carries no bodies.
 */
export function partialDiff(tool: string, preview: ApprovalPreview | undefined): PartialDiff | undefined {
  if (tool !== 'write_file' && tool !== 'edit_file') return undefined;
  if (!preview || preview.kind !== 'diff' || preview.truncated === true) return undefined;
  const { before, after, diff } = preview;
  if (typeof before !== 'string' || typeof after !== 'string' || typeof diff !== 'string') {
    return undefined;
  }
  const hunks = parseDiff(diff);
  if (hunks.length === 0) return undefined;
  const all = applyAccepted(before, hunks, hunks.map((_, index) => index));
  if (all !== after) return undefined;
  return { before, after, hunks };
}

/**
 * The call's arguments rewritten to carry the accepted text.
 *
 * `write_file` is one body; `edit_file` replaces one occurrence, and its
 * `old_string` becomes the whole `before` the review was computed against, so
 * the tool's single-match rule finds exactly one. The preview's bodies must be
 * the ones the arguments describe — when they are not, the arguments are not
 * this review's to rewrite and the answer is undefined rather than a guess.
 */
export function rewriteArgs(
  tool: string,
  args: unknown,
  before: string,
  after: string,
  acceptedText: string,
): Record<string, unknown> | undefined {
  if (!args || typeof args !== 'object') return undefined;
  const fields = args as Record<string, unknown>;

  if (tool === 'write_file') {
    if (fields.content !== after) return undefined;
    return { ...fields, content: acceptedText };
  }
  if (tool === 'edit_file') {
    if (fields.old_string !== before || fields.new_string !== after) return undefined;
    return { ...fields, old_string: before, new_string: acceptedText };
  }
  return undefined;
}
