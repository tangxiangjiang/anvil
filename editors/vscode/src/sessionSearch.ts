/**
 * The stored-session search: what `anvil.searchSessions` offers as the query
 * changes.
 *
 * It lives apart from the command so it can be tested without `vscode`, like the
 * sidebar's view model. The rule is the same one the sidebar follows: a session
 * is found by the name a person recognises and by the id a command takes, and by
 * nothing that was guessed.
 */

import { SessionInfo } from './protocol';
import { sessionLabel, sortSessionsByRecency } from './sessionList';

/** One row of the search picker. */
export interface SessionChoice {
  label: string;
  description: string;
  /** What the command takes back: the agent id, not the title. */
  agentId: string;
}

/**
 * The sessions whose title or agent id contains every term in the query.
 *
 * Several terms are an AND, because a person narrowing a list types more words,
 * not alternative ones. A query of only whitespace is no query: it offers
 * everything rather than nothing.
 */
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

/**
 * The picker's rows for a set of sessions, newest first.
 *
 * The description is the agent id in full: titles repeat, and a row whose title
 * matches another's needs the id in front of the user to be told apart.
 */
export function sessionChoices(sessions: readonly SessionInfo[]): SessionChoice[] {
  return sortSessionsByRecency(sessions).map((session) => ({
    label: sessionLabel(session),
    description: session.agent_id,
    agentId: session.agent_id,
  }));
}
