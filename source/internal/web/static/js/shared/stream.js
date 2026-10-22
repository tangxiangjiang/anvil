// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Stream resumption (p_0.45.14).
 *
 * A dropped SSE connection used to be the end of the story: the consumer caught
 * the error, said "stream ended", and the operator had to press Refresh. On a
 * phone that is most of the time.
 *
 * Two jobs here, both pure so they can be tested without a network:
 *
 *   - Decide when to retry, with capped backoff and a stop after the session is
 *     over (retrying a finished job forever is a busy loop with extra steps).
 *   - Deduplicate frames on reconnect. The Workbench stream replays from the
 *     beginning, so a reconnect without dedupe redraws the whole conversation.
 *     p_0.45.12's persisted events carry `seq`, and that is the key.
 */
window.AnvilUI = window.AnvilUI || {};

(function () {
  /**
   * createResumer tracks one stream's retry state and its seen seq set.
   *
   * `attempt` counts consecutive failures and resets on a successful open, so a
   * flaky connection that keeps recovering does not eventually give up.
   */
  function createResumer({ maxAttempts = 6, baseDelayMs = 400, maxDelayMs = 15000 } = {}) {
    const seen = new Set();
    let attempt = 0;

    return {
      get attempt() {
        return attempt;
      },

      /** opened records a successful open and clears the failure count. */
      opened() {
        attempt = 0;
      },

      /**
       * dropped records a failure and answers whether to retry, and after how long.
       *
       * The cap exists so a genuinely dead endpoint stops: six attempts spanning
       * roughly forty seconds is long enough to ride out a train tunnel and short
       * enough that a wrong URL fails visibly.
       */
      dropped() {
        attempt++;
        if (attempt > maxAttempts) return { retry: false, delayMs: 0 };
        const delay = Math.min(baseDelayMs * Math.pow(2, attempt - 1), maxDelayMs);
        return { retry: true, delayMs: delay };
      },

      /** reset forgets both the retry count and what has been seen. */
      reset() {
        attempt = 0;
        seen.clear();
      },

      /**
       * firstSeen answers whether this event is new.
       *
       * An event with no seq cannot be deduplicated, so it is always accepted:
       * dropping a frame because it lacks an id would lose real content, which is
       * worse than drawing a duplicate.
       */
      firstSeen(ev) {
        const seq = ev && ev.seq;
        if (seq == null) return true;
        if (seen.has(seq)) return false;
        seen.add(seq);
        return true;
      },

      /** seenCount is exposed for the tests and for the connection banner. */
      get seenCount() {
        return seen.size;
      },
    };
  }

  /** connectionLabel is what the banner says for a state. */
  function connectionLabel(state) {
    switch (state) {
      case "live":
        return "live";
      case "reconnecting":
        return "reconnecting…";
      case "offline":
        return "disconnected";
      case "ended":
        return "ended";
      default:
        return "";
    }
  }

  window.AnvilUI.stream = { createResumer, connectionLabel };
})();
