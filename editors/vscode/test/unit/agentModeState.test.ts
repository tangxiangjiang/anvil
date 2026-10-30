import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import {
  AGENT_MODE_KEY,
  LEGACY_COLLABORATION_KEY,
  ModeState,
  agentModeOptions,
  saveAgentMode,
  storedAgentMode,
} from '../../src/agentMode';

class MemoryState implements ModeState {
  constructor(readonly values = new Map<string, unknown>()) {}
  readonly writes: Array<{ key: string; value: unknown }> = [];

  get<T>(key: string, defaultValue?: T): T | undefined {
    return (this.values.has(key) ? this.values.get(key) : defaultValue) as T | undefined;
  }

  update(key: string, value: unknown): PromiseLike<void> {
    this.writes.push({ key, value });
    if (value === undefined) this.values.delete(key);
    else this.values.set(key, value);
    return Promise.resolve();
  }
}

test('a legacy conversation with no mode opens as Single without a write', () => {
  const state = new MemoryState();
  assert.equal(storedAgentMode(state, 'agent-old'), 'single');
  assert.equal(state.writes.length, 0);
});

test('modes are isolated by conversation and include Complex', () => {
  const state = new MemoryState();
  state.values.set(AGENT_MODE_KEY, {
    'agent-single': 'single',
    'agent-multi': 'multi',
    'agent-complex': 'complex',
  });
  assert.equal(storedAgentMode(state, 'agent-single'), 'single');
  assert.equal(storedAgentMode(state, 'agent-multi'), 'multi');
  assert.equal(storedAgentMode(state, 'agent-complex'), 'complex');
  assert.equal(storedAgentMode(state, 'agent-other'), 'single');
});

test('saved selections survive a fresh window state reader', async () => {
  const values = new Map<string, unknown>();
  const window = new MemoryState(values);
  await saveAgentMode(window, 'agent-single', 'single');
  await saveAgentMode(window, 'agent-multi', 'multi');
  await saveAgentMode(window, 'agent-complex', 'complex');

  const reloadedWindow = new MemoryState(values);
  assert.equal(storedAgentMode(reloadedWindow, 'agent-single'), 'single');
  assert.equal(storedAgentMode(reloadedWindow, 'agent-multi'), 'multi');
  assert.equal(storedAgentMode(reloadedWindow, 'agent-complex'), 'complex');
  assert.equal(storedAgentMode(reloadedWindow, 'agent-other'), 'single');
  assert.equal(reloadedWindow.writes.length, 0);
});

test('the former collaboration key migrates on the next save', async () => {
  const state = new MemoryState();
  state.values.set(LEGACY_COLLABORATION_KEY, {
    'agent-old': 'multi',
    'agent-other': 'single',
  });
  assert.equal(storedAgentMode(state, 'agent-old'), 'multi');

  await saveAgentMode(state, 'agent-old', 'complex');
  assert.deepEqual(state.values.get(AGENT_MODE_KEY), { 'agent-old': 'complex' });
  assert.deepEqual(state.values.get(LEGACY_COLLABORATION_KEY), { 'agent-other': 'single' });
});

test('wire options keep orchestration and child infrastructure aligned', () => {
  assert.deepEqual(agentModeOptions('single'), { agent_mode: 'single', subagents: false });
  assert.deepEqual(agentModeOptions('multi'), { agent_mode: 'multi', subagents: true });
  assert.deepEqual(agentModeOptions('complex'), { agent_mode: 'complex', subagents: true });
});
