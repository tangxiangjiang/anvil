/**
 * The fork route's wiring, checked where it cannot be run.
 *
 * The round trip is `test/integration/fork.test.ts`. What is left is agreement
 * between five places with no compiler joining them: the run boundary the panel
 * draws a button at, the order the renderer sends, the panel method that asks
 * the host, the hub that opens the new tab, and the sidebar command that names
 * a run from the stored row. A mismatch in any one is quiet — a button that
 * posts a type nobody routes, or a fork that is made but never shown.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');
const HUB = readFileSync(join(EXTENSION_ROOT, 'src', 'hub.ts'), 'utf8');
const PROTOCOL = readFileSync(join(EXTENSION_ROOT, 'src', 'protocol.ts'), 'utf8');
const EXTENSION = readFileSync(join(EXTENSION_ROOT, 'src', 'extension.ts'), 'utf8');

/** A top-level function of the renderer, from its declaration to the next one. */
function bodyOf(source: string, name: string): string {
  const start = source.indexOf(`\n  function ${name}(`);
  assert.ok(start >= 0, `${name} is in chat.js`);
  const rest = source.slice(start + 1);
  const next = rest.indexOf('\n  function ');
  return next >= 0 ? rest.slice(0, next) : rest;
}

/** A member of a TypeScript class, from its signature to the next member. */
function memberOf(source: string, signature: string): string {
  const start = source.indexOf(signature);
  assert.ok(start >= 0, `${signature} is there`);
  const rest = source.slice(start);
  const next = rest.indexOf('\n  private ', 1);
  const publicNext = rest.indexOf('\n  async ', 1);
  const candidates = [next, publicNext].filter((at) => at > 0);
  if (candidates.length === 0) return rest;
  return rest.slice(0, Math.min(...candidates));
}

test('the fork row draws text only, never markup', () => {
  for (const name of ['showRunActions', 'settleFork']) {
    const body = bodyOf(CHAT_JS, name);
    assert.doesNotMatch(body, /\.innerHTML\s*(\+)?=(?!=)/, `${name} does not set innerHTML`);
    assert.doesNotMatch(body, /insertAdjacentHTML\s*\(/, `${name} does not insert markup`);
    assert.match(body, /el\(/, `${name} builds nodes with textContent`);
  }
});

test('the fork button asks the extension, once', () => {
  const body = bodyOf(CHAT_JS, 'showRunActions');
  assert.match(body, /Fork from here/);
  assert.match(body, /type: 'forkSession'/);
  assert.match(body, /runId: runId/);
  // One click, one copy: the button waits for the answer rather than allowing
  // a second fork from the same boundary while the first is still in flight.
  assert.match(body, /button\.disabled = true/);
  assert.doesNotMatch(body, /fs\.|writeFile|unlink/, 'the renderer does not touch the filesystem');
});

test('the row is drawn at each finished run, live and replayed', () => {
  // A fork copies through a run_end, so the action belongs where the run ended —
  // both on the live `done` and on the transcript's run-end line.
  assert.match(bodyOf(CHAT_JS, 'finishRun'), /showRunActions\(done && done\.run_id\)/);
  assert.match(bodyOf(CHAT_JS, 'addRunEnd'), /showRunActions\(done && done\.run_id\)/);
});

test('the answer settles the row it belongs to', () => {
  const settle = bodyOf(CHAT_JS, 'settleFork');
  assert.match(settle, /forkBlocks\.get\(msg\.runId\)/);
  assert.match(settle, /msg\.error/);
  assert.match(settle, /Fork from here/);
  assert.match(settle, /Forked/);
  assert.match(CHAT_JS, /case 'forked':/);
});

test('the panel forks the agent it is showing, and reports both endings', () => {
  const fork = memberOf(CHAT_PANEL, 'private async forkSession(');
  assert.match(fork, /this\.currentAgentId\(\)/);
  assert.match(fork, /this\.hub\.forkSession\(agentId, runId\)/);
  assert.match(fork, /type: 'forked', runId, agentId: newId/);
  assert.match(fork, /error: \(err as Error\)\.message/);
  assert.match(CHAT_PANEL, /case 'forkSession':/);
});

test('the hub opens the fork in a tab and refreshes the list', () => {
  const fork = memberOf(HUB, 'async forkSession(');
  assert.match(fork, /this\.manager\.forkSession\(agentId, runId\)/);
  // The new conversation is a new row in the sidebar, and the tab is what makes
  // it usable — a fork that was copied but never shown reads as nothing happening.
  assert.match(fork, /this\.sessionsChanged\.fire\(\)/);
  assert.match(fork, /this\.openTab\(newId\)/);
});

test('the sidebar command branches at the row’s last finished run', () => {
  const start = EXTENSION.indexOf("registerCommand('anvil.forkSession'");
  assert.ok(start >= 0, 'the command is registered');
  const body = EXTENSION.slice(start, start + 900);
  assert.match(body, /sessionRowOf\(arg\)\?\.lastRunId/);
  assert.match(body, /hub\.forkSession\(agentId, runId\)/);
  // A session with no finished run says so; it does not guess at a boundary.
  assert.match(body, /no finished run to branch from/);
});

test('the protocol names the method and both homes of the id', () => {
  assert.match(PROTOCOL, /SessionFork: 'session\.fork'/);
  assert.match(PROTOCOL, /interface SessionForkParams/);
  assert.match(PROTOCOL, /interface SessionForkResult/);
});
