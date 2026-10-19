/**
 * Prompt recall: what Up and Down walk, and what they give back.
 *
 * The cursor arithmetic lives in `media/history.js`, a plain browser script
 * that assigns a global — `media/` is linted as browser code and never built,
 * so the same absolute-path load `usageMeter.test.ts` uses is how a Node test
 * reaches it. The half worth testing is the one a screenshot cannot show: the
 * draft survives a walk, the walk stops at both ends, and an edit ends it.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const HISTORY_PATH = join(EXTENSION_ROOT, 'media', 'history.js');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');

// A script loaded for its side effect, not a module imported for its exports.
// eslint-disable-next-line @typescript-eslint/no-require-imports
require(HISTORY_PATH);

interface HistoryState {
  entries: string[];
  cursor: number | null;
  draft: string;
}

const history = (
  globalThis as unknown as {
    anvilHistory: {
      LIMIT: number;
      create: () => HistoryState;
      clear: (state: HistoryState) => HistoryState;
      record: (state: HistoryState, text: string) => HistoryState;
      walk: (state: HistoryState, delta: number, current: string) => string | null;
      edited: (state: HistoryState) => HistoryState;
    };
  }
).anvilHistory;

/** A list of prompts, newest last, with the cursor on the live line. */
function sent(...prompts: string[]): HistoryState {
  const state = history.create();
  for (const prompt of prompts) history.record(state, prompt);
  return state;
}

test('Up walks from the newest prompt to the oldest and stops there', () => {
  const state = sent('one', 'two', 'three');
  assert.equal(history.walk(state, -1, ''), 'three');
  assert.equal(history.walk(state, -1, ''), 'two');
  assert.equal(history.walk(state, -1, ''), 'one');
  // Older than the oldest stays put rather than wrapping.
  assert.equal(history.walk(state, -1, ''), 'one');
  assert.equal(state.cursor, 0);
});

test('Down returns to the draft that was in the box', () => {
  const state = sent('one', 'two');
  assert.equal(history.walk(state, -1, 'half-typed'), 'two');
  assert.equal(history.walk(state, 1, ''), 'half-typed');
  assert.equal(state.cursor, null);
  assert.equal(state.draft, '');
});

test('Down on the live line is not the history’s key', () => {
  const state = sent('one');
  assert.equal(history.walk(state, 1, 'typing'), null);
  assert.equal(state.cursor, null);
});

test('Up with no prompts is not the history’s key', () => {
  const state = history.create();
  assert.equal(history.walk(state, -1, 'typing'), null);
});

test('a blank prompt and a repeat of the one before it are not entries', () => {
  const state = sent('one', '   ', 'one', 'two', 'two');
  assert.deepEqual(state.entries, ['one', 'two']);
});

test('the list is bounded, oldest first out', () => {
  const state = sent(...Array.from({ length: history.LIMIT + 5 }, (_, index) => `p${index}`));
  assert.equal(state.entries.length, history.LIMIT);
  assert.equal(state.entries[0], 'p5');
  assert.equal(state.entries[state.entries.length - 1], `p${history.LIMIT + 4}`);
});

test('sending puts the cursor back on the live line', () => {
  const state = sent('one', 'two');
  history.walk(state, -1, 'draft');
  history.record(state, 'three');
  assert.equal(state.cursor, null);
  assert.equal(state.draft, '');
  // The next Up starts from the newest prompt, not from where the walk stopped.
  assert.equal(history.walk(state, -1, ''), 'three');
});

test('editing a recalled prompt ends the walk', () => {
  const state = sent('one', 'two');
  history.walk(state, -1, '');
  history.edited(state);
  assert.equal(state.cursor, null);
  // The edit in the box is not the walk's to keep; the next Up is fresh.
  assert.equal(history.walk(state, -1, 'my edit'), 'two');
  assert.equal(history.walk(state, 1, ''), 'my edit');
});

test('clearing the history forgets the prompts', () => {
  const state = sent('one');
  history.clear(state);
  assert.deepEqual(state.entries, []);
  assert.equal(history.walk(state, -1, 'typing'), null);
});

test('the script is loaded before chat.js', () => {
  const before = CHAT_PANEL.indexOf("asset('history.js')");
  const after = CHAT_PANEL.indexOf("asset('chat.js')");
  assert.ok(before > 0, 'chatPanel.ts must load history.js');
  assert.ok(before < after, 'history.js must load before chat.js, which reads its global');
});

test('the composer records a send, recalls at the edges, and resets on edit', () => {
  assert.match(CHAT_JS, /historyMath\.record\(history, text\)/);
  assert.match(CHAT_JS, /historyMath\.walk\(history, delta, input\.value\)/);
  assert.match(CHAT_JS, /historyMath\.edited\(history\)/);
  // Up only from the first character, Down only from the end: the arrows still
  // move the caret through a multi-line draft.
  assert.match(CHAT_JS, /input\.selectionStart === 0 && input\.selectionEnd === 0/);
  assert.match(CHAT_JS, /input\.selectionStart === input\.value\.length/);
  // A replayed conversation repopulates the list, so a reloaded tab can recall
  // the prompts its transcript shows.
  assert.match(CHAT_JS, /historyMath\.record\(history, msg\.text \|\| ''\)/);
  // Clearing the transcript clears the list with it.
  assert.match(CHAT_JS, /historyMath\.clear\(history\)/);
});
