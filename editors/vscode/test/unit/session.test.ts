import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { HostRpcError } from '../../src/hostClient';
import { ChatSession, Transport } from '../../src/session';
import { ApprovalRequest, CodeNoApproval, DoneParams, HostEvent, Notify, SessionResult } from '../../src/protocol';

/** A transport that answers from a table and records what it was asked. */
class FakeTransport implements Transport {
  readonly calls: Array<{ method: string; params: unknown }> = [];
  private readonly answers = new Map<string, (params: unknown) => unknown>();

  answer(method: string, produce: (params: unknown) => unknown): this {
    this.answers.set(method, produce);
    return this;
  }

  request<T>(method: string, params?: unknown): Promise<T> {
    this.calls.push({ method, params });
    const produce = this.answers.get(method);
    if (!produce) {
      return Promise.reject(new HostRpcError({ code: -32601, message: `no answer for ${method}` }));
    }
    try {
      return Promise.resolve(produce(params) as T);
    } catch (err) {
      return Promise.reject(err as Error);
    }
  }

  notify(): void {
    // The session never notifies; it is worth failing loudly if that changes.
    throw new Error('the session should not send notifications');
  }

  methods(): string[] {
    return this.calls.map((call) => call.method);
  }
}

const SESSION_RESULT: SessionResult = {
  session_id: 's1',
  agent_id: 'a1',
  cwd: '/workspace',
  provider: 'openai',
  model: 'test-model',
  permission_mode: 'ask',
};

interface Built {
  session: ChatSession;
  transport: FakeTransport;
  events: HostEvent[];
  approvals: ApprovalRequest[];
  withdrawn: string[];
  dones: DoneParams[];
  states: string[];
}

function build(config: Record<string, unknown> = {}): Built {
  const transport = new FakeTransport()
    .answer('session.new', () => SESSION_RESULT)
    .answer('prompt', () => ({ session_id: 's1', run_id: 'r1', agent_id: 'a1' }))
    .answer('cancel', () => ({ cancelled: true }))
    .answer('session.close', () => ({ closed: true }));

  const events: HostEvent[] = [];
  const approvals: ApprovalRequest[] = [];
  const withdrawn: string[] = [];
  const dones: DoneParams[] = [];
  const states: string[] = [];

  const session = new ChatSession(transport, '/workspace', config, {
    onEvent: (event) => events.push(event),
    onApproval: (request) => approvals.push(request),
    onApprovalWithdrawn: (id) => withdrawn.push(id),
    onDone: (done) => dones.push(done),
    onState: (state) => states.push(state),
  });

  return { session, transport, events, approvals, withdrawn, dones, states };
}

function event(over: Partial<HostEvent>): HostEvent {
  return { session_id: 's1', run_id: 'r1', type: 'assistant', ...over };
}

function approval(id = 'req-1'): ApprovalRequest {
  return {
    request_id: id,
    session_id: 's1',
    run_id: 'r1',
    tool: 'write_file',
    decisions: ['allow', 'allow_session', 'deny'],
  };
}

test('open() sends the configured session parameters', async () => {
  const built = build({ provider: 'anthropic', model: 'claude-sonnet-5-5', load_rules: true });
  await built.session.open();

  const call = built.transport.calls[0];
  assert.equal(call.method, 'session.new');
  assert.deepEqual(call.params, { provider: 'anthropic', model: 'claude-sonnet-5-5', load_rules: true });
  assert.equal(built.session.id, 's1');
  assert.equal(built.session.agent, 'a1');
});

test('two concurrent opens create one session', async () => {
  const built = build();
  const [a, b] = await Promise.all([built.session.open(), built.session.open()]);
  assert.equal(a.session_id, b.session_id);
  assert.equal(built.transport.methods().filter((m) => m === 'session.new').length, 1);
});

test('a send opens the session first and moves it to running', async () => {
  const built = build();
  const result = await built.session.send('hello');
  assert.deepEqual(built.transport.methods(), ['session.new', 'prompt']);
  assert.deepEqual(built.transport.calls[1].params, { session_id: 's1', message: 'hello' });
  assert.equal(result.run_id, 'r1');
  assert.equal(built.session.currentState, 'running');
  assert.equal(built.session.activeRun, 'r1');
  assert.deepEqual(built.states, ['running']);
});

test('a second send while a turn is running is refused', async () => {
  const built = build();
  await built.session.send('one');
  await assert.rejects(() => built.session.send('two'), /already running/);
  assert.equal(built.transport.methods().filter((m) => m === 'prompt').length, 1);
});

test('an empty prompt is refused before anything is sent', async () => {
  const built = build();
  await assert.rejects(() => built.session.send('   '), /empty/);
  assert.deepEqual(built.transport.methods(), []);
});

test('done returns the session to idle and clears the run', async () => {
  const built = build();
  await built.session.send('go');
  built.session.handle(Notify.Done, {
    session_id: 's1',
    run_id: 'r1',
    status: 'finished',
    steps: 3,
  });

  assert.equal(built.session.currentState, 'idle');
  assert.equal(built.session.activeRun, undefined);
  assert.deepEqual(built.states, ['running', 'idle']);
  assert.equal(built.dones.length, 1);
  assert.equal(built.dones[0].steps, 3);
});

