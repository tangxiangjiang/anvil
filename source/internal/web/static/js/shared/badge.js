// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Lifecycle vocabulary and the badge that renders it.
 *
 * Defined once because the status strings are used as keys in three places — the
 * label map, the badge class, and the filter lists — and a second copy is how
 * "Completed" and "complete" end up meaning different things on two screens.
 */
window.AnvilUI = window.AnvilUI || {};

window.AnvilUI.lifecycleLabel = function lifecycleLabel(st) {
  const map = {
    awaiting_design: "Awaiting design",
    awaiting_roadmap: "Awaiting roadmap",
    awaiting_development: "Awaiting development",
    in_development: "In development",
    testing: "Testing",
    complete: "Complete",
  };
  return map[st] || st || "";
};

/**
 * badgeClass maps a status to ok / warn / bad.
 *
 * Unknown statuses get the neutral class rather than a guess: a status this
 * function does not know is a status that was added somewhere else, and painting
 * it green would assert something nobody verified.
 */
window.AnvilUI.badgeClass = function badgeClass(status) {
  const s = (status || "").toLowerCase();
  if (
    s === "approved" ||
    s === "done" ||
    s === "active" ||
    s === "complete" ||
    s === "closed" ||
    s === "verified" ||
    s === "passed" ||
    s === "awaiting_development"
  )
    return "ok";
  if (
    s === "draft" ||
    s === "planned" ||
    s === "pending" ||
    s === "in_progress" ||
    s === "awaiting_design" ||
    s === "awaiting_roadmap" ||
    s === "awaiting_test" ||
    s === "in_development" ||
    s === "testing" ||
    s === "running" ||
    s === "open"
  )
    return "warn";
  if (s === "rejected" || s === "failed" || s === "error" || s === "cancelled") return "bad";
  return "";
};

/** badge renders the span; `label` overrides the lifecycle wording. */
window.AnvilUI.badge = function badge(status, label) {
  const text = label != null ? label : window.AnvilUI.lifecycleLabel(status);
  return `<span class="badge ${window.AnvilUI.badgeClass(status)}">${window.AnvilUI.esc(text)}</span>`;
};
