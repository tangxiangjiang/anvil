// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * In-app notice — replaces every alert() in the console.
 *
 * A console an operator keeps open all day cannot use alert(): it blocks the
 * event loop, freezes any streaming paint behind it, and reads as a crash. This
 * is the same modal machinery, so it traps focus and closes on Escape like every
 * other dialog.
 */
window.AnvilUI = window.AnvilUI || {};

window.AnvilUI.notice = function notice(host, message, title) {
  const m = window.AnvilUI.modal;
  if (!host) return;
  m.open(
    host,
    `
    <div class="modal-card">
      <h3 style="margin-top:0">${window.AnvilUI.esc(title || "Notice")}</h3>
      <p class="muted" style="white-space:pre-wrap">${window.AnvilUI.esc(message || "")}</p>
      <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
        <button type="button" data-close-ui>OK</button>
      </div>
    </div>`,
    { title: title || "Notice" },
  );
};
