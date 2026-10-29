import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { buildAgentMap } from '../../src/agentMap';
import { SessionInfo, TaskInfo } from '../../src/protocol';

function session(agent_id: string, parent_agent_id?: string): SessionInfo {
  return {
    agent_id,
    parent_agent_id,
    cwd: '/workspace',
    provider: 'test',
    model: 'model',
    runs: 1,
  };
}

test('agent map contains only the selected root tree', () => {
  const sessions = [
    { ...session('root'), title: 'Main' },
    { ...session('child', 'root'), task_title: 'Research' },
    session('grandchild', 'child'),
    session('other'),
    session('other-child', 'other'),
  ];

  const state = buildAgentMap('root', sessions, [], new Set(['child']));

  assert.deepEqual(
    state.nodes.map((node) => [node.agentId, node.depth, node.status]),
    [
      ['root', 0, 'idle'],
      ['child', 1, 'working'],
      ['grandchild', 2, 'idle'],
    ],
  );
  assert.equal(state.nodes[1].title, 'Research');
  assert.deepEqual(state.counts, { total: 2, working: 1, finished: 0, failed: 0 });
});

test('agent map correlates a background task by exact child id', () => {
  const tasks: TaskInfo[] = [
    {
      id: 'task-1',
      kind: 'agent',
      title: 'Background research',
      status: 'failed',
      agent_id: 'root',
      child_agent_id: 'child',
      started_at: '2026-10-23T10:00:00Z',
      ended_at: '2026-10-23T10:01:00Z',
      exit_code: -1,
    },
  ];

  const state = buildAgentMap('root', [session('root'), session('child', 'root')], tasks);
  const child = state.nodes[1];

  assert.equal(child.status, 'failed');
  assert.equal(child.background, true);
  assert.equal(child.taskId, 'task-1');
  assert.equal(child.createdAt, tasks[0].started_at);
  assert.deepEqual(state.counts, { total: 1, working: 0, finished: 0, failed: 1 });
});

test('agent map breaks malformed parent cycles', () => {
  const state = buildAgentMap(
    'root',
    [session('root', 'child'), session('child', 'root')],
    [],
  );
  assert.deepEqual(state.nodes.map((node) => node.agentId), ['root', 'child']);
});

test('the open conversation model overrides only the root listing', () => {
  const root = { ...session('root'), model: 'stale-model' };
  const child = { ...session('child', 'root'), model: 'child-model' };

  const state = buildAgentMap('root', [root, child], [], new Set(), 'claude-sonnet-5.5');

  assert.equal(state.nodes[0].model, 'claude-sonnet-5.5');
  assert.equal(state.nodes[1].model, 'child-model');
});
