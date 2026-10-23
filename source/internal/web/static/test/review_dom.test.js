// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { JSDOM } = require("jsdom");
const { ROOT } = require("./harness");

function run(context, rel) {
  vm.runInContext(fs.readFileSync(path.join(ROOT, rel), "utf8"), context, { filename: rel });
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

test("review renders repository content as text and exposes read-only states", async (t) => {
  const dom = new JSDOM(`<main id="app"></main>`, {
    url: "http://anvil.test/#/clusters/fleet/review/node/worker",
    runScripts: "outside-only",
  });
  t.after(() => dom.window.close());
  const { window } = dom;
  window.AnvilUI = {};
  window.api = {
    v1: {
      reviewTree: async () => ({
        entries: [{ name: "<img src=x onerror=alert(1)>.md", path: "xss.md", is_dir: false }],
      }),
      reviewStatus: async () => ({
        branch: "main",
        status: [{ path: "xss.md", index: "?", worktree: "?" }],
      }),
      reviewFile: async () => ({
        content: "<script>window.pwned=true</script>\n# markdown",
        encoding: "utf-8",
      }),
      reviewDiff: async () => ({
        diff: "+<img src=x onerror=alert(1)>",
      }),
    },
  };
  const context = dom.getInternalVMContext();
  run(context, "js/shared/esc.js");
  run(context, "js/features/clusters/review.js");

  const app = window.document.querySelector("#app");
  window.AnvilReview.mount(app, { fleetId: "fleet", nodeId: "node", workerId: "worker" });
  await tick();
  await tick();
  assert.match(app.textContent, /Read-only in Anvil/);
  assert.equal(app.querySelectorAll("script, img").length, 0);
  app.querySelector(".review-tree-row").click();
  await tick();
  assert.equal(app.querySelector("[data-review-file]").textContent, "<script>window.pwned=true</script>\n# markdown");
  assert.equal(window.pwned, undefined);
  app.querySelector(".review-change-row").click();
  await tick();
  assert.equal(app.querySelector("[data-review-diff]").textContent, "+<img src=x onerror=alert(1)>");
  assert.equal(app.querySelectorAll("script, img").length, 0);
  assert.equal(app.querySelector("textarea, [contenteditable], [data-terminal]"), null);
});

test("review flattens staged, unstaged, and untracked status", () => {
  const dom = new JSDOM(``, { runScripts: "outside-only" });
  const { window } = dom;
  window.AnvilUI = { esc: String };
  run(dom.getInternalVMContext(), "js/features/clusters/review.js");
  assert.deepEqual(
    Array.from(window.AnvilReview.flattenStatus([
      { path: "both", index: "M", worktree: "M" },
      { path: "new", index: "?", worktree: "?" },
    ]), (item) => ({ ...item })),
    [
      { path: "both", staged: true },
      { path: "both", staged: false },
      { path: "new", staged: false },
    ],
  );
  dom.window.close();
});
