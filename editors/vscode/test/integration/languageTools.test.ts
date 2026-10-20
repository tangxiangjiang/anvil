/**
 * The language-server and command fields over a real child process.
 *
 * What is pinned is the two ends of the same rule: a configured client sends
 * exactly the shape the host maps, and an unconfigured one sends nothing at
 * all. The second case is the one that keeps an older session.new message
 * byte-for-byte unchanged.
 */

import { strict as assert } from 'node:assert';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { TestContext, test } from 'node:test';
import { argv, lspParams } from '../../src/languageTools';
import { Method, SessionParams, SessionResult } from '../../src/protocol';
import { startFakeHost } from '../helpers/harness';

async function scratch(t: TestContext) {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-language-'));
  const echo = path.join(dir, 'echo.jsonl');
  const harness = await startFakeHost('chat', {
    cwd: dir,
    env: { FAKE_HOST_ECHO: echo },
  });
  t.after(async () => {
    await harness.stop();
    rmSync(dir, { recursive: true, force: true, maxRetries: 5 });
  });
  return { echo, harness };
}

function lastSessionNew(echo: string): Record<string, unknown> | undefined {
  return readFileSync(echo, 'utf8')
    .split('\n')
    .filter((line) => line !== '')
    .map((line) => JSON.parse(line) as { method: string; params?: Record<string, unknown> })
    .filter((candidate) => candidate.method === Method.SessionNew)
    .pop()?.params;
}

test('a configured session carries lsp, test and build exactly once', async (t) => {
  const { echo, harness } = await scratch(t);

  const params: SessionParams = { model: 'fake-model' };
  const lsp = lspParams(true, [
    { language: 'go', pattern: '**/*.go', command: 'gopls', args: ['-remote=auto'] },
  ]);
  if (lsp) params.lsp = lsp;
  const testCommand = argv(['go', 'test', './...']);
  if (testCommand) params.test_command = testCommand;
  const buildCommand = argv(['go', 'build', './...']);
  if (buildCommand) params.build_command = buildCommand;

  await harness.client.request<SessionResult>(Method.SessionNew, params);

  assert.deepEqual(lastSessionNew(echo), {
    model: 'fake-model',
    lsp: {
      enabled: true,
      servers: [{ language: 'go', pattern: '**/*.go', command: 'gopls', args: ['-remote=auto'] }],
    },
    test_command: ['go', 'test', './...'],
    build_command: ['go', 'build', './...'],
  });
});

test('a session with none of it configured says none of it', async (t) => {
  const { echo, harness } = await scratch(t);

  const params: SessionParams = { model: 'fake-model' };
  const lsp = lspParams(false, [{ language: 'go', command: 'gopls' }]);
  if (lsp) params.lsp = lsp;
  const testCommand = argv([]);
  if (testCommand) params.test_command = testCommand;
  const buildCommand = argv(undefined);
  if (buildCommand) params.build_command = buildCommand;

  await harness.client.request<SessionResult>(Method.SessionNew, params);

  assert.deepEqual(lastSessionNew(echo), { model: 'fake-model' });
});
