/*
 * A stand-in `anvil-host`, for the extension's tests.
 *
 * It speaks the real protocol — newline-delimited JSON-RPC 2.0 on stdio — but
 * scripts its runs from FAKE_HOST_SCENARIO instead of calling a model. That is
 * what makes the extension's side testable without a key, a network, or 30
 * seconds of latency, and it pins the wire shape the extension reads.
 *
 * Scenarios:
 *   chat       assistant text, one tool call and its result, done(finished)
 *   approval   a gated write_file: waits for approval.response, then finishes
 *   slow       streams, then waits for `cancel` and reports done(cancelled)
 *   fail       done(error, code=max_steps)
 *   silent     accepts the prompt and never answers (for timeout paths)
 */

import { createInterface } from 'node:readline';

const scenario = process.env.FAKE_HOST_SCENARIO || 'chat';
// A host that answers with a revision the client does not speak, to prove the
// handshake fails loudly instead of misreading every later message.
const protocol = Number(process.env.FAKE_HOST_PROTOCOL || 1);
/** A method to swallow, for the client's timeout path. */
const ignore = process.env.FAKE_HOST_IGNORE || '';

const out = (message) => process.stdout.write(`${JSON.stringify(message)}\n`);
const notify = (method, params) => out({ jsonrpc: '2.0', method, params });
const ok = (id, result) => out({ jsonrpc: '2.0', id, result });
const fail = (id, code, message) => out({ jsonrpc: '2.0', id, error: { code, message } });

const sessions = new Map();
let sessionSeq = 0;
let runSeq = 0;
/** Pending approval responders, by request id. */
const approvals = new Map();
let activeRun = null;

function newSession(params) {
  sessionSeq += 1;
  const id = `s${sessionSeq}`;
  const session = {
    session_id: id,
    agent_id: `a${sessionSeq}`,
    cwd: process.env.FAKE_HOST_CWD || process.cwd(),
    provider: params.provider || 'openai',
    model: params.model || 'fake-model',
    permission_mode: params.permission_mode || 'ask',
  };
  sessions.set(id, session);
  return session;
}

function event(session, run, body) {
  notify('event', { session_id: session.session_id, run_id: run, ...body });
}

function finish(session, run, body) {
  activeRun = null;
  notify('done', { session_id: session.session_id, run_id: run, ...body });
}

/** Drives one scripted run, in the background. */
async function runScript(session, run, message) {
  const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

  if (scenario === 'silent') return;

  if (scenario === 'fail') {
    await wait(5);
    finish(session, run, { status: 'error', err: 'the agent hit its step limit', code: 'max_steps', steps: 12 });
    return;
  }

  if (scenario === 'slow') {
    event(session, run, { type: 'assistant', text: 'working' });
    activeRun = { session, run, cancelled: false, cancelledPromise: null };
    activeRun.cancelledPromise = new Promise((resolve) => {
      activeRun.markCancelled = resolve;
    });
    await activeRun.cancelledPromise;
    finish(session, run, { status: 'cancelled', steps: 1 });
    return;
  }

  if (scenario === 'approval') {
    const args = message.includes('deny') ? { path: 'denied.txt' } : { path: 'notes.txt', content: 'hello' };
    event(session, run, { type: 'assistant', text: 'I will write the file.' });
    event(session, run, { type: 'tool_call', tool_call_id: 't1', name: 'write_file', args });
    const requestId = `req-${run}`;
    notify('approval.request', {
      request_id: requestId,
      session_id: session.session_id,
      run_id: run,
      tool: 'write_file',
      args,
      preview: { kind: 'summary', path: args.path, summary: `write ${args.path}` },
      decisions: ['allow', 'allow_session', 'deny'],
      timeout_ms: 30_000,
    });
    const answer = await new Promise((resolve) => approvals.set(requestId, resolve));
    if (answer.decision === 'deny') {
      event(session, run, { type: 'tool_result', tool_call_id: 't1', content: 'denied by the user', is_error: true });
      event(session, run, { type: 'assistant', text: 'Understood — I will not write that file.' });
      finish(session, run, { status: 'finished', result: 'ok', steps: 2 });
      return;
    }
    event(session, run, { type: 'tool_result', tool_call_id: 't1', content: 'wrote 5 bytes', is_error: false });
    event(session, run, { type: 'assistant', text: 'Done.' });
    finish(session, run, { status: 'finished', result: 'ok', steps: 3, usage: { input_tokens: 10, output_tokens: 4, total_tokens: 14 } });
    return;
  }

  // chat
  event(session, run, { type: 'progress', text: 'step 1' });
  event(session, run, { type: 'assistant', text: 'Reading ' });
  await wait(2);
  event(session, run, { type: 'assistant', text: 'the file.' });
  event(session, run, { type: 'tool_call', tool_call_id: 't1', name: 'read_file', args: { path: 'README.md' } });
  await wait(2);
  event(session, run, {
    type: 'tool_result',
    tool_call_id: 't1',
    content: '# Project\n'.repeat(400),
    is_error: false,
  });
  event(session, run, { type: 'assistant', text: 'The file is a README.' });
  finish(session, run, {
    status: 'finished',
    result: 'The file is a README.',
    steps: 2,
    usage: { input_tokens: 120, output_tokens: 18, total_tokens: 138 },
  });
}

