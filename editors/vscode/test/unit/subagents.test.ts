/**
 * Subagents and background tasks: the four surfaces that have to agree.
 *
 * A child agent is a stored session, a spawn call is a tool card, a background
 * task is a row in a panel, and a count in the status bar is all of them added
 * up. None of that can be run in this suite — the renderer needs a DOM, the
 * panel needs an extension host, the host needs a model — so what is checked
 * here is the agreement between them, the same static shape as
 * `permissionKinds.test.ts`. Every fault it catches is silent at runtime: a
 * child that renders into its parent's flow, a `/tasks` that goes to the model
 * as a prompt, a panel button the extension never routes, a method name the Go
 * side spells differently.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import { SessionInfo, TaskInfo } from '../../src/protocol';
import { SessionNode, SessionRow, buildSessionView } from '../../src/sessionList';
import { transcriptFor } from '../../src/taskList';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const ROOT = join(EXTENSION_ROOT, '..', '..');
const read = (path: string): string => readFileSync(join(EXTENSION_ROOT, path), 'utf8');
const CHAT_JS = read('media/chat.js');
const CHAT_CSS = read('media/chat.css');
const CHAT_PANEL = read('src/chatPanel.ts');
const HUB = read('src/hub.ts');
const PROTOCOL_TS = read('src/protocol.ts');
const STATUS_BAR = read('src/statusBar.ts');
const TASKS_JS = read('media/tasks.js');
const TASKS_PANEL = read('src/tasksPanel.ts');
const TASK_LIST = read('src/taskList.ts');
const PACKAGE_JSON = JSON.parse(read('package.json')) as {
  contributes: {
    commands: Array<{ command: string }>;
    configuration: { properties: Record<string, { default?: unknown }> };
  };
};
const PROTOCOL_GO = readFileSync(join(ROOT, 'source', 'internal', 'editorhost', 'proto.go'), 'utf8');
const SUBAGENT_GO = readFileSync(join(ROOT, 'source', 'subagent.go'), 'utf8');

/** A top-level function of the renderer, from its declaration to the next one. */
function bodyOf(source: string, name: string): string {
  const start = source.indexOf(`\n  function ${name}(`);
  assert.ok(start >= 0, `${name} is in chat.js`);
  const rest = source.slice(start + 1);
  const next = rest.indexOf('\n  function ');
  return next >= 0 ? rest.slice(0, next) : rest;
}

function session(agentId: string, extra: Partial<SessionInfo> = {}): SessionInfo {
  return {
    agent_id: agentId,
    cwd: 'E:\\work',
    provider: 'openai',
    model: 'gpt-5.2',
    runs: 1,
    ...extra,
  };
}

function rows(nodes: SessionNode[]): SessionRow[] {
  return nodes.filter((node): node is SessionRow => node.kind === 'session');
}

test('a subagent session hangs under the agent that spawned it', () => {
  const view = buildSessionView(
    [
      session('agent-parent'),
      session('agent-child', {
        parent_agent_id: 'agent-parent',
        subagent: true,
        depth: 1,
        task_title: 'Read the parser',
      }),
      session('agent-grandchild', {
        parent_agent_id: 'agent-child',
        subagent: true,
        depth: 2,
        task_title: 'Check one function',
      }),
    ],
    { now: Date.parse('2026-10-19T12:00:00Z') },
  );

  // One root, because the children are the parent's story. Their names are the
  // descriptions from the spawn calls, which is what the person saw.
  const roots = rows(view);
  assert.equal(roots.length, 1);
  assert.equal(roots[0].agentId, 'agent-parent');
  const child = roots[0].children[0];
  assert.equal(child.agentId, 'agent-child');
  assert.equal(child.label, 'Read the parser');
  // Nesting is a walk, not one level: a grandchild is under its own parent.
  assert.equal(child.children[0].agentId, 'agent-grandchild');
  assert.match(child.tooltip, /Subagent of agent-parent/);
  assert.match(child.tooltip, /Task Read the parser/);
});

test('a child whose parent is gone is a root, not a disappearance', () => {
  const view = buildSessionView(
    [
      session('agent-orphan', {
        parent_agent_id: 'agent-deleted',
        subagent: true,
        task_title: 'Left behind',
      }),
      // A malformed parent pointer must not make a cycle into an infinite walk,
      // and a session that names itself as its parent is simply a root.
      session('agent-self', { parent_agent_id: 'agent-self', subagent: true }),
    ],
    { now: Date.parse('2026-10-19T12:00:00Z') },
  );
  const roots = rows(view);
  assert.deepEqual(
    roots.map((row) => row.agentId).sort(),
    ['agent-orphan', 'agent-self'],
  );
});

