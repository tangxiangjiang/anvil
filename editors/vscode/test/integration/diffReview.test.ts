/**
 * Reviewing a change before it lands.
 *
 * The scripted host previews a real write the way the real one does — a unified
 * diff plus both bodies — and then really writes the file, or does not,
 * depending on the answer. So the claim under test is the one that matters to a
 * user: what the review showed is what would happen, and a rejection leaves the
 * file exactly as it was.
 */

import { strict as assert } from 'node:assert';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { TestContext, test } from 'node:test';
import { answerNewestApproval, startFakeHost, waitFor } from '../helpers/harness';

/**
 * A workspace with one file in it, and a host pointed at that file. The single
 * teardown hook stops the child before removing its working directory, because
 * on Windows the removal fails while the child still holds it.
 */
async function scratch(t: TestContext, body: string) {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-diff-'));
  const file = path.join(dir, 'target.txt');
  writeFileSync(file, body);
  const harness = await startFakeHost('diff', { cwd: dir, env: { FAKE_HOST_FILE: 'target.txt' } });
  t.after(async () => {
    await harness.stop();
    rmSync(dir, { recursive: true, force: true, maxRetries: 5 });
  });
  return { dir, file, harness };
}

test('a write previews as a diff with both bodies, so a diff editor can open it', async (t) => {
  const { harness } = await scratch(t, 'alpha\n');

  await harness.session.send('append a line');
  await waitFor(() => harness.approvals.length === 1, 'the approval request');

  const request = harness.approvals[0];
  assert.equal(request.tool, 'write_file');
  assert.equal(request.preview?.kind, 'diff');
  assert.equal(request.preview?.path, 'target.txt');
  // The two bodies are what the editor's own diff view needs; a unified diff
  // alone would make the client re-implement the patch, and the client's second
  // implementation is the thing being approved.
  assert.equal(request.preview?.before, 'alpha\n');
  assert.equal(request.preview?.after, 'alpha\nadded by the fake host\n');
  assert.equal(request.preview?.truncated, undefined);
  // The diff text stays for the card, which is what shows when the bodies are
  // not there at all (a multi-file patch) or have been dropped as too large.
  assert.ok(request.preview?.diff?.includes('-alpha'), request.preview?.diff);
  assert.ok(request.preview?.diff?.includes('+added by the fake host'), request.preview?.diff);

  await answerNewestApproval(harness, 'allow');
  await waitFor(() => harness.dones.length === 1, 'the run to finish');
});

test('denying a previewed write leaves the file byte-identical', async (t) => {
  const body = 'alpha\nbeta\n';
  const { file, harness } = await scratch(t, body);

  await harness.session.send('append a line');
  await waitFor(() => harness.approvals.length === 1, 'the approval request');
  const preview = harness.approvals[0].preview;
  assert.equal(preview?.before, body, 'the preview showed the file as it was');

  const answered = await answerNewestApproval(harness, 'deny');
  assert.equal(answered, true);
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  assert.equal(readFileSync(file, 'utf8'), body);
  // The denial reaches the model as a tool error, not as silence: a model that
  // cannot tell a refusal from a failure asks again.
  const result = harness.events.find((event) => event.type === 'tool_result');
  assert.equal(result?.is_error, true);
  assert.equal(harness.dones[0].done.status, 'finished');
  assert.equal(harness.session.currentState, 'idle');
});

test('allowing the same preview writes exactly what it showed', async (t) => {
  const { file, harness } = await scratch(t, 'alpha\n');

  await harness.session.send('append a line');
  await waitFor(() => harness.approvals.length === 1, 'the approval request');
  const shown = harness.approvals[0].preview?.after;
  assert.ok(typeof shown === 'string' && shown.length > 0);

  await answerNewestApproval(harness, 'allow');
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  // What the review displayed is what landed on disk.
  assert.equal(readFileSync(file, 'utf8'), shown);
  assert.equal(harness.dones[0].failure.kind, 'none');
});

test('a preview of a file that does not exist yet shows an empty pre-image', async (t) => {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-diff-'));
  const file = path.join(dir, 'fresh.txt');
  const harness = await startFakeHost('diff', { cwd: dir, env: { FAKE_HOST_FILE: 'fresh.txt' } });
  t.after(async () => {
    await harness.stop();
    rmSync(dir, { recursive: true, force: true, maxRetries: 5 });
  });

  await harness.session.send('create it');
  await waitFor(() => harness.approvals.length === 1, 'the approval request');

  // An empty pre-image is not a missing preview: it is what a create looks like
  // in a diff editor, and rendering it as "no preview" would hide the addition.
  assert.equal(harness.approvals[0].preview?.before, '');
  assert.equal(harness.approvals[0].preview?.after, 'added by the fake host\n');

  await answerNewestApproval(harness, 'allow');
  await waitFor(() => harness.dones.length === 1, 'the run to finish');
  assert.equal(readFileSync(file, 'utf8'), 'added by the fake host\n');
});
