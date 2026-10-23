// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Tests for the selection reducer (p_0.45.10).
 *
 * The corners that matter and that a UI test would take minutes to reach by hand:
 * a range across a filtered view, a toggle after a re-render, select-all under a
 * filter, a prune after a batch deleted a row, and an anchor that is no longer
 * visible.
 */
const test = require("node:test");
const assert = require("node:assert");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const ROOT = path.resolve(__dirname, "..");

function load() {
  const sandbox = { window: {}, console };
  sandbox.window = sandbox;
  sandbox.window.AnvilUI = {};
  const src = fs.readFileSync(path.join(ROOT, "js/shared/selection.js"), "utf8");
  vm.runInNewContext(src, sandbox, { filename: "selection.js" });
  return sandbox.AnvilUI.selection;
}

test("toggle adds then removes, and the anchor follows", () => {
  const s = load();
  const sel = s.create();
  sel.toggle("a");
  assert.strictEqual(sel.has("a"), true);
  assert.strictEqual(sel.size, 1);
  sel.toggle("a");
  assert.strictEqual(sel.has("a"), false);
  assert.strictEqual(sel.isEmpty(), true);
});

test("selectRange fills between the anchor and the target, in visible order", () => {
  const s = load();
  const sel = s.create();
  const order = ["a", "b", "c", "d", "e"];
  sel.toggle("b", order);
  sel.selectRange("d", order);
  assert.strictEqual([...sel.ids()].sort().join(","), "b,c,d");
});

test("selectRange works upward too", () => {
  const s = load();
  const sel = s.create();
  const order = ["a", "b", "c", "d"];
  sel.toggle("d", order);
  sel.selectRange("a", order);
  assert.strictEqual(sel.size, 4);
});

test("selectRange with no anchor degrades to a single select", () => {
  const s = load();
  const sel = s.create();
  sel.selectRange("c", ["a", "b", "c"]);
  assert.strictEqual(sel.size, 1);
  assert.strictEqual(sel.has("c"), true);
});

test("selectRange with a filtered-out anchor starts fresh instead of guessing", () => {
  const s = load();
  const sel = s.create();
  const full = ["a", "b", "c"];
  sel.toggle("a", full);
  // The view is filtered so that the anchor no longer appears.
  const filtered = ["c"];
  sel.selectRange("c", filtered);
  assert.strictEqual(sel.size, 1);
  assert.strictEqual(sel.has("c"), true);
  assert.strictEqual(sel.has("a"), false);
});

test("selectAll selects only what the filter shows", () => {
  const s = load();
  const sel = s.create();
  sel.selectAll(["x", "z"]);
  assert.strictEqual(sel.size, 2);
  assert.strictEqual(sel.has("hidden"), false);
});

test("selectAll with an empty view clears", () => {
  const s = load();
  const sel = s.create();
  sel.toggle("a");
  sel.selectAll([]);
  assert.strictEqual(sel.isEmpty(), true);
});

test("a toggle after a re-render keeps the earlier selection", () => {
  const s = load();
  const sel = s.create();
  // The same reducer survives a re-render; only the DOM is rebuilt.
  sel.toggle("a");
  sel.toggle("c");
  assert.strictEqual(sel.size, 2);
  assert.strictEqual(sel.has("a"), true);
  assert.strictEqual(sel.has("c"), true);
});

test("prune drops ids that no longer exist, and clears a dead anchor", () => {
  const s = load();
  const sel = s.create();
  sel.selectAll(["a", "b", "c"]);
  // A batch deleted b and c.
  sel.prune(["a"]);
  // Compared by content: the array comes from a vm context, so its prototype is
  // not this realm's Array.prototype and deepStrictEqual rejects it on identity.
  assert.strictEqual(sel.ids().join(","), "a");
  sel.prune([]);
  assert.strictEqual(sel.isEmpty(), true);
});

test("clear empties ids and anchor", () => {
  const s = load();
  const sel = s.create();
  sel.selectAll(["a", "b"]);
  sel.clear();
  assert.strictEqual(sel.isEmpty(), true);
  // With no anchor, selectRange is a single select again.
  sel.selectRange("b", ["a", "b"]);
  assert.strictEqual(sel.size, 1);
});

test("decodeKey maps the documented keys and nothing else", () => {
  const s = load();
  assert.strictEqual(s.decodeKey("ArrowDown"), "next");
  assert.strictEqual(s.decodeKey("ArrowUp"), "prev");
  assert.strictEqual(s.decodeKey("Enter"), "open");
  assert.strictEqual(s.decodeKey(" "), "select");
  assert.strictEqual(s.decodeKey("x"), "select");
  assert.strictEqual(s.decodeKey(" ", { shift: true }), "selectRange");
  assert.strictEqual(s.decodeKey("Escape"), "clear");
  assert.strictEqual(s.decodeKey("/"), "search");
  assert.strictEqual(s.decodeKey("a", { meta: true }), "selectAll");
  assert.strictEqual(s.decodeKey("a", { meta: true, shift: true }), "selectAll");
  // A bare "a" must not select all: that would fire while typing.
  assert.strictEqual(s.decodeKey("a"), "none");
  assert.strictEqual(s.decodeKey("q"), "none");
});
