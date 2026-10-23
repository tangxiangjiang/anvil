// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { JSDOM } = require("jsdom");
const { ROOT } = require("./harness");

function loadDOM(html) {
  const dom = new JSDOM(html, { url: "http://anvil.test/", runScripts: "outside-only" });
  const { window } = dom;
  window.Features = {};
  window.AnvilUI = {};
  const context = dom.getInternalVMContext();
  for (const rel of [
    "js/shared/esc.js",
    "js/shared/action.js",
    "js/shared/selection.js",
    "js/features/delivery/selection.js",
  ]) {
    vm.runInContext(fs.readFileSync(path.join(ROOT, rel), "utf8"), context, { filename: rel });
  }
  const delivery = window.Features.delivery;
  delivery.el = window.document.querySelector("#app");
  delivery.uiModal = () => null;
  delivery.showNotice = () => {};
  return { dom, window, delivery };
}

function key(window, row, value, init = {}) {
  row.dispatchEvent(new window.KeyboardEvent("keydown", { key: value, bubbles: true, ...init }));
}

test("all three lists share keyboard, focus, and screen-reader selection behavior", (t) => {
  const { dom, window, delivery } = loadDOM(`
    <input id="global-search">
    <main id="app">
      <div data-sel-bar="roadmap" hidden><span data-sel-count></span><span data-sel-live></span></div>
      <div id="roadmap"><div class="del-sel-row" data-sel-list="roadmap" data-sel-id="m1"></div>
        <div class="del-sel-row" data-sel-list="roadmap" data-sel-id="m2"></div></div>
      <div data-sel-bar="test-cases" hidden><span data-sel-count></span><span data-sel-live></span></div>
      <ul id="cases"><li class="del-sel-row" data-sel-list="test-cases" data-sel-id="c1"></li></ul>
      <div data-sel-bar="design-versions" hidden><span data-sel-count></span><span data-sel-live></span></div>
      <div id="versions"><div class="del-sel-row" data-sel-list="design-versions" data-sel-id="v1"></div></div>
    </main>`);
  t.after(() => dom.window.close());
  const opened = [];
  for (const [name, ids] of [
    ["roadmap", ["m1", "m2"]],
    ["test-cases", ["c1"]],
    ["design-versions", ["v1"]],
  ]) {
    delivery.bindSelection(name, { presentIds: ids, onOpen: (id) => opened.push(id) });
  }

  const [m1, m2] = window.document.querySelectorAll('[data-sel-list="roadmap"][data-sel-id]');
  key(window, m1, " ");
  assert.equal(m1.getAttribute("aria-selected"), "true");
  assert.equal(window.document.querySelector('[data-sel-bar="roadmap"]').hidden, false);
  key(window, m2, " ", { shiftKey: true });
  assert.equal(delivery.sel("roadmap").size, 2);
  key(window, m1, "Escape");
  assert.equal(delivery.sel("roadmap").size, 0);
  key(window, m1, "a", { ctrlKey: true });
  assert.equal(delivery.sel("roadmap").size, 2);
  m1.focus();
  key(window, m1, "ArrowDown");
  assert.equal(window.document.activeElement, m2);
  key(window, m2, "Enter");
  assert.deepEqual(opened, ["m2"]);
  key(window, m2, "/");
  assert.equal(window.document.activeElement.id, "global-search");

  const c1 = window.document.querySelector('[data-sel-id="c1"]');
  key(window, c1, " ");
  assert.equal(delivery.sel("test-cases").size, 1);
  assert.equal(delivery.sel("design-versions").size, 0);
  assert.equal(c1.parentElement.getAttribute("aria-multiselectable"), "true");
  assert.equal(c1.getAttribute("role"), "option");
});

test("empty and refreshed lists prune stale ids without crossing list state", (t) => {
  const { dom, delivery } = loadDOM(`
    <main id="app"><div data-sel-bar="test-cases" hidden><span data-sel-count></span></div>
      <div><div data-sel-list="test-cases" data-sel-id="gone"></div></div></main>`);
  t.after(() => dom.window.close());
  delivery.bindSelection("test-cases", { presentIds: ["gone"] });
  delivery.sel("test-cases").toggle("gone");
  delivery.el.querySelector('[data-sel-id="gone"]').remove();
  delivery.bindSelection("test-cases", { presentIds: [] });
  assert.equal(delivery.sel("test-cases").size, 0);
  assert.equal(delivery.el.querySelector('[data-sel-bar="test-cases"]').hidden, true);
});

test("batch is single-flight, ordered, and keeps only failed selections", async (t) => {
  const { dom, delivery } = loadDOM(`
    <main id="app">
      <div data-sel-bar="roadmap"><button data-batch="start">Start</button>
        <button data-batch="stop">Stop</button><span data-sel-count></span></div>
      <div><div data-sel-list="roadmap" data-sel-id="a"></div>
        <div data-sel-list="roadmap" data-sel-id="b"></div>
        <div data-sel-list="roadmap" data-sel-id="c"></div></div>
    </main>`);
  t.after(() => dom.window.close());
  delivery.bindSelection("roadmap", { presentIds: ["a", "b", "c"] });
  delivery.sel("roadmap").toggle("c").toggle("a");
  let release;
  let calls = 0;
  const wait = new Promise((resolve) => (release = resolve));
  delivery.bindBatchBar("roadmap", {
    start: async (ids) => {
      calls++;
      assert.equal(ids.join(","), "a,c");
      await wait;
    },
    stop: async () => {
      calls++;
    },
  });
  const [start, stop] = delivery.el.querySelectorAll("[data-batch]");
  start.click();
  stop.click();
  assert.equal(calls, 1);
  assert.equal(start.disabled, true);
  assert.equal(start.closest("[data-sel-bar]").getAttribute("aria-busy"), "true");
  release();
  await new Promise((resolve) => setTimeout(resolve, 0));

  delivery.sel("roadmap").selectAll(["a", "b", "c"]);
  const order = [];
  let notice = "";
  delivery.showNotice = (message) => (notice = message);
  const results = await delivery.runBatch(
    delivery.selectionIds("roadmap"),
    async (id) => {
      order.push(id);
      if (id === "b") throw new Error("already running");
    },
    { key: "roadmap", okVerb: "started" },
  );
  assert.deepEqual(order, ["a", "b", "c"]);
  assert.equal(results.map((result) => result.ok).join(","), "true,false,true");
  assert.equal(delivery.sel("roadmap").ids().join(","), "b");
  assert.match(notice, /2 succeeded, 1 failed/);
  assert.match(notice, /b: already running/);
});
