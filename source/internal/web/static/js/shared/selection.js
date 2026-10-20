// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Selection for lists (p_0.46.8).
 *
 * One implementation for every list that supports it, because selection has more
 * corner cases than it looks: range across a *filtered* view, a toggle after a
 * re-render, select-all under a filter, and an escape that clears. Four lists each
 * growing their own version is four chances to disagree about what Shift-click
 * means.
 *
 * The state is a Set of ids plus an anchor, so it survives a re-render by identity
 * rather than by DOM position — the reload after a batch action must not clear what
 * was selected.
 *
 * Pure on purpose: the reducer has no DOM in it, so the p_0.46.5 harness can test
 * the corners.
 */
window.AnvilUI = window.AnvilUI || {};

(function () {
  /**
   * create makes a selection over a list of ids.
   *
   * `order` is the currently visible order (after filtering); range selection walks
   * it, which is what makes Shift-click mean "these rows on screen" rather than
   * "these rows in the data".
   */
  function create() {
    const state = { ids: new Set(), anchor: null };

    const api = {
      get size() {
        return state.ids.size;
      },
      has(id) {
        return state.ids.has(id);
      },
      ids() {
        return [...state.ids];
      },
      isEmpty() {
        return state.ids.size === 0;
      },

      /**
       * toggle flips one id and moves the anchor to it.
       *
       * `order` is the visible list; it is only needed for the anchor, so it can be
       * omitted when the caller does not want range behaviour.
       */
      toggle(id, order) {
        if (state.ids.has(id)) state.ids.delete(id);
        else state.ids.add(id);
        state.anchor = id;
        void order;
        return api;
      },

      /**
       * selectRange selects every visible id between the anchor and `id`.
       *
       * Called for Shift-click. With no anchor it degrades to a single toggle,
       * because an unanchored range has no meaning.
       */
      selectRange(id, order) {
        const list = Array.isArray(order) ? order : [];
        if (state.anchor == null) return api.toggle(id, list);
        const from = list.indexOf(state.anchor);
        const to = list.indexOf(id);
        if (from < 0 || to < 0) {
          // The anchor is no longer visible (filtered out): treat it as a fresh
          // single selection rather than selecting something arbitrary.
          state.ids.clear();
          state.ids.add(id);
          state.anchor = id;
          return api;
        }
        const lo = Math.min(from, to);
        const hi = Math.max(from, to);
        for (let i = lo; i <= hi; i++) state.ids.add(list[i]);
        return api;
      },

      /** selectAll selects every visible id; with none visible it clears. */
      selectAll(order) {
        const list = Array.isArray(order) ? order : [];
        state.ids.clear();
        for (const id of list) state.ids.add(id);
        state.anchor = list.length ? list[list.length - 1] : null;
        return api;
      },

      clear() {
        state.ids.clear();
        state.anchor = null;
        return api;
      },

      /**
       * prune drops ids that are no longer in the data.
       *
       * Called after a reload: a selected row can disappear because a batch action
       * ran, and a selection that keeps a ghost id lets a second batch fire at
       * something that no longer exists.
       */
      prune(present) {
        const live = new Set(Array.isArray(present) ? present : []);
        for (const id of [...state.ids]) {
          if (!live.has(id)) state.ids.delete(id);
        }
        if (state.anchor != null && !live.has(state.anchor)) state.anchor = null;
        return api;
      },
    };
    return api;
  }

  /**
   * decodeKey turns a keyboard event into a list command.
   *
   * Kept here so every list answers the same keys, and so the mapping is testable
   * without synthesising events.
   */
  function decodeKey(key, { meta = false, shift = false } = {}) {
    switch (key) {
      case "ArrowDown":
        return "next";
      case "ArrowUp":
        return "prev";
      case "Enter":
        return "open";
      case " ":
        return shift ? "selectRange" : "select";
      case "Escape":
        return "clear";
      case "a":
        return meta ? "selectAll" : "none";
      default:
        return "none";
    }
  }

  window.AnvilUI.selection = { create, decodeKey };
})();
