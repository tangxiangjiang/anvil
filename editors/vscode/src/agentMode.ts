import type { AgentMode } from './protocol';
import type { SessionConfig } from './session';

export const AGENT_MODE_KEY = 'anvil.agentModeByAgent';
export const LEGACY_COLLABORATION_KEY = 'anvil.collaborationByAgent';
const KEPT = 100;

export interface ModeState {
  get<T>(key: string, defaultValue?: T): T | undefined;
  update(key: string, value: unknown): PromiseLike<void>;
}

export function isAgentMode(value: unknown): value is AgentMode {
  return value === 'single' || value === 'multi' || value === 'complex';
}

/** A conversation without a stored mode is deliberately conservative. */
export function storedAgentMode(state: ModeState, agentId: string | undefined): AgentMode {
  if (!agentId) return 'single';
  const current = state.get<Record<string, unknown>>(AGENT_MODE_KEY, {}) ?? {};
  if (isAgentMode(current[agentId])) return current[agentId];

  // The former picker stored only Single and Multi under this key. Reading it
  // is the migration; the next save moves that one conversation to the new key.
  const legacy = state.get<Record<string, unknown>>(LEGACY_COLLABORATION_KEY, {}) ?? {};
  return legacy[agentId] === 'multi' ? 'multi' : 'single';
}

export function agentModeOptions(mode: AgentMode): Pick<SessionConfig, 'agent_mode' | 'subagents'> {
  return {
    agent_mode: mode,
    subagents: mode !== 'single',
  };
}

export async function saveAgentMode(
  state: ModeState,
  agentId: string | undefined,
  mode: AgentMode,
): Promise<void> {
  if (!agentId) return;
  const current = state.get<Record<string, unknown>>(AGENT_MODE_KEY, {}) ?? {};
  const entries = Object.entries(current)
    .filter(([id, value]) => id !== agentId && isAgentMode(value));
  entries.push([agentId, mode]);
  await state.update(AGENT_MODE_KEY, Object.fromEntries(entries.slice(-KEPT)));

  const legacy = state.get<Record<string, unknown>>(LEGACY_COLLABORATION_KEY, {}) ?? {};
  if (Object.prototype.hasOwnProperty.call(legacy, agentId)) {
    const next = { ...legacy };
    delete next[agentId];
    await state.update(
      LEGACY_COLLABORATION_KEY,
      Object.keys(next).length > 0 ? next : undefined,
    );
  }
}
