/**
 * Forking, over a real child process.
 *
 * The panel's button and the sidebar command are different entry points to the
 * same request; this test asks the host through the same client both use, so a
 * renamed method or a param the host reads under a different key fails here
 * rather than as a fork that quietly copies nothing.
 */

import { strict as assert } from 'node:assert';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { TestContext, test } from 'node:test';
import { Method } from '../../src/protocol';
import { startFakeHost, waitFor } from '../helpers/harness';

async function scratch(t: TestContext, env: NodeJS.ProcessEnv = {}) {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-fork-'));
  const echo = path.join(dir, 'echo.jsonl');
  const harness = await startFakeHost('chat', {
    cwd: dir,
    env: { FAKE_HOST_ECHO: echo, FAKE_HOST_TITLE: 'write the note', ...env },
  });
  t.after(async () => {
    await harness.stop();
    rmSync(dir, { recursive: true, force: true, maxRetries: 5 });
  });
  return { echo, harness };
}

function recorded(echo: string): Array<{ method: string; params?: Record<string, unknown> }> {
  return readFileSync(echo, 'utf8')
    .split('\n')
    .filter((line) => line !== '')
    .map((line) => JSON.parse(line) as { method: string; params?: Record<string, unknown> });
}

test('a finished run can be forked, and the branch is listed apart from it', async (t) => {
  const { echo, harness } = await scratch(t);
  await harness.session.send('write the note');
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  const agentId = harness.sessions[0]?.agent_id;
  const runId = harness.dones[0]?.done.run_id;
  assert.ok(agentId, 'the session has an agent id');
  assert.ok(runId, 'the finished run has an id');

  const forked = await harness.client.request<{ agent_id: string }>(Method.SessionFork, {
    agent_id: agentId,
    run_id: runId,
  });
  assert.ok(forked.agent_id, 'the host answered with the new conversation');
  assert.notEqual(forked.agent_id, agentId, 'a fork is a new agent, not the same one');

  // The sidebar is how a person finds the branch again, so it has to be a row
  // of its own and read as one.
  const listed = await harness.client.request<{
    sessions: Array<{ agent_id: string; title?: string }>;
  }>(Method.SessionList, {});
  const titles = new Map(listed.sessions.map((session) => [session.agent_id, session.title]));
  assert.equal(titles.get(agentId), 'write the note');
  assert.equal(titles.get(forked.agent_id), 'write the note (fork)');

  // The host saw the run the panel would name, not one the test invented.
  const fork = recorded(echo)
    .filter((entry) => entry.method === Method.SessionFork)
    .pop();
  assert.ok(fork, 'the host recorded the fork');
  assert.deepEqual(fork.params, { agent_id: agentId, run_id: runId });
});

test('a run that never finished is refused', async (t) => {
  const { harness } = await scratch(t);
  await harness.session.send('write the note');
  await waitFor(() => harness.dones.length === 1, 'the run to finish');
  const agentId = harness.sessions[0]?.agent_id;

  await assert.rejects(
    harness.client.request(Method.SessionFork, { agent_id: agentId, run_id: 'run-nope' }),
    /run not found/,
  );
});

// The tree entry point names an agent this window never opened. That is the
// ordinary case for a sidebar row, and it is why the method is agent-keyed.
test('a stored conversation can be forked without being open', async (t) => {
  const { harness } = await scratch(t, { FAKE_HOST_STORED: 'a-stored' });

  const forked = await harness.client.request<{ agent_id: string }>(Method.SessionFork, {
    agent_id: 'a-stored',
    run_id: 'r1',
  });
  assert.ok(forked.agent_id);
  assert.notEqual(forked.agent_id, 'a-stored');

  const listed = await harness.client.request<{ sessions: Array<{ agent_id: string }> }>(
    Method.SessionList,
    {},
  );
  const ids = listed.sessions.map((session) => session.agent_id);
  assert.ok(ids.includes('a-stored'), 'the source is still there');
  assert.ok(ids.includes(forked.agent_id), 'the fork is listed');
});
