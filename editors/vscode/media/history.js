/*
 * Prompt recall: what the arrow keys walk, and where the draft goes.
 *
 * A plain script assigning a global, for the same reason `meter.js` is one:
 * `media/` is linted as browser code and never built, so a module would have to
 * be copied by hand. Splitting the cursor arithmetic out of `chat.js` is what
 * lets a Node test drive it without a document.
 *
 * The rules it encodes:
 *  - the list is the prompts this document has shown, oldest first, and it is
 *    cleared with the transcript: recalling a prompt from a conversation that
 *    is no longer on screen would put words in the user's mouth about it;
 *  - a prompt sent twice in a row is one entry, because the second press of Up
 *    showing the same text again reads as a key that did nothing;
 *  - leaving the list downwards returns the text that was in the box when the
 *    walk started, not an empty box: the draft was the user's, and the walk
 *    borrowed it.
 */
(function () {
  /** The most prompts one tab remembers. Past this the oldest are dropped. */
  const LIMIT = 100;

  function create() {
    return { entries: [], cursor: null, draft: '' };
  }

  /** Forgets the conversation's prompts, for a transcript that is being replaced. */
  function clear(state) {
    state.entries = [];
    state.cursor = null;
    state.draft = '';
    return state;
  }

  /**
   * Adds one prompt the conversation has shown.
   *
   * A blank prompt is not an entry, and neither is a repeat of the one before
   * it. Recording also puts the cursor back on the live line: the text that was
   * just sent is the newest entry, so the next Up should start from it rather
   * than from wherever the last walk stopped.
   */
  function record(state, text) {
    const value = typeof text === 'string' ? text.trim() : '';
    if (value === '') return state;
    const last = state.entries.length > 0 ? state.entries[state.entries.length - 1] : undefined;
    if (value !== last) {
      state.entries.push(value);
      if (state.entries.length > LIMIT) state.entries.shift();
    }
    state.cursor = null;
    state.draft = '';
    return state;
  }

  /**
   * Walks the list by one: -1 is Up (older), 1 is Down (newer).
   *
   * `current` is what the box holds now, and it is captured as the draft the
   * first time the walk leaves the live line. Returns the value the box should
   * hold, or null when the key belongs to the textarea — no history, or already
   * at the live line and asked to go further down.
   */
  function walk(state, delta, current) {
    if (state.entries.length === 0) return null;
    if (state.cursor === null) {
      if (delta > 0) return null;
      state.draft = typeof current === 'string' ? current : '';
      state.cursor = state.entries.length - 1;
      return state.entries[state.cursor];
    }
    const next = state.cursor + delta;
    if (next >= state.entries.length) {
      // Off the newest end is the live line, and the live line is the draft.
      const draft = state.draft;
      state.cursor = null;
      state.draft = '';
      return draft;
    }
    // Asked to go older than the oldest: stay there rather than wrap around,
    // which would make the list feel like it had no beginning.
    if (next < 0) return state.entries[0];
    state.cursor = next;
    return state.entries[next];
  }

  /**
   * The user edited a recalled value. The cursor goes back to the live line, so
   * the next Up starts from the newest prompt again instead of overwriting the
   * edit with the entry next to the one being edited.
   */
  function edited(state) {
    state.cursor = null;
    state.draft = '';
    return state;
  }

  globalThis.anvilHistory = { LIMIT, create, clear, record, walk, edited };
})();
