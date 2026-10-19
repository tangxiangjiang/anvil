/**
 * Resuming a conversation, and the two catalogue calls the panel makes.
 *
 * The distinction this file exists for: `session.close` followed by
 * `session.resume` is a *new* session on the host carrying the *old* agent, so
 * an option can change without the conversation being thrown away. That is the
 * mechanism every mode switch, model change and skill selection in the panel
 * rests on, and it is exactly the thing a unit test with a table of answers
 * cannot prove — that the host really does answer a resume under the id it was
 * given, over the real pipe.
 */

import { strict as assert } from 'node:assert';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { TestContext, test } from 'node:test';
import { Harness, startFakeHost, waitFor } from '../helpers/harness';
import { commandCatalogue, route } from '../../src/commands';
import { Method, RuleFileInfo, SkillEntry } from '../../src/protocol';
import { SessionConfig } from '../../src/session';

interface Recorded {
  method: string;
  params: Record<string, unknown>;
}

interface Fixture {
  echo: string;
  harness: Harness;
}

/** A scripted host on its own scratch directory, recording what it is asked. */
async function host(
  t: TestContext,
  config: SessionConfig = {},
  env: Record<string, string> = {},
): Promise<Fixture> {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-sessions-'));
  const echo = path.join(dir, 'echo.jsonl');
  const harness = await startFakeHost('chat', {
    cwd: dir,
    env: { FAKE_HOST_ECHO: echo, ...env },
    config,
  });
  t.after(async () => {
    await harness.stop();
    rmSync(dir, { recursive: true, force: true, maxRetries: 5 });
  });
  return { echo, harness };
}

function readEcho(file: string): Recorded[] {
  return readFileSync(file, 'utf8')
    .split('\n')
    .filter((line) => line.trim() !== '')
    .map((line) => JSON.parse(line) as Recorded);
}

test('a resumed conversation keeps its agent and its options come from the new session', async (t) => {
  const { echo, harness } = await host(t, { permission_mode: 'ask', model: 'small' });
  await harness.session.send('first question');
  await waitFor(() => harness.dones.length === 1, 'the first run to finish');

  const agent = harness.session.agent;
  assert.ok(agent && agent.length > 0);
  await harness.session.continueWith(agent);

  // The host answered the resume with the agent it was asked for: a host that
  // started a fresh agent here would silently fork the conversation.
  assert.equal(harness.session.agent, agent);
  const resumed = readEcho(echo).filter((record) => record.method === 'session.resume');
  assert.equal(resumed.length, 1);
  assert.equal(resumed[0].params.agent_id, agent);

  // And the conversation continues: another prompt lands on the new session.
  await harness.session.send('second question');
  await waitFor(() => harness.dones.length === 2, 'the second run to finish');
  const methods = readEcho(echo).map((record) => record.method);
  assert.deepEqual(methods, ['session.new', 'prompt', 'session.resume', 'prompt']);
});

test('changing the mode re-opens the same agent under the new mode', async (t) => {
  const { echo, harness } = await host(t, { permission_mode: 'ask' });
  await harness.session.send('look at main.go');
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  const agent = harness.session.agent;
  await harness.session.reopen({ permission_mode: 'plan' });

  const resumed = readEcho(echo).find((record) => record.method === 'session.resume');
  assert.equal(resumed?.params.agent_id, agent);
  assert.equal(resumed?.params.permission_mode, 'plan');
  // The host echoes the mode it built the session with, which is what the
  // panel reports back to the select.
  assert.equal(harness.session.currentState, 'idle');
});

test('the session listing carries what the picker shows', async (t) => {
  const { harness } = await host(t);
  await harness.session.send('anything');
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  const listing = await harness.client.request<{
    sessions: Array<{
      agent_id: string;
      title?: string;
      runs: number;
      last_status?: string;
      usage?: { total_tokens: number };
    }>;
  }>(Method.SessionList, {});

  assert.ok(listing.sessions.length >= 1);
  const row = listing.sessions[listing.sessions.length - 1];
  assert.equal(typeof row.agent_id, 'string');
  assert.equal(row.runs, 1);
  assert.equal(row.last_status, 'finished');
  // Cumulative usage is what makes the listing worth showing at all: a row that
  // could not say what a past session cost would be a row nobody picks.
  assert.ok((row.usage?.total_tokens ?? 0) > 0);
  // A session nobody prompted carries no title *key*, which is what the sidebar
  // falls back to the agent id on.
  assert.equal(row.title, undefined);
});

