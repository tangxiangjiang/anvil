/*
 * A stand-in `anvil-host`, for the extension's tests.
 *
 * It speaks the real protocol — newline-delimited JSON-RPC 2.0 on stdio — but
 * scripts its runs from FAKE_HOST_SCENARIO instead of calling a model. That is
 * what makes the extension's side testable without a key, a network, or 30
 * seconds of latency, and it pins the wire shape the extension reads.
 *
 * Scenarios:
 *   chat       coalesced reasoning, Markdown assistant text, one tool call and
 *              its result, done(finished)
 *   approval   a gated write_file: waits for approval.response, then finishes
 *   diff       a gated write_file whose preview carries both bodies, and which
 *              really writes the file when it is allowed (FAKE_HOST_FILE)
 *   slow       streams, then waits for `cancel` and reports done(cancelled)
 *   fail       done(error, code=max_steps)
 *   silent     accepts the prompt and never answers (for timeout paths)
 *   permissions a shell call a rule insisted on asking about: the card carries
 *              that rule and the "always allow" shapes for it, and a
 *              `permissions.add` naming the request settles the card, so the
 *              call runs without an approval.response
 *
 * Environment:
 *   FAKE_HOST_ECHO=<path>  append every session.new / session.resume / prompt
 *                          this host receives to that file as one JSON line
 *                          each, so a test can assert what the client sent
 *   FAKE_HOST_RULES_BROKEN=1  report the workspace rule file as malformed, both
 *                          from permissions.get and from permissions.add
 */

import { appendFileSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { createInterface } from 'node:readline';

const scenario = process.env.FAKE_HOST_SCENARIO || 'chat';
// A host that answers with a revision the client does not speak, to prove the
// handshake fails loudly instead of misreading every later message.
const protocol = Number(process.env.FAKE_HOST_PROTOCOL || 1);
/** A method to swallow, for the client's timeout path. */
const ignore = process.env.FAKE_HOST_IGNORE || '';
/** Where to write the record of what the client asked for, if anywhere. */
const echoPath = process.env.FAKE_HOST_ECHO || '';
/** The file the `diff` scenario edits, relative to this process's cwd. */
const diffFile = process.env.FAKE_HOST_FILE || 'target.txt';
/** Whether the workspace rule file is reported as one that will not compile. */
const rulesBroken = process.env.FAKE_HOST_RULES_BROKEN === '1';
/** The workspace rule file, which `permissions.get/add` are about. */
const rulesPath = join(process.env.FAKE_HOST_CWD || process.cwd(), '.anvil', 'permissions.json');
/** Its rules, as a session that appended them would have them. */
const rules = [];
/** Where the parse error is reported: the place, not a line for the file. */
const rulesProblem = {
  message: 'rule 2: effect "maybe" is not one of allow, ask, deny',
  line: 4,
  column: 12,
};

const out = (message) => process.stdout.write(`${JSON.stringify(message)}\n`);
const notify = (method, params) => out({ jsonrpc: '2.0', method, params });
const ok = (id, result) => out({ jsonrpc: '2.0', id, result });
const fail = (id, code, message) => out({ jsonrpc: '2.0', id, error: { code, message } });

/**
 * Records one inbound request. Written synchronously and in arrival order, so a
 * test that reads the file after a `done` sees the whole conversation.
 */
function echo(method, params) {
  if (echoPath === '') return;
  try {
    appendFileSync(echoPath, `${JSON.stringify({ method, params })}\n`);
  } catch {
    // A record that cannot be written must not take the run down with it.
  }
}

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
    agent_id: params.agent_id || `a${sessionSeq}`,
    cwd: process.env.FAKE_HOST_CWD || process.cwd(),
    provider: params.provider || 'openai',
    model: params.model || 'fake-model',
    permission_mode: params.permission_mode || 'ask',
    // What the real host keeps for a stored agent, so the extension's session
    // list and its /resume picker have something to render.
    created_at: '2026-01-02T03:04:05Z',
    updated_at: '2026-01-02T03:05:06Z',
    last_status: 'finished',
    last_run_id: 'r1',
    runs: 1,
    usage: { input_tokens: 120, output_tokens: 18, total_tokens: 138 },
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

  if (scenario === 'diff') {
    // A single-file write, previewed the way the real host previews one: a
    // unified diff plus both bodies, which is exactly what the extension opens
    // in the editor's diff view. It really writes the file when allowed, so a
    // denial can be checked against the file on disk.
    const before = readIfPresent(diffFile);
    const after = `${before}added by the fake host\n`;
    const args = { path: diffFile, content: after };
    event(session, run, { type: 'assistant', text: 'I will append a line.' });
    event(session, run, { type: 'tool_call', tool_call_id: 't1', name: 'write_file', args });
    const requestId = `req-${run}`;
    notify('approval.request', {
      request_id: requestId,
      session_id: session.session_id,
      run_id: run,
      tool: 'write_file',
      args,
      preview: {
        kind: 'diff',
        path: diffFile,
        diff: unifiedDiff(diffFile, before, after),
        before,
        after,
      },
      decisions: ['allow', 'allow_session', 'deny'],
      timeout_ms: 30_000,
    });
    const answer = await new Promise((resolve) => approvals.set(requestId, resolve));
    if (answer.decision === 'deny') {
      event(session, run, { type: 'tool_result', tool_call_id: 't1', content: 'denied by the user', is_error: true });
      finish(session, run, { status: 'finished', result: 'nothing written', steps: 2 });
      return;
    }
    writeFileSync(diffFile, after);
    event(session, run, { type: 'tool_result', tool_call_id: 't1', content: `wrote ${after.length} bytes`, is_error: false });
    event(session, run, { type: 'assistant', text: 'Done.' });
    finish(session, run, { status: 'finished', result: 'ok', steps: 3 });
    return;
  }

  if (scenario === 'permissions') {
    // What the real host sends when a rule says `ask`: the card names the rule
    // that insisted, and every "always allow" shape that would decide this very
    // command comes with it, already evaluated.
    const command = 'git status -s';
    const args = { command };
    event(session, run, { type: 'assistant', text: 'I will check the working tree.' });
    event(session, run, { type: 'tool_call', tool_call_id: 't1', name: 'shell', args });
    const requestId = `req-${run}`;
    notify('approval.request', {
      request_id: requestId,
      session_id: session.session_id,
      run_id: run,
      tool: 'shell',
      args,
      preview: { kind: 'command', command: ['git', 'status', '-s'] },
      decisions: ['allow', 'allow_session', 'deny'],
      rule: {
        index: 1,
        effect: 'ask',
        tool: 'shell',
        pattern: 'git:*',
        reason: 'show me git before it runs',
        describe: '.anvil/permissions.json rule 1 (ask shell "git:*"): show me git before it runs',
      },
      remember: [
        { kind: 'exact', rule: { effect: 'allow', tool: 'shell', pattern: command } },
        { kind: 'prefix', rule: { effect: 'allow', tool: 'shell', pattern: 'git status:*' } },
        { kind: 'readonly', rule: { effect: 'allow', tool: 'shell', pattern: 'readonly' } },
      ],
      timeout_ms: 30_000,
    });
    const answer = await new Promise((resolve) => approvals.set(requestId, resolve));
    if (answer.decision === 'deny') {
      event(session, run, { type: 'tool_result', tool_call_id: 't1', content: 'denied by the user', is_error: true });
      finish(session, run, { status: 'finished', result: 'nothing ran', steps: 2 });
      return;
    }
    event(session, run, {
      type: 'tool_result',
      tool_call_id: 't1',
      content: 'M file.txt',
      is_error: false,
      // Set only when a rule decided the call, which here is the one written
      // from the card: that is the difference this scenario exists to pin.
      rule: answer.rule,
    });
    finish(session, run, { status: 'finished', result: 'the tree is dirty', steps: 2 });
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
  // Reasoning arrives before the answer of the same round, and coalesced rather
  // than per token, because that is what the real host does. The panel's
  // Thinking block is built from these.
  event(session, run, { type: 'reasoning', text: 'The user wants the README. ' });
  event(session, run, { type: 'reasoning', text: 'I should read it before answering.' });
  event(session, run, { type: 'assistant', text: 'Reading ' });
  await wait(2);
  event(session, run, { type: 'assistant', text: 'the file.\n\n' });
  event(session, run, { type: 'tool_call', tool_call_id: 't1', name: 'read_file', args: { path: 'README.md' } });
  await wait(2);
  event(session, run, {
    type: 'tool_result',
    tool_call_id: 't1',
    content: '# Project\n'.repeat(400),
    is_error: false,
  });
  // Markdown, so the wire-to-UI path for the renderer is covered without a key
  // or a network: a heading, a list and a fenced block, none of which can be
  // produced by the plain-text path this replaced.
  event(session, run, {
    type: 'assistant',
    text: '## The file\n\nIt has:\n\n- a title\n- nothing else\n\n```md\n# Project\n```\n',
  });
  finish(session, run, {
    status: 'finished',
    result: 'The file is a README.',
    steps: 2,
    usage: { input_tokens: 120, output_tokens: 18, total_tokens: 138 },
  });
}

/** Reads a file's body, or "" when it is not there yet (a create). */
function readIfPresent(path) {
  try {
    return readFileSync(path, 'utf8');
  } catch {
    return '';
  }
}

/**
 * A minimal unified diff: one hunk, the whole old body removed and the whole
 * new one added. It is not what the real host produces — that one keeps three
 * lines of context — but it has the same shape, which is all this fixture is
 * here to pin.
 */
function unifiedDiff(path, before, after) {
  if (before === after) return '';
  const oldLines = before === '' ? [] : before.replace(/\n$/, '').split('\n');
  const newLines = after === '' ? [] : after.replace(/\n$/, '').split('\n');
  const lines = [`--- a/${path}`, `+++ b/${path}`, `@@ -1,${oldLines.length} +1,${newLines.length} @@`];
  for (const line of oldLines) lines.push(`-${line}`);
  for (const line of newLines) lines.push(`+${line}`);
  return `${lines.join('\n')}\n`;
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
      echo('session.new', msg.params || {});
      ok(msg.id, newSession(msg.params || {}));
      return;
    case 'session.resume':
      echo('session.resume', msg.params || {});
      ok(msg.id, newSession(msg.params || {}));
      return;
    case 'session.list':
      ok(msg.id, { sessions: [...sessions.values()] });
      return;
    case 'skills.list':
      // Two packages on purpose: one selected by hand, one always-on, which is
      // the pair the extension's catalogue has to render differently.
      ok(msg.id, {
        skills: [
          {
            id: 'commit-style',
            name: 'Commit style',
            description: 'Write commit messages the way this repository does',
            path: '.anvil/skills/commit-style/SKILL.md',
            always: false,
          },
          {
            id: 'house-rules',
            name: 'House rules',
            description: '',
            path: '.anvil/skills/house-rules/SKILL.md',
            always: true,
          },
        ],
      });
      return;
    case 'rules.list': {
      // Answers *what was asked for*: the layers are the session's own, so a
      // result that ignored them would be worse than no list at all. The last
      // entry is one the budget never reached, which is the one state the
      // extension has to show plainly.
      const params = msg.params || {};
      const files = [];
      if (params.load_project !== false) {
        files.push({ path: 'AGENTS.md', bytes: 42, included: true, truncated: false });
      }
      if (params.load_cursor_rules === true) {
        files.push({ path: '.cursor/rules/style.mdc', bytes: 7, included: true, truncated: true });
        files.push({ path: '.cursor/rules/huge.mdc', bytes: 40960, included: false, truncated: false });
      }
      ok(msg.id, { files });
      return;
    }
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
    case 'permissions.get':
      echo('permissions.get', msg.params || {});
      if (rulesBroken) {
        // A result, not an error: the client asked about the state of the file,
        // and a file somebody is halfway through editing is one of its states.
        ok(msg.id, { path: rulesPath, exists: true, rules: [], problem: rulesProblem });
        return;
      }
      ok(msg.id, { path: rulesPath, exists: rules.length > 0, rules });
      return;
    case 'permissions.add': {
      echo('permissions.add', msg.params || {});
      if (rulesBroken) {
        // The same place, in the shape a session failure would carry it.
        fail(msg.id, -32000, `${rulesPath}:${rulesProblem.line}:${rulesProblem.column}: ${rulesProblem.message}`);
        return;
      }
      const params = msg.params || {};
      // An empty effect means allow, as it does in the SDK, and the appended
      // rule lands last — which is where the index it reports points.
      const rule = { effect: 'allow', ...(params.rule || {}) };
      const info = {
        index: rules.length + 1,
        ...rule,
        describe: `.anvil/permissions.json rule ${rules.length + 1} (${rule.effect} ${rule.tool} "${rule.pattern}")`,
      };
      rules.push(info);
      const requestId = params.request_id;
      const resolve = requestId ? approvals.get(requestId) : undefined;
      ok(msg.id, {
        path: rulesPath,
        index: info.index,
        rules,
        decided_by: info,
        approved: resolve !== undefined,
      });
      // The real host answers the approval itself when the file now decides the
      // call, and names the rule on the tool result that follows.
      if (resolve) {
        approvals.delete(requestId);
        resolve({ request_id: requestId, decision: 'allow', rule: info });
      }
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
      echo('prompt', msg.params || {});
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