const reader = createInterface({ input: process.stdin });
const queue = [];
let draining = false;

reader.on('line', (line) => {
  const text = line.trim();
  if (text === '') return;
  let msg;
  try {
    msg = JSON.parse(text);
  } catch {
    out({ jsonrpc: '2.0', id: null, error: { code: -32700, message: 'parse error' } });
    return;
  }
  queue.push(msg);
  void drain();
});

async function drain() {
  if (draining) return;
  draining = true;
  while (queue.length > 0) {
    const msg = queue.shift();
    await handle(msg);
  }
  draining = false;
}

async function handle(msg) {
  if (msg.method === 'initialized' || msg.method === undefined) return;
  if (ignore !== '' && msg.method === ignore) return;
  if (msg.method === 'shutdown') {
    process.exit(0);
  }

  switch (msg.method) {
    case 'initialize':
      ok(msg.id, {
        protocol,
        host: 'anvil-host (fake)',
        cwd: process.env.FAKE_HOST_CWD || process.cwd(),
        capabilities: ['approval', 'sessions', 'store'],
      });
      return;
    case 'session.new':
      ok(msg.id, newSession(msg.params || {}));
      return;
    case 'session.resume':
      ok(msg.id, newSession(msg.params || {}));
      return;
    case 'session.list':
      ok(msg.id, { sessions: [...sessions.values()] });
      return;
    case 'session.close': {
      const id = msg.params && msg.params.session_id;
      if (!sessions.has(id)) {
        fail(msg.id, -32001, 'no such session');
        return;
      }
      sessions.delete(id);
      ok(msg.id, { closed: true });
      return;
    }
    case 'models.list':
      ok(msg.id, { models: [{ id: 'fake-model', provider: 'openai' }] });
      return;
    case 'prompt': {
      const session = sessions.get(msg.params && msg.params.session_id);
      if (!session) {
        fail(msg.id, -32001, 'no such session');
        return;
      }
      if (activeRun) {
        fail(msg.id, -32003, 'a run is already in progress');
        return;
      }
      runSeq += 1;
      const run = `r${runSeq}`;
      activeRun = { session, run, cancelled: false };
      ok(msg.id, { session_id: session.session_id, run_id: run, agent_id: session.agent_id });
      const message = (msg.params && msg.params.message) || '';
      void runScript(session, run, message);
      return;
    }
    case 'cancel': {
      if (!activeRun) {
        fail(msg.id, -32002, 'no run in progress');
        return;
      }
      activeRun.cancelled = true;
      if (activeRun.markCancelled) activeRun.markCancelled();
      ok(msg.id, { cancelled: true });
      return;
    }
    case 'approval.response': {
      const requestId = msg.params && msg.params.request_id;
      const resolve = approvals.get(requestId);
      if (!resolve) {
        fail(msg.id, -32004, 'no pending approval');
        return;
      }
      approvals.delete(requestId);
      ok(msg.id, { accepted: true });
      resolve(msg.params);
      return;
    }
    default:
      fail(msg.id, -32601, `unknown method ${msg.method}`);
  }
}

process.stdin.on('end', () => process.exit(0));
