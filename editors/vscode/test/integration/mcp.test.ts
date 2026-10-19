/**
 * The MCP listing, over a real child process.
 *
 * The host is the scripted one, so what is pinned is the wire: the sources the
 * client sends, the shape the panel reads back, and the two failures — a
 * malformed file and a source the host does not know — arriving as errors
 * rather than as an empty listing that would read as "nothing configured".
 */

import { strict as assert } from 'node:assert';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { TestContext, test } from 'node:test';
import { McpListResult, Method } from '../../src/protocol';
import { startFakeHost } from '../helpers/harness';

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

test('the project layer is listed with each server and its file', async (t) => {
  const { dir, echo, harness } = await scratch(t, { FAKE_HOST_MCP: 'files:stdio,docs:http' });
  const source = path.join(dir, '.cursor', 'mcp.json');

  const listed = await harness.client.request<McpListResult>(Method.McpList, {
    setting_sources: ['project'],
  });
  assert.equal(listed.servers.length, 2);
  assert.deepEqual(listed.servers[0], { name: 'files', transport: 'stdio', source });
  assert.deepEqual(listed.servers[1], { name: 'docs', transport: 'http', source });
  assert.deepEqual(listed.sources, [{ source: 'project', path: source, exists: true }]);

  // The panel builds this request from the same setting that configures the
  // session, so the key and its values are part of the contract.
  const call = recorded(echo)
    .filter((entry) => entry.method === Method.McpList)
    .pop();
  assert.deepEqual(call?.params, { setting_sources: ['project'] });
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

test('a malformed file is an error, not an empty listing', async (t) => {
  const { harness } = await scratch(t, { FAKE_HOST_MCP_BROKEN: '1' });

  await assert.rejects(
    harness.client.request(Method.McpList, { setting_sources: ['project'] }),
    /parse .*mcp\.json/,
  );
});

test('an unknown source is refused by name', async (t) => {
  const { harness } = await scratch(t);

  await assert.rejects(
    harness.client.request(Method.McpList, { setting_sources: ['galaxy'] }),
    /unknown setting source: galaxy/,
  );
});