test('a background agent task is matched to the child session it wrote', () => {
  const task: TaskInfo = {
    id: 'task-1',
    kind: 'agent',
    // The host prefixes the row with "subagent "; the stored title is the bare
    // description, so the two are only equal after the prefix is removed.
    title: 'subagent Read the parser',
    status: 'running',
    agent_id: 'agent-parent',
    started_at: '2026-10-19T12:00:00Z',
    exit_code: 0,
  };
  const sessions: SessionInfo[] = [
    session('agent-parent'),
    session('agent-child', {
      parent_agent_id: 'agent-parent',
      subagent: true,
      task_title: 'Read the parser',
    }),
    // Same description, another parent: not this task's child.
    session('agent-other', {
      parent_agent_id: 'agent-someone-else',
      subagent: true,
      task_title: 'Read the parser',
    }),
  ];

  assert.equal(transcriptFor(task, sessions), 'agent-child');
  // Nothing to open is a normal answer, not an error.
  assert.equal(transcriptFor({ ...task, title: 'subagent Something else' }, sessions), undefined);
  // A shell task never has a transcript, even with a matching title.
  assert.equal(transcriptFor({ ...task, kind: 'shell', title: 'Read the parser' }, sessions), undefined);
  assert.equal(transcriptFor({ ...task, agent_id: undefined }, sessions), undefined);
});

test('a spawn call opens a card whose body is its child conversation', () => {
  const call = bodyOf(CHAT_JS, 'addToolCall');
  assert.match(call, /const spawn = event\.name === 'spawn_agent'/);
  assert.match(call, /el\('details', 'tool' \+ \(spawn \? ' spawn' : ''\)\)/);
  // The brief is shown as the call's input, under its own label.
  assert.match(call, /section\('task', 'TASK', brief\)/);
  // The child's events append here, and the scope is registered from inside
  // this body — which is what lets a grandchild's spawn register its own.
  assert.match(call, /body = el\('div', 'spawn-body'\)/);
  assert.match(call, /spawnScopes\.set\(event\.tool_call_id, makeScope\(body\)\)/);
  assert.match(call, /card\.open = true/);
});

