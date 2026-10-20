/**
 * The MCP surface, over a real child process.
 *
 * The host is the scripted one, so what is pinned is the wire: the sources the
 * client sends, the shape it reads back — state, tool count, tools, the error a
 * server failed with — the params each management call carries, and the two ways
 * a broken file reaches a client. A host from before 0.44 answers the old shape
 * and none of the methods, which is a supported pair beside this extension.
 */

import { strict as assert } from 'node:assert';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import type { TestContext } from 'node:test';
import { test } from 'node:test';
import {
  McpAddParams,
  McpAuthenticateResult,
  McpListResult,
  McpSetEnabledParams,
  Method,
  McpStatusParams,
  Notify,
} from '../../src/protocol';
import { startFakeHost, waitFor } from '../helpers/harness';

async function scratch(t: TestContext, env: NodeJS.ProcessEnv = {}) {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-mcp-'));
  const echo = path.join(dir, 'echo.jsonl');
  const harness = await startFakeHost('chat', {
    cwd: dir,
    env: { FAKE_HOST_ECHO: echo, ...env },
  });
  t.after(async () => {
    await harness.stop();
    rmSync(dir, { recursive: true, force: true, maxRetries: 5 });
  });
  return { dir, echo, harness };
}

function recorded(echo: string): Array<{ method: string; params?: Record<string, unknown> }> {
  return readFileSync(echo, 'utf8')
    .split('\n')
    .filter((line) => line !== '')
    .map((line) => JSON.parse(line) as { method: string; params?: Record<string, unknown> });
}

/** The last params sent for one method, which is what a test pins. */
function lastParams(echo: string, method: string): Record<string, unknown> | undefined {
  return recorded(echo)
    .filter((entry) => entry.method === method)
    .pop()?.params;
}

test('the project layer is listed with each server, its file and its state', async (t) => {
  const { dir, echo, harness } = await scratch(t, {
    FAKE_HOST_MCP: 'files:stdio,docs:http:failed',
  });
  const source = path.join(dir, '.cursor', 'mcp.json');

  const listed = await harness.client.request<McpListResult>(Method.McpList, {
    setting_sources: ['project'],
  });
  assert.equal(listed.runtime, true);
  assert.deepEqual(listed.sources, [{ source: 'project', path: source, exists: true }]);
  assert.deepEqual(listed.servers[0], {
    name: 'files',
    transport: 'stdio',
    source,
    enabled: true,
    state: 'connected',
    tool_count: 2,
    connected_at: '2026-10-20T09:00:00Z',
  });
  assert.equal(listed.servers[1].name, 'docs');
  assert.equal(listed.servers[1].state, 'failed');
  assert.equal(listed.servers[1].error, 'spawn failed: command not found');
  // The catalogue is asked for, not assumed: the chat card only wants the count.
  assert.equal('tools' in listed.servers[0], false);

  const call = recorded(echo)
    .filter((entry) => entry.method === Method.McpList)
    .pop();
  assert.deepEqual(call?.params, { setting_sources: ['project'] });
});

test('the tool catalogue arrives when it is asked for', async (t) => {
  const { harness } = await scratch(t, { FAKE_HOST_MCP: 'files:stdio' });

  const listed = await harness.client.request<McpListResult>(Method.McpList, {
    setting_sources: ['project'],
    include_tools: true,
  });
  const tools = listed.servers[0].tools ?? [];
  assert.deepEqual(
    tools.map((tool) => tool.name),
    ['echo', 'read_file'],
  );
});

// Nothing is read that the session was not told to read.
test('no source means an empty listing', async (t) => {
  const { harness } = await scratch(t, { FAKE_HOST_MCP: 'files:stdio' });

  const listed = await harness.client.request<McpListResult>(Method.McpList, { setting_sources: [] });
  assert.deepEqual(listed.servers, []);
  assert.deepEqual(listed.sources, []);
});

// An enabled layer with no file is reported, not dropped: the panel needs the
// difference between "off" and "on with nothing there".
test('an enabled layer whose file is absent is reported as absent', async (t) => {
  const { harness } = await scratch(t);

  const listed = await harness.client.request<McpListResult>(Method.McpList, {
    setting_sources: ['project'],
  });
  assert.equal(listed.sources.length, 1);
  assert.equal(listed.sources[0].source, 'project');
  assert.equal(listed.sources[0].exists, false);
  assert.deepEqual(listed.servers, []);
});

// A malformed file is a result on a runtime host: the panel shows the last good
// statuses beside it and the document goes read-only. Chapter 43 §4.
test('a malformed file is a problem in the listing', async (t) => {
  const { dir, harness } = await scratch(t, { FAKE_HOST_MCP_BROKEN: '1' });

  const listed = await harness.client.request<McpListResult>(Method.McpList, {
    setting_sources: ['project'],
  });
  assert.equal(listed.runtime, true);
  assert.deepEqual(listed.problem, {
    path: path.join(dir, '.cursor', 'mcp.json'),
    message: 'unexpected end of JSON input',
    line: 1,
    column: 17,
  });
});

test('an unknown source is refused by name', async (t) => {
  const { harness } = await scratch(t);

  await assert.rejects(
    harness.client.request(Method.McpList, { setting_sources: ['galaxy'] }),
    /unknown setting source: galaxy/,
  );
});

