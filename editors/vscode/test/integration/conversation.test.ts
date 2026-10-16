/**
 * The extension's client and session against a scripted host, end to end.
 *
 * These are the tests that would have caught the mistakes a unit test cannot:
 * a mis-framed notification, a run that never leaves `running`, a prompt that
 * stays on screen after the host has moved on.
 */

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { answerNewestApproval, startFakeHost, waitFor } from '../helpers/harness';

test('a chat turn streams text, pairs the tool call with its result, and ends idle', async (t) => {
  const harness = await startFakeHost('chat');
  t.after(() => harness.stop());

  const result = await harness.session.send('read the readme');
  assert.ok(result.run_id.length > 0);
  assert.equal(harness.session.currentState, 'running');

  await waitFor(() => harness.dones.length === 1, 'the run to finish');
  const types = harness.events.map((event) => event.type);
  assert.ok(types.includes('progress'), 'a step indicator was reported');
  assert.ok(types.includes('assistant'));
  assert.ok(types.includes('tool_call'));
  assert.ok(types.includes('tool_result'));

  const call = harness.events.find((event) => event.type === 'tool_call');
  const output = harness.events.find((event) => event.type === 'tool_result');
  assert.equal(call?.name, 'read_file');
  // The panel pairs the card by this id; a host that changed it would leave
  // orphaned "running…" cards behind.
  assert.equal(call?.tool_call_id, output?.tool_call_id);
  assert.equal(output?.is_error, false);

  const finished = harness.dones[0];
  assert.equal(finished.done.status, 'finished');
  assert.equal(finished.failure.kind, 'none');
  assert.equal(finished.done.usage?.total_tokens, 138);
  assert.equal(harness.session.currentState, 'idle');
  assert.deepEqual(harness.states.map((s) => s.state), ['running', 'idle']);
});

test('an approved write runs and the turn finishes', async (t) => {
  const harness = await startFakeHost('approval');
  t.after(() => harness.stop());

  await harness.session.send('write notes.txt');
  await waitFor(() => harness.approvals.length === 1, 'the approval request');

  const request = harness.approvals[0];
  assert.equal(request.tool, 'write_file');
  assert.deepEqual(request.decisions, ['allow', 'allow_session', 'deny']);
  assert.equal(request.preview?.path, 'notes.txt');
  assert.ok(request.timeout_ms && request.timeout_ms > 0, 'the prompt can say when it expires');

  assert.equal(await answerNewestApproval(harness, 'allow'), true);
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  const output = harness.events.find((event) => event.type === 'tool_result');
  assert.equal(output?.is_error, false);
  assert.equal(harness.dones[0].done.status, 'finished');
});

test('a denied write comes back to the model as an error, not a crash', async (t) => {
  const harness = await startFakeHost('approval');
  t.after(() => harness.stop());

  await harness.session.send('write notes.txt');
  await waitFor(() => harness.approvals.length === 1, 'the approval request');
  assert.equal(await answerNewestApproval(harness, 'deny'), true);

  await waitFor(() => harness.dones.length === 1, 'the run to finish');
  const output = harness.events.find((event) => event.type === 'tool_result');
  assert.equal(output?.is_error, true);
  // The model reacts: the host reports a finished turn, not a failed one.
  assert.equal(harness.dones[0].done.status, 'finished');
  assert.equal(harness.session.currentState, 'idle');
});

test('an answer scoped to a session carries the scope the prompt showed', async (t) => {
  const harness = await startFakeHost('approval');
  t.after(() => harness.stop());

  await harness.session.send('write notes.txt');
  await waitFor(() => harness.approvals.length === 1, 'the approval request');
  const scope = harness.approvals[0].preview?.path;

  assert.equal(await answerNewestApproval(harness, 'allow_session', scope), true);
  await waitFor(() => harness.dones.length === 1, 'the run to finish');
  assert.equal(harness.session.pendingApprovals().length, 0);
});

test('an answer arriving after the run ended is reported as stale, not thrown', async (t) => {
  const harness = await startFakeHost('approval');
  t.after(() => harness.stop());

  await harness.session.send('write notes.txt');
  await waitFor(() => harness.approvals.length === 1, 'the approval request');
  const request = harness.approvals[0];

  // Answering is what ends the run here, so answering twice is exactly the
  // double-click the panel has to survive.
  assert.equal(await harness.session.respond(request.request_id, 'allow'), true);
  await waitFor(() => harness.dones.length === 1, 'the run to finish');
  assert.equal(await harness.session.respond(request.request_id, 'allow'), false);
});

test('stop interrupts a running turn and is reported as stopped, not failed', async (t) => {
  const harness = await startFakeHost('slow');
  t.after(() => harness.stop());

  await harness.session.send('think about it');
  await waitFor(() => harness.events.some((event) => event.type === 'assistant'), 'the first chunk');
  assert.equal(harness.session.currentState, 'running');

  await harness.session.cancel();
  await waitFor(() => harness.dones.length === 1, 'the run to end');

  assert.equal(harness.dones[0].done.status, 'cancelled');
  assert.equal(harness.dones[0].failure.kind, 'cancelled');
  assert.equal(harness.dones[0].failure.isError, false);
  assert.equal(harness.session.currentState, 'idle');

  // And the session is usable again afterwards.
  await harness.session.send('a second turn');
  await waitFor(() => harness.session.currentState === 'running', 'the second run to start');
  await harness.session.cancel();
  await waitFor(() => harness.dones.length === 2, 'the second run to end');
});

test('a step-limit failure is presented as its own kind', async (t) => {
  const harness = await startFakeHost('fail');
  t.after(() => harness.stop());

  await harness.session.send('do too much');
  await waitFor(() => harness.dones.length === 1, 'the run to fail');

  const { done, failure } = harness.dones[0];
  assert.equal(done.status, 'error');
  assert.equal(done.code, 'max_steps');
  assert.equal(failure.kind, 'max_steps');
  assert.equal(failure.isError, true);
  assert.match(failure.message, /step limit/);
  assert.equal(harness.session.currentState, 'idle');
});

test('a host that dies mid-turn leaves nothing running', async (t) => {
  const harness = await startFakeHost('silent');
  t.after(() => harness.stop());

  await harness.session.send('never answers');
  assert.equal(harness.session.currentState, 'running');

  // The manager wires this to `session.fail`; here the assertion is that the
  // client notices the death at all, so the panel is not left with a progress
  // spinner forever.
  await new Promise<void>((resolve) => {
    harness.child.once('exit', () => resolve());
    harness.child.kill();
  });
  assert.equal(harness.client.running, false);

  harness.session.fail(new Error('the host exited (code null)'));
  assert.equal(harness.session.currentState, 'idle');
  assert.equal(harness.dones.length, 1);
  assert.equal(harness.dones[0].done.status, 'error');
});
