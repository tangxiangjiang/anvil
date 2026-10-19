/**
 * The undo route, over a real child process.
 *
 * What the panel does with a checkpoint is one thing; that the requests it sends
 * are the ones the host answers is another. This test asks the host directly
 * through the same client the extension uses, so a renamed method or a param the
 * host reads under a different key fails here rather than as an undo that
 * silently does nothing.
 */

import { strict as assert } from 'node:assert';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { TestContext, test } from 'node:test';
import { Method } from '../../src/protocol';
import { startFakeHost, waitFor } from '../helpers/harness';

async function scratch(t: TestContext) {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-checkpoint-'));
  const echo = path.join(dir, 'echo.jsonl');
  const file = path.join(dir, 'target.txt');
  writeFileSync(file, 'before the run\n');
  const harness = await startFakeHost('chat', {
    cwd: dir,
    env: { FAKE_HOST_CHECKPOINT: 'target.txt', FAKE_HOST_ECHO: echo },
  });
  t.after(async () => {
    await harness.stop();
    rmSync(dir, { recursive: true, force: true, maxRetries: 5 });
  });
  return { dir, echo, file, harness };
}

test('a finished run is listed and can be put back', async (t) => {
  const { echo, file, harness } = await scratch(t);
  await harness.session.send('change the file');
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  const agentId = harness.sessions[0]?.agent_id;
  const runId = harness.dones[0]?.done.run_id;
  assert.ok(agentId, 'the session has an agent id');
  assert.ok(runId, 'the finished run has an id');

  const listed = await harness.client.request<{
    runs: Array<{ run_id: string; files: Array<{ path: string; existed: boolean }> }>;
  }>(Method.CheckpointList, { agent_id: agentId });
  assert.equal(listed.runs.length, 1, 'the run is listed');
  assert.equal(listed.runs[0].run_id, runId);
  assert.deepEqual(
    listed.runs[0].files.map((entry) => entry.path),
    ['target.txt'],
  );

  const restored = await harness.client.request<{
    run_id: string;
    files: Array<{ path: string; deleted: boolean }>;
  }>(Method.CheckpointRestore, { agent_id: agentId, run_id: runId });
  assert.equal(restored.run_id, runId);
  assert.deepEqual(restored.files, [{ path: 'target.txt', deleted: false }]);
  assert.equal(readFileSync(file, 'utf8'), 'restored by the fake host\n');

  // The host saw the run id the panel would send, not one this test invented.
  const requests = readFileSync(echo, 'utf8')
    .split('\n')
    .filter((line) => line !== '')
    .map((line) => JSON.parse(line) as { method: string; params?: Record<string, unknown> });
  const restore = requests.filter((entry) => entry.method === Method.CheckpointRestore).pop();
  assert.ok(restore, 'the host recorded the restore');
  assert.deepEqual(restore.params, { agent_id: agentId, run_id: runId });
});

test('a session with no checkpoint lists nothing', async (t) => {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-checkpoint-none-'));
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
  assert.deepEqual(listed.runs, []);
});
