// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * withAction — one running state, one in-flight claim, per action key.
 *
 * Two problems it solves, both of which cost real work when they happen:
 *
 *   1. A double-click on a job-enqueuing button enqueues two jobs. Disabling the
 *      button is not enough on its own: several buttons can drive the same
 *      action (a milestone's Start in its card and in the toolbar), so the claim
 *      is keyed rather than per-element.
 *   2. A request that takes two seconds shows nothing at all. The label changes
 *      so the operator can see *what* is running, not only that the UI is stuck.
 *
 * The button is always restored, including on failure, because a button left
 * disabled by a thrown error is indistinguishable from a broken app.
 */
window.AnvilUI = window.AnvilUI || {};

(function () {
  /** key → true while an action with that key is in flight. */
  const inflight = new Set();

  /**
   * withAction(btn, opts, fn)
   *
   * opts.key          claim key; omit to claim per-button
   * opts.runningLabel label shown while running ("Starting…")
   * opts.onError      (err) => void; defaults to a notice on opts.host
   * opts.host         modal host used for the default error notice
   */
  async function withAction(btn, opts, fn) {
    const o = opts || {};
    const key = o.key || null;
    if (key && inflight.has(key)) return undefined;

    const original = btn ? btn.textContent : null;
    if (key) inflight.add(key);
    if (btn) {
      btn.disabled = true;
      if (o.runningLabel) btn.textContent = o.runningLabel;
      btn.setAttribute("aria-busy", "true");
    }
    try {
      return await fn();
    } catch (err) {
      if (typeof o.onError === "function") o.onError(err);
      else if (window.AnvilShell?.showError) {
        await window.AnvilShell.showError(
          err && err.message ? err.message : String(err),
          err && err.status ? err.status : 0,
          err && err.requestId ? err.requestId : "",
        );
      }
      else if (o.host) {
        window.AnvilUI.notice(o.host, err && err.message ? err.message : String(err), o.errorTitle || "Action failed");
      } else {
        throw err;
      }
      return undefined;
    } finally {
      if (key) inflight.delete(key);
      if (btn) {
        btn.disabled = false;
        if (original !== null) btn.textContent = original;
        btn.removeAttribute("aria-busy");
      }
    }
  }

  /** isRunning reports whether a claim key is currently held. */
  function isRunning(key) {
    return inflight.has(key);
  }

  /** reset clears every claim; used when a feature unmounts mid-flight. */
  function reset() {
    inflight.clear();
  }

  window.AnvilUI.withAction = withAction;
  window.AnvilUI.isRunning = isRunning;
  window.AnvilUI.resetActions = reset;
})();