test('adding a server sends the draft and answers with the new listing', async (t) => {
  const { echo, harness } = await scratch(t, { FAKE_HOST_MCP: 'files:stdio' });
  const params: McpAddParams = {
    scope: 'project',
    server: {
      name: 'docs',
      transport: 'http',
      url: 'https://docs.example.test/mcp',
      headers: { Authorization: 'Bearer s3cret-value' },
      enabled: true,
    },
    setting_sources: ['project'],
    include_tools: true,
  };

  const listed = await harness.client.request<McpListResult>(Method.McpAdd, params);

  // Slot for slot, so a field renamed on one side fails here.
  assert.deepEqual(lastParams(echo, Method.McpAdd), params);
  assert.deepEqual(
    listed.servers.map((server) => server.name),
    ['files', 'docs'],
  );
  assert.equal(listed.servers[1].state, 'connected');
});

test('disabling and removing a server are one round trip each', async (t) => {
  const { echo, harness } = await scratch(t, { FAKE_HOST_MCP: 'files:stdio,notes:stdio' });

  const disabled = await harness.client.request<McpListResult>(Method.McpSetEnabled, {
    scope: 'project',
    name: 'notes',
    enabled: false,
    setting_sources: ['project'],
  } satisfies McpSetEnabledParams);
  assert.deepEqual(lastParams(echo, Method.McpSetEnabled), {
    scope: 'project',
    name: 'notes',
    enabled: false,
    setting_sources: ['project'],
  });
  assert.equal(disabled.servers[1].state, 'disabled');
  assert.equal(disabled.servers[1].enabled, false);

  const removed = await harness.client.request<McpListResult>(Method.McpRemove, {
    scope: 'project',
    name: 'notes',
    setting_sources: ['project'],
  });
  assert.deepEqual(
    removed.servers.map((server) => server.name),
    ['files'],
  );
});

test('reconnecting a failed server reports the new state', async (t) => {
  const { harness } = await scratch(t, { FAKE_HOST_MCP: 'docs:http:failed' });

  const before = await harness.client.request<McpListResult>(Method.McpList, {
    setting_sources: ['project'],
  });
  assert.equal(before.servers[0].state, 'failed');

  const after = await harness.client.request<McpListResult>(Method.McpReconnect, {
    name: 'docs',
    setting_sources: ['project'],
  });
  assert.equal(after.servers[0].state, 'connected');
  assert.equal(after.servers[0].error, undefined);
});

test('signing in opens a URL and completion arrives as a notification', async (t) => {
  const { harness } = await scratch(t, { FAKE_HOST_MCP: 'docs:http:auth_required' });
  const statuses: McpStatusParams[] = [];
  // The window-level listener: the router would drop an unkeyed notification,
  // which is why the manager intercepts this one before it gets there.
  harness.client.setNotificationHandler((method, params) => {
    if (method === Notify.McpStatus) statuses.push(params as McpStatusParams);
  });

  const started = await harness.client.request<McpAuthenticateResult>(Method.McpAuthenticate, {
    name: 'docs',
    setting_sources: ['project'],
  });
  assert.equal(started.server, 'docs');
  assert.match(started.auth_url, /^https:\/\/auth\.example\.test\//);
  // The URL is what the browser opens; no token may be in it.
  assert.doesNotMatch(started.auth_url, /token|access/i);

  await waitFor(() => statuses.some((entry) => entry.server.state === 'connected'), 'the sign-in to complete');
  const connected = statuses.find((entry) => entry.server.state === 'connected');
  assert.equal(connected?.server.name, 'docs');
  assert.deepEqual(connected?.setting_sources, ['project']);
  assert.equal('tools' in (connected?.server ?? {}), false);
});

test('signing out leaves the server wanting a sign-in again', async (t) => {
  const { harness } = await scratch(t, { FAKE_HOST_MCP: 'docs:http' });

  const listed = await harness.client.request<McpListResult>(Method.McpSignOut, {
    name: 'docs',
    setting_sources: ['project'],
  });
  assert.equal(listed.servers[0].state, 'auth_required');
});

// The pair this extension has to survive: a 0.40.6 host beside a 0.44 client.
test('a host without a runtime answers the old shape and refuses the methods', async (t) => {
  const { dir, harness } = await scratch(t, { FAKE_HOST_MCP: 'files:stdio', FAKE_HOST_MCP_LEGACY: '1' });

  const listed = await harness.client.request<McpListResult>(Method.McpList, {
    setting_sources: ['project'],
  });
  assert.equal(listed.runtime, undefined);
  assert.deepEqual(listed.servers, [
    { name: 'files', transport: 'stdio', source: path.join(dir, '.cursor', 'mcp.json') },
  ]);
  assert.equal('state' in listed.servers[0], false);

  await assert.rejects(harness.client.request(Method.McpReconnect, { name: 'files' }), /unknown method/);
  await assert.rejects(
    harness.client.request(Method.McpAdd, { scope: 'project', server: { name: 'docs' } }),
    /unknown method/,
  );
});

test('a legacy host fails the listing when the file will not parse', async (t) => {
  const { harness } = await scratch(t, { FAKE_HOST_MCP_BROKEN: '1', FAKE_HOST_MCP_LEGACY: '1' });

  await assert.rejects(
    harness.client.request(Method.McpList, { setting_sources: ['project'] }),
    /parse .*mcp\.json/,
  );
});
