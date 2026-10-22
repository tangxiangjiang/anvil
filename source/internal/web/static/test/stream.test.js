// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Tests for stream resumption (p_0.45.14).
 *
 * The two behaviours a dropped connection needs and that a manual test cannot
 * reach reliably: capped backoff that stops, and dedupe across a replay. Without
 * the second, a reconnect redraws the whole conversation.
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
  const src = fs.readFileSync(path.join(ROOT, "js/shared/stream.js"), "utf8");
  vm.runInNewContext(src, sandbox, { filename: "stream.js" });
  return sandbox.AnvilUI.stream;
}

test("backoff grows and then stops", () => {
  const s = load();
  const r = s.createResumer({ maxAttempts: 3, baseDelayMs: 100, maxDelayMs: 1000 });
  assert.strictEqual(r.dropped().delayMs, 100);
  assert.strictEqual(r.dropped().delayMs, 200);
  assert.strictEqual(r.dropped().delayMs, 400);
  // Fourth failure is past the cap.
  const last = r.dropped();
  assert.strictEqual(last.retry, false);
});

test("backoff is capped by maxDelayMs", () => {
  const s = load();
  const r = s.createResumer({ maxAttempts: 20, baseDelayMs: 1000, maxDelayMs: 5000 });
  for (let i = 0; i < 10; i++) r.dropped();
  const next = r.dropped();
  assert.ok(next.delayMs <= 5000, `delay ${next.delayMs} exceeded the cap`);
});

test("a successful open resets the failure count", () => {
  const s = load();
  const r = s.createResumer({ maxAttempts: 2 });
  r.dropped();
  r.dropped();
  r.opened();
  assert.strictEqual(r.attempt, 0);
  // It gets the full budget again.
  assert.strictEqual(r.dropped().retry, true);
});

test("dedupe drops a replayed event and keeps a new one", () => {
  const s = load();
  const r = s.createResumer();
  assert.strictEqual(r.firstSeen({ seq: 1 }), true);
  assert.strictEqual(r.firstSeen({ seq: 2 }), true);
  // The reconnect replays from the start.
  assert.strictEqual(r.firstSeen({ seq: 1 }), false);
  assert.strictEqual(r.firstSeen({ seq: 2 }), false);
  assert.strictEqual(r.firstSeen({ seq: 3 }), true);
  assert.strictEqual(r.seenCount, 3);
});

test("an event without a seq is always accepted", () => {
  const s = load();
  const r = s.createResumer();
  // Dropping a frame because it lacks an id would lose real content, which is
  // worse than drawing a duplicate.
  assert.strictEqual(r.firstSeen({ type: "assistant" }), true);
  assert.strictEqual(r.firstSeen({ type: "assistant" }), true);
  assert.strictEqual(r.seenCount, 0);
});

test("reset clears the seen set so a new session is not filtered", () => {
  const s = load();
  const r = s.createResumer();
  r.firstSeen({ seq: 1 });
  r.reset();
  assert.strictEqual(r.seenCount, 0);
  // A different session legitimately reuses seq numbers.
  assert.strictEqual(r.firstSeen({ seq: 1 }), true);
});

test("connection labels are the words the banner shows", () => {
  const s = load();
  assert.strictEqual(s.connectionLabel("live"), "live");
  assert.strictEqual(s.connectionLabel("reconnecting"), "reconnecting…");
  assert.strictEqual(s.connectionLabel("offline"), "disconnected");
  assert.strictEqual(s.connectionLabel("ended"), "ended");
  assert.strictEqual(s.connectionLabel("nonsense"), "");
});
