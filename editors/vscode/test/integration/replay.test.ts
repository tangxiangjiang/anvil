/**
 * `session.transcript` on the wire.
 *
 * The reader itself is tested in Go, against a real store. What this file pins
 * is the join: the shape the host sends, the shape `src/protocol.ts` describes,
 * and the shape `src/transcript.ts` folds. A field renamed on one side of a
 * boundary no compiler checks is a silent `undefined` — an entry with no role is
 * an entry the replay skips, and a conversation that comes back half drawn.
 *
 * The panel's half — asking for this at the right moment and posting what comes
 * back through the right door — cannot run here, because `ChatPanel` needs an
 * extension host. `test/unit/replayWiring.test.ts` holds that side by reading
 * the source.
 */

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import {
  CodeMethodNotFound,
  SessionTranscriptResult,
  TranscriptEntry,
} from '../../src/protocol';
import { HostRpcError } from '../../src/hostClient';
import { startFakeHost } from '../helpers/harness';

test('a stored conversation arrives whole: turns, arguments, and where the run ended', async (t) => {
  const harness = await startFakeHost('chat', { env: { FAKE_HOST_TRANSCRIPT: '1' } });
  t.after(() => harness.stop());

  const result = await harness.client.request<SessionTranscriptResult>('session.transcript', {
    agent_id: 'a1',
  });
  const entries = result.entries;
  assert.ok(entries.length > 0, 'the transcript has entries');

  // Oldest first, which is the order the store appends in and the only order a
  // transcript can be read in.
  const first = entries[0];
  assert.equal(first.kind, 'message');
  assert.equal(first.role, 'user');
  assert.ok(first.run_id, 'an entry belongs to a run');

  // The assistant turn that asked for a tool carries the call, id and name and
  // **arguments**: the arguments are what a replayed card derives its change
  // from, so an omitted `args` is a card that opens nothing.
  const call = entries
    .flatMap((entry: TranscriptEntry) => entry.tool_calls ?? [])
    .find((entry) => entry.name === 'write_file');
  assert.ok(call, 'the tool call survived the projection');
  assert.equal(call.id, 't1');
  assert.deepEqual(call.args, { path: 'target.txt', content: 'added by the fake host\n' });

  // The result is on the wire under a role the replay maps to a `tool_result`
  // event, paired by the same id.
  const output = entries.find((entry) => entry.role === 'tool');
  assert.equal(output?.tool_call_id, 't1');
  assert.equal(output?.name, 'write_file');
  assert.ok(output?.content, 'the tool output is carried');

  // And the boundary, with the two things a client draws from it.
  const end = entries.find((entry) => entry.kind === 'run_end');
  assert.equal(end?.status, 'finished');
  assert.equal(end?.steps, 3);
  assert.equal(end?.usage?.total_tokens, 138);
  assert.equal(result.dropped, undefined, 'nothing was left out');
});

test('a host that does not know the method answers -32601, which is not an error to show', async (t) => {
  // An older host beside a newer extension is a pair that has to keep working.
  // The panel treats exactly this code as "no replay available" and leaves the
  // tab usable, so the code is what this test is about.
  const harness = await startFakeHost('chat');
  t.after(() => harness.stop());

  await assert.rejects(
    () => harness.client.request('session.transcript', { agent_id: 'a1' }),
    (err: unknown) => {
      assert.ok(err instanceof HostRpcError, `want an RPC error, got ${String(err)}`);
      assert.equal((err as HostRpcError).code, CodeMethodNotFound);
      return true;
    },
  );
});

test('the reader says how much of a long conversation it left out', async (t) => {
  const harness = await startFakeHost('chat', {
    env: { FAKE_HOST_TRANSCRIPT: '1', FAKE_HOST_TRANSCRIPT_DROPPED: '12' },
  });
  t.after(() => harness.stop());

  const result = await harness.client.request<SessionTranscriptResult>('session.transcript', {
    agent_id: 'a1',
  });
  // A count rather than a flag, because the panel says how much: "earlier turns
  // are not shown" without a number is a claim the user cannot judge.
  assert.equal(result.dropped, 12);
});

test('an agent the workspace stores but nothing has opened is still listed', async (t) => {
  // The premise of coming back to a session: on a window that has just reloaded,
  // the host has no session open and the agent is on disk. If the listing could
  // not report it, the remembered id would look like an id whose agent is gone,
  // and every reload would start a fresh conversation instead.
  const harness = await startFakeHost('chat', {
    env: { FAKE_HOST_STORED: 'a7,a8', FAKE_HOST_TITLE: 'append a line to the target' },
  });
  t.after(() => harness.stop());

  const listed = await harness.client.request<{
    sessions: Array<{ agent_id: string; title?: string }>;
  }>('session.list');
  const ids = listed.sessions.map((session) => session.agent_id);
  assert.ok(ids.includes('a7') && ids.includes('a8'), `stored agents are listed, got ${ids}`);

  // Alongside the session this process did open, and indistinguishable from it
  // in shape — the sidebar builds one row per entry and cannot tell which rows
  // came off the disk.
  const stored = listed.sessions.find((session) => session.agent_id === 'a7');
  assert.equal(stored?.title, 'append a line to the target');
});
