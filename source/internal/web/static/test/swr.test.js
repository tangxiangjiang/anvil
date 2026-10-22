// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

function load() {
  const sandbox = { window: {}, JSON, structuredClone, Date };
  sandbox.window = sandbox;
  sandbox.window.AnvilUI = {};
  vm.runInNewContext(
    fs.readFileSync(path.resolve(__dirname, "../js/shared/swr.js"), "utf8"),
    sandbox,
    { filename: "swr.js" },
  );
  return sandbox.AnvilUI.swr;
}

test("stale snapshot is detached and success replaces it atomically", async () => {
  const swr = load();
  let now = 100;
  const store = swr.createStore({ now: () => now });
  await store.revalidate("project:a:generation:1", async () => ({ rows: ["old"] }));
  const stale = store.peek("project:a:generation:1");
  stale.data.rows.push("caller mutation");
  assert.deepEqual(store.peek("project:a:generation:1").data.rows, ["old"]);

  now = 200;
  await store.revalidate("project:a:generation:1", async () => ({ rows: ["new"] }));
  assert.deepEqual(JSON.parse(JSON.stringify(store.peek("project:a:generation:1"))), {
    data: { rows: ["new"] },
    updatedAt: 200,
  });
});

test("scope keys do not cross projects or generations", async () => {
  const store = load().createStore();
  await store.revalidate("project:a:generation:1", async () => ({ id: "a1" }));
  await store.revalidate("project:a:generation:2", async () => ({ id: "a2" }));
  assert.equal(store.peek("project:b:generation:1"), null);
  assert.equal(store.peek("project:a:generation:1").data.id, "a1");
  assert.equal(store.peek("project:a:generation:2").data.id, "a2");
});

test("late responses cannot replace a newer generation", async () => {
  const store = load().createStore();
  let release;
  const slow = store.revalidate("scope", () => new Promise((resolve) => { release = resolve; }));
  const fast = await store.revalidate("scope", async () => ({ value: "new" }));
  release({ value: "old" });
  const late = await slow;
  assert.equal(fast.accepted, true);
  assert.equal(late.accepted, false);
  assert.equal(store.peek("scope").data.value, "new");
});

test("failed refresh preserves the last success and cache is bounded", async () => {
  const store = load().createStore({ maxEntries: 2 });
  await store.revalidate("a", async () => ({ value: "a" }));
  await assert.rejects(store.revalidate("a", async () => { throw new Error("offline"); }));
  assert.equal(store.peek("a").data.value, "a");
  await store.revalidate("b", async () => ({ value: "b" }));
  await store.revalidate("c", async () => ({ value: "c" }));
  assert.equal(store.size, 2);
  assert.equal(store.peek("a"), null);
});

test("projection excludes sensitive fields and expiry removes old snapshots", async () => {
  let now = 0;
  const store = load().createStore({ maxAgeMs: 10, now: () => now });
  await store.revalidate(
    "ops",
    async () => ({ id: "job", prompt: "secret incident text" }),
    { project: ({ id }) => ({ id }) },
  );
  assert.deepEqual(store.peek("ops").data, { id: "job" });
  now = 11;
  assert.equal(store.peek("ops"), null);
});
