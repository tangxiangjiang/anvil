// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * HTML escaping, and the one place it is defined.
 *
 * Every value interpolated into a template literal goes through esc unless it is
 * provably not text: a number, a boolean, or an internal enum. That rule is
 * enforced by a test over the builders (p_0.46.5) rather than by discipline,
 * because the failure mode — stored XSS via a filename, a project title, or a
 * tool result — is silent.
 *
 * Deliberately not `innerHTML`-aware: this escapes for element content and for
 * a double-quoted attribute, which is every use in this UI. A single-quoted
 * attribute would need the apostrophe adding.
 */
window.AnvilUI = window.AnvilUI || {};

window.AnvilUI.esc = function esc(s) {
  return String(s ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
};
