/**
 * Approving a plan, over a real child process.
 *
 * The claim in the design is an order: leaving plan mode comes before the
 * sentence that asks for the plan to be carried out, so the write tools exist
 * by the time anything wants them. That order is two wire calls, and neither
 * the panel's source nor a renderer test can show that the host accepts them
 * in it. This asks a real child the same pair in the same order and reads back
 * what it recorded.
 *
 * The panel's own glue needs a window; what is exercised is the pair of facts
 * the glue is made of — a session re-opened under another mode for the same
 * agent, and one prompt carrying the fixed sentence.
 */

import { strict as assert } from 'node:assert';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { TestContext, test } from 'node:test';
import { PLAN_APPROVED_PROMPT } from '../../src/plan';
import { startFakeHost, waitFor } from '../helpers/harness';

interface Recorded {
  method: string;
  params: Record<string, unknown>;
}

function readEcho(file: string): Recorded[] {
  return readFileSync(file, 'utf8')
    .split('\n')
    .filter((line) => line.trim() !== '')
    .map((line) => JSON.parse(line) as Recorded);
}

async function planHost(t: TestContext) {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-plan-'));
  const echo = path.join(dir, 'echo.jsonl');
  const harness = await startFakeHost('chat', {
    cwd: dir,
    env: { FAKE_HOST_ECHO: echo },
    config: { permission_mode: 'plan' },
  });
  t.after(async () => {
    await harness.stop();
    rmSync(dir, { recursive: true, force: true, maxRetries: 5 });
  });
  return { echo, harness };
}

test('approval reopens in the earlier mode before the fixed instruction', async (t) => {
  const { echo, harness } = await planHost(t);

  await harness.session.send('plan the change');
  await waitFor(() => harness.dones.length === 1, 'the plan turn to finish');
  const agentId = harness.sessions[0]?.agent_id;
  assert.ok(agentId, 'the plan turn produced a conversation');
  assert.equal(harness.sessions[0]?.permission_mode, 'plan');

  // The panel's two steps, in the panel's order. `reopen` closes the session
  // and opens it again for the same agent under the mode in force before plan
  // mode; only then is the sentence sent.
  const reopened = await harness.session.reopen({ permission_mode: 'accept_edits' });
  assert.equal(reopened?.permission_mode, 'accept_edits');
  assert.equal(reopened?.agent_id, agentId, 'the same conversation, under another mode');
  await harness.session.send(PLAN_APPROVED_PROMPT);
  await waitFor(() => harness.dones.length === 2, 'the carrying-out turn to finish');

  const records = readEcho(echo).filter(
    (record) => record.method === 'session.new' || record.method === 'session.resume' || record.method === 'prompt',
  );
  const reopenedAt = records.findIndex(
    (record, index) => index > 0 && record.method === 'session.resume' && record.params.permission_mode === 'accept_edits',
  );
  assert.ok(reopenedAt > 0, 'the mode change was asked of the host');

  const prompts = records
    .map((record, index) => ({ record, index }))
    .filter((entry) => entry.record.method === 'prompt');
  assert.equal(prompts.length, 2, 'one instruction after one question');
  assert.equal(prompts[0].record.params.message, 'plan the change');
  assert.equal(prompts[1].record.params.message, PLAN_APPROVED_PROMPT);
  // Immediately before the instruction, not merely somewhere before it: a turn
  // started between the two would be a turn under plan mode.
  assert.equal(reopenedAt, prompts[1].index - 1, 'the mode is in force before the turn starts');

  // The conversation the plan belongs to is the one that carries it out.
  const opened = records[reopenedAt];
  assert.equal(opened.params.agent_id, agentId);
});
