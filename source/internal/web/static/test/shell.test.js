// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { JSDOM } = require("jsdom");
const { ROOT } = require("./harness");

function loadShell(report) {
  const dom = new JSDOM(
    `<aside class="side"><div class="side-foot"><div id="whoami"></div></div></aside>`,
    { url: "http://anvil.test/", runScripts: "outside-only" },
  );
  const { window } = dom;
  window.AnvilUI = {};
  window.api = {
    v1: {
      firstRun: async () => report,
      health: async () => ({ fleets: [], summary: "No fleet configured" }),
    },
  };
  const context = dom.getInternalVMContext();
  for (const rel of ["js/shared/esc.js", "js/shared/badge.js", "js/shared/modal.js", "js/shared/shell.js", "js/shared/action.js"]) {
    vm.runInContext(fs.readFileSync(path.join(ROOT, rel), "utf8"), context, { filename: rel });
  }
  return { dom, window, shell: window.AnvilShell };
}

async function settle() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

test("first-run progress is server-owned while close and reopen are browser-local", async (t) => {
  const report = {
    admin_created: true,
    fleet_configured: true,
    project_bound: false,
    requirement_created: false,
  };
  const { dom, window, shell } = loadShell(report);
  t.after(() => dom.window.close());

  shell.mountFirstRun();
  await settle();
  const panel = window.document.querySelector("#shell-first-run");
  const open = window.document.querySelector("#shell-first-run-open");
  assert.equal(open.textContent, "Setup checklist 2/4");
  assert.equal(panel.hidden, false);
  assert.equal(panel.querySelectorAll("li.done").length, 2);
  assert.equal(panel.getAttribute("aria-label"), "First-run checklist");

  panel.querySelector("[data-first-run-close]").click();
  assert.equal(panel.hidden, true);
  assert.deepEqual(JSON.parse(window.localStorage.getItem(shell.firstRunStorageKey)), {
    version: 1,
    dismissed: true,
  });

  report.project_bound = true;
  await shell.refreshFirstRun();
  assert.equal(open.textContent, "Setup checklist 3/4");
  assert.equal(panel.hidden, true, "refresh must retain the explicit close preference");

  open.click();
  await settle();
  assert.equal(panel.hidden, false);
  assert.equal(panel.querySelectorAll("li.done").length, 3);
  assert.equal(window.localStorage.getItem(shell.firstRunStorageKey).includes("project"), false);
});

test("clearing browser data restores automatic first-run display", async (t) => {
  const state = {
    admin_created: true,
    fleet_configured: false,
    project_bound: false,
    requirement_created: false,
  };
  const { dom, window, shell } = loadShell(state);
  t.after(() => dom.window.close());
  window.localStorage.setItem(shell.firstRunStorageKey, JSON.stringify({ version: 1, dismissed: true }));
  shell.mountFirstRun();
  await settle();
  assert.equal(window.document.querySelector("#shell-first-run").hidden, true);
  window.localStorage.clear();
  await shell.refreshFirstRun();
  assert.equal(window.document.querySelector("#shell-first-run").hidden, false);
});

test("help and classified errors use accessible dialogs and retain raw text", async (t) => {
  const { dom, window, shell } = loadShell({});
  t.after(() => dom.window.close());
  window.api.v1.classifyError = async () => ({
    title: "Worker unavailable",
    what: "The Worker is not in a Fleet.",
    next: "Open Clusters.",
    raw: "worker w1 missing",
    href: "#/clusters",
    request_id: "req-help-1",
  });
  shell.openHelp("clusters");
  let dialog = window.document.querySelector("#shell-modal");
  assert.equal(dialog.getAttribute("role"), "dialog");
  assert.ok(dialog.getAttribute("aria-labelledby"));
  window.AnvilUI.modal.close(dialog);

  await shell.showError("worker w1 missing", 409);
  dialog = window.document.querySelector("#shell-modal");
  assert.equal(dialog.getAttribute("role"), "dialog");
  assert.equal(dialog.querySelector(".err-detail pre").textContent, "worker w1 missing");
  assert.equal(dialog.querySelector("a").getAttribute("href"), "#/clusters");
  assert.match(dialog.textContent, /Request ID req-help-1/);
});

test("health strip reports Fleet, Worker capacity, and active Jobs", async (t) => {
  const { dom, window, shell } = loadShell({});
  t.after(() => dom.window.close());
  window.api.v1.health = async () => ({
    summary: "All Fleets reachable",
    active_jobs: 3,
    fleets: [{ id: "local", reachable: true, idle: 2, busy: 1 }],
  });
  shell.mountHealth();
  await settle();
  const text = window.document.querySelector("#shell-health").textContent;
  assert.match(text, /All Fleets reachable/);
  assert.match(text, /Workers: 2 idle · 1 busy/);
  assert.match(text, /Jobs: 3 active/);
});

test("shared actions route failures through actionable classification", async (t) => {
  const { dom, window } = loadShell({});
  t.after(() => dom.window.close());
  let classified;
  window.api.v1.classifyError = async (body) => {
    classified = body;
    return {
      title: "A Gate is incomplete",
      what: "A Design Version Gate has unchecked items.",
      next: "Open Designs and finish the Gate.",
      raw: body.message,
      href: "#/delivery/designs",
    };
  };
  const error = new Error("gate checklist incomplete");
  error.status = 409;
  error.requestId = "req-action-1";
  await window.AnvilUI.withAction(null, {}, async () => {
    throw error;
  });
  assert.equal(classified.message, "gate checklist incomplete");
  assert.equal(classified.status, 409);
  assert.equal(classified.request_id, "req-action-1");
  assert.equal(window.document.querySelector("#shell-modal a").getAttribute("href"), "#/delivery/designs");
});
