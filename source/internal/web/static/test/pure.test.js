// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Tests for the pure functions the Delivery UI depends on.
 *
 * These are the functions whose failure is silent: a route that parses to the
 * wrong view, a checklist index that toggles the wrong line, a markdown compose
 * that drops a section. None of them throw when they are wrong, which is why
 * they are tested rather than reviewed.
 */
const test = require("node:test");
const assert = require("node:assert");
const { loadUI } = require("./harness");

const ui = () => loadUI().Features.delivery;

// ---------------------------------------------------------------- esc / badge
test("esc escapes the four characters that can break out of content or an attribute", () => {
  const { AnvilUI } = loadUI();
  assert.strictEqual(AnvilUI.esc(`<a href="x">&</a>`), "&lt;a href=&quot;x&quot;&gt;&amp;&lt;/a&gt;");
});

test("esc treats null and undefined as empty rather than the string", () => {
  const { AnvilUI } = loadUI();
  assert.strictEqual(AnvilUI.esc(null), "");
  assert.strictEqual(AnvilUI.esc(undefined), "");
});

test("badgeClass maps the documented statuses and leaves unknown ones neutral", () => {
  const { AnvilUI } = loadUI();
  for (const s of ["approved", "done", "complete", "passed", "verified", "closed"]) {
    assert.strictEqual(AnvilUI.badgeClass(s), "ok", s);
  }
  for (const s of ["draft", "planned", "in_progress", "testing", "in_development"]) {
    assert.strictEqual(AnvilUI.badgeClass(s), "warn", s);
  }
  for (const s of ["failed", "rejected", "error", "cancelled"]) {
    assert.strictEqual(AnvilUI.badgeClass(s), "bad", s);
  }
  // A status nobody classified must not be painted green.
  assert.strictEqual(AnvilUI.badgeClass("some_new_status"), "");
});

test("lifecycleLabel returns the human wording, and the raw value when unknown", () => {
  const { AnvilUI } = loadUI();
  assert.strictEqual(AnvilUI.lifecycleLabel("in_development"), "In development");
  assert.strictEqual(AnvilUI.lifecycleLabel("brand_new"), "brand_new");
  assert.strictEqual(AnvilUI.lifecycleLabel(undefined), "");
});

// ------------------------------------------------------------------ parseRoute
test("parseRoute reads the section from the hash", () => {
  for (const section of ["requirements", "designs", "roadmap", "test"]) {
    const sandbox = loadUI();
    sandbox.location.hash = `#/delivery/${section}`;
    const r = sandbox.Features.delivery.parseRoute();
    assert.strictEqual(r.section, section);
    assert.strictEqual(r.routeMajor, null);
  }
});

test("parseRoute defaults to requirements with no hash", () => {
  const sandbox = loadUI();
  sandbox.location.hash = "";
  assert.strictEqual(sandbox.Features.delivery.parseRoute().section, "requirements");
});

test("parseRoute reads the generation scope from /m/{M}", () => {
  const sandbox = loadUI();
  sandbox.location.hash = "#/delivery/designs/m/2";
  const r = sandbox.Features.delivery.parseRoute();
  assert.strictEqual(r.section, "designs");
  assert.strictEqual(r.routeMajor, 2);
  assert.strictEqual(r.msId, null);
});

// ---------------------------------------------------------- parseRoadmapMsBind
test("parseRoadmapMsBind splits the bootstrap milestone id", () => {
  const d = ui();
  // Compared field by field: the object is built inside a vm context, so it has
  // that context's Object.prototype and deepStrictEqual would reject it on
  // identity rather than on value.
  const b = d.parseRoadmapMsBind("ms_eng_1_2_3");
  // `^(.*)_(\d+)_(\d+)$` is greedy, so the *last* two digit groups are the
  // version and everything before them is the eng id.
  assert.strictEqual(b.engId, "eng_1");
  assert.strictEqual(b.major, 2);
  assert.strictEqual(b.minor, 3);
});

test("parseRoadmapMsBind rejects anything that is not a milestone id", () => {
  const d = ui();
  assert.strictEqual(d.parseRoadmapMsBind("not-a-milestone"), null);
  assert.strictEqual(d.parseRoadmapMsBind("ms_nounderscore"), null);
  assert.strictEqual(d.parseRoadmapMsBind(""), null);
  assert.strictEqual(d.parseRoadmapMsBind(null), null);
});

