// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Tests for the Workbench's SSE frame splitter.
 *
 * This is the part of the surface that fails silently: a frame that spans a read
 * boundary, a heartbeat comment treated as data, an `event:` line that belongs to
 * the next frame. None of those throw — they make a live stream look truncated or
 * make a JSON parse fail at runtime, which is why they are tested here rather
 * than by watching a stream by eye.
 */
const test = require("node:test");
const assert = require("node:assert");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const ROOT = path.resolve(__dirname, "..");

function loadWorkbench() {
  const sandbox = {
    window: {},
    console,
    document: undefined,
    module: { exports: {} },
    TextDecoder,
    JSON,
  };
  sandbox.window = sandbox;
  sandbox.window.Features = {};
  sandbox.window.AnvilUI = { esc: (s) => String(s ?? ""), badgeClass: () => "" };
  const src = fs.readFileSync(path.join(ROOT, "js/features/workbench.js"), "utf8");
  vm.runInNewContext(src, sandbox, { filename: "workbench.js" });
  return sandbox.module.exports;
}

test("splitFrames reads event + data pairs", () => {
  const wb = loadWorkbench();
  const frames = wb.splitFrames(
    'event: job.event\ndata: {"type":"assistant","text":"hi"}\n\n' +
      'event: job.done\ndata: {"status":"finished"}\n\n',
  );
  assert.strictEqual(frames.length, 2);
  assert.strictEqual(frames[0].event, "job.event");
  assert.strictEqual(frames[0].data.text, "hi");
  assert.strictEqual(frames[1].event, "job.done");
  assert.strictEqual(frames[1].data.status, "finished");
});

test("splitFrames ignores heartbeat comments", () => {
  const wb = loadWorkbench();
  const frames = wb.splitFrames(': ping\n\nevent: job.event\ndata: {"a":1}\n\n');
  assert.strictEqual(frames.length, 1);
  assert.strictEqual(frames[0].data.a, 1);
});

test("splitFrames tolerates data that is not JSON", () => {
  const wb = loadWorkbench();
  const frames = wb.splitFrames("event: job.event\ndata: plain text\n\n");
  assert.strictEqual(frames.length, 1);
  // A non-JSON payload degrades to text rather than being dropped: losing an
  // event is worse than showing it unparsed.
  assert.strictEqual(frames[0].data.text, "plain text");
});

test("splitFrames resets the event name at a frame boundary", () => {
  const wb = loadWorkbench();
  // The second frame has no `event:` line; it must not inherit the first one's.
  const frames = wb.splitFrames('event: job.done\ndata: {"a":1}\n\ndata: {"b":2}\n\n');
  assert.strictEqual(frames.length, 2);
  assert.strictEqual(frames[0].event, "job.done");
  assert.strictEqual(frames[1].event, "");
});

test("a frame split across reads reassembles", () => {
  const wb = loadWorkbench();
  // Model the consumer's buffering: split on newline, keep the remainder, and
  // only hand complete lines to the splitter.
  const parts = ['event: job.event\nda', 'ta: {"type":"assis', 'tant","text":"ok"}\n\n'];
  let buf = "";
  const collected = [];
  for (const p of parts) {
    buf += p;
    const lines = buf.split("\n");
    buf = lines.pop() || "";
    if (lines.length) {
      // Re-join only the complete lines, newline-terminated, so the splitter sees
      // whole frames exactly as the live consumer would.
      const complete = lines.join("\n") + "\n";
      for (const f of wb.splitFrames(complete)) collected.push(f);
    }
  }
  assert.strictEqual(collected.length, 1);
  assert.strictEqual(collected[0].data.text, "ok");
});
