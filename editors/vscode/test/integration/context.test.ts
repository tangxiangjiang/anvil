/**
 * What actually goes on the wire for a prompt.
 *
 * The host is what reads files, caps them, and refuses escapes; that side is
 * covered by the Go tests. What is covered here is the other half of the
 * bargain: that the extension sends a path and not a body, that an empty
 * context is left off entirely, and that the session asks for vision before any
 * image can be accepted.
 *
 * The assertions are made against the scripted host's own record of the
 * requests it received (FAKE_HOST_ECHO), not against a mock of the client — so
 * a change in framing or in the params shape shows up here.
 */

import { strict as assert } from 'node:assert';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { TestContext, test } from 'node:test';
import { Harness, startFakeHost, waitFor } from '../helpers/harness';
import { SessionConfig } from '../../src/session';
import { PromptContext } from '../../src/protocol';

/** A real 1x1 PNG. */
const PNG =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';

interface Recorded {
  method: string;
  params: Record<string, unknown>;
}

interface Fixture {
  dir: string;
  echo: string;
  harness: Harness;
}

/**
 * A scripted host on its own scratch directory, with one teardown hook that
 * stops the child before removing the directory. That order is the whole point
 * of doing it in one hook: the child's working directory *is* that directory,
 * and on Windows removing it while the child is alive fails with EPERM.
 */
async function host(t: TestContext, config: SessionConfig = {}): Promise<Fixture> {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-context-'));
  const echo = path.join(dir, 'echo.jsonl');
  const harness = await startFakeHost('chat', { cwd: dir, env: { FAKE_HOST_ECHO: echo }, config });
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

function only(records: Recorded[], method: string): Recorded {
  const found = records.filter((record) => record.method === method);
  assert.equal(found.length, 1, `exactly one ${method}`);
  return found[0];
}

test('mentions, selection and diagnostics travel as context, not as text', async (t) => {
  const { echo, harness } = await host(t);

  const context: PromptContext = {
    mentions: [{ path: 'src/main.go' }, { path: 'README.md' }],
    selection: {
      path: 'src/main.go',
      range: { start_line: 4, start_column: 1, end_line: 6, end_column: 12 },
      text: 'func main() {}',
    },
    diagnostics: [{ path: 'src/main.go', line: 4, severity: 'error', message: 'undefined: foo' }],
  };
  await harness.session.send('what is wrong here?', { context });
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  const prompt = only(readEcho(echo), 'prompt');
  // The client names paths; it never puts file contents in the message. The
  // host is the one that reads, and it does so against its own sandbox.
  assert.deepEqual(prompt.params.context, context);
  assert.equal(prompt.params.message, 'what is wrong here?');
  assert.equal('attachments' in prompt.params, false);
});

test('a prompt with nothing to add carries only its text', async (t) => {
  const { echo, harness } = await host(t);

  await harness.session.send('  plain question  ');
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  const prompt = only(readEcho(echo), 'prompt');
  // Exactly the two keys a prompt had before context existed: an empty context
  // object on the wire would be a claim that there was nothing to say, which is
  // not the same thing.
  assert.deepEqual(Object.keys(prompt.params).sort(), ['message', 'session_id']);
  assert.equal(prompt.params.message, 'plain question');
});

test('attachments travel as base64, without a path', async (t) => {
  const { echo, harness } = await host(t, { vision: true });

  await harness.session.send('what is in this screenshot?', {
    attachments: [{ data: PNG, mime: 'image/png' }],
  });
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  const prompt = only(readEcho(echo), 'prompt');
  assert.deepEqual(prompt.params.attachments, [{ data: PNG, mime: 'image/png' }]);
  assert.equal('context' in prompt.params, false);
  // No attachment carries a path: a paste is bytes the user had in hand, and a
  // path would mean the host reading a file it was never asked to read.
  const [attachment] = prompt.params.attachments as Array<Record<string, unknown>>;
  assert.equal('path' in attachment, false);
});

test('vision is asked for when the session sets it, and not otherwise', async (t) => {
  const on = await host(t, { vision: true });
  const off = await host(t);

  assert.equal(only(readEcho(on.echo), 'session.new').params.vision, true);
  // Off is the host's default and stays off: a session that never asked for
  // vision is one the host will refuse an image on, which is the intended
  // refusal. Which way the editor asks is its own decision (p_0.46.9).
  assert.equal('vision' in only(readEcho(off.echo), 'session.new').params, false);
});
