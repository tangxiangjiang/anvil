/**
 * Whether the window should come back to the session it was talking to.
 *
 * A reload throws away the tab's document, and the extension host with it — so
 * the conversation that was on screen is gone from both memory and the webview,
 * even though the agent it belonged to is on disk. This is the decision half of
 * putting it back: given the agent id the workspace remembered and the agents
 * that are actually stored, what should happen. The doing half is the panel's.
 *
 * It is a function of two arguments rather than a method because the interesting
 * case is the one that cannot be reached by hand in a test: an id the workspace
 * remembered for an agent it no longer has. `clear` is that case, and it is not
 * the same as `none` — one has something to forget, the other nothing to do.
 */

/** The workspaceState key the current agent id is remembered under. */
export const SessionAgentKey = 'anvil.sessionAgent';

export type ReattachDecision =
  /** The agent is stored: come back to it. */
  | 'resume'
  /** The id is remembered but the agent is gone: forget it and open fresh. */
  | 'clear'
  /** Nothing to come back to, or nothing that could be asked. */
  | 'none';

/**
 * `listed` is what `session.list` answered, or `undefined` when it could not be
 * read at all — no host, a host too old to answer, a workspace that would not
 * start one. The two must not be conflated: an empty list is a workspace with no
 * stored sessions, while a listing that failed says nothing about the agent, and
 * clearing the memory on it would forget a session that is perfectly intact.
 */
export function shouldReattach(
  stored: string | undefined,
  listed: readonly { agent_id: string }[] | undefined,
): ReattachDecision {
  if (!stored) return 'none';
  if (!listed) return 'none';
  return listed.some((session) => session.agent_id === stored) ? 'resume' : 'clear';
}
