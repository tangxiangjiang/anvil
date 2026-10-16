/**
 * Where a session's starting mode comes from, end to end.
 *
 * A mode is not only what the user picked in the select. The workspace's
 * `.anvil/permissions.json` can declare one, and the host resolves the two
 * against each other — which makes "no choice of my own" a state the client
 * has to be able to be in, and a state it has to be able to *show*. Neither
 * half is visible in a unit test: the first is what travels on the wire, and
 * the second is what comes back on it.
 *
 * The panel's own glue is not exercised here (it needs a window); what is
 * exercised is the pair of facts it renders from — the choice the session was
 * opened with, and the resolved mode and source the host answered with.
 */

import { strict as assert } from 'node:assert';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { TestContext, test } from 'node:test';
import { Harness, startFakeHost, waitFor } from '../helpers/harness';
import { Method, PermissionsResult } from '../../src/protocol';
import { SessionConfig } from '../../src/session';

interface Recorded {
  method: string;
  params: Record<string, unknown>;
}

interface Fixture {
  dir: string;
  echo: string;
  harness: Harness;
}

/** A scripted host on its own workspace, recording what it is asked. */
async function host(t: TestContext, options: { config?: SessionConfig; declare?: string } = {}): Promise<Fixture> {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-mode-'));
  const echo = path.join(dir, 'echo.jsonl');
  if (options.declare !== undefined) {
    mkdirSync(path.join(dir, '.anvil'), { recursive: true });
    writeFileSync(path.join(dir, '.anvil', 'permissions.json'), options.declare);
  }
  const harness = await startFakeHost('chat', {
    cwd: dir,
    env: { FAKE_HOST_ECHO: echo },
    config: options.config ?? {},
  });
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

test('a workspace with no stored choice sends no mode, and the file decides', async (t) => {
  // The state the panel is in before anyone touches the select: it has no
  // opinion, so the resolution is entirely the host's to make.
  const { echo, harness } = await host(t, { declare: '{\n  "version": 1,\n  "defaultMode": "plan"\n}\n' });
  await harness.session.send('what would you change?');
  await waitFor(() => harness.dones.length === 1, 'the run to finish');

  const opened = readEcho(echo).find((record) => record.method === 'session.new');
  assert.ok(opened, 'the session was opened');
  // Absent, not empty: an empty string is a mode the host would have to reject,
  // and a key carrying one would say this client had an opinion after all.
  assert.equal('permission_mode' in opened.params, false, 'no mode is claimed');

  // And what came back is where the panel's banner and select get their state:
  // the mode in force, and that the workspace's file is why.
  await waitFor(() => harness.sessions.length === 1, 'the session result');
  const result = harness.sessions[0];
  assert.equal(result.permission_mode, 'plan');
  assert.equal(result.permission_mode_source, 'file');
  assert.equal(result.permission_mode_refused, undefined);
});

test('a workspace that declares nothing opens in ask, from the default', async (t) => {
  const { harness } = await host(t);
  await harness.session.send('hello');
  await waitFor(() => harness.sessions.length === 1, 'the session result');

  // The bottom of the ladder, and named as such: `default` is what tells the
  // panel there is no file to point at, so it shows the mode without a reason.
  assert.equal(harness.sessions[0].permission_mode, 'ask');
  assert.equal(harness.sessions[0].permission_mode_source, 'default');
});

test('a mode the user picked is what is sent, and is what comes back', async (t) => {
  // The other half of the same ruling: when the client does have a choice, it
  // is the choice that wins — even over a file that says otherwise.
  const { echo, harness } = await host(t, {
    config: { permission_mode: 'accept_edits' },
    declare: '{\n  "version": 1,\n  "defaultMode": "plan"\n}\n',
  });
  await harness.session.send('fix the typo');
  await waitFor(() => harness.sessions.length === 1, 'the session result');

  const opened = readEcho(echo).find((record) => record.method === 'session.new');
  assert.equal(opened?.params.permission_mode, 'accept_edits');
  assert.equal(harness.sessions[0].permission_mode, 'accept_edits');
  assert.equal(harness.sessions[0].permission_mode_source, 'request');
});

test('a declared bypass is refused in words, and the session still opens', async (t) => {
  // A project file may not grant the mode that approves everything without
  // showing it. The refusal is not a failure of the session — a workspace whose
  // rules file is wrong still has to be openable — so it travels as a mode,
  // a source, and a sentence.
  const { harness } = await host(t, { declare: '{\n  "version": 1,\n  "defaultMode": "bypass"\n}\n' });
  await harness.session.send('carry on');
  await waitFor(() => harness.sessions.length === 1, 'the session result');

  const result = harness.sessions[0];
  assert.equal(result.permission_mode, 'ask');
  assert.equal(result.permission_mode_source, 'default');
  assert.match(result.permission_mode_refused ?? '', /may not grant/);
  // The sentence has to name the file the user has to edit, or the warning is
  // one they can only act on by guessing.
  assert.match(result.permission_mode_refused ?? '', /\.anvil\/permissions\.json/);
});

test('the rules file reports the mode it declares alongside its rules', async (t) => {
  // The panel reads the file twice — once to list rules, once to know what it
  // declares — and both are about the same file. A half-read file is how a
  // panel ends up showing a mode the file no longer declares.
  const { harness } = await host(t, { declare: '{\n  "version": 1,\n  "defaultMode": "plan"\n}\n' });

  const state = await harness.client.request<PermissionsResult>(Method.PermissionsGet, {});
  assert.equal(state.exists, true);
  assert.equal(state.default_mode, 'plan');
  assert.equal(state.default_mode_refused, undefined);
});
