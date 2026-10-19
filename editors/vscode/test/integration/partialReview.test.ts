/**
 * Keeping part of a change, over a real child process.
 *
 * The unit tests compute what a selection means; this test checks the one link
 * they cannot: that the computed text travels as rewritten arguments, that the
 * host runs *those* arguments, and that the file on disk is the selection. A
 * mistake here looks like the right file for the wrong reason — it would still
 * be a file — so the assertions are byte-exact and read from the host's own
 * record of the call it was asked to run.
 */

import { strict as assert } from 'node:assert';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { TestContext, test } from 'node:test';
import { applyAccepted, partialDiff, rewriteArgs } from '../../src/hunks';
import { startFakeHost, waitFor } from '../helpers/harness';

const BODY = 'alpha\nbravo\ncharlie\ndelta\necho\nfoxtrot\n';

/**
 * A workspace with the file the scenario edits, and the host's echo beside it.
 * The single teardown stops the child before removing its directory, because on
 * Windows the removal fails while the child holds it.
 */
async function scratch(t: TestContext) {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-partial-'));
  const file = path.join(dir, 'target.txt');
  const echo = path.join(dir, 'echo.jsonl');
  writeFileSync(file, BODY);
  const harness = await startFakeHost('partial', {
    cwd: dir,
    env: { FAKE_HOST_FILE: 'target.txt', FAKE_HOST_ECHO: echo },
  });
  t.after(async () => {
    await harness.stop();
    rmSync(dir, { recursive: true, force: true, maxRetries: 5 });
  });
  return { dir, file, echo, harness };
}

/** The approval the scenario raises, and the parts it was proven to apply. */
async function opened(harness: Awaited<ReturnType<typeof startFakeHost>>) {
  await harness.session.send('make two edits');
  await waitFor(() => harness.approvals.length === 1, 'the approval request');
  const request = harness.approvals[0];
  const partial = partialDiff(request.tool, request.preview);
  assert.ok(partial, 'the preview is available in parts');
  return { request, partial };
}

/** What the client asked the host to answer, as the host recorded it. */
function answered(echoPath: string): Record<string, unknown> {
  const lines = readFileSync(echoPath, 'utf8')
    .split('\n')
    .filter((line) => line !== '')
    .map((line) => JSON.parse(line) as { method: string; params?: Record<string, unknown> });
  const response = lines.filter((line) => line.method === 'approval.response').pop();
  assert.ok(response, 'the host recorded the approval answer');
  return response.params ?? {};
}

test('accepting one hunk rewrites the call, and the host runs that text', async (t) => {
  const { file, echo, harness } = await scratch(t);
  const { request, partial } = await opened(harness);

  assert.equal(request.tool, 'edit_file');
  assert.equal(partial.hunks.length, 2, 'the scenario proposes two parts');

  // The reviewer keeps the first part and leaves the second as it was.
  const accepted = applyAccepted(partial.before, partial.hunks, [0]);
  assert.ok(accepted !== undefined);
  const args = rewriteArgs(request.tool, request.args, partial.before, partial.after, accepted);
  assert.ok(args);

  const answeredOk = await harness.session.respond(request.request_id, 'allow', { args });
  assert.equal(answeredOk, true);
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  // Exactly the accepted part landed; the untouched one did not.
  assert.equal(readFileSync(file, 'utf8'), 'alpha\nBRAVO\ncharlie\ndelta\necho\nfoxtrot\n');
  // And the host was asked to run those very arguments — not the preview's.
  assert.deepEqual(answered(echo).args, args);
});

test('keeping both parts writes the whole preview', async (t) => {
  const { file, harness } = await scratch(t);
  const { request, partial } = await opened(harness);

  const accepted = applyAccepted(partial.before, partial.hunks, [0, 1]);
  assert.ok(accepted !== undefined);
  const args = rewriteArgs(request.tool, request.args, partial.before, partial.after, accepted);
  assert.ok(args);

  await harness.session.respond(request.request_id, 'allow', { args });
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  assert.equal(readFileSync(file, 'utf8'), 'alpha\nBRAVO\ncharlie\ndelta\nECHO\nfoxtrot\n');
});

test('rejecting every part denies the call and leaves the file untouched', async (t) => {
  const { file, harness } = await scratch(t);
  const { request } = await opened(harness);

  // What `rejectPartial` sends when the selection is empty.
  const answeredOk = await harness.session.respond(request.request_id, 'deny', {
    message: 'The reviewer rejected this change.',
  });
  assert.equal(answeredOk, true);
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  assert.equal(readFileSync(file, 'utf8'), BODY);
  const result = harness.events.find((event) => event.type === 'tool_result');
  assert.equal(result?.is_error, true, 'a refusal reaches the model as a refusal');
  assert.equal(harness.session.currentState, 'idle');
});

test('a stale preview is not available in parts', async (t) => {
  const { harness } = await scratch(t);
  const { request } = await opened(harness);

  // A host whose bodies disagree with its diff cannot be taken in parts: the
  // card falls back to Allow/Deny rather than applying a guess.
  const stale = {
    ...request.preview,
    after: `${BODY}something else\n`,
  };
  assert.equal(partialDiff(request.tool, stale), undefined);
});
