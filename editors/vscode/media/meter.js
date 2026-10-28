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
 *  - both clocks are the caller's to anchor: remaining context comes from the
 *    host's estimate, and the cache countdown runs from the run's last request
 *    against a TTL the client knows. Neither is invented when the input is
 *    absent.
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
    const scaled = (amount, unit) => {
      const text = amount.toFixed(amount >= 10 ? 0 : 1).replace(/\.0$/, '');
      return text + unit;
    };
    if (value >= 1000000) return scaled(value / 1000000, 'M');
    if (value >= 1000) return scaled(value / 1000, 'k');
    return String(Math.floor(value));
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
    // Cached input is recorded only when the provider reported it. The field
    // is absent otherwise, so a provider that says nothing cannot be mistaken
    // for one whose cache is always empty — the same rule as the counts.
    const cached = count(usage.cached_tokens);
    if (cached > 0) state.last.cached = cached;
    return state;
  }

  /**
   * A percentage with the precision the number deserves: below ten percent a
   * fraction of a percent is worth a decimal, above it a whole number is all
   * anybody wants. One spelling for every place the meter says a share.
   */
  function percent(used, window) {
    const total = count(window);
    if (total <= 0) return '';
    const pct = (count(used) / total) * 100;
    return pct >= 10 ? String(Math.round(pct)) : pct.toFixed(1);
  }

  /**
   * The meter's one line for the session's spend, or the empty string.
   *
   * Without a window the line is counts only. With one, the host's current
   * context estimate is drawn against it. The last provider total is only a
   * fallback for older hosts: provider usage may accumulate multiple model
   * rounds and must not replace an available context estimate.
   */
  function label(state, contextWindow, info) {
    const parts = [];
    if (state && (state.input > 0 || state.output > 0)) {
      if (state.input > 0) parts.push(short(state.input) + ' in');
      if (state.output > 0) parts.push(short(state.output) + ' out');
    }
    const declaredWindow = count(info && info.window_tokens);
    const window = declaredWindow || count(contextWindow);
    const estimate = info && info.estimated_tokens;
    const used =
      typeof estimate === 'number' && isFinite(estimate) && estimate >= 0
        ? Math.floor(estimate)
        : state && state.last
          ? count(state.last.total)
          : 0;
    if (window > 0 && used > 0) {
      parts.push('last ' + short(used) + ' of ' + short(window) + ' (' + percent(used, window) + '%)');
    }
    return parts.join(' · ');
  }

  /**
   * Where the conversation stands against its declared window, or null when
   * nobody declared one.
   *
   * `info` is the host's `ContextInfo` — an estimate of the next request, not
   * the provider's count. When the host did not answer with one, the last run's
   * total is the only measured figure there is, and it is used rather than a
   * zero. The auto-compact mark comes from the host when it states it and is
   * computed from the declared fraction otherwise; the fraction is the same
   * declaration either way, so neither is a guess.
   */
  function remaining(state, info, autoCompactAt) {
    const window = count(info && info.window_tokens);
    if (window <= 0) return null;
    const estimate = info && info.estimated_tokens;
    const measured =
      typeof estimate === 'number' && isFinite(estimate) && estimate >= 0
        ? Math.floor(estimate)
        : state && state.last
          ? count(state.last.total)
          : 0;
    const declared = count(info && info.compact_at_tokens);
    const share = fraction(autoCompactAt);
    const compactAt = declared > 0 ? declared : share > 0 ? Math.round(window * share) : 0;
    return {
      used: measured,
      window: window,
      left: Math.max(0, window - measured),
      compactAt: compactAt,
      leftUntilCompact: compactAt > 0 ? Math.max(0, compactAt - measured) : 0,
      percent: (measured / window) * 100,
    };
  }

  /**
   * The provider cache's share and, when its TTL is known, its remaining life.
   *
   * `remaining` is null when no TTL is known or the run has no anchor — a
   * countdown needs both. At or past the TTL it is 0, which the caller draws as
   * cold: the cache is information even when it has expired. `cached` is the
   * last run's cached input, and 0 when the provider said nothing.
   */
  function cache(state, ttlSeconds, lastRequestAt, now) {
    const cached = state && state.last ? count(state.last.cached) : 0;
    const ttl = count(ttlSeconds);
    const anchor =
      typeof lastRequestAt === 'number' && isFinite(lastRequestAt) ? lastRequestAt : null;
    let remaining = null;
    if (ttl > 0 && anchor !== null) {
      const clockNow = typeof now === 'number' && isFinite(now) ? now : Date.now();
      remaining = Math.max(0, Math.ceil((anchor + ttl * 1000 - clockNow) / 1000));
    }
    return { cached: cached, ttl: ttl, remaining: remaining };
  }

  /** A fractional setting in (0, 1], or 0 when it is not one. */
  function fraction(value) {
    if (typeof value !== 'number' || !isFinite(value) || value <= 0) return 0;
    return value > 1 ? 1 : value;
  }

  /** Seconds as `m:ss`, or the empty string when there is nothing to count. */
  function clock(seconds) {
    if (typeof seconds !== 'number' || !isFinite(seconds) || seconds <= 0) return '';
    const total = Math.ceil(seconds);
    const minutes = Math.floor(total / 60);
    const rest = total % 60;
    return minutes + ':' + (rest < 10 ? '0' : '') + rest;
  }

  globalThis.anvilMeter = { empty, add, label, count, short, percent, remaining, cache, clock };
})();
