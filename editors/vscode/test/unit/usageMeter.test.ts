/**
 * The composer's token meter: the arithmetic, and where it is wired in.
 *
 * The sums live in `media/meter.js`, a plain browser script that assigns a
 * global — `media/` is linted as browser code and never built, so the same
 * absolute-path load `markdown.test.ts` uses is how a Node test reaches it.
 * What is checked here is the half a screenshot cannot: that a run with no
 * usage adds nothing, that the counts are cumulative across a session, and that
 * a fraction appears only when somebody configured a window.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const METER_PATH = join(EXTENSION_ROOT, 'media', 'meter.js');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_CSS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.css'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');
const HUB = readFileSync(join(EXTENSION_ROOT, 'src', 'hub.ts'), 'utf8');
const PROTOCOL = readFileSync(join(EXTENSION_ROOT, 'src', 'protocol.ts'), 'utf8');
const SETTINGS = readFileSync(join(EXTENSION_ROOT, 'src', 'settings.ts'), 'utf8');
const PACKAGE = JSON.parse(readFileSync(join(EXTENSION_ROOT, 'package.json'), 'utf8'));

// A script loaded for its side effect, not a module imported for its exports.
// eslint-disable-next-line @typescript-eslint/no-require-imports
require(METER_PATH);

interface Usage {
  input_tokens?: number;
  output_tokens?: number;
  total_tokens?: number;
}

interface Totals {
  input: number;
  output: number;
  total: number;
  last: { input: number; output: number; total: number } | null;
}

const meter = (
  globalThis as unknown as {
    anvilMeter: {
      empty: () => Totals;
      add: (state: Totals, done: { usage?: Usage } | undefined) => Totals;
      label: (state: Totals, contextWindow: number) => string;
      count: (value: unknown) => number;
      short: (value: unknown) => string;
    };
  }
).anvilMeter;

/** A finished run, as the host's `done` or a replayed `run_end` carries it. */
function run(usage?: Usage): { usage?: Usage } {
  return usage === undefined ? {} : { usage };
}

/** A top-level function of the renderer, from its declaration to the next one. */
function bodyOf(source: string, name: string): string {
  const start = source.indexOf(`\n  function ${name}(`);
  assert.ok(start >= 0, `${name} is in chat.js`);
  const rest = source.slice(start + 1);
  const next = rest.indexOf('\n  function ');
  return next >= 0 ? rest.slice(0, next) : rest;
}

test('an empty session says nothing', () => {
  const state = meter.empty();
  assert.deepEqual(state, { input: 0, output: 0, total: 0, last: null });
  assert.equal(meter.label(state, 0), '');
  assert.equal(meter.label(state, 200000), '');
});

test('a run with no usage adds nothing, because unknown is not zero', () => {
  const state = meter.empty();
  meter.add(state, run());
  assert.equal(state.input, 0);
  assert.equal(state.output, 0);
  assert.equal(state.total, 0);
  assert.equal(meter.label(state, 200000), '', 'nothing to say, window or not');
  // A run that reported usage is not forgotten by a later run that did not:
  // the meter keeps the last figure it actually has.
  meter.add(state, run({ input_tokens: 4, output_tokens: 3, total_tokens: 7 }));
  meter.add(state, run());
  assert.equal(state.last?.total, 7);
  assert.equal(state.total, 7);
});

test('one run shows its input and output, with no fraction unasked', () => {
  const state = meter.empty();
  meter.add(state, run({ input_tokens: 120, output_tokens: 18, total_tokens: 138 }));
  assert.deepEqual(state, {
    input: 120,
    output: 18,
    total: 138,
    last: { input: 120, output: 18, total: 138 },
  });
  assert.equal(meter.label(state, 0), '120 in · 18 out');
});

test('the session totals accumulate, and the last run stays the last', () => {
  const state = meter.empty();
  meter.add(state, run({ input_tokens: 4, output_tokens: 3, total_tokens: 7 }));
  meter.add(state, run({ input_tokens: 10, output_tokens: 4, total_tokens: 14 }));
  assert.deepEqual(state, {
    input: 14,
    output: 7,
    total: 21,
    last: { input: 10, output: 4, total: 14 },
  });
  assert.equal(meter.label(state, 100), '14 in · 7 out · last 14 of 100 (14%)');
});

test('a window turns the last run into a fraction of it', () => {
  const state = meter.empty();
  meter.add(state, run({ input_tokens: 120, output_tokens: 18, total_tokens: 138 }));
  // Below ten percent the difference between 0.0% and 0.1% is the whole story.
  assert.match(meter.label(state, 200000), /last 138 of 200k \(0\.1%\)$/);
  const atTen = meter.empty();
  meter.add(atTen, run({ input_tokens: 18000, output_tokens: 2000, total_tokens: 20000 }));
  assert.match(meter.label(atTen, 200000), /\(10%\)$/);
});