test("a child's events land in the card its spawn call made", () => {
  const dispatch = /case 'event': \{[\s\S]*?\n {6}\}/.exec(CHAT_JS);
  assert.ok(dispatch, 'the event case is in chat.js');
  const body = dispatch[0];
  // The route is the parent call's id, which the host stamps on every child
  // event. The child's own id is remembered for the transcript button.
  assert.match(body, /e\.parent_tool_call_id \? spawnScopes\.get\(e\.parent_tool_call_id\)/);
  assert.match(body, /target\.agentId = e\.agent_id/);
  assert.match(body, /inScope\(target, \(\) => renderEvent\(e, msg\)\)/);
  // The renderer was split out so the route could choose a scope; without the
  // fallback, a replayed child's events would vanish instead of landing at the
  // top level.
  assert.match(body, /renderEvent\(e, msg\);/);
  assert.match(CHAT_JS, /function renderEvent\(e, msg\) \{/);
  // Replacing the transcript drops the scopes with it: a card that no longer
  // exists cannot receive a child's events.
  assert.match(CHAT_JS, /spawnScopes\.clear\(\);\s*active = mainScope;/);
});

test('a spawn result settles the card, whatever the child did', () => {
  const result = bodyOf(CHAT_JS, 'addSpawnResult');
  // The two lines the SDK writes are what tell foreground from background.
  assert.match(result, /finished in \(\\d\+\) steps\?\\\./);
  assert.match(result, /started in the background/);
  // A background spawn keeps running, so the card offers the one surface that
  // can now tell the truth about it.
  assert.match(result, /type: 'openTasks'/);
  // Both routes open the stored child, which is a session like any other.
  assert.match(CHAT_JS, /function addSpawnTranscriptButton\(pane, agentId\)/);
  assert.match(bodyOf(CHAT_JS, 'addSpawnTranscriptButton'), /type: 'openAgentTranscript'/);
  assert.match(result, /state\.textContent = failed \? 'failed'|state\.textContent = 'failed'/);
  // A replayed spawn has no child records; the result's own text is the only
  // copy, and it is shown rather than an empty body pretending success.
  assert.match(CHAT_JS, /function addSpawnResult\(card, event\) \{/);
  const toolResult = bodyOf(CHAT_JS, 'addToolResult');
  assert.match(toolResult, /if \(event\.tool_call_id && spawnScopes\.has\(event\.tool_call_id\)\) \{/);
});

test('the spawn card and its nested body are styled', () => {
  assert.match(CHAT_CSS, /\.tool\.spawn \{/);
  assert.match(CHAT_CSS, /\.tool \.spawn-body \{/);
  assert.match(CHAT_CSS, /\.tool \.spawn-actions \{/);
  assert.match(CHAT_CSS, /\.tool \.spawn-action \{/);
});

test('/tasks opens the table instead of becoming a prompt', () => {
  const submit = /composer\.addEventListener\('submit', \(event\) => \{[\s\S]*?\r?\n {2}\}\);\r?\n/.exec(CHAT_JS);
  assert.ok(submit, 'the submit handler is in chat.js');
  const body = submit[0];
  const command = body.indexOf("if (text === '/tasks')");
  const running = body.indexOf("if (text === '' || running) return;");
  assert.ok(command >= 0, 'the composer intercepts exactly /tasks');
  // Before the running guard: the table is most useful during a turn.
  assert.ok(running > command, '/tasks is checked before the running guard');
  const block = body.slice(command, running);
  assert.match(block, /vscode\.postMessage\(\{ type: 'openTasks' \}\)/);
  // The text is consumed, but nothing is echoed, recorded, or sent: it is a
  // command, so it must not become a turn in the transcript or the history.
  assert.doesNotMatch(block, /historyMath\.record/);
  assert.doesNotMatch(block, /type: 'send'/);
  assert.doesNotMatch(block, /addUser/);
  // The palette's `/tasks` row is a built-in, so typing it after `/` finds it.
  assert.match(CHAT_PANEL, /case 'tasks':/);
  assert.match(CHAT_PANEL, /this\.hub\.openTasks\(\)/);
});

test('the panel and the tab route the two buttons a spawn card offers', () => {
  assert.match(CHAT_PANEL, /case 'openTasks':[\s\S]*?this\.hub\.openTasks\(\)/);
  assert.match(CHAT_PANEL, /case 'openAgentTranscript':[\s\S]*?this\.hub\.openSession\(msg\.agentId\)/);
  // The window owns one host, so the count is per session id and the panel
  // merges the live sessions rather than one tab's own table.
  assert.match(HUB, /noteTask\(sessionId: string, task: TaskInfo\)/);
  assert.match(HUB, /this\.manager\.liveSessions\(\)/);
  assert.match(HUB, /CodeNoSession/);
  assert.match(STATUS_BAR, /hub\.onTasksChanged\(\(\) => this\.refreshTasks\(\)\)/);
  assert.match(STATUS_BAR, /this\.hub\.runningTaskCount/);
});

test('the task wire names are the ones both halves spell', () => {
  const methods = /export const Method = \{([\s\S]*?)\r?\n\} as const;/.exec(PROTOCOL_TS)?.[1];
  assert.ok(methods, 'Method is in protocol.ts');
  for (const name of ['tasks.list', 'tasks.output', 'tasks.cancel']) {
    assert.match(methods, new RegExp(`'${name}'`), `${name} in protocol.ts`);
    assert.match(PROTOCOL_GO, new RegExp(`"${name}"`), `${name} in proto.go`);
  }
  assert.match(PROTOCOL_GO, /NotifyTask = "task"/);
  assert.match(PROTOCOL_TS, /Task: 'task'/);
  // The fields the panel reads off a row are fields the host sends.
  for (const field of ['agent_id', 'started_at', 'ended_at', 'exit_code']) {
    assert.match(PROTOCOL_GO, new RegExp(`json:"${field}`), `${field} in proto.go`);
    assert.match(TASKS_PANEL, new RegExp(`\\.${field}\\b`), `${field} read in tasksPanel.ts`);
  }
  // The two result shapes the renderer parses are the two the SDK writes.
  assert.match(SUBAGENT_GO, /%s finished in %d steps\./);
  assert.match(SUBAGENT_GO, /%s started in the background/);
});

test('the task panel and the spawn card render output as text only', () => {
  for (const [name, source] of [
    ['tasksPanel.ts', TASKS_PANEL],
    ['tasks.js', TASKS_JS],
    ['taskList.ts', TASK_LIST],
  ] as const) {
    assert.doesNotMatch(source, /\.innerHTML\s*=|insertAdjacentHTML/, `${name} never writes markup`);
  }
  // The document's one text sink is `textContent`, and a task's output is
  // written there even though a `<pre>` would happily parse HTML.
  assert.match(TASKS_JS, /outputTextEl\.textContent = msg\.output/);
  // The panel is a second entry point into the host, so it checks trust itself
  // rather than relying on the tab that opened it.
  const handle = /private async handle\(raw: unknown\): Promise<void> \{([\s\S]*?)\n {2}\}/.exec(TASKS_PANEL);
  assert.ok(handle, 'the message handler is in tasksPanel.ts');
  assert.match(handle[1], /HostManager\.assertTrusted\(\)/);
});

test('the setting and the command exist for the two new surfaces', () => {
  const commands = PACKAGE_JSON.contributes.commands.map((entry) => entry.command);
  assert.ok(commands.includes('anvil.openTasks'));
  const subagents = PACKAGE_JSON.contributes.configuration.properties['anvil.subagents'];
  assert.ok(subagents, 'anvil.subagents is a setting');
  assert.equal(subagents.default, true);
});
