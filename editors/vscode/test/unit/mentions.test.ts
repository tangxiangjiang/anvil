/**
 * Mention paths, without a window.
 *
 * These are the string differences that break on one platform and not the
 * other: separators, case, and whether a path that climbed out of the folder is
 * recognised as having done so.
 */

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { dedupeMentions, mentionLabel, relativeMention, toolCallPaths } from '../../src/mentions';

const win = process.platform === 'win32';
const SEP = win ? '\\' : '/';

test('a path inside the folder becomes a forward-slashed relative path', () => {
  const root = `${win ? 'C:' : ''}${SEP}work${SEP}repo`;
  const file = `${win ? 'C:' : ''}${SEP}work${SEP}repo${SEP}src${SEP}main.go`;
  assert.equal(relativeMention(root, file), 'src/main.go');
});

test('the folder itself is not a mention', () => {
  const root = `${win ? 'C:' : ''}${SEP}work${SEP}repo`;
  assert.equal(relativeMention(root, root), undefined);
  // A trailing separator is the same folder, not a file named "".
  assert.equal(relativeMention(`${root}${SEP}`, root), undefined);
});

test('a sibling that merely shares a prefix is not inside the folder', () => {
  const root = `${win ? 'C:' : ''}${SEP}work${SEP}repo`;
  const sibling = `${win ? 'C:' : ''}${SEP}work${SEP}repository${SEP}main.go`;
  assert.equal(relativeMention(root, sibling), undefined);
});

test('a path outside the folder is not a mention', () => {
  const root = `${win ? 'C:' : ''}${SEP}work${SEP}repo`;
  assert.equal(relativeMention(root, `${win ? 'C:' : ''}${SEP}elsewhere${SEP}main.go`), undefined);
});

test('case is folded on Windows and kept elsewhere', () => {
  const root = `${win ? 'C:' : ''}${SEP}Work${SEP}Repo`;
  const file = `${win ? 'C:' : ''}${SEP}work${SEP}repo${SEP}main.go`;
  const result = relativeMention(root, file);
  if (win) assert.equal(result, 'main.go');
  else assert.equal(result, undefined);
});

test('a mention never climbs', () => {
  const root = `${win ? 'C:' : ''}${SEP}work${SEP}repo`;
  assert.equal(relativeMention(root, `${root}${SEP}..${SEP}..${SEP}etc${SEP}passwd`), undefined);
  assert.equal(relativeMention('', 'anything'), undefined);
});

test('a label is the last segment', () => {
  assert.equal(mentionLabel('src/internal/tools/preview.go'), 'preview.go');
  assert.equal(mentionLabel('README.md'), 'README.md');
});

test('duplicates are dropped and the pick order is kept', () => {
  assert.deepEqual(dedupeMentions(['b.go', 'a.go', 'b.go', '', 'a.go']), ['b.go', 'a.go']);
});

test('a single-file write names its target', () => {
  for (const tool of ['write_file', 'edit_file']) {
    assert.deepEqual(toolCallPaths(tool, { path: 'src\\main.go', content: 'x' }), ['src/main.go'], tool);
  }
});

test('a call with no path, or no arguments, names nothing', () => {
  assert.deepEqual(toolCallPaths('write_file', {}), []);
  assert.deepEqual(toolCallPaths('write_file', undefined), []);
  assert.deepEqual(toolCallPaths(undefined, { path: 'a.go' }), []);
  assert.deepEqual(toolCallPaths('read_file', { path: 'a.go' }), []);
});

test('a patch names every file it touches, header by header', () => {
  const patch = [
    '*** Begin Patch',
    '*** Update File: internal/tools/patch.go',
    '@@',
    '-old',
    '+new',
    '*** Add File: docs/new.md',
    '+hello',
    '*** Delete File: gone.txt',
    '*** End Patch',
  ].join('\n');
  assert.deepEqual(toolCallPaths('apply_patch', { patch }), [
    'internal/tools/patch.go',
    'docs/new.md',
    'gone.txt',
  ]);
});

test('a patch with no file headers names nothing', () => {
  assert.deepEqual(toolCallPaths('apply_patch', { patch: '*** Begin Patch\n*** End Patch' }), []);
  assert.deepEqual(toolCallPaths('apply_patch', { patch: 42 }), []);
});

test('a header-shaped line inside a hunk is not mistaken for a header', () => {
  // Hunk bodies are prefixed by a space, a `+`, or a `-`, and the match is
  // anchored at the start of the line, exactly as the host's parser is.
  const patch = [
    '*** Begin Patch',
    '*** Update File: a.txt',
    '@@',
    ' *** Update File: b.txt',
    '+ *** Add File: c.txt',
    '*** End Patch',
  ].join('\n');
  assert.deepEqual(toolCallPaths('apply_patch', { patch }), ['a.txt']);
});