test('a missing total is the sum of the halves, not zero', () => {
  const state = meter.empty();
  meter.add(state, run({ input_tokens: 4, output_tokens: 3 }));
  assert.equal(state.total, 7);
  assert.equal(state.last?.total, 7);
});

test('a malformed count cannot put NaN on the screen', () => {
  const state = meter.empty();
  meter.add(state, run({ input_tokens: Number.NaN, output_tokens: -4, total_tokens: 9 }));
  assert.equal(state.input, 0);
  assert.equal(state.output, 0);
  assert.equal(state.total, 9);
  assert.equal(meter.count(Number.POSITIVE_INFINITY), 0);
  assert.equal(meter.count('12' as unknown), 0);
});

test('counts use one shorthand', () => {
  assert.equal(meter.short(999), '999');
  assert.equal(meter.short(1200), '1.2k');
  assert.equal(meter.short(12000), '12k');
  assert.equal(meter.short(0), '');
  assert.equal(meter.count(42.9), 42);
});

test('the renderer counts a run once, at its terminal envelope', () => {
  assert.match(bodyOf(CHAT_JS, 'finishRun'), /recordUsage\(done\)/);
  assert.match(bodyOf(CHAT_JS, 'addRunEnd'), /recordUsage\(done\)/);
  // The transcript reset is also the session change, and the meter describes
  // the session, so the two are cleared together.
  assert.match(bodyOf(CHAT_JS, 'clearTranscript'), /resetUsage\(\)/);
  // An event-carried usage would be counted twice: the `done` that follows an
  // error carries the same run's total. Only the envelopes are counted.
  const event = CHAT_JS.slice(CHAT_JS.indexOf("case 'event'"), CHAT_JS.indexOf("case 'approval'"));
  assert.doesNotMatch(event, /recordUsage|meterMath\.add/);
});

test('the context window reaches the meter, and unknown stays unknown', () => {
  assert.match(CHAT_JS, /case 'context':/);
  assert.match(CHAT_JS, /contextWindow = typeof msg\.window === 'number' \? msg\.window : 0/);
  assert.match(CHAT_JS, /const count = meterMath\.short/);
  // Text only, like every other renderer here.
  const render = bodyOf(CHAT_JS, 'renderMeter');
  assert.doesNotMatch(render, /\.innerHTML\s*(\+)?=(?!=)/);
  assert.doesNotMatch(render, /insertAdjacentHTML\s*\(/);
  assert.match(render, /meter\.textContent/);
  // The element and its script have to exist in the document it is drawn in.
  assert.match(CHAT_PANEL, /<div id="meter" class="meter" hidden><\/div>/);
  const meterScript = CHAT_PANEL.indexOf("asset('meter.js')");
  const chatScript = CHAT_PANEL.indexOf("asset('chat.js')");
  assert.ok(meterScript > 0 && meterScript < chatScript, 'meter.js loads before chat.js');
  assert.match(CHAT_CSS, /\.meter\s*\{/);
});

test('the panel reports the window from the setting, on load and on change', () => {
  const report = CHAT_PANEL.slice(CHAT_PANEL.indexOf('reportContext(): void {'));
  assert.ok(report.length > 0, 'reportContext is in the panel');
  assert.match(report.slice(0, report.indexOf('\n  }')), /type: 'context', window: readSettings\(\)\.contextWindow/);
  // The ready path posts it every time a document starts, and a settings
  // change restarts the host without reloading tabs, so the hub reposts it.
  assert.match(CHAT_PANEL, /this\.post\(\{ type: 'vision'.*\}\);\r?\n\s*this\.reportContext\(\);/);
  assert.match(HUB, /panel\.reportContext\(\)/);
});

test('the setting is a number that defaults to unknown', () => {
  const property = PACKAGE.contributes.configuration.properties['anvil.contextWindow'];
  assert.equal(property.type, 'number');
  assert.equal(property.default, 0);
  assert.equal(property.minimum, 0);
  assert.match(property.markdownDescription, /does not guess/);
  assert.match(SETTINGS, /get<number>\('contextWindow', 0\)/);
  assert.match(SETTINGS, /Number\.isFinite\(window\) && window > 0 \? Math\.floor\(window\) : 0/);
});

test('the protocol carries usage on an event', () => {
  const hostEvent = PROTOCOL.slice(PROTOCOL.indexOf('export interface HostEvent'));
  assert.match(hostEvent.slice(0, hostEvent.indexOf('\n}')), /usage\?: Usage/);
  assert.match(PROTOCOL, /export interface Usage \{/);
});
