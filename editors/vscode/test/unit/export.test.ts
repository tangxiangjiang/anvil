/**
 * The Markdown a conversation exports to.
 *
 * Two audiences, and the tests are about the second one: the file leaves the
 * editor — pasted into a ticket, sent to a colleague — so what it may carry is
 * narrower than what the transcript draws. Prose is the conversation; a tool
 * call is its name and, when its arguments name files, those paths. Not a
 * command line, not a file body, not tool output, not a run or call id. Each of
 * those is checked by putting one in the fixture and demanding it stay out.
 *
 * The fold is `replayOf`, and the rule that comes with it is checked here too:
 * only the first `role:"user"` record of a run is the user's turn. A later one
 * is the loop talking to the model, and exporting it as "You" would put words
 * in the user's mouth in a document that outlives the conversation.
 */

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { exportMarkdown } from '../../src/export';
import { TranscriptEntry } from '../../src/protocol';

function message(over: Partial<TranscriptEntry> & { content: string }): TranscriptEntry {
  return { kind: 'message', run_id: 'run-1', role: 'assistant', ...over };
}

test('a turn is the user, the answer, and what the call touched', () => {
  const entries: TranscriptEntry[] = [
    message({ role: 'user', content: 'Rename the parser.' }),
    message({
      content: 'I will start with the file.',
      tool_calls: [
        { id: 'call-1', name: 'write_file', args: { path: 'src/parser.ts', content: 'SECRET BODY' } },
      ],
    }),
    message({ role: 'tool', tool_call_id: 'call-1', name: 'write_file', content: 'SECRET OUTPUT' }),
    { kind: 'run_end', run_id: 'run-1', status: 'finished' },
  ];
  const text = exportMarkdown(entries);
  assert.match(text, /^# Conversation\n/);
  assert.match(text, /### You\n\nRename the parser\./);
  assert.match(text, /### Anvil\n\nI will start with the file\./);
  assert.match(text, /- `write_file` — `src\/parser\.ts`/);

  // The three things a summary must not become: a file body, a tool result, and
  // an id. The ids in the fixture are named so a leak is unmistakable.
  assert.doesNotMatch(text, /SECRET BODY|SECRET OUTPUT|call-1|run-1/);
});

test('a call that names no file is only its name', () => {
  const text = exportMarkdown([
    message({ role: 'user', content: 'Run the tests.' }),
    message({
      content: 'Running them.',
      tool_calls: [
        { id: 'c', name: 'run_tests', args: { command: 'go test ./... -run SECRET_TEST' } },
        { id: 'd', name: 'apply_patch', args: { patch: '*** Update File: a.ts\n*** Update File: b.ts' } },
      ],
    }),
  ]);
  assert.match(text, /- `run_tests`/);
  // Only the tool's own path headers travel; the patch body does not.
  assert.match(text, /- `apply_patch` — `a\.ts`, `b\.ts`/);
  assert.doesNotMatch(text, /SECRET_TEST|\*\*\* Update/);
});

test('only the first user record of a run is the user’s turn', () => {
  const text = exportMarkdown([
    message({ role: 'user', content: 'First request.' }),
    message({ role: 'assistant', content: 'Working.' }),
    // The loop's own note to the model, stored with the same role.
    message({ role: 'user', content: 'A loop note the user never typed.' }),
    { kind: 'run_end', run_id: 'run-1', status: 'finished' },
    message({ role: 'user', content: 'Second request.' }),
  ]);
  assert.equal(text.match(/### You/g)?.length, 2);
  assert.match(text, /### You\n\nSecond request\./);
  // The note is part of the conversation's machinery, not the conversation.
  assert.doesNotMatch(text, /loop note/);
});

test('a turn that was stopped or failed says so', () => {
  const base = [message({ role: 'user', content: 'Try it.' })];
  assert.match(exportMarkdown([...base, { kind: 'run_end', status: 'cancelled' }]), /The turn was stopped\./);
  assert.match(exportMarkdown([...base, { kind: 'run_end', status: 'error' }]), /The turn failed\./);
  assert.doesNotMatch(
    exportMarkdown([...base, { kind: 'run_end', status: 'finished' }]),
    /The turn (was stopped|failed)/,
  );
});

test('a truncated transcript says how much is missing', () => {
  const text = exportMarkdown([message({ role: 'user', content: 'Hi.' })], { dropped: 42, date: '2026-10-20' });
  assert.match(text, /Earlier turns are not included/);
  assert.match(text, /42 came before them/);
  assert.match(text, /_Exported 2026-10-20\._/);
  // Nothing is invented when the count is unknown or zero.
  assert.doesNotMatch(exportMarkdown([message({ content: 'x' })]), /Earlier turns|Exported/);
});

test('the file is a Markdown document with one newline at the end', () => {
  const text = exportMarkdown([
    message({ role: 'user', content: 'One.' }),
    message({ content: 'One answer.' }),
  ]);
  assert.ok(text.endsWith('\n'));
  assert.ok(!text.endsWith('\n\n'));
  assert.doesNotMatch(text, /\n{3,}/);
});

test('a conversation with nothing in it is still a file', () => {
  // An empty conversation exports a title rather than an empty file: a file a
  // Markdown preview opens has to be a document.
  assert.equal(exportMarkdown([]), '# Conversation\n');
  assert.equal(exportMarkdown([], { title: '  Naming things  ' }), '# Naming things\n');
});
