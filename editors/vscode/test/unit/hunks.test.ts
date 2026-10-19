/**
 * Accepting a change in parts: the arithmetic that stands between a set of
 * checkboxes and a file on disk.
 *
 * These are the tests that matter most in the extension, because the failure
 * mode is silent and destructive: a wrong result is a written file. So the file
 * checks bytes, not just line counts — the terminators are half the content on
 * a Windows checkout — and it checks that an unusable diff is refused rather
 * than approximated.
 */

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import {
  applyAccepted,
  parseDiff,
  partialDiff,
  rewriteArgs,
} from '../../src/hunks';

const BEFORE = 'one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\n';
const AFTER_ALL = 'one\nTWO\nthree\nfour\nFIVE\nsix\nseven\nEIGHT\nnine\n';
const HUNKS = [
  '@@ -1,3 +1,3 @@',
  ' one',
  '-two',
  '+TWO',
  ' three',
  '@@ -4,3 +4,3 @@',
  ' four',
  '-five',
  '+FIVE',
  ' six',
  '@@ -7,3 +7,3 @@',
  ' seven',
  '-eight',
  '+EIGHT',
  ' nine',
].join('\n');

test('a parser reads the ranges and the body of every hunk', () => {
  const hunks = parseDiff(HUNKS);
  assert.equal(hunks.length, 3);
  assert.deepEqual(
    hunks.map((hunk) => [hunk.oldStart, hunk.oldLines, hunk.newStart, hunk.newLines]),
    [
      [1, 3, 1, 3],
      [4, 3, 4, 3],
      [7, 3, 7, 3],
    ],
  );
  assert.deepEqual(
    hunks[0].lines.map((line) => line.kind),
    ['context', 'del', 'add', 'context'],
  );
  // An omitted count is one line, which is what the unified format means.
  const single = parseDiff('@@ -12 +12 @@\n-old\n+new');
  assert.equal(single[0].oldLines, 1);
  assert.equal(single[0].newLines, 1);
});

test('applying no hunks gives the file back byte for byte', () => {
  const hunks = parseDiff(HUNKS);
  assert.equal(applyAccepted(BEFORE, hunks, []), BEFORE);
});

test('every hunk gives the diff its whole post-image', () => {
  const hunks = parseDiff(HUNKS);
  assert.equal(applyAccepted(BEFORE, hunks, [0, 1, 2]), AFTER_ALL);
});

test('a non-adjacent subset applies only the parts chosen', () => {
  const hunks = parseDiff(HUNKS);
  // The middle hunk alone: the other two keep the file's own lines, including
  // the range between them that no hunk mentioned.
  assert.equal(
    applyAccepted(BEFORE, hunks, [1]),
    'one\ntwo\nthree\nfour\nFIVE\nsix\nseven\neight\nnine\n',
  );
  // First and last: the gap between them (four..seven) has to be copied, not
  // recomputed from a new range.
  assert.equal(
    applyAccepted(BEFORE, hunks, [0, 2]),
    'one\nTWO\nthree\nfour\nfive\nsix\nseven\nEIGHT\nnine\n',
  );
});

test('a CRLF file stays CRLF, including the lines the change adds', () => {
  const before = 'one\r\ntwo\r\nthree\r\n';
  // The diff is joined with \n, as a host that normalized its transport would.
  const diff = ['@@ -1,3 +1,4 @@', ' one', '-two', '+TWO', '+extra', ' three'].join('\n');
  const hunks = parseDiff(diff);
  assert.equal(
    applyAccepted(before, hunks, [0]),
    'one\r\nTWO\r\nextra\r\nthree\r\n',
  );
  // Untouched lines are the file's own bytes, not re-joined text.
  assert.equal(applyAccepted(before, hunks, []), before);
});

test('a hunk whose context no longer matches refuses the whole operation', () => {
  const hunks = parseDiff(HUNKS);
  // The file moved: `two` is already upper-case, so hunk 1's old side cannot be
  // found where the diff says. A guess here would write the wrong text.
  assert.equal(applyAccepted(BEFORE.replace('two', 'TWO'), hunks, [0, 1, 2]), undefined);
  // Even selecting a *different* hunk is refused: the operation either applies
  // as a whole or it does not apply.
  assert.equal(applyAccepted(BEFORE.replace('two', 'TWO'), hunks, [1]), undefined);
});

test('an insertion with no old side applies where the header points', () => {
  const before = 'a\nb\nc\n';
  const diff = ['@@ -2,0 +3,1 @@', '+new'].join('\n');
  const hunks = parseDiff(diff);
  assert.deepEqual(hunks[0].oldLines, 0);
  assert.equal(applyAccepted(before, hunks, [0]), 'a\nb\nnew\nc\n');
});

