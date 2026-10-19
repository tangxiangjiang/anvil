/**
 * What the window's status-bar item says, decided without a window.
 *
 * The status bar is one item for every chat tab, so it can only report one
 * thing: the most urgent thing any tab is doing. An approval waiting for an
 * answer outranks a turn in flight, because a run stopped on a question is the
 * one state that needs the user — a run making progress does not.
 */

/** The three states the window can be in. */
export type ChatPhase = 'idle' | 'running' | 'waiting';

/** What one tab is doing, as far as the status bar cares. */
export interface TabActivity {
  /** A turn is in flight. */
  busy: boolean;
  /** A question is waiting for an answer. */
  awaiting: boolean;
}

/**
 * The window's phase, from what every tab reports.
 *
 * A tab can be busy and awaiting at the same time — an approval is raised
 * inside a run — which is why `awaiting` is checked first rather than being
 * folded into the busy count.
 */
export function phaseOf(tabs: Iterable<TabActivity>): ChatPhase {
  let running = false;
  for (const tab of tabs) {
    if (tab.awaiting) return 'waiting';
    if (tab.busy) running = true;
  }
  return running ? 'running' : 'idle';
}

/** The item's text and tooltip for a phase. */
export function chatStatusOf(phase: ChatPhase): { text: string; tooltip: string } {
  switch (phase) {
    case 'waiting':
      return {
        text: '$(bell) Anvil',
        tooltip: 'Anvil is waiting for your approval. Click to open the chat.',
      };
    case 'running':
      return {
        // `~spin` is the codicon modifier that turns the icon, so the item says
        // "working" without a second line of text.
        text: '$(sync~spin) Anvil',
        tooltip: 'Anvil is working. Click to open the chat.',
      };
    default:
      return {
        text: '$(comment-discussion) Anvil',
        tooltip: 'Anvil: click to open the chat.',
      };
  }
}
