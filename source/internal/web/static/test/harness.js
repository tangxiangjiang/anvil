// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Harness for the front-end unit tests.
 *
 * The UI has no build step and no framework: `js/` files are classic scripts that
 * attach to `window`. This loads the ones the pure functions live in into a
 * minimal global, in the same order index.html uses, so a test exercises the same
 * wiring the browser does.
 *
 * What is deliberately *not* simulated: rendering. Anything that touches
 * `document` is out of scope here — a half-fake DOM would give false confidence,
 * and those paths are covered by each phase's manual acceptance check.
 */
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const ROOT = path.resolve(__dirname, "..");

/** The script order from index.html, restricted to what the tests need. */
const FILES = [
  "js/shared/esc.js",
  "js/shared/badge.js",
  "js/shared/action.js",
  "js/shared/selection.js",
  "js/features/delivery/core.js",
  "js/features/delivery/helpers.js",
  "js/features/delivery/modals.js",
  "js/features/delivery/jobs.js",
  "js/features/delivery/reqs.js",
  "js/features/delivery/versions.js",
  "js/features/delivery/designs.js",
  "js/features/delivery/roadmap.js",
  "js/features/delivery/canvas.js",
  "js/features/delivery/test.js",
  "js/features/delivery/wizard.js",
];

/** loadUI returns a fresh sandbox with `window.AnvilUI` and `Features.delivery`. */
function loadUI() {
  const sandbox = {
    window: {},
    document: undefined,
    location: { hash: "" },
    console,
    localStorage: { getItem: () => "", setItem: () => {}, removeItem: () => {} },
    setTimeout,
    clearTimeout,
    JSON,
  };
  sandbox.window = sandbox; // the scripts use both `window.X` and bare globals
  sandbox.window.Features = {};
  sandbox.window.AnvilUI = {};
  sandbox.globalThis = sandbox;

  for (const rel of FILES) {
    const src = fs.readFileSync(path.join(ROOT, rel), "utf8");
    vm.runInNewContext(src, sandbox, { filename: rel });
  }
  return sandbox;
}

module.exports = { loadUI, ROOT, FILES };
