// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Delivery list selection and deterministic batch execution (p_0.45.10).
 *
 * State transitions live in shared/selection.js; this file is the one DOM adapter
 * used by Roadmap milestones, test cases, and Design Versions.
 */
window.Features = window.Features || {};
window.Features.delivery = window.Features.delivery || {};

Object.assign(window.Features.delivery, {
  sel(key) {
    if (!this._sels) this._sels = {};
    if (!this._sels[key]) this._sels[key] = window.AnvilUI.selection.create();
    return this._sels[key];
  },

  visibleIds(key) {
    return [...(this.el?.querySelectorAll(`[data-sel-id][data-sel-list="${key}"]`) || [])].map(
      (node) => node.getAttribute("data-sel-id"),
    );
  },

  paintSelection(key) {
    const sel = this.sel(key);
    this.el?.querySelectorAll(`[data-sel-id][data-sel-list="${key}"]`).forEach((row) => {
      const on = sel.has(row.getAttribute("data-sel-id"));
      row.classList.toggle("sel-on", on);
      row.setAttribute("aria-selected", on ? "true" : "false");
    });
    const bar = this.el?.querySelector(`[data-sel-bar="${key}"]`);
    if (!bar) return;
    bar.hidden = sel.isEmpty();
    const header = this.el?.querySelector(`[data-sel-header="${key}"]`);
    if (header) header.hidden = !sel.isEmpty();
    const count = bar.querySelector("[data-sel-count]");
    if (count) count.textContent = String(sel.size);
    const live = bar.querySelector("[data-sel-live]");
    if (live) live.textContent = String(sel.size) + " selected";
  },

  bindSelection(key, { onOpen, presentIds } = {}) {
    const rows = [...(this.el?.querySelectorAll(`[data-sel-id][data-sel-list="${key}"]`) || [])];
    const visible = rows.map((row) => row.getAttribute("data-sel-id"));
    const present = Array.isArray(presentIds) ? presentIds : visible;
    this._selOrders = this._selOrders || {};
    this._selOrders[key] = [...present];
    this.sel(key).prune(present);
    if (!rows.length) {
      this.paintSelection(key);
      return;
    }
    const list = rows[0].parentElement;
    if (list) {
      list.setAttribute("role", "listbox");
      list.setAttribute("aria-multiselectable", "true");
    }
    this.paintSelection(key);

    rows.forEach((row) => {
      const id = row.getAttribute("data-sel-id");
      row.setAttribute("role", "option");
      row.setAttribute("tabindex", "0");
      row.addEventListener("click", (event) => {
        if (event.target.closest("button, a, input, select, textarea")) return;
        const sel = this.sel(key);
        const order = this.visibleIds(key);
        if (event.shiftKey) sel.selectRange(id, order);
        else if (event.metaKey || event.ctrlKey) sel.toggle(id, order);
        else {
          sel.clear();
          sel.toggle(id, order);
        }
        this.paintSelection(key);
      });

      row.addEventListener("keydown", (event) => {
        if (event.target.closest("input, textarea, select")) return;
        const command = window.AnvilUI.selection.decodeKey(event.key, {
          meta: event.metaKey || event.ctrlKey,
          shift: event.shiftKey,
        });
        const sel = this.sel(key);
        const order = this.visibleIds(key);
        const at = order.indexOf(id);
        if (command !== "none") event.preventDefault();
        if (command === "select") sel.toggle(id, order);
        else if (command === "selectRange") sel.selectRange(id, order);
        else if (command === "selectAll") sel.selectAll(order);
        else if (command === "clear") sel.clear();
        else if (command === "next") rows[Math.min(at + 1, rows.length - 1)]?.focus();
        else if (command === "prev") rows[Math.max(at - 1, 0)]?.focus();
        else if (command === "open" && onOpen) onOpen(id);
        else if (command === "search") document.getElementById("global-search")?.focus();
        if (["select", "selectRange", "selectAll", "clear"].includes(command)) {
          this.paintSelection(key);
        }
      });
    });

    this.el?.querySelector(`[data-sel-all="${key}"]`)?.addEventListener("change", (event) => {
      const sel = this.sel(key);
      if (event.target.checked) sel.selectAll(this.visibleIds(key));
      else sel.clear();
      this.paintSelection(key);
    });
    this.el?.querySelector(`[data-sel-clear="${key}"]`)?.addEventListener("click", () => {
      this.sel(key).clear();
      this.paintSelection(key);
    });
  },

  selectionIds(key) {
    const sel = this.sel(key);
    const order = this._selOrders?.[key] || this.visibleIds(key);
    const known = order.filter((id) => sel.has(id));
    const knownSet = new Set(known);
    return known.concat(sel.ids().filter((id) => !knownSet.has(id)).sort());
  },

  async runBatch(ids, fn, { key, okVerb } = {}) {
    const results = [];
    for (const id of ids) {
      try {
        await fn(id);
        results.push({ id, ok: true });
      } catch (error) {
        results.push({ id, ok: false, error: error.message || String(error) });
      }
    }
    const failed = results.filter((result) => !result.ok);
    const done = results.length - failed.length;
    if (key) {
      this.sel(key).prune(ids.filter((id) => failed.some((failure) => failure.id === id)));
      this.paintSelection(key);
    }
    if (!failed.length) {
      this.showNotice(`${done} ${okVerb || "done"}.`, "Batch");
    } else {
      this.showNotice(
        `${done} succeeded, ${failed.length} failed:\n` +
          failed.map((failure) => `· ${failure.id}: ${failure.error}`).join("\n"),
        "Partial failure",
      );
    }
    return results;
  },

  batchBarHtml(key, actions) {
    const buttons = (actions || [])
      .map(
        (action) =>
          `<button type="button" class="ghost" data-batch="${this.esc(action.id)}">${this.esc(action.label)}</button>`,
      )
      .join("");
    return `<div class="del-sel-bar" data-sel-bar="${this.esc(key)}" hidden>
        <strong><span data-sel-count>0</span> selected</strong>
        ${buttons}
        <button type="button" class="ghost" data-sel-clear="${this.esc(key)}">Clear</button>
        <span class="sr-only" role="status" aria-live="polite" data-sel-live></span>
      </div>`;
  },

  bindBatchBar(key, handlers) {
    const bar = this.el?.querySelector(`[data-sel-bar="${key}"]`);
    if (!bar) return;
    bar.querySelectorAll("[data-batch]").forEach((button) => {
      button.addEventListener("click", async () => {
        const claim = "batch:" + key;
        if (window.AnvilUI.isRunning(claim)) return;
        const handler = handlers[button.getAttribute("data-batch")];
        const ids = this.selectionIds(key);
        if (!handler || !ids.length) return;
        const buttons = [...bar.querySelectorAll("button")];
        buttons.forEach((item) => (item.disabled = true));
        bar.setAttribute("aria-busy", "true");
        try {
          return await window.AnvilUI.withAction(
            button,
            { key: claim, host: this.uiModal(), errorTitle: "Batch failed" },
            () => handler(ids, key),
          );
        } finally {
          buttons.forEach((item) => (item.disabled = false));
          bar.removeAttribute("aria-busy");
        }
      });
    });
  },
});
