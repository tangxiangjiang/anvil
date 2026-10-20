/**
 * What "Rewind to here" means, as data.
 *
 * A run is one turn, and one turn starts from one user message. Rewinding the
 * message therefore has two halves that never merge: branching the conversation
 * and putting the turn's files back. The message offers them separately, and
 * this module decides what a chosen action asks for — the panel carries it out,
 * and the webview draws the same three choices from its own state.
 *
 * The first message has no previous run to branch at. A fork before it is an
 * empty conversation, which is what `/clear` already makes, so the plan says
 * "start over" rather than inventing an id the host would refuse.
 */

export type RewindAction = 'fork' | 'restore' | 'both';

export interface RewindPlan {
  /** The run to branch at, or null when no branch is asked for. */
  fork: string | null;
  /** The turn is the first one: its branch is an empty conversation. */
  startOver: boolean;
  /** Put the turn's files back, after whatever branch was asked for. */
  restore: boolean;
}

export function rewindPlan(action: RewindAction, prevRunId?: string): RewindPlan {
  const wantsFork = action === 'fork' || action === 'both';
  const restore = action === 'restore' || action === 'both';
  if (!wantsFork) return { fork: null, startOver: false, restore };
  if (typeof prevRunId === 'string' && prevRunId !== '') {
    return { fork: prevRunId, startOver: false, restore };
  }
  return { fork: null, startOver: true, restore };
}
