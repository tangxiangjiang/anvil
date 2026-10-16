/**
 * The change a call carried, and how it should be opened.
 *
 * `src/editDiff.ts` is the one implementation of "what did that edit do" that a
 * live card and a replayed one share, which is why it is a pure function of
 * `(name, args)` and why it is tested here rather than through a window. What
 * is pinned is mostly what it refuses: a call that changes no file, arguments
 * that are not what the tool declares, and — for the plan — the difference
 * between a real diff against the pre-image and the fallback that shows only
 * what the arguments carried.
 */

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { PreImage, planReview, reviewOf } from '../../src/editDiff';

test('an edit_file carries both sides of the replacement', () => {
  const review = reviewOf('edit_file', {
    path: 'src/main.go',
    old_string: 'alpha',
    new_string: 'beta',
  });
  assert.deepEqual(review, { kind: 'edit', path: 'src/main.go', before: 'alpha', after: 'beta' });
});

test('a write_file carries the body it wrote', () => {
  const review = reviewOf('write_file', { path: 'notes.md', content: '# notes\n' });
  assert.deepEqual(review, { kind: 'write', path: 'notes.md', body: '# notes\n' });
});

test('an apply_patch carries its text and the files its headers name', () => {
  const patch = [
    '*** Begin Patch',
    '*** Update File: src/a.go',
    '@@',
    '-old',
    '+new',
    '*** Add File: src/b.go',
    '+package b',
    '*** End Patch',
  ].join('\n');
  const review = reviewOf('apply_patch', { patch });
  assert.equal(review?.kind, 'patch');
  assert.deepEqual(review?.kind === 'patch' ? review.paths : [], ['src/a.go', 'src/b.go']);
  assert.equal(review?.kind === 'patch' ? review.patch : '', patch);
});

test('the path is normalised the way the change tracker normalises it', () => {
  // The review and the tracker have to agree about which file a call touched,
  // or a card's pre-image lookup would miss the file the write really changed.
  // Both take the answer from `toolCallPaths`, and this is the property that
  // makes sharing it worth doing.
  const review = reviewOf('write_file', { path: 'src\\nested\\a.ts', content: 'x' });
  assert.equal(review?.kind === 'write' ? review.path : '', 'src/nested/a.ts');
});

test('a call that changes no file has nothing to review', () => {
  assert.equal(reviewOf('shell', { command: 'rm -rf build' }), undefined);
  assert.equal(reviewOf('read_file', { path: 'src/main.go' }), undefined);
  assert.equal(reviewOf('run_tests', {}), undefined);
});

test('arguments that are not what the tool declares are refused, not thrown', () => {
  // Every one of these is model output, so the only wrong answer is an
  // exception: a card that cannot offer a review is still a card.
  assert.equal(reviewOf('edit_file', { path: 'a.go', old_string: 42, new_string: 'x' }), undefined);
  assert.equal(reviewOf('edit_file', { old_string: 'a', new_string: 'b' }), undefined);
  assert.equal(reviewOf('write_file', { path: 'a.go' }), undefined);
  assert.equal(reviewOf('write_file', { path: '', content: 'x' }), undefined);
  assert.equal(reviewOf('apply_patch', {}), undefined);
  assert.equal(reviewOf('apply_patch', { patch: '' }), undefined);
  assert.equal(reviewOf('apply_patch', { patch: 7 }), undefined);
  assert.equal(reviewOf(undefined, { path: 'a.go', content: 'x' }), undefined);
  assert.equal(reviewOf('write_file', undefined), undefined);
  assert.equal(reviewOf('write_file', null), undefined);
  assert.equal(reviewOf('write_file', 'path=a.go'), undefined);

  const cyclic: Record<string, unknown> = { content: 'x' };
  cyclic.self = cyclic;
  assert.equal(reviewOf('write_file', cyclic), undefined);
});

