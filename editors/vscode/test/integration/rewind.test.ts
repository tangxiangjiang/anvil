/**
 * Rewinding a turn, over a real child process.
 *
 * The panel composes two host methods that know nothing about each other: a
 * fork copies stored records, a restore writes files. This test asks the host
 * for the same pair in the same order the panel's handler uses, so the wire
 * methods, their params, and the fact that the restore still finds the
 * checkpoint after a fork are pinned rather than assumed.
 */

import { strict as assert } from 'node:assert';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { TestContext, test } from 'node:test';
import { Method } from '../../src/protocol';
import { startFakeHost, waitFor } from '../helpers/harness';

async function scratch(t: TestContext, env: NodeJS.ProcessEnv = {}) {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-rewind-'));
  const echo = path.join(dir, 'echo.jsonl');
  const file = path.join(dir, 'target.txt');
  writeFileSync(file, 'after the turn\n');
  const harness = await startFakeHost('chat', {
    cwd: dir,
    env: { FAKE_HOST_CHECKPOINT: 'target.txt', FAKE_HOST_ECHO: echo, ...env },
  });
  t.after(async () => {
    await harness.stop();
    rmSync(dir, { recursive: true, force: true, maxRetries: 5 });
  });
  return { dir, echo, file, harness };
}

function methods(echo: string): string[] {
  return readFileSync(echo, 'utf8')
    .split('\n')
    .filter((line) => line !== '')
    .map((line) => JSON.parse(line) as { method: string })
    .map((record) => record.method);
}

test('a rewind forks at the previous run, then puts this run’s files back', async (t) => {
  const { echo, file, harness } = await scratch(t);

  await harness.session.send('first turn');
  await waitFor(() => harness.dones.length === 1, 'the first run to finish');
  await harness.session.send('second turn');
  await waitFor(() => harness.dones.length === 2, 'the second run to finish');

  const agentId = harness.sessions[0]?.agent_id;
  const [first, second] = harness.dones.map((entry) => entry.done.run_id);
  assert.ok(agentId && first && second, 'both runs have ids');

  // The panel's order, on purpose: the fork returns a new conversation, and the
  // restore is asked of the one that owns the checkpoint. Both open on the same
  // workspace root, which is why the file still goes back.
  const forked = await harness.client.request<{ agent_id: string }>(Method.SessionFork, {
    agent_id: agentId,
    run_id: first,
  });
  assert.ok(forked.agent_id && forked.agent_id !== agentId, 'the branch is a new conversation');

  const restored = await harness.client.request<{
    run_id: string;
    files: Array<{ path: string; deleted: boolean }>;
  }>(Method.CheckpointRestore, { agent_id: agentId, run_id: second });
  assert.deepEqual(restored.files, [{ path: 'target.txt', deleted: false }]);
  assert.equal(readFileSync(file, 'utf8'), 'restored by the fake host\n');

  const order = methods(echo).filter(
    (method) => method === Method.SessionFork || method === Method.CheckpointRestore,
  );
  assert.deepEqual(order, [Method.SessionFork, Method.CheckpointRestore]);
});

test('a run that captured nothing offers no restore', async (t) => {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-rewind-none-'));
  const harness = await startFakeHost('chat', { cwd: dir });
  t.after(async () => {
    await harness.stop();
    rmSync(dir, { recursive: true, force: true, maxRetries: 5 });
  });

  await harness.session.send('say something');
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  const agentId = harness.sessions[0]?.agent_id;
  assert.ok(agentId);
  const listed = await harness.client.request<{ runs: unknown[] }>(Method.CheckpointList, {
    agent_id: agentId,
  });
  // The menu hides the file action on this answer, and the combined action with
  // it: there is nothing to put back.
  assert.deepEqual(listed.runs, []);
});
