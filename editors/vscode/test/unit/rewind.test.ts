/**
 * "Rewind to here", checked where it cannot be run.
 *
 * The plan is pure, so the three actions and the first-message case are stated
 * as data. What is left is agreement between four places with no compiler
 * joining them: the user message that owns the control, the panel method that
 * carries the action out, the order its two host calls are made in, and the
 * answer that settles the menu. A mismatch in any one is quiet — a menu that
 * posts a type nobody routes, or a restore that runs before the fork.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import { rewindPlan } from '../../src/rewind';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_CSS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.css'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');

/** A top-level function of the renderer, from its declaration to the next one. */
function bodyOf(source: string, name: string): string {
  const start = source.indexOf(`\n  function ${name}(`);
  assert.ok(start >= 0, `${name} is in chat.js`);
  const rest = source.slice(start + 1);
  const next = rest.indexOf('\n  function ');
  return next >= 0 ? rest.slice(0, next) : rest;
}

/** A private method of the panel, from its declaration to the next one. */
function memberOf(source: string, signature: string): string {
  const start = source.indexOf(signature);
  assert.ok(start >= 0, `${signature} is in chatPanel.ts`);
  const rest = source.slice(start);
  const next = rest.indexOf('\n  private ', 1);
  const publicNext = rest.indexOf('\n  async ', 1);
  const candidates = [next, publicNext].filter((at) => at > 0);
  if (candidates.length === 0) return rest;
  return rest.slice(0, Math.min(...candidates));
}

test('fork and restore are separate plans, and both is the pair', () => {
  assert.deepEqual(rewindPlan('fork', 'r1'), { fork: 'r1', startOver: false, restore: false });
  assert.deepEqual(rewindPlan('restore', 'r1'), { fork: null, startOver: false, restore: true });
  assert.deepEqual(rewindPlan('both', 'r1'), { fork: 'r1', startOver: false, restore: true });
});

test('the first message has no run to branch at, so its branch is an empty conversation', () => {
  assert.deepEqual(rewindPlan('fork'), { fork: null, startOver: true, restore: false });
  assert.deepEqual(rewindPlan('both', ''), { fork: null, startOver: true, restore: true });
  // A restore-only rewind never needs a previous run.
  assert.deepEqual(rewindPlan('restore'), { fork: null, startOver: false, restore: true });
});

test('an action that names no half asks for nothing', () => {
  assert.deepEqual(rewindPlan('nonsense' as never, 'r1'), { fork: null, startOver: false, restore: false });
});

test('the control belongs to the user message, and is given the run that answered it', () => {
  const add = bodyOf(CHAT_JS, 'addUser');
  assert.match(add, /rewindTurns\.push/);
  // A spawned child's prompt is not a turn of this conversation.
  assert.match(add, /active === mainScope/);
  for (const name of ['finishRun', 'addRunEnd']) {
    assert.match(bodyOf(CHAT_JS, name), /noteRun\(done && done\.run_id\)/, `${name} hands the run to its message`);
  }
  const note = bodyOf(CHAT_JS, 'noteRun');
  assert.match(note, /rewindTurns\[rewindTurns\.length - 1\]/);
  assert.match(note, /turn\.runId !== ''/);
});

test('the menu offers the three actions and reads availability from the extension', () => {
  const render = bodyOf(CHAT_JS, 'renderRewind');
  assert.match(render, /Fork from before this turn/);
  assert.match(render, /Start a new conversation/);
  assert.match(render, /Restore this turn’s files/);
  assert.match(render, /Fork and restore/);
  // No checkpoint, no file action: the empty list and the unanswered case are
  // different, and one waits while the other says there is nothing to restore.
  assert.match(render, /rewindStates\.get\(turn\.runId\)/);
  assert.match(render, /rewindStates\.has\(turn\.runId\)/);
  assert.match(render, /This turn changed no files/);
  assert.match(render, /Checking this turn’s files/);
  assert.match(bodyOf(CHAT_JS, 'rewindRow'), /type: 'rewind'/);
  assert.match(bodyOf(CHAT_JS, 'rewindRow'), /prevRunId: prevRunId/);
  // "This turn" is the granularity the labels and the hint have to keep saying.
  assert.match(bodyOf(CHAT_JS, 'drawRewind'), /A checkpoint is taken before the turn, not before each tool call/);
  assert.match(CHAT_CSS, /\.rewind-menu/);
});

test('the menu draws text only, never markup', () => {
  for (const name of ['drawRewind', 'renderRewind', 'rewindRow', 'showRewindState', 'settleRewind']) {
    const body = bodyOf(CHAT_JS, name);
    assert.doesNotMatch(body, /\.innerHTML\s*(\+)?=(?!=)/, `${name} does not set innerHTML`);
    assert.doesNotMatch(body, /insertAdjacentHTML\s*\(/, `${name} does not insert markup`);
  }
  assert.doesNotMatch(bodyOf(CHAT_JS, 'settleRewind'), /fs\.|writeFile|unlink/, 'the renderer does not write files');
});

test('the panel forks before it restores, and reports each half', () => {
  const rewind = memberOf(CHAT_PANEL, 'private async rewind(');
  const fork = rewind.indexOf('this.hub.forkSession');
  const restore = rewind.indexOf('this.hub.restoreCheckpoint');
  assert.ok(fork >= 0, 'a fork is asked for');
  assert.ok(restore > fork, 'the fork is asked for before the restore');
  assert.match(rewind, /rewindPlan\(action, prevRunId\)/);
  // One confirmation covers the write half, and it comes before anything runs.
  const warn = rewind.indexOf('showWarningMessage');
  assert.ok(warn >= 0 && warn < fork, 'the confirmation comes before the host calls');
  assert.match(rewind, /cancelled: true/);
  assert.match(rewind, /result\.forkError/);
  assert.match(rewind, /result\.restoreError/);
  // The restore is asked of the conversation that owns the checkpoint, not of
  // the fork: a fork copies records, not checkpoint directories.
  assert.match(rewind, /restoreCheckpoint\(agentId, runId\)/);
  assert.match(rewind, /this\.clearConversation\(\)/);
  assert.match(CHAT_PANEL, /case 'rewind':/);
});

test('the extension tells the document which runs can restore', () => {
  assert.match(CHAT_PANEL, /type: 'rewindState', runId, files/);
  assert.match(CHAT_PANEL, /checkpoints\.set\(run\.run_id/);
  assert.match(CHAT_JS, /case 'rewindState':/);
  assert.match(CHAT_JS, /case 'rewound':/);
  assert.match(CHAT_JS, /case 'rewindOpen':/);
  assert.match(CHAT_PANEL, /type: 'rewindOpen'/);
});

test('a rewind result names what happened and what did not', () => {
  const settle = bodyOf(CHAT_JS, 'settleRewind');
  assert.match(settle, /msg\.cancelled === true/);
  assert.match(settle, /msg\.forkError/);
  assert.match(settle, /msg\.restoreError/);
  assert.match(settle, /msg\.forked/);
  assert.match(settle, /msg\.restored/);
  // The menu comes back so a failed half can be tried again.
  assert.match(settle, /renderRewind\(turn\)/);
});
