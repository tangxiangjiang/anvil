/**
 * Test scaffolding: drives the extension's own client and session against the
 * scripted host in `test/fixtures/fake-host.mjs`.
 *
 * The tests use the real `HostClient` (a real child process, real pipes, real
 * framing) and the real `ChatSession`. Only the host is a stand-in, so a
 * failure here is a failure in the extension's code or in the protocol shape it
 * assumes — not in a mock's idea of what the extension does.
 */

import { ChildProcess } from 'node:child_process';
import * as path from 'node:path';
import { ApprovalRequest, Decision, DoneParams, HostEvent } from '../../src/protocol';
import { HostClient } from '../../src/hostClient';
import { ChatSession, SessionConfig } from '../../src/session';

/** The scripted host. Spawned with the same Node that runs the tests. */
export const FIXTURE_HOST = path.resolve(__dirname, '../../..', 'test', 'fixtures', 'fake-host.mjs');

export type Scenario = 'chat' | 'approval' | 'diff' | 'slow' | 'fail' | 'silent';

export interface Harness {
  client: HostClient;
  session: ChatSession;
  /** Everything the client logged, including the child's stderr. */
  log: string[];
  events: HostEvent[];
  approvals: ApprovalRequest[];
  withdrawn: string[];
  dones: Array<{ done: DoneParams; failure: { kind: string; message: string; isError: boolean } }>;
  states: Array<{ state: string; runId?: string }>;
  /** The child, for assertions about its lifetime. */
  child: ChildProcess;
  stop(): Promise<void>;
}

export interface HarnessOptions {
  config?: SessionConfig;
  /** Awaited before the session is wired up, to observe a notification race. */
  requestTimeoutMs?: number;
  /** Working directory of the host child; tests that touch files set this. */
  cwd?: string;
  /** Extra environment for the child (FAKE_HOST_ECHO, FAKE_HOST_FILE, …). */
  env?: NodeJS.ProcessEnv;
}

/** Starts a scripted host and opens a session on it. */
export async function startFakeHost(scenario: Scenario, options: HarnessOptions = {}): Promise<Harness> {
  const log: string[] = [];
  const events: HostEvent[] = [];
  const approvals: ApprovalRequest[] = [];
  const withdrawn: string[] = [];
  const dones: Harness['dones'] = [];
  const states: Array<{ state: string; runId?: string }> = [];

  const client = new HostClient({
    command: process.execPath,
    args: [FIXTURE_HOST],
    cwd: options.cwd ?? process.cwd(),
    env: { ...process.env, FAKE_HOST_SCENARIO: scenario, ...options.env },
    log: (line) => log.push(line),
    requestTimeoutMs: options.requestTimeoutMs ?? 5_000,
  });

  const session = new ChatSession(client, options.cwd ?? process.cwd(), options.config ?? {}, {
    onEvent: (event) => events.push(event),
    onApproval: (request) => approvals.push(request),
    onApprovalWithdrawn: (id) => withdrawn.push(id),
    onDone: (done, failure) => dones.push({ done, failure }),
    onState: (state, runId) => states.push({ state, runId }),
    onLog: (line) => log.push(line),
  });
  client.setNotificationHandler((method, params) => session.handle(method, params));

  await client.start();
  await client.initialize(options.cwd ?? process.cwd(), { name: 'anvil-vscode-test', version: '0.0.0' });
  await session.open();

  return {
    client,
    session,
    log,
    events,
    approvals,
    withdrawn,
    dones,
    states,
    child: client.process as ChildProcess,
    stop: () => client.stop(),
  };
}

/** Polls until `predicate` holds, or fails the test with `what` unfinished. */
export async function waitFor(
  predicate: () => boolean,
  what: string,
  timeoutMs = 4_000,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
  throw new Error(`timed out waiting for ${what}`);
}

/** Answers the newest approval the way a user clicking a button would. */
export async function answerNewestApproval(
  harness: Harness,
  decision: Decision,
  scope?: string,
): Promise<boolean> {
  await waitFor(() => harness.approvals.length > 0, 'an approval request');
  const request = harness.approvals[harness.approvals.length - 1];
  return harness.session.respond(request.request_id, decision, scope === undefined ? {} : { scope });
}