// ------------------------------------------------------- parseGateChecklist
test("parseGateChecklist reports index, text and done state", () => {
  const d = ui();
  const items = d.parseGateChecklist("- [ ] first\n- [x] second\nplain text");
  assert.strictEqual(items.length, 2);
  assert.strictEqual(items[0].index, 0);
  assert.strictEqual(items[0].text, "first");
  assert.strictEqual(items[0].done, false);
  assert.strictEqual(items[1].index, 1);
  assert.strictEqual(items[1].done, true);
});

test("parseGateChecklist finds nothing in prose that has no checkboxes", () => {
  const d = ui();
  assert.strictEqual(d.parseGateChecklist("just a paragraph\nwith two lines").length, 0);
  assert.strictEqual(d.parseGateChecklist("").length, 0);
});

// ------------------------------------------------------ patchChecklistItem
test("patchChecklistItem flips the checkbox the index names", () => {
  const d = ui();
  const before = "- [ ] a\n- [ ] b\n- [ ] c";
  const after = d.patchChecklistItem(before, 1, true);
  assert.strictEqual(after, "- [ ] a\n- [x] b\n- [ ] c");
});

test("patchChecklistItem flips back to unchecked", () => {
  const d = ui();
  assert.strictEqual(
    d.patchChecklistItem("- [x] a\n- [X] b", 0, false),
    "- [ ] a\n- [X] b",
  );
});

test("patchChecklistItem ignores an index that is not there", () => {
  const d = ui();
  const before = "- [ ] a";
  assert.strictEqual(d.patchChecklistItem(before, 9, true), before);
});

test("patchChecklistItem leaves non-checkbox lines alone", () => {
  const d = ui();
  const before = "intro\n- [ ] a\nnote";
  assert.strictEqual(d.patchChecklistItem(before, 0, true), "intro\n- [x] a\nnote");
});

// ------------------------------------------------------- campaignPassReady
test("campaignPassReady is false while a case is unrun", () => {
  const d = ui();
  const cases = [{ status: "passed" }, { status: "pending" }];
  assert.strictEqual(d.campaignPassReady(cases, []), false);
});

test("campaignPassReady is false while a bug is open", () => {
  const d = ui();
  const cases = [{ status: "passed" }];
  assert.strictEqual(d.campaignPassReady(cases, [{ status: "open" }]), false);
  assert.strictEqual(d.campaignPassReady(cases, [{ status: "fixing" }]), false);
});

test("campaignPassReady is true with all cases passed and no open bug", () => {
  const d = ui();
  assert.strictEqual(
    d.campaignPassReady([{ status: "passed" }, { status: "passed" }], [{ status: "closed" }]),
    true,
  );
});

test("campaignPassReady is false with no cases at all", () => {
  const d = ui();
  assert.strictEqual(d.campaignPassReady([], []), false);
});

// --------------------------------------------------------- parseCasePaste
test("parseCasePaste takes one case per line", () => {
  const d = ui();
  const cases = d.parseCasePaste("Checkpoint restore\nLogging works");
  assert.strictEqual(cases.length, 2);
  assert.strictEqual(cases[0].title, "Checkpoint restore");
  assert.strictEqual(cases[0].body, "Checkpoint restore");
  assert.strictEqual(cases[1].title, "Logging works");
});

test("parseCasePaste splits a multi-line body on a bare ---", () => {
  const d = ui();
  const cases = d.parseCasePaste(
    "Given a CRLF file\nWhen a run edits it\nThen endings survive\n---\nSecond case",
  );
  assert.strictEqual(cases.length, 2);
  assert.strictEqual(cases[0].title, "Given a CRLF file");
  // The body keeps its lines; the title is only the first one.
  assert.strictEqual(cases[0].body.split("\n").length, 3);
  assert.strictEqual(cases[1].title, "Second case");
});

test("parseCasePaste strips a leading 'Case:' label", () => {
  const d = ui();
  assert.strictEqual(d.parseCasePaste("Case: something works")[0].title, "something works");
});

test("parseCasePaste ignores blank input and blank blocks", () => {
  const d = ui();
  assert.strictEqual(d.parseCasePaste("").length, 0);
  assert.strictEqual(d.parseCasePaste("   \n  \n").length, 0);
  assert.strictEqual(d.parseCasePaste("a\n---\n---\nb").length, 2);
});

test("parseCasePaste titles an empty first line", () => {
  const d = ui();
  const cases = d.parseCasePaste("\n---\nbody only");
  assert.strictEqual(cases.length, 1);
  assert.strictEqual(cases[0].title, "body only");
});
