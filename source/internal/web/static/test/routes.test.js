// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { JSDOM } = require("jsdom");
const { FILES, ROOT } = require("./harness");

function snapshotFixture() {
  return {
    majors: [{ major: 0, title: "Pre-release", summary: "Fixture generation", status: "developing" }],
    workers: [{ id: "worker-1", name: "Fixture worker", project_rel: "fixture" }],
    eng_reqs: [{
      id: "eng-1",
      worker_id: "worker-1",
      title: "Fixture requirement",
      versions: [{
        id: "req-ver-1",
        major: 0,
        minor: 45,
        patch: 0,
        status: "draft",
        goal: "Render every route",
        success: "- [ ] all routes render",
      }],
    }],
    designs: [{
      id: "design-1",
      eng_req_id: "eng-1",
      title: "Fixture design",
      major: 0,
      minor: 45,
      status: "draft",
      versions: [{
        id: "design-ver-1",
        sort_order: 1,
        title: "Route seam",
        gate: "- [ ] smoke",
        sub_architecture: "DOM fixture",
        class_design: "No classes",
        depends_on: [],
      }],
    }],
    roadmap: {
      milestones: [{
        id: "ms_eng-1_0_45",
        title: "Fixture milestone",
        status: "planned",
        phases: [],
      }],
      edges: [],
    },
    gates: [],
    polishes: [],
    buglist_items: [],
    test_cases: [],
    campaigns: [],
    audit_events: [],
    trash_count: 0,
  };
}

async function loadDOM() {
  const dom = new JSDOM("<!doctype html><main id=\"app\"></main>", {
    url: "http://anvil.test/#/delivery/requirements",
    runScripts: "outside-only",
  });
  const { window } = dom;
  window.console = console;
  window.Features = {};
  window.AnvilUI = {};
  window.api = {
    v1: new Proxy(
      { delivery: async () => snapshotFixture() },
      { get: (target, key) => target[key] || (async () => ({})) },
    ),
  };
  window.AiChat = { flash() {} };
  window.ResizeObserver = class {
    observe() {}
    disconnect() {}
  };

  const context = dom.getInternalVMContext();
  for (const rel of FILES) {
    if (rel === "js/features/delivery/index.js") {
      for (const component of ["js/components/ai_chat.js", "js/components/md_preview.js"]) {
        vm.runInContext(
          fs.readFileSync(path.join(ROOT, component), "utf8"),
          context,
          { filename: component },
        );
      }
    }
    vm.runInContext(fs.readFileSync(path.join(ROOT, rel), "utf8"), context, { filename: rel });
  }
  const delivery = window.Features.delivery;
  delivery.el = window.document.querySelector("#app");
  delivery._clustersLoaded = true;
  delivery._projectCatalog = [];
  await delivery.reload();
  return { dom, window, delivery };
}

test("every documented Delivery route renders a non-empty body", async (t) => {
  const { dom, window, delivery } = await loadDOM();
  t.after(() => dom.window.close());

  const routes = [
    "#/delivery/requirements",
    "#/delivery/requirements/m/0",
    "#/delivery/requirements/projects",
    "#/delivery/requirements/eng-1",
    "#/delivery/requirements/eng-1/0/45",
    "#/delivery/requirements/eng-1/0/45/0",
    "#/delivery/designs",
    "#/delivery/designs/m/0",
    "#/delivery/designs/design-1",
    "#/delivery/roadmap",
    "#/delivery/roadmap/m/0",
    "#/delivery/test",
    "#/delivery/test/m/0",
    "#/delivery/readiness",
  ];

  for (const hash of routes) {
    window.location.hash = hash;
    assert.doesNotThrow(() => delivery.render(), hash);
    const body = delivery.el.querySelector(".del-body");
    assert.ok(body, `${hash} did not produce .del-body`);
    assert.ok(body.textContent.trim().length > 0, `${hash} produced an empty .del-body`);
  }
});

test("wide-only Delivery surfaces declare their 480px fallback in the DOM", async (t) => {
  const { dom, window, delivery } = await loadDOM();
  t.after(() => dom.window.close());
  Object.defineProperty(window, "innerWidth", { configurable: true, value: 480 });

  for (const hash of ["#/delivery/roadmap/m/0", "#/delivery/designs/design-1"]) {
    window.location.hash = hash;
    delivery.render();
    const note = delivery.el.querySelector(".desktop-only");
    const wide = delivery.el.querySelector(".needs-wide");
    assert.ok(note, `${hash} has no desktop-only fallback`);
    assert.ok(wide, `${hash} does not mark its wide-only control`);
    assert.match(note.textContent, /wider screen/i);
    assert.match(note.textContent, /still|remain|available|below/i);
  }
});

test("readiness refresh renders server evidence and fix navigation", async (t) => {
  const { dom, window, delivery } = await loadDOM();
  t.after(() => dom.window.close());
  let calls = 0;
  window.api.v1.deliveryReadiness = async () => {
    calls++;
    return {
      rows: [{
        kind: "milestone",
        id: "ms_eng-1_0_45",
        title: "Fixture milestone",
        lifecycle: "awaiting_development",
        transition: "start_phase",
        ok: false,
        href: "#/delivery/roadmap/m/0",
        blockers: [{
          code: "gate_open",
          message: "checkpointing is unchecked",
          href: "#/delivery/designs/design-1",
        }],
      }],
      consistency: {
        summary: { blocking_errors: 1, warnings: 0, informational: 1 },
        findings: [{
          severity: "blocking_error",
          code: "version_dependency_cycle",
          message: "cycle",
          href: "#/delivery/designs/design-1",
        }],
      },
    };
  };
  window.location.hash = "#/delivery/readiness";
  delivery.render();
  await new Promise((resolve) => window.setTimeout(resolve, 0));
  assert.match(delivery.el.textContent, /checkpointing is unchecked/);
  assert.match(delivery.el.textContent, /version_dependency_cycle/);

  delivery.el.querySelector("#readiness-refresh").click();
  await new Promise((resolve) => window.setTimeout(resolve, 0));
  assert.equal(calls, 2);

  const fix = [...delivery.el.querySelectorAll("a")].find((a) => a.textContent === "Fix");
  assert.ok(fix);
  assert.equal(fix.getAttribute("href"), "#/delivery/designs/design-1");
  window.location.hash = fix.getAttribute("href");
  assert.equal(window.location.hash, "#/delivery/designs/design-1");
});
