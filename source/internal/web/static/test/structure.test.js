// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

const test = require("node:test");
const assert = require("node:assert");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const ROOT = path.resolve(__dirname, "..");

function jsFiles(dir = path.join(ROOT, "js")) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = path.join(dir, entry.name);
    return entry.isDirectory() ? jsFiles(full) : entry.name.endsWith(".js") ? [full] : [];
  });
}

test("every static JavaScript file stays within the milestone module limit", () => {
  for (const file of jsFiles()) {
    const lines = fs.readFileSync(file, "utf8").split(/\r?\n/).length;
    assert.ok(lines <= 1200, `${path.relative(ROOT, file)} has ${lines} lines`);
  }
});

test("index loads every local script in dependency order and retires delivery.js", () => {
  const html = fs.readFileSync(path.join(ROOT, "index.html"), "utf8");
  const scripts = [...html.matchAll(/<script(?:\s+defer)?\s+src="([^"]+)"/g)].map((m) => m[1]);
  const local = scripts.filter((src) => src.startsWith("/js/"));
  for (const src of local) {
    assert.ok(fs.existsSync(path.join(ROOT, src.slice(1))), `missing ${src}`);
  }
  assert.ok(!scripts.includes("/js/features/delivery.js"));
  assert.ok(scripts.includes("/js/features/delivery/index.js"));
  assert.ok(!scripts.includes("/js/features/delivery/core.js"));
  assert.ok(
    scripts.indexOf("/js/features/clusters.js") <
      scripts.indexOf("/js/features/clusters/workers.js"),
  );
  assert.ok(
    scripts.indexOf("/js/features/delivery/index.js") <
      scripts.indexOf("/js/features/delivery/helpers.js"),
  );
});

test("cluster split preserves one global feature object and worker API", () => {
  const sandbox = { window: { Features: {}, AnvilUI: {} } };
  sandbox.window.window = sandbox.window;
  sandbox.window.location = { hash: "" };
  sandbox.window.document = {};
  sandbox.window.api = {};
  const context = vm.createContext(sandbox.window);
  for (const rel of ["js/features/clusters.js", "js/features/clusters/workers.js"]) {
    vm.runInContext(fs.readFileSync(path.join(ROOT, rel), "utf8"), context, { filename: rel });
  }
  const feature = sandbox.window.Features.clusters;
  assert.strictEqual(typeof feature.mount, "function");
  assert.strictEqual(typeof feature.onRoute, "function");
  assert.strictEqual(typeof feature.openAddWorker, "function");
  assert.strictEqual(typeof feature.fsLoad, "function");
});

test("shared helper implementations have one owner", () => {
  const all = jsFiles().map((file) => fs.readFileSync(file, "utf8")).join("\n");
  for (const [name, pattern] of [
    ["esc", /AnvilUI\.esc\s*=\s*function\s+esc\b/g],
    ["lifecycleLabel", /AnvilUI\.lifecycleLabel\s*=\s*function\s+lifecycleLabel\b/g],
    ["badge", /AnvilUI\.badge\s*=\s*function\s+badge\b/g],
    ["notice", /AnvilUI\.notice\s*=\s*function\s+notice\b/g],
    ["action", /AnvilUI\.withAction\s*=\s*withAction\b/g],
    ["modal", /AnvilUI\.modal\s*=\s*\{/g],
    ["stream", /AnvilUI\.stream\s*=\s*\{/g],
  ]) {
    assert.strictEqual((all.match(pattern) || []).length, 1, name);
  }
});