test('a prompted session is listed under the first thing that was typed', async (t) => {
  const { harness } = await host(t, {}, { FAKE_HOST_TITLE: '看一下这是个什么工程' });
  await harness.session.send('anything');
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  const listing = await harness.client.request<{ sessions: Array<{ title?: string }> }>(
    Method.SessionList,
    {},
  );
  const row = listing.sessions[listing.sessions.length - 1];
  assert.equal(row.title, '看一下这是个什么工程');
});

test('renaming and deleting are agent-keyed, and the listing follows both', async (t) => {
  const { echo, harness } = await host(t, {}, { FAKE_HOST_STORED: 'agent-stored-1' });
  await harness.session.send('anything');
  await waitFor(() => harness.dones.length === 1, 'the run to finish');
  const agent = harness.session.agent;
  assert.ok(agent);

  const renamed = await harness.client.request<{ agent_id: string; title: string }>(
    Method.SessionRename,
    { agent_id: agent, title: '  Target work  ' },
  );
  assert.deepEqual(renamed, { agent_id: agent, title: 'Target work' });
  // Rename is keyed by agent, like the listing and the transcript: the session
  // need not be open for the conversation it names to be renamed.
  const recorded = readEcho(echo).find((record) => record.method === 'session.rename');
  assert.deepEqual(recorded?.params, { agent_id: agent, title: '  Target work  ' });

  let listing = await harness.client.request<{ sessions: Array<{ agent_id: string; title?: string }> }>(
    Method.SessionList,
    {},
  );
  assert.equal(listing.sessions.find((row) => row.agent_id === agent)?.title, 'Target work');

  await harness.client.request(Method.SessionDelete, { agent_id: 'agent-stored-1' });
  listing = await harness.client.request<{ sessions: Array<{ agent_id: string }> }>(
    Method.SessionList,
    {},
  );
  assert.equal(
    listing.sessions.some((row) => row.agent_id === 'agent-stored-1'),
    false,
    'the deleted session is still listed',
  );
  // The live session next to it is untouched: a delete names one agent.
  assert.ok(listing.sessions.some((row) => row.agent_id === agent));

  // Deleting what is not there is an error, not a silent success: a stale row
  // has to be told it was already gone.
  await assert.rejects(harness.client.request(Method.SessionDelete, { agent_id: 'agent-stored-1' }));
});

test('skills.list turns into commands the composer can honour', async (t) => {
  const { harness } = await host(t);

  const result = await harness.client.request<{ skills: SkillEntry[] }>(Method.SkillsList, {});
  const catalogue = commandCatalogue(result.skills);

  assert.deepEqual(
    catalogue.filter((command) => command.source === 'skill').map((command) => command.name),
    ['commit-style', 'house-rules'],
  );
  // The wire shape and the routing agree: what the catalogue lists is what a
  // typed line resolves to.
  assert.deepEqual(route('/commit-style tidy up', catalogue), {
    kind: 'skill',
    skillId: 'commit-style',
    args: 'tidy up',
  });
});

test('rules.list answers for the layers it was asked about', async (t) => {
  const { harness } = await host(t);

  const projectOnly = await harness.client.request<{ files: RuleFileInfo[] }>(Method.RulesList, {
    load_project: true,
    load_cursor_rules: false,
  });
  assert.deepEqual(projectOnly.files.map((file) => file.path), ['AGENTS.md']);

  const withCursor = await harness.client.request<{ files: RuleFileInfo[] }>(Method.RulesList, {
    load_project: true,
    load_cursor_rules: true,
  });
  assert.deepEqual(
    withCursor.files.map((file) => file.path),
    ['AGENTS.md', '.cursor/rules/style.mdc', '.cursor/rules/huge.mdc'],
  );
  // The state the panel has to be able to say out loud: a rule file the budget
  // never reached is not a rule file the model saw.
  const skipped = withCursor.files[withCursor.files.length - 1];
  assert.equal(skipped.included, false);
  assert.equal(withCursor.files[1].truncated, true);
});
