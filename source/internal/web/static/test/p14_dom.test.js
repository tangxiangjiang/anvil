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
  vm.runInContext(
    fs.readFileSync(path.join(ROOT, rel), "utf8"),
    context,
    { filename: rel },
  );
}

test("Ops keeps stale DOM on failure and Retry replaces it", async (t) => {
  const dom = new JSDOM(`<main id="app"></main>`, {
    url: "http://anvil.test/#/ops",
    runScripts: "outside-only",
  });
  t.after(() => dom.window.close());
  const { window } = dom;
  const context = dom.getInternalVMContext();
  window.Features = {};
  window.AnvilUI = {};
  window.setInterval = () => 1;
  window.clearInterval = () => {};
  let offline = false;
  window.api = {
    v1: {
      health: async () => {
        if (offline) throw new Error("local fake disconnected");
        return {
          summary: "healthy",
          fleets: [{ id: "local", reachable: true, idle: 1, busy: 0 }],
        };
      },
      jobs: async () => ({
        jobs: offline
          ? []
          : [{ job_id: "job-1", kind: "phase", status: "running", prompt: "secret" }],
      }),
      deliveryReadiness: async () => ({
        rows: [{
          id: "ms-1",
          title: "Ship phone ops",
          lifecycle: "in_progress",
          transition: "submit_test",
          ok: false,
          blockers: [{ code: "gate", message: "Gate incomplete" }],
          href: "#/delivery/roadmap/m/0",
        }],
      }),
      jobCancel: async () => {},
    },
  };
  for (const rel of [
    "js/shared/esc.js",
    "js/shared/badge.js",
    "js/shared/action.js",
    "js/shared/swr.js",
    "js/features/ops.js",
  ]) run(context, rel);

  const app = window.document.querySelector("#app");
  await window.Features.ops.mount(app);
  assert.ok(app.querySelector("[data-ops-stop='job-1']"));
  assert.match(app.textContent, /Readiness/);
  assert.match(app.textContent, /1 blocked/);
  assert.equal(app.querySelector(".ops-open").getAttribute("href"), "#/delivery/roadmap/m/0");
  assert.equal(JSON.stringify(window.Features.ops._snapshot).includes("secret"), false);

  offline = true;
  await window.Features.ops.refresh();
  assert.ok(app.querySelector("[data-ops-stop='job-1']"), "failed refresh removed stale Job");
  assert.match(app.querySelector("[role='status']").textContent, /disconnected/);
  const retry = app.querySelector("[data-ops-retry]");
  assert.ok(retry, "failed refresh did not expose Retry");

  offline = false;
  retry.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.doesNotMatch(app.querySelector("[role='status']").textContent, /disconnected/);
});
