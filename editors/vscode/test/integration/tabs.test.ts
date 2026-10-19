/**
 * Two conversations, one host child.
 *
 * This is the rule the whole multi-tab design rests on: a notification that
 * names session B never reaches the listener of session A. It is asserted over
 * the real pipe, with the real `HostClient` and the real `ChatSession`, and with
 * the same `SessionRouter` the extension installs — because the failure this
 * guards against is silent: a second tab that quietly receives the first one's
 * approvals looks like a working tab until somebody clicks Allow on the wrong
 * conversation.
 */

import { strict as assert } from 'node:assert';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { TestContext, test } from 'node:test';
import { HostClient } from '../../src/hostClient';
import {
  ApprovalRequest,
  DoneParams,
  HostEvent,
  Notify,
  SessionResult,
} from '../../src/protocol';
import { SessionRouter } from '../../src/router';
import { ChatSession, SessionConfig } from '../../src/session';
import { FIXTURE_HOST, waitFor } from '../helpers/harness';

/** What one tab recorded, so the two can be compared. */
interface Tab {
  session: ChatSession;
  result: SessionResult;
  events: HostEvent[];
  approvals: ApprovalRequest[];
  dones: DoneParams[];
}

interface Window {
  client: HostClient;
  router: SessionRouter<ChatSession>;
  log: string[];
  /** Opens one more tab on the same child. */
  open(config?: SessionConfig): Promise<Tab>;
}

/**
 * A window with one child and however many tabs a test opens.
 *
 * It reproduces what `HostManager` does, without the parts that need `vscode`:
 * one client, one router, and each session registered under the id the host
 * answered with.
 */
async function window(t: TestContext, scenario: string, env: Record<string, string> = {}): Promise<Window> {
  const dir = mkdtempSync(path.join(tmpdir(), 'anvil-tabs-'));
  const log: string[] = [];
  const client = new HostClient({
    command: process.execPath,
    args: [FIXTURE_HOST],
    cwd: dir,
    env: { ...process.env, FAKE_HOST_SCENARIO: scenario, ...env },
    log: (line) => log.push(line),
    requestTimeoutMs: 5_000,
  });
  const router = new SessionRouter<ChatSession>();
  client.setNotificationHandler((method, params) => router.route(method, params, (line) => log.push(line)));

  await client.start();
  await client.initialize(dir, { name: 'anvil-vscode-test', version: '0.0.0' });

  t.after(async () => {
    await client.stop();
    rmSync(dir, { recursive: true, force: true, maxRetries: 5 });
  });

  return {
    client,
    router,
    log,
    async open(config: SessionConfig = {}): Promise<Tab> {
      const events: HostEvent[] = [];
      const approvals: ApprovalRequest[] = [];
      const dones: DoneParams[] = [];
      let result: SessionResult | undefined;
      const session = new ChatSession(client, dir, config, {
        onEvent: (event) => events.push(event),
        onApproval: (request) => approvals.push(request),
        onDone: (done) => dones.push(done),
        onSession: (opened) => {
          result = opened;
          router.register(opened.session_id, session);
        },
        onLog: (line) => log.push(line),
      });
      await session.open();
      if (!result) throw new Error('the host did not describe the session it opened');
      return { session, result, events, approvals, dones };
    },
  };
}

// `t.after` above stops the child and removes the scratch directory.

test('two tabs run at once, and neither hears the other', async (t) => {
  const w = await window(t, 'approval');
  const a = await w.open();
  const b = await w.open();

  assert.notEqual(a.result.session_id, b.result.session_id);
  assert.notEqual(a.result.agent_id, b.result.agent_id);
  assert.equal(w.router.size, 2);

  // Both prompts are accepted while the other is in flight: the fake host
  // refuses a *session's* second run, not the process's.
  await Promise.all([a.session.send('write the first file'), b.session.send('write the second file')]);
  await waitFor(() => a.approvals.length === 1 && b.approvals.length === 1, 'one approval each');

  // Each tab's events name its own run, and its own session.
  assert.ok(a.events.length > 0);
  assert.ok(b.events.length > 0);
  for (const event of a.events) assert.equal(event.session_id, a.result.session_id);
  for (const event of b.events) assert.equal(event.session_id, b.result.session_id);

  // Approvals are the ones that would be catastrophic to mix up: answering one
  // tab's card must not settle anything in the other.
  assert.equal(a.approvals[0].session_id, a.result.session_id);
  assert.equal(b.approvals[0].session_id, b.result.session_id);

  await a.session.respond(a.approvals[0].request_id, 'allow');
  await waitFor(() => a.dones.length === 1, "tab A's run to finish");
  assert.equal(a.dones[0].status, 'finished');
  // B is still waiting on its own card.
  assert.equal(b.dones.length, 0);
  assert.equal(b.session.currentState, 'running');

  await b.session.respond(b.approvals[0].request_id, 'allow');
  await waitFor(() => b.dones.length === 1, "tab B's run to finish");
  assert.equal(b.dones[0].status, 'finished');
});

test('closing one tab leaves the other running', async (t) => {
  const w = await window(t, 'approval');
  const a = await w.open();
  const b = await w.open();

  await a.session.send('one');
  await waitFor(() => a.approvals.length === 1, "tab A's approval");

  await w.router.unregister(a.session);
  await a.session.close();
  assert.equal(w.router.size, 1);

  // A prompt still lands on B, and the host has forgotten only A.
  await b.session.send('two');
  await waitFor(() => b.approvals.length === 1, "tab B's approval");
  assert.equal(b.approvals[0].session_id, b.result.session_id);

  await b.session.respond(b.approvals[0].request_id, 'allow');
  await waitFor(() => b.dones.length === 1, "tab B's run to finish");
});

test('a re-opened session replaces its old registration rather than adding one', async (t) => {
  const w = await window(t, 'chat');
  const a = await w.open();
  const first = a.result.session_id;

  // A mode change closes the session and resumes the same agent, which is a new
  // host session id for the same tab. Two keys for one tab would leave the old
  // one reachable and every notification on it logged as a drop.
  await a.session.reopen({ permission_mode: 'ask' });
  assert.notEqual(a.session.id, first);
  assert.equal(w.router.size, 1);
  assert.deepEqual(
    w.router.sessions().map((session) => session.id),
    [a.session.id],
  );
});

test('an unkeyed notification is logged and dropped, not broadcast', async (t) => {
  const w = await window(t, 'chat');
  const a = await w.open();
  const b = await w.open();

  // This is the shape a notification takes when it names no session. The real
  // host keys everything but its log lines; broadcasting one would put a
  // stranger's event into every tab, because `ChatSession` reads an absent id
  // as "mine".
  w.router.route(Notify.Event, { type: 'assistant', text: 'not mine' }, (line) => w.log.push(line));
  assert.equal(a.events.length, 0);
  assert.equal(b.events.length, 0);
  assert.ok(w.log.some((line) => line.includes(`dropped an unkeyed ${Notify.Event} notification`)));
});

test('a notification for a closed session is dropped', async (t) => {
  const w = await window(t, 'chat');
  const a = await w.open();
  await w.router.unregister(a.session);

  w.router.route(Notify.Done, { session_id: a.result.session_id, status: 'finished' }, (line) =>
    w.log.push(line),
  );
  assert.equal(a.dones.length, 0);
  assert.ok(w.log.some((line) => line.includes(`notification for closed session ${a.result.session_id}`)));
});
