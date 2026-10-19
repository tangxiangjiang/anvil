/**
 * The web-tools request over a real child process.
 *
 * The point of the round trip is what is *not* in it: the credential reaches
 * the host through the environment, so the record of everything the client sent
 * must contain neither key. The params themselves come from the same pure
 * builder `settings.ts` uses, so this is the request a session would really get.
 */

import { strict as assert } from 'node:assert';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { TestContext, test } from 'node:test';
import { Method, SessionParams, SessionResult } from '../../src/protocol';
import { webSearchParams } from '../../src/webSearch';
import { startFakeHost } from '../helpers/harness';

async function scratch(t: TestContext) {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-websearch-'));
  const echo = path.join(dir, 'echo.jsonl');
  const harness = await startFakeHost('chat', {
    cwd: dir,
    env: {
      FAKE_HOST_ECHO: echo,
      // A value distinctive enough that finding it anywhere in the record
      // means it crossed the pipe.
      ANVIL_API_KEY: 'model-secret-do-not-send',
      ANVIL_WEB_SEARCH_API_KEY: 'web-secret-do-not-send',
    },
  });
  t.after(async () => {
    await harness.stop();
    rmSync(dir, { recursive: true, force: true, maxRetries: 5 });
  });
  return { echo, harness };
}

test('the session carries the web tools and neither key', async (t) => {
  const { echo, harness } = await scratch(t);
  const web = webSearchParams({
    enabled: true,
    provider: 'http',
    baseUrl: 'https://search.example.test/v1',
    maxResults: 3,
  });

  await harness.client.request<SessionResult>(Method.SessionNew, {
    model: 'fake-model',
    web_search: web,
  });

  const signed = readFileSync(echo, 'utf8');
  const entry = signed
    .split('\n')
    .filter((line) => line !== '')
    .map((line) => JSON.parse(line) as { method: string; params?: Record<string, unknown> })
    .filter((candidate) => candidate.method === Method.SessionNew)
    .pop();
  assert.ok(entry, 'the host recorded session.new');
  assert.deepEqual(entry.params?.web_search, {
    provider: 'http',
    base_url: 'https://search.example.test/v1',
    max_results: 3,
  });
  // The key is in the environment, not the envelope — the rule the model key
  // has followed since 0.35.
  assert.doesNotMatch(signed, /do-not-send/);
  assert.doesNotMatch(signed, /api_key/);
});

test('a session with the tools off says nothing about them', async (t) => {
  const { echo, harness } = await scratch(t);

  // What `readSettings` does: the field is set only when there is a value.
  const params: SessionParams = { model: 'fake-model' };
  const web = webSearchParams({ enabled: false, provider: 'http', baseUrl: '', maxResults: 0 });
  if (web) params.web_search = web;
  await harness.client.request<SessionResult>(Method.SessionNew, params);

  const entry = readFileSync(echo, 'utf8')
    .split('\n')
    .filter((line) => line !== '')
    .map((line) => JSON.parse(line) as { method: string; params?: Record<string, unknown> })
    .filter((candidate) => candidate.method === Method.SessionNew)
    .pop();
  assert.ok(entry);
  assert.equal('web_search' in (entry.params ?? {}), false);
});