test('the last line without a newline is kept that way', () => {
  const before = 'a\nb';
  const diff = [
    '@@ -1,2 +1,2 @@',
    ' a',
    '-b',
    '\\ No newline at end of file',
    '+B',
    '\\ No newline at end of file',
  ].join('\n');
  const hunks = parseDiff(diff);
  assert.equal(applyAccepted(before, hunks, [0]), 'a\nB');
  // Without the marker the synthesized line takes the file's terminator.
  const plain = parseDiff(['@@ -1,2 +1,2 @@', ' a', '-b', '+B'].join('\n'));
  assert.equal(applyAccepted(before, plain, [0]), 'a\nB\n');
});

// --- availability ------------------------------------------------------------

test('a diff that reconstructs the post-image is available in parts', () => {
  const partial = partialDiff('edit_file', {
    kind: 'diff',
    path: 'notes.txt',
    diff: HUNKS,
    before: BEFORE,
    after: AFTER_ALL,
  });
  assert.ok(partial);
  assert.equal(partial.hunks.length, 3);
  assert.equal(partial.before, BEFORE);
  assert.equal(partial.after, AFTER_ALL);
});

test('availability is proven, not assumed', () => {
  const base = { kind: 'diff' as const, path: 'notes.txt', diff: HUNKS, before: BEFORE };
  // A body the diff does not produce is a host whose preview cannot be trusted.
  assert.equal(partialDiff('edit_file', { ...base, after: AFTER_ALL + 'ten\n' }), undefined);
  // A truncated preview dropped its bodies for a reason.
  assert.equal(
    partialDiff('edit_file', { ...base, after: AFTER_ALL, truncated: true }),
    undefined,
  );
  // A patch can span files and carries no single body.
  assert.equal(partialDiff('apply_patch', { ...base, after: AFTER_ALL }), undefined);
  // A command has hunks of nothing.
  assert.equal(partialDiff('edit_file', { ...base, diff: '', after: AFTER_ALL }), undefined);
  assert.equal(partialDiff('edit_file', undefined), undefined);
  assert.equal(partialDiff('shell', { ...base, after: AFTER_ALL }), undefined);
});

// --- the rewritten call ------------------------------------------------------

test('write_file is rewritten to carry the accepted text', () => {
  const accepted = 'one\nTWO\nthree\nfour\nfive\nsix\nseven\neight\nnine\n';
  const args = { path: 'notes.txt', content: AFTER_ALL };
  assert.deepEqual(rewriteArgs('write_file', args, BEFORE, AFTER_ALL, accepted), {
    path: 'notes.txt',
    content: accepted,
  });
});

test('edit_file is rewritten against the whole before text', () => {
  const accepted = 'one\nTWO\nthree\nfour\nfive\nsix\nseven\neight\nnine\n';
  const args = { path: 'notes.txt', old_string: BEFORE, new_string: AFTER_ALL };
  // `old_string` stays the full pre-image: the tool's single-match rule then
  // finds exactly the one place the review was computed against.
  assert.deepEqual(rewriteArgs('edit_file', args, BEFORE, AFTER_ALL, accepted), {
    path: 'notes.txt',
    old_string: BEFORE,
    new_string: accepted,
  });
});

test('arguments that are not the preview refuse the rewrite', () => {
  const accepted = 'anything';
  assert.equal(rewriteArgs('write_file', { content: 'other' }, BEFORE, AFTER_ALL, accepted), undefined);
  assert.equal(
    rewriteArgs('edit_file', { old_string: 'other', new_string: AFTER_ALL }, BEFORE, AFTER_ALL, accepted),
    undefined,
  );
  assert.equal(rewriteArgs('apply_patch', { patch: 'x' }, BEFORE, AFTER_ALL, accepted), undefined);
  assert.equal(rewriteArgs('write_file', null, BEFORE, AFTER_ALL, accepted), undefined);
});

test('a rewritten call is the one that actually runs', () => {
  // The end-to-end shape, in one place: a selected subset, applied, rewritten,
  // and read back the way the tool would read it.
  const hunks = parseDiff(HUNKS);
  const accepted = applyAccepted(BEFORE, hunks, [0, 2]);
  assert.ok(accepted !== undefined);
  const args = rewriteArgs(
    'edit_file',
    { path: 'notes.txt', old_string: BEFORE, new_string: AFTER_ALL },
    BEFORE,
    AFTER_ALL,
    accepted,
  );
  assert.ok(args);
  assert.equal(
    (BEFORE as string).replace(args.old_string as string, args.new_string as string),
    'one\nTWO\nthree\nfour\nfive\nsix\nseven\nEIGHT\nnine\n',
  );
});