test('events and approvals for the open session reach the handlers', async () => {
  const built = build();
  await built.session.open();
  built.session.handle(Notify.Event, event({ text: 'hi' }));
  built.session.handle(Notify.ApprovalRequest, approval());

  assert.equal(built.events.length, 1);
  assert.equal(built.events[0].text, 'hi');
  assert.equal(built.approvals.length, 1);
  assert.equal(built.session.pendingApprovals().length, 1);
});

test('traffic for another session is ignored', async () => {
  const built = build();
  await built.session.open();
  built.session.handle(Notify.Event, event({ session_id: 'other', text: 'not ours' }));
  built.session.handle(Notify.ApprovalRequest, { ...approval(), session_id: 'other' });
  built.session.handle(Notify.Done, { session_id: 'other', run_id: 'r9', status: 'finished' });

  assert.deepEqual(built.events, []);
  assert.deepEqual(built.approvals, []);
  assert.deepEqual(built.dones, []);
  assert.equal(built.session.currentState, 'idle');
});

test('traffic that arrives before the session exists is dropped, not queued', () => {
  const built = build();
  built.session.handle(Notify.Event, event({ text: 'too early' }));
  assert.deepEqual(built.events, []);
});

test('a run that ends withdraws the prompts it left open', async () => {
  const built = build();
  await built.session.send('go');
  built.session.handle(Notify.ApprovalRequest, approval('req-1'));
  built.session.handle(Notify.ApprovalRequest, approval('req-2'));
  built.session.handle(Notify.Done, { session_id: 's1', run_id: 'r1', status: 'finished' });

  assert.deepEqual(built.withdrawn, ['req-1', 'req-2']);
  assert.deepEqual(built.session.pendingApprovals(), []);
});

test('cancelling is a no-op when nothing is running', async () => {
  const built = build();
  await built.session.cancel();
  assert.deepEqual(built.transport.methods(), []);
});

test('cancel names the run that is actually in flight', async () => {
  const built = build();
  await built.session.send('go');
  await built.session.cancel();
  assert.deepEqual(built.transport.calls[built.transport.calls.length - 1], {
    method: 'cancel',
    params: { session_id: 's1', run_id: 'r1' },
  });
});

test('an answer to a prompt the host already dropped reports false', async () => {
  const built = build();
  built.transport.answer('approval.response', () => {
    throw new HostRpcError({ code: CodeNoApproval, message: 'no pending approval' });
  });
  await built.session.open();
  assert.equal(await built.session.respond('gone', 'allow'), false);

  // The failure is a race the user cannot help, so it must not be thrown at
  // the caller: the panel would show it as a crash.
  built.transport.answer('approval.response', () => ({ accepted: true }));
  assert.equal(await built.session.respond('live', 'allow'), true);
});

test('an answer carries the scope and the edited args', async () => {
  const built = build();
  built.transport.answer('approval.response', () => ({ accepted: true }));
  await built.session.open();
  await built.session.respond('req-1', 'allow_session', { scope: 'src/a.ts', args: { path: 'src/a.ts' } });

  const call = built.transport.calls[built.transport.calls.length - 1];
  assert.deepEqual(call.params, {
    request_id: 'req-1',
    decision: 'allow_session',
    scope: 'src/a.ts',
    args: { path: 'src/a.ts' },
  });
});

test('answering twice sends twice, and lets the host call the second stale', async () => {
  const built = build();
  built.transport.answer('approval.response', () => ({ accepted: true }));
  await built.session.open();
  await built.session.respond('req-1', 'allow');
  await built.session.respond('req-1', 'allow');
  // The duplicate is not the session's to reject: the host owns the pending
  // table, and a client that swallowed the second answer would hide a real
  // double-submit bug.
  assert.equal(built.transport.calls.filter((c) => c.method === 'approval.response').length, 2);
});

test('close() ends the session on both sides', async () => {
  const built = build();
  await built.session.send('go');
  await built.session.close();

  assert.equal(built.session.id, undefined);
  assert.equal(built.session.currentState, 'idle');
  assert.deepEqual(built.transport.calls[built.transport.calls.length - 1], {
    method: 'session.close',
    params: { session_id: 's1' },
  });
});

test('failIfRunning() only ends a turn that was actually running', async () => {
  const built = build();
  // An idle session has no turn to end, so a host that dies between prompts
  // must not produce a failure the user never caused.
  assert.equal(built.session.failIfRunning('the host stopped'), false);
  assert.equal(built.dones.length, 0);

  await built.session.send('go');
  assert.equal(built.session.failIfRunning('the host stopped'), true);
  assert.equal(built.session.currentState, 'idle');
  assert.equal(built.dones.length, 1);
  assert.equal(built.dones[0].status, 'error');
  assert.match(built.dones[0].err ?? '', /host stopped/);

  // And it is not reported twice if the exit handler runs again.
  assert.equal(built.session.failIfRunning('the host stopped'), false);
  assert.equal(built.dones.length, 1);
});

test('fail() presents a dead transport as a finished, failed run', async () => {
  const built = build();
  await built.session.send('go');
  built.session.fail(new Error('the host exited (code 1)'));

  assert.equal(built.session.currentState, 'idle');
  assert.equal(built.dones.length, 1);
  assert.equal(built.dones[0].status, 'error');
  assert.match(built.dones[0].err ?? '', /host exited/);
});
