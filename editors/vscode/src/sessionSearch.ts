/**
 * Pure helpers for the Sessions search picker. Keeping this module free of
 * vscode imports makes query policy, untrusted snippets, and pagination state
 * testable without an Extension Host.
 */

import {
  SessionInfo,
  SessionSearchHit,
  SessionSearchParams,
  SessionSearchTextRange,
} from './protocol';
import { sessionLabel, sortSessionsByRecency } from './sessionList';

export type SessionSearchRole = 'all' | 'user' | 'assistant' | 'tool';
export type SessionSearchKind = 'all' | 'message' | 'error' | 'plan' | 'todo';

export interface SessionSearchFilters {
  role: SessionSearchRole;
  kind: SessionSearchKind;
  includeArgs: boolean;
}

export type SessionChoiceKind = 'session' | 'result' | 'loadMore' | 'status' | 'retry';

export interface SessionChoice {
  choiceKind: SessionChoiceKind;
  label: string;
  description?: string;
  detail?: string;
  agentId?: string;
  hit?: SessionSearchHit;
  alwaysShow: true;
}

const QUERY_FILTER = /^(?:role|kind|status|after|before):/i;

export function filterSessions(
  sessions: readonly SessionInfo[],
  query: string,
): SessionInfo[] {
  const terms = query.toLowerCase().split(/\s+/).filter((term) => term !== '');
  if (terms.length === 0) return [...sessions];
  return sessions.filter((session) => {
    const text = `${session.title ?? ''}\n${session.agent_id}`.toLowerCase();
    return terms.every((term) => text.includes(term));
  });
}

export function sessionChoices(sessions: readonly SessionInfo[]): SessionChoice[] {
  return sortSessionsByRecency(sessions).map((session) => ({
    choiceKind: 'session',
    label: plainQuickPickText(sessionLabel(session)),
    description: session.agent_id,
    agentId: session.agent_id,
    alwaysShow: true,
  }));
}

/**
 * Content search waits for two meaningful characters, while a structured
 * filter may stand alone. This avoids scanning every transcript for a single
 * accidental keystroke.
 */
export function contentSearchEligible(query: string): boolean {
  const tokens = query.match(/"[^"]*"|\S+/g) ?? [];
  if (tokens.some((token) => QUERY_FILTER.test(token))) return true;
  const text = tokens
    .map((token) => token.replace(/^"|"$/g, ''))
    .join('')
    .trim();
  return Array.from(text).length >= 2;
}

export function buildSessionSearchParams(
  query: string,
  filters: SessionSearchFilters,
  cursor?: string,
): SessionSearchParams {
  const params: SessionSearchParams = {
    query: query.trim(),
    limit: 50,
    include_args: filters.includeArgs,
  };
  if (filters.role !== 'all') params.roles = [filters.role];
  if (filters.kind !== 'all') params.kinds = [filters.kind];
  if (cursor) params.cursor = cursor;
  return params;
}

export function sessionSearchChoices(
  hits: SessionSearchHit[],
  sessions: readonly SessionInfo[] = [],
): SessionChoice[] {
  const metadata = new Map(sessions.map((session) => [session.agent_id, session]));
  return hits.map((hit) => {
    const session = metadata.get(hit.agent_id);
    const relation = session?.parent_agent_id
      ? `subagent${session.task_title ? `: ${plainQuickPickText(session.task_title)}` : ''}`
      : '';
    const descriptors = [hit.role, hit.kind, relation, shortAgentId(hit.agent_id)].filter(Boolean);
    return {
      choiceKind: 'result',
      label: plainQuickPickText(
        hit.title?.trim() || session?.task_title?.trim() || shortAgentId(hit.agent_id),
      ),
      description: descriptors.join(' · '),
      detail: highlightSnippet(plainQuickPickText(hit.snippet), hit.match),
      agentId: hit.agent_id,
      hit,
      alwaysShow: true,
    };
  });
}

/** Append a page without showing the same stored record twice. */
export function mergeSessionSearchHits(
  previous: SessionSearchHit[],
  next: SessionSearchHit[],
): SessionSearchHit[] {
  const seen = new Set(previous.map(hitKey));
  return [...previous, ...next.filter((hit) => !seen.has(hitKey(hit)))];
}

/**
 * Marks code-point ranges with plain Unicode delimiters. Quick Pick detail is
 * plain text: transcript content is never interpreted as Markdown, HTML, or a
 * command URI.
 */
export function highlightSnippet(
  snippet: string,
  ranges: SessionSearchTextRange[],
): string {
  const runes = Array.from(snippet);
  const merged = ranges
    .map(({ start, end }) => ({
      start: Math.max(0, Math.min(runes.length, start)),
      end: Math.max(0, Math.min(runes.length, end)),
    }))
    .filter((range) => range.end > range.start)
    .sort((a, b) => a.start - b.start || a.end - b.end)
    .reduce<Array<{ start: number; end: number }>>((out, range) => {
      const last = out[out.length - 1];
      if (last && range.start <= last.end) {
        last.end = Math.max(last.end, range.end);
      } else {
        out.push({ ...range });
      }
      return out;
    }, []);

  for (let i = merged.length - 1; i >= 0; i -= 1) {
    const range = merged[i];
    runes.splice(range.end, 0, '⟧');
    runes.splice(range.start, 0, '⟦');
  }
  return runes.join('');
}

/** Monotonic client-side cancellation for superseded Host requests. */
export class SessionSearchEpoch {
  private epoch = 0;

  supersede(): number {
    this.epoch += 1;
    return this.epoch;
  }

  current(epoch: number): boolean {
    return epoch === this.epoch;
  }
}

export const loadMoreChoice = (): SessionChoice => ({
  choiceKind: 'loadMore',
  label: '$(chevron-down) Load more results',
  description: 'Continue this local search',
  alwaysShow: true,
});

export const statusChoice = (message: string): SessionChoice => ({
  choiceKind: 'status',
  label: message,
  alwaysShow: true,
});

export const retryChoice = (message: string): SessionChoice => ({
  choiceKind: 'retry',
  label: '$(refresh) Retry search',
  description: message,
  alwaysShow: true,
});

function hitKey(hit: SessionSearchHit): string {
  return `${hit.agent_id}\0${hit.run_id ?? ''}\0${hit.record_pos}`;
}

function plainQuickPickText(value: string): string {
  return Array.from(value, (character) => {
    const code = character.codePointAt(0) ?? 0;
    return (code < 32 && code !== 9 && code !== 10) || code === 127 ? ' ' : character;
  }).join('').replace(/\s+/g, ' ').trim();
}

function shortAgentId(agentId: string): string {
  return agentId.length <= 12 ? agentId : agentId.slice(0, 12);
}
