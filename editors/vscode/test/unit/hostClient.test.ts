import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { HostClient, HostDownError, HostRpcError, HostTimeoutError } from '../../src/hostClient';
import { FIXTURE_HOST, waitFor } from '../helpers/harness';

function client(env: Record<string, string> = {}, requestTimeoutMs = 2_000): HostClient {
  return new HostClient({
    command: process.execPath,
    args: [FIXTURE_HOST],
    cwd: process.cwd(),
    env: { ...process.env, ...env },
    requestTimeoutMs,
  });
}

test('a missing binary is reported as a failure to start', async () => {
  const down = new HostClient({
    command: 'anvil-host-that-does-not-exist-4f9a',
    cwd: process.cwd(),
    env: process.env,
    requestTimeoutMs: 500,
  });
  await assert.rejects(() => down.start(), HostDownError);
  assert.equal(down.running, false);
});

test('initialize reports the host and refuses a protocol it does not speak', async () => {
  const good = client();
  await good.start();
  const result = await good.initialize(process.cwd(), { name: 'test', version: '1' });
  assert.equal(result.host, 'anvil-host (fake)');
  assert.ok(result.capabilities.includes('approval'));
  await good.stop();

  // A revision mismatch must read as a mismatch and not as a hang: it names
  // the binary that answered, both revisions, and the way out.
  const wrong = client({ FAKE_HOST_PROTOCOL: '99' });
  await wrong.start();
  await assert.rejects(
    () => wrong.initialize(process.cwd(), { name: 'test', version: '1' }),
    /anvil-host \(fake\) speaks editor-host protocol 99; this extension speaks 1\..*anvil\.hostPath/s,
  );
  await wrong.stop();
});

test('replies are matched to the request that asked for them', async () => {
  const host = client();
  await host.start();
  const [models, session] = await Promise.all([
    host.request<{ models: Array<{ id: string }> }>('models.list'),
    host.request<{ session_id: string }>('session.new', { provider: 'openai' }),
  ]);
  assert.equal(models.models[0].id, 'fake-model');
  assert.equal(session.session_id, 's1');
  await host.stop();
});

test('notifications reach the handler without an id of their own', async () => {
  const host = client({ FAKE_HOST_SCENARIO: 'chat' });
  const notifications: Array<{ method: string; params: unknown }> = [];
  host.setNotificationHandler((method, params) => notifications.push({ method, params }));
  await host.start();
  await host.initialize(process.cwd(), { name: 'test', version: '1' });
  const session = await host.request<{ session_id: string }>('session.new', {});
  await host.request('prompt', { session_id: session.session_id, message: 'hi' });

  await waitFor(() => notifications.some((n) => n.method === 'done'), 'the run to finish');
  const methods = notifications.map((n) => n.method);
  assert.ok(methods.includes('event'));
  assert.ok(methods.includes('done'));
  await host.stop();
});

test('a JSON-RPC error becomes a HostRpcError carrying its code', async () => {
  const host = client();
  await host.start();
  await assert.rejects(
    () => host.request('no.such.method'),
    (err: unknown) => err instanceof HostRpcError && err.code === -32601,
  );
  await host.stop();
});

test('an unanswered request times out rather than hanging the caller', async () => {
  const host = client({ FAKE_HOST_IGNORE: 'models.list' }, 120);
  await host.start();
  await assert.rejects(() => host.request('models.list'), HostTimeoutError);
  // The client stays usable: a dropped request must not poison the pipe.
  const session = await host.request<{ session_id: string }>('session.new', {});
  assert.equal(session.session_id, 's1');
  await host.stop();
});

test('an unexpected exit fails everything in flight and reports itself once', async () => {
  const exits: Array<{ unexpected: boolean }> = [];
  const host = new HostClient({
    command: process.execPath,
    args: [FIXTURE_HOST],
    cwd: process.cwd(),
    env: { ...process.env, FAKE_HOST_IGNORE: 'models.list' },
    requestTimeoutMs: 5_000,
    onExit: (info) => exits.push(info),
  });
  await host.start();
  const pending = host.request('models.list');
  host.process?.kill();
  await assert.rejects(() => pending, HostDownError);
  assert.equal(exits.length, 1);
  assert.equal(exits[0].unexpected, true);
  assert.equal(host.running, false);
  // The child is gone, so a later request is refused rather than queued.
  await assert.rejects(() => host.request('models.list'), HostDownError);
});

test('an exit we asked for is not reported as unexpected', async () => {
  const exits: Array<{ unexpected: boolean }> = [];
  const host = new HostClient({
    command: process.execPath,
    args: [FIXTURE_HOST],
    cwd: process.cwd(),
    env: process.env,
    requestTimeoutMs: 2_000,
    onExit: (info) => exits.push(info),
  });
  await host.start();
  await host.stop();
  assert.equal(exits.length, 1);
  assert.equal(exits[0].unexpected, false);
  assert.equal(host.running, false);
});

test('stopping twice is harmless, and a request after stop is refused', async () => {
  const host = client();
  await host.start();
  await host.stop();
  await host.stop();
  await assert.rejects(() => host.request('models.list'), HostDownError);
  // Notifications to a dead child are dropped rather than thrown.
  host.notify('shutdown');
});

test('the child\'s stderr is logged, never parsed as protocol', async () => {
  const lines: string[] = [];
  const host = new HostClient({
    command: process.execPath,
    args: [FIXTURE_HOST],
    cwd: process.cwd(),
    env: process.env,
    requestTimeoutMs: 2_000,
    log: (line) => lines.push(line),
  });
  await host.start();
  await host.initialize(process.cwd(), { name: 'test', version: '1' });
  // The fixture writes nothing to stderr, so what is checked here is that a
  // clean child leaves the log clean: a stray line would mean the framer is
  // reading the wrong stream.
  assert.deepEqual(lines.filter((line) => line.startsWith('host: unparseable')), []);
  await host.stop();
});
