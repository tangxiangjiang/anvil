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
  cached_tokens?: number;
  cache_write_tokens?: number;
}

interface Totals {
  input: number;
  output: number;
  total: number;
  last: { input: number; output: number; total: number; cached?: number } | null;
}

interface ContextInfo {
  estimated_tokens?: number;
  window_tokens?: number;
  compact_at_tokens?: number;
  messages?: number;
}

interface Remaining {
  used: number;
  window: number;
  left: number;
  compactAt: number;
  leftUntilCompact: number;
  percent: number;
}

const meter = (
  globalThis as unknown as {
    anvilMeter: {
      empty: () => Totals;
      add: (state: Totals, done: { usage?: Usage } | undefined) => Totals;
      label: (state: Totals, contextWindow: number) => string;
      count: (value: unknown) => number;
      short: (value: unknown) => string;
      percent: (used: number, window: number) => string;
      remaining: (state: Totals, info: ContextInfo | null | undefined, autoCompactAt?: number) => Remaining | null;
      cache: (
        state: Totals,
        ttlSeconds: number,
        lastRequestAt: number | null,
        now?: number,
      ) => { cached: number; ttl: number; remaining: number | null };
      clock: (seconds: number) => string;
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

test('the panel tells the document how to measure, then asks the session', () => {
  const post = CHAT_PANEL.slice(CHAT_PANEL.indexOf('private postContext('));
  assert.ok(post.length > 0, 'postContext is in the panel');
  const postBody = post.slice(0, post.indexOf('\n  }'));
  assert.match(postBody, /type: 'context'/);
  assert.match(postBody, /window: settings\.contextWindow/);
  assert.match(postBody, /auto_compact_at: settings\.autoCompactAt/);
  assert.match(postBody, /cache_ttl_seconds: settings\.cacheTtlSeconds/);
  // The estimate travels only when there is one: a null would be a claim that
  // the conversation is empty rather than that the host did not answer.
  assert.match(postBody, /\.\.\.\(info \? \{ info \} : \{\}\)/);

  const report = CHAT_PANEL.slice(CHAT_PANEL.indexOf('reportContext(): void {'));
  const reportBody = report.slice(0, report.indexOf('\n  }'));
  assert.match(reportBody, /this\.postContext\(\)/, 'the settings always go out');
  assert.match(reportBody, /this\.refreshContext\(\)/, 'the live estimate is asked for too');
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

test('the budget travels only with a window, and the three settings are resolved', () => {
  const auto = PACKAGE.contributes.configuration.properties['anvil.autoCompactAt'];
  assert.equal(auto.type, 'number');
  assert.equal(auto.default, 0.8);
  assert.equal(auto.minimum, 0.1);
  assert.equal(auto.maximum, 1);

  const keep = PACKAGE.contributes.configuration.properties['anvil.contextKeepRecent'];
  assert.equal(keep.default, 8);
  assert.equal(keep.minimum, 2);

  const ttl = PACKAGE.contributes.configuration.properties['anvil.cacheTtlSeconds'];
  assert.equal(ttl.default, 0);
  assert.equal(ttl.minimum, 0);
  assert.match(ttl.markdownDescription, /300 seconds for Anthropic/);

  // A window of 0 is the host's "nothing is full": sending a fraction with it
  // would declare a rule that can never fire, so the whole object is omitted
  // and the request stays the one an older client sent.
  assert.match(SETTINGS, /if \(contextWindow > 0\) \{/);
  assert.match(SETTINGS, /window_tokens: contextWindow/);
  assert.match(SETTINGS, /auto_compact_at: autoCompactAt/);
  assert.match(SETTINGS, /keep_recent: contextKeepRecent/);
  // The provider's default is the only TTL this client knows; any other
  // provider gets 0, which draws the cached share with no clock.
  assert.match(SETTINGS, /return provider === 'anthropic' \? 300 : 0;/);
});

test('a window and an estimate become used, left and the compact mark', () => {
  const state = meter.empty();
  meter.add(state, run({ input_tokens: 1000, output_tokens: 100, total_tokens: 1100 }));
  const info: ContextInfo = {
    estimated_tokens: 40000,
    window_tokens: 200000,
    compact_at_tokens: 160000,
    messages: 12,
  };
  assert.deepEqual(meter.remaining(state, info, 0.8), {
    used: 40000,
    window: 200000,
    left: 160000,
    compactAt: 160000,
    leftUntilCompact: 120000,
    percent: 20,
  });

  // The host's mark wins over the fraction, because it is the number the host
  // will actually act on.
  assert.equal(meter.remaining(state, { ...info, compact_at_tokens: 150000 }, 0.8)?.compactAt, 150000);
  // Without one, the declared fraction computes the same mark.
  assert.equal(meter.remaining(state, { ...info, compact_at_tokens: 0 }, 0.9)?.compactAt, 180000);
  // With neither, there is no mark to draw — not a zero one.
  assert.equal(meter.remaining(state, { ...info, compact_at_tokens: 0 }, 0)?.compactAt, 0);

  // No window anywhere is the state before this existed: null, so the caller
  // keeps drawing counts only.
  assert.equal(meter.remaining(state, null), null);
  assert.equal(meter.remaining(state, { estimated_tokens: 10, window_tokens: 0 }), null);

  // An estimate that is missing falls back to the last run's total, which was
  // measured — not to zero, which would reset a meter to empty between turns.
  assert.equal(meter.remaining(state, { window_tokens: 200000 })?.used, 1100);
});

test('an estimate past the window is clamped, and never negative', () => {
  const state = meter.empty();
  const over = meter.remaining(state, {
    estimated_tokens: 250000,
    window_tokens: 200000,
    compact_at_tokens: 160000,
  });
  assert.equal(over?.used, 250000);
  assert.equal(over?.left, 0);
  assert.equal(over?.leftUntilCompact, 0);
  assert.equal(over?.percent, 125);
});

test('the cache counts down from the run’s own request, and stops at zero', () => {
  const state = meter.empty();
  meter.add(state, run({ input_tokens: 1000, output_tokens: 100, total_tokens: 1100, cached_tokens: 8192 }));
  const anchor = Date.UTC(2026, 9, 19, 12, 0, 0);

  assert.deepEqual(meter.cache(state, 300, anchor, anchor), { cached: 8192, ttl: 300, remaining: 300 });
  assert.deepEqual(meter.cache(state, 300, anchor, anchor + 125_000), {
    cached: 8192,
    ttl: 300,
    remaining: 175,
  });
  // At and past the TTL it is 0, which the caller draws as cold: an expired
  // cache is information, not an absence of any.
  assert.equal(meter.cache(state, 300, anchor, anchor + 300_000).remaining, 0);
  assert.equal(meter.cache(state, 300, anchor, anchor + 900_000).remaining, 0);

  // No TTL means no clock — the cached share is still drawn.
  assert.deepEqual(meter.cache(state, 0, anchor, anchor), { cached: 8192, ttl: 0, remaining: null });
  // A TTL without an anchor cannot be counted from; inventing "now" would make
  // the countdown restart every render.
  assert.equal(meter.cache(state, 300, null, anchor).remaining, null);
});

test('a run that reports no cached input leaves no cache share behind', () => {
  const state = meter.empty();
  meter.add(state, run({ input_tokens: 10, output_tokens: 4, total_tokens: 14, cached_tokens: 96 }));
  assert.equal(state.last?.cached, 96);
  // The next run's usage replaces the last figure whole: a provider that said
  // nothing about caching this time has not said the old figure still holds.
  meter.add(state, run({ input_tokens: 12, output_tokens: 4, total_tokens: 16 }));
  assert.equal(state.last?.cached, undefined);
  assert.equal(meter.cache(state, 300, 1, 1).cached, 0);
});

test('the countdown reads as a clock, and an empty one says nothing', () => {
  assert.equal(meter.clock(252), '4:12');
  assert.equal(meter.clock(60), '1:00');
  assert.equal(meter.clock(9), '0:09');
  assert.equal(meter.clock(0), '');
  assert.equal(meter.clock(-4), '');
  assert.equal(meter.percent(40000, 200000), '20');
  assert.equal(meter.percent(138, 200000), '0.1');
  assert.equal(meter.percent(10, 0), '');
});

test('the renderer keeps both clocks, and only the cache one ticks', () => {
  const record = bodyOf(CHAT_JS, 'recordContext');
  assert.match(record, /done\.context/);
  assert.match(record, /last_request_at/);
  assert.match(record, /Date\.parse/);
  assert.match(bodyOf(CHAT_JS, 'recordUsage'), /recordContext\(done\)/);
  // The transcript reset is also the session change, and both clocks describe
  // the session, so they are cleared together.
  const reset = bodyOf(CHAT_JS, 'resetUsage');
  assert.match(reset, /contextInfo = null/);
  assert.match(reset, /lastRequestAt = null/);

  const render = bodyOf(CHAT_JS, 'renderMeter');
  assert.doesNotMatch(render, /\.innerHTML\s*(\+)?=(?!=)/);
  // The line is no longer one string: each count has its own node so a landing
  // count can pulse without moving the others (p_0.46.26).
  assert.match(render, /setNumber\(meterIn, /);
  assert.match(render, /meter\.title = text/);
  assert.match(render, /meterMath\.remaining\(/);
  assert.match(render, /meterMath\.cache\(/);
  // Two scopes, two places (p_0.46.24): the conversation's share of its window
  // is written on the root line, and the session's spent total and cache are
  // written in the composer.
  assert.match(render, /facts\.push\('compact at '/);
  assert.match(render, /rootFacts\.textContent = factsText/);
  // The root line lives in the transcript; only a change to it may pull a
  // reader back to the bottom. This renderer runs once a second while a cache
  // is live, and pinning on a countdown would undo a scroll the reader made.
  assert.match(render, /rootFacts\.textContent !== factsText/);
  assert.equal(
    (render.match(/scrollIfPinned\(/g) ?? []).length,
    1,
    'only the root line change pins the transcript, not the per-second tick',
  );
  assert.match(render, /meterMath\.clock\(cache\.remaining\)/);
  assert.match(render, /'cold'/);
  // The per-second tick is written into its own node, so the cache count beside
  // it is not re-written (and not pulsed) once a second (p_0.46.26).
  assert.match(render, /meterClock\.textContent = ''/);
  // All three session counts are drawn whenever the provider reported them.
  // Gating in/out on a missing context window left the line saying only
  // 'cache' (p_0.46.26).
  assert.match(render, /setNumber\(meterIn, countText\(usageTotals\.input/);
  assert.match(render, /setNumber\(meterOut, countText\(usageTotals\.output/);
  assert.match(render, /setNumber\(meterCache, countText\(cache\.cached/);
  assert.doesNotMatch(render, /if \(!context\)/);
  // The composer's row can truncate the total; the whole line stays reachable.
  assert.match(render, /meter\.title = text/);
  assert.match(render, /meter\.setAttribute\('aria-label', text\)/);

  // Two intervals, each one second and each with its own lifetime: the cache
  // clock only while there is a live cache on a tab somebody can see, and the
  // working clock only while a turn is in flight (p_0.46.20). Everything else
  // redraws on an event.
  const clock = bodyOf(CHAT_JS, 'syncCacheClock');
  assert.match(clock, /setInterval\(renderMeter, 1000\)/);
  assert.match(clock, /document\.hidden/);
  assert.match(clock, /clearInterval\(cacheClock\)/);
  assert.doesNotMatch(CHAT_JS, /setInterval\((?!renderMeter, 1000|renderWorkingMeta, 1000)/);
  assert.match(bodyOf(CHAT_JS, 'startWorking'), /setInterval\(renderWorkingMeta, 1000\)/);
  assert.match(bodyOf(CHAT_JS, 'stopWorking'), /clearInterval\(workingTimer\)/);
  assert.match(CHAT_JS, /addEventListener\('visibilitychange'/);
});

test('the context message carries the settings and the host’s estimate', () => {
  const start = CHAT_JS.indexOf("case 'context':");
  assert.ok(start > 0);
  const handler = CHAT_JS.slice(start, CHAT_JS.indexOf("case 'reset':"));
  assert.match(handler, /msg\.auto_compact_at/);
  assert.match(handler, /msg\.cache_ttl_seconds/);
  assert.match(handler, /msg\.info/);
  assert.match(handler, /renderMeter\(\)/);
});

test('a compact draws one divider, live and replayed, as text', () => {
  const add = bodyOf(CHAT_JS, 'addCompact');
  assert.match(add, /'context compacted'/);
  assert.match(add, /' messages summarized'/);
  assert.doesNotMatch(add, /innerHTML/);
  assert.match(add, /textContent|el\('div'/);

  assert.match(CHAT_JS, /e\.type === 'compact'.*addCompact\(e\.compact\)/);
  // The row is drawn where a live event would put it, through the same `el`
  // helper as every other message — no markup, no `innerHTML`.
  assert.match(CHAT_JS, /function addCompact\(info\) \{/);
  assert.match(CHAT_CSS, /\.msg\.compact \.body \{/);
});

test('the protocol carries usage on an event', () => {
  const hostEvent = PROTOCOL.slice(PROTOCOL.indexOf('export interface HostEvent'));
  assert.match(hostEvent.slice(0, hostEvent.indexOf('\n}')), /usage\?: Usage/);
  assert.match(PROTOCOL, /export interface Usage \{/);
});

test('a count that lands pulses once, and a redraw does not', () => {
  // A number is written through `setNumber`, which compares before it writes:
  // `renderMeter` runs on every cache tick and every root update, and most of
  // those do not move a count, so a pulse must mean "this changed" rather than
  // "this was redrawn" (p_0.46.26).
  const set = bodyOf(CHAT_JS, 'setNumber');
  // The comparison is against the value the node was last told, not against
  // its text: the cache count owns the clock node, so its text changes every
  // second while the number does not. Comparing text pulsed it once a second.
  assert.match(set, /const before = numberText\.get\(node\) \|\| ''/);
  assert.match(set, /if \(before === next\) return/);
  assert.match(set, /numberText\.set\(node, next\)/);
  assert.doesNotMatch(set, /node\.textContent === next/);
  assert.match(set, /node\.textContent = next/);
  // The first value drawn, and a reset to nothing, are not landings.
  assert.match(set, /if \(before !== '' && next !== ''\) pulseNumber\(node\)/);

  // The pulse is script, not a stylesheet: `chat.css` may carry no at-rule.
  const pulse = bodyOf(CHAT_JS, 'pulseNumber');
  assert.match(pulse, /node\.animate\(/);
  assert.match(pulse, /scale\(1\.18\)/);
  assert.match(pulse, /duration: 520/);
  // Motion is still a preference: a reader who asked for less gets no pulse.
  assert.match(pulse, /if \(!node \|\| reduceMotion\) return/);
  assert.match(pulse, /typeof Element\.prototype\.animate !== 'function'/);
  // A second landing on the same node must not queue a second animation.
  assert.match(pulse, /getAnimations\(\)/);

  // The same test `chatLayout.test.ts` makes, stated here because this is the
  // file that would be tempted to add one: no at-rule at all, so the stylesheet
  // can be parsed as text. The pulse is the script's job for that reason.
  assert.doesNotMatch(CHAT_CSS, /@[a-z-]+\s*[({]/, 'chat.css must stay free of at-rules');
  // Each count needs its own box: a transform on an inline box does not apply.
  assert.match(CHAT_CSS, /\.composer \.meter > span \{[^}]*display: inline-block/);
  assert.match(CHAT_CSS, /\.status \.working-step \{[^}]*display: inline-block/);
});

test('a count with nothing to report keeps its place instead of vanishing', () => {
  // A count is only known once a run has ended, so during a conversation's first
  // turn all three are unknown. The line used to vanish entirely, which read as
  // "this build has no meter" rather than "nothing has been counted yet"
  // (p_0.46.26).
  const render = bodyOf(CHAT_JS, 'renderMeter');
  assert.match(render, /const pending = usageTotals\.input <= 0 && usageTotals\.output <= 0/);
  // An em dash is the one spelling that cannot be mistaken for a count — and
  // it keeps the count's name, so the first turn reads `— · — · cache —`
  // rather than three bare dashes.
  assert.match(render, /const name = label \? label \+ ' ' : ''/);
  assert.match(render, /pending \? name \+ '\\u2014' : ''/);
  // Once anything has been reported, an absent count is absent rather than a
  // dash: the placeholders are for "not yet", not for "never".
  assert.match(render, /value > 0/);
});

test('the meter line is assembled from its nodes, so a hidden count is not doubled', () => {
  const render = bodyOf(CHAT_JS, 'renderMeter');
  assert.match(render, /for \(const node of meterCounts\) if \(!node\.hidden\) parts\.push\(node\.textContent\)/);
  assert.match(render, /for \(const node of meterCounts\) node\.hidden = \(numberText\.get\(node\) \|\| ''\) === ''/);
  // The window share is a node like the counts, so `meterMath.label`'s share
  // is drawn beside them rather than only in the tooltip — and it is drawn
  // whether or not the host sent a context estimate.
  assert.match(render, /setNumber\(meterShare, share\)/);
  assert.doesNotMatch(render, /if \(spent && context\)/);
  // A separator belongs to the count in front of it and is shown only while
  // that count is visible and another count follows it — exactly one dot
  // between each pair of visible counts, however many counts are hidden.
  assert.match(render, /const dots = \[meterDot1, meterDot2, meterDot3\]/);
  assert.match(render, /const lastShown = meterCounts\.map\(\(node\) => !node\.hidden\)\.lastIndexOf\(true\)/);
  assert.match(render, /dots\[i\]\.hidden = meterCounts\[i\]\.hidden \|\| i >= lastShown/);
});