test('a patch whose headers cannot be read is still the patch text', () => {
  // The call will fail at the tool, and a failed call offers no review — but
  // the descriptor is a reading of the arguments, not a guess about the
  // outcome, so it reports the text it has and no files.
  const review = reviewOf('apply_patch', { patch: 'not a patch at all' });
  assert.deepEqual(review, { kind: 'patch', paths: [], patch: 'not a patch at all' });
});

test('the pre-image turns a review into the real diff against disk', () => {
  const review = reviewOf('edit_file', { path: 'a.go', old_string: 'x', new_string: 'y' });
  assert.ok(review);
  const pre: PreImage = { before: 'the file as it was\n' };
  assert.deepEqual(planReview(review, pre), {
    kind: 'disk',
    path: 'a.go',
    before: 'the file as it was\n',
    deleted: false,
  });
});

test('a deleted pre-image says so, because there is no disk side left', () => {
  const review = reviewOf('write_file', { path: 'gone.go', content: 'x' });
  assert.ok(review);
  const plan = planReview(review, { before: 'was here\n', deleted: true });
  assert.deepEqual(plan, { kind: 'disk', path: 'gone.go', before: 'was here\n', deleted: true });
});

test('without a pre-image an edit shows the replacement the call carried', () => {
  const review = reviewOf('edit_file', { path: 'a.go', old_string: 'alpha', new_string: 'beta' });
  assert.ok(review);
  assert.deepEqual(planReview(review), {
    kind: 'memory',
    label: 'a.go',
    before: 'alpha',
    after: 'beta',
    title: 'a.go (what this call replaced)',
  });
});

test('without a pre-image a write shows the body, against an empty side', () => {
  // The empty left side is not a claim that the file was empty: the title says
  // what the window is, and the alternative — reading the file now and calling
  // it the "after" — would show the change as nothing at all.
  const review = reviewOf('write_file', { path: 'notes.md', content: '# notes\n' });
  assert.ok(review);
  assert.deepEqual(planReview(review), {
    kind: 'memory',
    label: 'notes.md',
    before: '',
    after: '# notes\n',
    title: 'notes.md (the body this call wrote)',
  });
});

test('a pre-image outranks the arguments for a write too', () => {
  const review = reviewOf('write_file', { path: 'notes.md', content: 'new\n' });
  assert.ok(review);
  const plan = planReview(review, { before: 'old\n' });
  assert.equal(plan.kind, 'disk');
});

test('a patch opens as its own text even when a pre-image exists', () => {
  // It spans files, so there is no one file whose "before" it is.
  const review = reviewOf('apply_patch', { patch: '*** Update File: a.go\n@@\n-x\n+y' });
  assert.ok(review);
  const plan = planReview(review, { before: 'ignored\n' });
  assert.deepEqual(plan, {
    kind: 'text',
    label: 'a.go.diff',
    body: '*** Update File: a.go\n@@\n-x\n+y',
  });
});

test('a patch over several files is named for the patch, not for one of them', () => {
  const patch = '*** Update File: a.go\n*** Update File: b.go\n';
  const review = reviewOf('apply_patch', { patch });
  assert.ok(review);
  const plan = planReview(review);
  assert.equal(plan.kind === 'text' ? plan.label : '', 'patch.diff');
  // A patch whose headers were not readable has no file to be named after.
  const unreadable = reviewOf('apply_patch', { patch: 'nonsense' });
  assert.ok(unreadable);
  const fallback = planReview(unreadable);
  assert.equal(fallback.kind === 'text' ? fallback.label : '', 'patch.diff');
});

test('the same call always produces the same plan', () => {
  // This is the property p_0.38.3 rests on: a card rebuilt from a stored record
  // renders and reviews exactly like the live one, because the derivation reads
  // the arguments and nothing else — no clock, no counter, no window.
  const args = { path: 'a.go', old_string: 'alpha', new_string: 'beta' };
  const first = reviewOf('edit_file', args);
  const second = reviewOf('edit_file', { ...args });
  assert.deepEqual(first, second);
  assert.deepEqual(first && planReview(first), second && planReview(second));
});
