/*
 * The composer's token meter: arithmetic, not DOM.
 *
 * A plain script assigning a global, for the same reason `markdown.js` is one:
 * `media/` is linted as browser code and never built, so a module would have to
 * be copied by hand. Splitting the sums out of `chat.js` is what lets a Node
 * test load them — a number a person reads deserves a test that runs.
 *
 * The rules it encodes:
 *  - a usage object the provider never sent is absent, and absent is not zero:
 *    such a run adds nothing rather than claiming it cost nothing;
 *  - the session's totals are the sum of the terminal runs it has seen, live or
 *    replayed, so a reload adds them up again from the stored `run_end`s;
 *  - a fraction needs a window somebody configured. There is no table here and
 *    no guess from a model name.
 */
(function () {
  /**
   * A non-negative whole count from whatever arrived. A value that is not a
   * number counts as zero, so a malformed field cannot put NaN on the screen.
   */
  function count(value) {
    if (typeof value !== 'number' || !isFinite(value) || value <= 0) return 0;
    return Math.floor(value);
  }

  /** The same shorthand `runSummary` uses for a run's own numbers. */
  function short(value) {
    if (typeof value !== 'number' || !isFinite(value) || value <= 0) return '';
    return value >= 1000 ? (value / 1000).toFixed(value >= 10000 ? 0 : 1) + 'k' : String(value);
  }

  /** A fresh accumulator: nothing spent, no previous run. */
  function empty() {
    return { input: 0, output: 0, total: 0, last: null };
  }

  /**
   * Adds one finished run to the session's totals.
   *
   * `done` is the host's `done` envelope or a replayed `run_end` entry; both
   * carry the run's own usage. A run that reports none leaves the totals and
   * the last-run figure alone — it is unknown, not free.
   */
  function add(state, done) {
    const usage = done && done.usage;
    if (!usage) return state;
    const input = count(usage.input_tokens);
    const output = count(usage.output_tokens);
    // Providers that report no total still report the halves; a total of zero
    // with halves present is the one case where adding them up is arithmetic
    // rather than invention.
    const total = count(usage.total_tokens) || input + output;
    state.input += input;
    state.output += output;
    state.total += total;
    state.last = { input: input, output: output, total: total };
    return state;
  }

  /**
   * The meter's one line, or the empty string when there is nothing to say.
   *
   * Without a window the line is counts only. With one, the last run's total is
   * also drawn against it: below ten percent a fraction of a percent is worth a
   * decimal, and above it a whole number is all anybody wants.
   */
  function label(state, contextWindow) {
    const parts = [];
    if (state && (state.input > 0 || state.output > 0)) {
      if (state.input > 0) parts.push(short(state.input) + ' in');
      if (state.output > 0) parts.push(short(state.output) + ' out');
    }
    const window = count(contextWindow);
    if (window > 0 && state && state.last && state.last.total > 0) {
      const pct = (state.last.total / window) * 100;
      const shown = pct >= 10 ? String(Math.round(pct)) : pct.toFixed(1);
      parts.push('last ' + short(state.last.total) + ' of ' + short(window) + ' (' + shown + '%)');
    }
    return parts.join(' · ');
  }

  globalThis.anvilMeter = { empty, add, label, count, short };
})();
