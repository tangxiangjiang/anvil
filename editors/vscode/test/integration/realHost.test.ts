/**
 * The extension's client against the real `anvil-host` binary.
 *
 * Everything else in this suite runs against a scripted host, which pins the
 * extension's assumptions but not the Go side of the wire. These tests close
 * that gap: real framing, real JSON shapes, real error codes — with the model
 * call deliberately routed at a port nothing listens on, so the run ends
 * without a key, a network, or money.
 *
 * Skipped when the sidecar has not been built (`npm run build:host`).
 */

import { strict as assert } from 'node:assert';
import { existsSync } from 'node:fs';
import * as path from 'node:path';
import { test } from 'node:test';
import { HostClient } from '../../src/hostClient';
import { ChatSession } from '../../src/session';
import { classifyDone } from '../../src/errors';
import { DoneParams, HostEvent } from '../../src/protocol';

const BINARY = path.resolve(
  __dirname,
  '../../..',
  'out',
  process.platform === 'win32' ? 'anvil-host.exe' : 'anvil-host',
);

const missing = existsSync(BINARY) ? false : `the sidecar is not built at ${BINARY} (run: npm run build:host)`;

/** An environment with every provider setting removed, so the run is offline. */
function bareEnv(): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = { ...process.env };
  for (const key of ['ANVIL_API_KEY', 'ANVIL_MODEL', 'ANVIL_BASE_URL', 'ANVIL_PROVIDER', 'OPENAI_API_KEY', 'ANTHROPIC_API_KEY']) {
    delete env[key];
  }
  return env;
}

function realClient(env: NodeJS.ProcessEnv = bareEnv()): HostClient {
  return new HostClient({
    command: BINARY,
    cwd: path.resolve(__dirname, '../../..'),
    env,
    requestTimeoutMs: 20_000,
  });
}

/** A provider that cannot be reached: port 9 is the discard port. */
const UNREACHABLE = { provider: 'openai', model: 'anvil-extension-test', base_url: 'http://127.0.0.1:9/v1' };

test('the real host handshakes and reports what it can do', { skip: missing }, async () => {
  const client = realClient();
  try {
    await client.start();
    const info = await client.initialize(process.cwd(), { name: 'anvil-vscode', version: '0.35.5' });

    // `initialize()` only resolves after it has checked the protocol revision,
    // so reaching this line is the assertion that the two sides agree.
    assert.match(info.host, /^anvil-host /);
    assert.ok(info.cwd.length > 0);
    assert.ok(info.capabilities.includes('sessions'));
    assert.ok(info.capabilities.includes('approval'), 'the extension renders prompts, so the host must offer them');

    const listed = await client.request<{ sessions: unknown[] }>('session.list', {});
    assert.ok(Array.isArray(listed.sessions));
  } finally {
    await client.stop();
  }
  assert.equal(client.running, false);
});

test('a run the provider refuses ends as a done with its own code', { skip: missing }, async () => {
  const events: HostEvent[] = [];
  const dones: DoneParams[] = [];
  const client = realClient();
  const session = new ChatSession(client, process.cwd(), UNREACHABLE, {
    onEvent: (event) => events.push(event),
    onDone: (done) => dones.push(done),
  });
  client.setNotificationHandler((method, params) => session.handle(method, params));

  try {
    await client.start();
    await client.initialize(process.cwd(), { name: 'anvil-vscode', version: '0.35.5' });
    const opened = await session.open();
    assert.equal(opened.model, UNREACHABLE.model);
    assert.equal(opened.provider, 'openai');
    assert.equal(opened.permission_mode, 'ask', 'the extension asks before mutating calls');
    assert.ok(opened.session_id.length > 0);

    const run = await session.send('say hello');
    assert.ok(run.run_id.length > 0);

    const deadline = Date.now() + 20_000;
    while (dones.length === 0 && Date.now() < deadline) {
      await new Promise((resolve) => setTimeout(resolve, 20));
    }
    assert.equal(dones.length, 1, 'exactly one terminal envelope per prompt');

    const done = dones[0];
    assert.equal(done.status, 'error');
    assert.equal(done.code, 'provider');
    assert.ok((done.err ?? '').length > 0, 'the host says why');
    assert.equal(session.currentState, 'idle');

    // And what the panel would show for it: a provider problem with a fix,
    // not a generic failure.
    const failure = classifyDone(done);
    assert.equal(failure.kind, 'provider');
    assert.match(failure.message, /API key|provider/i);
    assert.ok(events.length > 0, 'the run reported progress before it failed');
  } finally {
    await session.close();
    await client.stop();
  }
});
