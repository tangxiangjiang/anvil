/**
 * Compaction and the context estimate, over the real pipe.
 *
 * Two things are being pinned here that a unit test cannot: the budget the
 * panel declares reaches the host in exactly the shape it decodes, and a
 * compaction is asked for over `session.compact`, answered to, and announced —
 * the announcement is what the transcript draws its divider from, so a client
 * that only read the answer would show nothing.
 *
 * The fake host keeps a conversation per session and grows it by each prompt,
 * which is what makes an estimate, a refusal ("nothing earlier to fold") and a
 * real compaction all reachable states rather than scripted constants.
 */

import { strict as assert } from 'node:assert';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { TestContext, test } from 'node:test';
import { Harness, Scenario, startFakeHost, waitFor } from '../helpers/harness';
import { SessionConfig } from '../../src/session';

interface Recorded {
  method: string;
  params: Record<string, unknown>;
}

interface Fixture {
  dir: string;
  echo: string;
  harness: Harness;
}

/** A scripted host on its own scratch directory, stopped before it is removed. */
async function host(t: TestContext, scenario: Scenario, config: SessionConfig = {}): Promise<Fixture> {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-compaction-'));
  const echo = path.join(dir, 'echo.jsonl');
  const harness = await startFakeHost(scenario, { cwd: dir, env: { FAKE_HOST_ECHO: echo }, config });
  t.after(async () => {
    await harness.stop();
    rmSync(dir, { recursive: true, force: true, maxRetries: 5 });
  });
  return { dir, echo, harness };
}

function readEcho(file: string): Recorded[] {
  return readFileSync(file, 'utf8')
    .split('\n')
    .filter((line) => line.trim() !== '')
    .map((line) => JSON.parse(line) as Recorded);
}

/** The budget the panel builds for a 200k window. */
const BUDGET: SessionConfig = {
  context: { window_tokens: 200000, auto_compact_at: 0.8, keep_recent: 8 },
};

async function twoTurns(harness: Harness): Promise<void> {
  await harness.session.send('first');
  await waitFor(() => harness.dones.length === 1, 'the first run to finish');
  await harness.session.send('second');
  await waitFor(() => harness.dones.length === 2, 'the second run to finish');
}

test('the declared budget travels in the exact shape the host decodes', async (t) => {
  const { echo } = await host(t, 'chat', BUDGET);

  const opened = readEcho(echo).filter((record) => record.method === 'session.new');
  assert.equal(opened.length, 1);
  assert.deepEqual(opened[0].params.context, {
    window_tokens: 200000,
    auto_compact_at: 0.8,
    keep_recent: 8,
  });

  // With no budget the field is absent, not zero: a window of 0 is the host's
  // "nothing is full", so an absent field is the request older clients sent.
  const bare = await host(t, 'chat');
  const plain = readEcho(bare.echo).filter((record) => record.method === 'session.new');
  assert.equal('context' in plain[0].params, false);
});

test('session.context answers the estimate the meter draws', async (t) => {
  const { harness } = await host(t, 'chat', BUDGET);
  const before = await harness.session.context();
  // Nothing has been said yet: an empty conversation is 0 messages and 0
  // tokens, not an error and not a missing answer.
  assert.deepEqual(before, {
    estimated_tokens: 0,
    window_tokens: 200000,
    compact_at_tokens: 160000,
    messages: 0,
  });

  await twoTurns(harness);
  const after = await harness.session.context();
  assert.equal(after.estimated_tokens, 40000);
  assert.equal(after.messages, 12);
  assert.equal(after.window_tokens, 200000);
  // The mark is the declared fraction of the window, exactly as the host
  // computes it — the client never recomputes it into a different number.
  assert.equal(after.compact_at_tokens, 160000);
});

test('done carries the estimate and the moment the run reached the provider', async (t) => {
  const { harness } = await host(t, 'chat', BUDGET);
  await harness.session.send('hello');
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  const done = harness.dones[0].done;
  assert.equal(done.status, 'finished');
  assert.equal(done.context?.estimated_tokens, 20000);
  assert.equal(done.context?.window_tokens, 200000);
  // The cache countdown is anchored to this, so it has to be a real instant.
  const at = Date.parse(done.last_request_at ?? '');
  assert.equal(Number.isFinite(at), true, 'last_request_at parses');
});

test('there is nothing to compact until there is earlier history', async (t) => {
  const { harness } = await host(t, 'chat', BUDGET);
  await harness.session.send('only turn');
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  const result = await harness.session.compact();
  // Six messages is fewer than the eight kept verbatim, so the answer is a
  // valid "no" rather than an error — and nothing is announced.
  assert.equal(result.compacted, false);
  assert.equal(result.before, undefined);
  assert.equal(result.context.messages, 6);
  assert.deepEqual(harness.compacts, []);
});

test('a compaction is asked for, answered, and announced', async (t) => {
  const { echo, harness } = await host(t, 'chat', BUDGET);
  await twoTurns(harness);

  const result = await harness.session.compact();
  assert.equal(result.compacted, true);
  assert.equal(result.before, 40000);
  assert.equal(result.after, 26667);
  assert.equal(result.summarized, 4);
  // The answer's estimate is the post-compaction one, so the meter is correct
  // without a second round trip.
  assert.equal(result.context.estimated_tokens, 26667);
  assert.equal(result.context.messages, 8);

  // The answer and the announcement are separate messages; the panel draws the
  // divider from the latter, so a client must tolerate it arriving just after.
  await waitFor(() => harness.compacts.length === 1, 'the compact notification');
  const announcement = harness.compacts[0];
  assert.equal(announcement.sessionId, harness.session.id);
  assert.deepEqual(announcement.info, {
    reason: 'manual',
    before: 40000,
    after: 26667,
    summarized: 4,
    window: 200000,
  });

  const records = readEcho(echo);
  assert.equal(records.filter((record) => record.method === 'session.compact').length, 1);
});

test('a compaction is refused in words while a turn is running', async (t) => {
  const { echo, harness } = await host(t, 'slow', BUDGET);
  await harness.session.send('take your time');
  await waitFor(() => harness.states.some((state) => state.state === 'running'), 'the run to start');

  // Refused locally: the host's -32003 would arrive without the advice that
  // makes it actionable, and the request never leaves the client.
  await assert.rejects(() => harness.session.compact(), /a turn is running \(r1\); stop it first/);
  assert.equal(
    readEcho(echo).filter((record) => record.method === 'session.compact').length,
    0,
    'no compact request was sent',
  );

  await harness.session.cancel();
  await waitFor(() => harness.dones.length === 1, 'the cancelled run to finish');
});
