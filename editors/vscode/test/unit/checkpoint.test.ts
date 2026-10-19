/**
 * The undo route's wiring, checked where it cannot be run.
 *
 * The round trip is `test/integration/checkpoint.test.ts`. What is left is
 * agreement between four places with no compiler between them: the panel that
 * decides a run has something to undo, the document that draws the button, the
 * confirmation that guards the write, and the answer that settles the card. A
 * mismatch in any of them is silent — a button that posts a type nobody routes,
 * or a restore that runs without asking — so the names are compared here.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');
const PROTOCOL = readFileSync(join(EXTENSION_ROOT, 'src', 'protocol.ts'), 'utf8');

/** A top-level function of the renderer, from its declaration to the next one. */
function bodyOf(source: string, name: string): string {
  const start = source.indexOf(`\n  function ${name}(`);
  assert.ok(start >= 0, `${name} is in chat.js`);
  const rest = source.slice(start + 1);
  const next = rest.indexOf('\n  function ');
  return next >= 0 ? rest.slice(0, next) : rest;
}

/** A private method of the panel, from its declaration to the next one. */
function methodOf(source: string, signature: string): string {
  const start = source.indexOf(signature);
  assert.ok(start >= 0, `${signature} is in chatPanel.ts`);
  const rest = source.slice(start);
  const next = rest.indexOf('\n  private ');
  assert.ok(next > 0, `${signature} is followed by another member`);
  return rest.slice(0, next);
}

test('the undo card draws text only, never markup', () => {
  for (const name of ['showCheckpoint', 'settleCheckpoint']) {
    const body = bodyOf(CHAT_JS, name);
    assert.doesNotMatch(body, /\.innerHTML\s*(\+)?=(?!=)/, `${name} does not set innerHTML`);
    assert.doesNotMatch(body, /insertAdjacentHTML\s*\(/, `${name} does not insert markup`);
    assert.match(body, /el\(/, `${name} builds nodes with textContent`);
  }
});

test('the undo button asks the extension rather than writing anything itself', () => {
  const body = bodyOf(CHAT_JS, 'showCheckpoint');
  assert.match(body, /type: 'restoreCheckpoint'/);
  assert.match(body, /runId: runId/);
  // One click, one request: the button disables itself while it waits, so a
  // second click cannot be a second restore.
  assert.match(body, /button\.disabled = true/);
  assert.doesNotMatch(body, /fs\.|writeFile|unlink/, 'the renderer does not touch the filesystem');
});

test('the answer settles the card it belongs to', () => {
  const settle = bodyOf(CHAT_JS, 'settleCheckpoint');
  assert.match(settle, /checkpointBlocks\.get\(msg\.runId\)/);
  assert.match(settle, /msg\.cancelled === true/);
  assert.match(settle, /msg\.error/);
  assert.match(settle, /Restored/);
  assert.match(CHAT_JS, /case 'checkpoint':/);
  assert.match(CHAT_JS, /case 'checkpointRestored':/);
});

test('the panel offers only a run that captured something, and only for this agent', () => {
  const report = methodOf(CHAT_PANEL, 'private async reportCheckpoint(');
  assert.match(report, /this\.hub\.listCheckpoints\(agentId\)/);
  assert.match(report, /candidate\.run_id === runId/);
  assert.match(report, /run\.files\.length === 0/);
  assert.match(report, /type: 'checkpoint'/);
  // The run's own id travels, so the host is asked about the run that finished.
  assert.match(report, /files: run\.files\.map\(\(file\) => file\.path\)/);
  assert.match(CHAT_PANEL, /void this\.reportCheckpoint\(done\.run_id\)/);
});

test('the restore is confirmed before anything on disk changes', () => {
  const restore = methodOf(CHAT_PANEL, 'private async restoreCheckpoint(');
  const warn = restore.indexOf('showWarningMessage');
  const write = restore.indexOf('this.hub.restoreCheckpoint');
  assert.ok(warn >= 0, 'a modal warning is shown');
  assert.ok(write > warn, 'the warning comes before the write');
  assert.match(restore, /modal: true/);
  assert.match(restore, /cancelled: true/);
  assert.match(restore, /error: \(err as Error\)\.message/);
  // The changed-files card described a workspace that is no longer there.
  assert.match(restore, /this\.changes\.begin\(\)/);
  assert.match(restore, /this\.forgetChanges\(\)/);
});

test('the protocol names the methods the panel sends', () => {
  assert.match(PROTOCOL, /CheckpointList: 'checkpoint\.list'/);
  assert.match(PROTOCOL, /CheckpointRestore: 'checkpoint\.restore'/);
  assert.match(PROTOCOL, /interface CheckpointRestoreResult/);
});
