/**
 * The panel's layout invariants, read out of `media/chat.css`.
 *
 * There is no browser in this test suite, so the things that can be checked
 * are the ones that are properties of the stylesheet itself. Two of them are
 * load-bearing, and both failed in the shipped 0.35.7 panel:
 *
 *  - A scroll container that lays its children out in a column must stop those
 *    children from shrinking. Flex items shrink by default, and an item's
 *    automatic minimum size is its content height only while its `overflow` is
 *    `visible`; a tool card sets `overflow: hidden`, so its minimum was 0. A
 *    long run of tool calls therefore compressed into evenly spaced slivers —
 *    the gap between items is not compressible — and the container never
 *    overflowed, so there was nothing to scroll.
 *  - The transcript only scrolls because the height chain from `html` down to
 *    it is unbroken: a full-height body that is a column flex container, and a
 *    transcript that is the flexible child with `min-height: 0`. A transcript
 *    without `min-height: 0` grows to fit its content instead, and the panel
 *    scrolls as a whole (or does not scroll at all) rather than the transcript
 *    doing it.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const CSS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.css'), 'utf8');

interface Rule {
  selectors: string[];
  body: string;
}

/**
 * Every rule in the stylesheet. Comments are dropped first, and blocks are
 * split on `}` — `chat.css` has no at-rules, so a rule never contains one.
 */
function parseRules(): Rule[] {
  return CSS.replace(/\/\*[\s\S]*?\*\//g, '')
    .split('}')
    .map((block) => {
      const open = block.indexOf('{');
      if (open < 0) return undefined;
      const selectors = block
        .slice(0, open)
        .split(',')
        .map((selector) => selector.trim())
        .filter((selector) => selector.length > 0);
      return selectors.length > 0 ? { selectors, body: block.slice(open + 1) } : undefined;
    })
    .filter((rule): rule is Rule => rule !== undefined);
}

const RULES = parseRules();

/** The bodies of every rule whose selector list contains `selector` exactly. */
function bodiesOf(selector: string): string[] {
  return RULES.filter((rule) => rule.selectors.includes(selector)).map((rule) => rule.body);
}

/** One declaration's value, or undefined when the rule does not set it. */
function valueOf(selector: string, property: string): string | undefined {
  const match = new RegExp(`(?:^|;)\\s*${property}\\s*:\\s*([^;]+)`).exec(
    bodiesOf(selector).join(';'),
  );
  return match ? match[1].trim() : undefined;
}

/**
 * Containers that intend to scroll: a column flex container with a scrolling
 * overflow. Their children are the ones at risk of being compressed.
 */
function scrollColumns(): string[] {
  return RULES.filter(
    (rule) =>
      /display:\s*flex/.test(rule.body) &&
      /flex-direction:\s*column/.test(rule.body) &&
      /overflow(-y)?:\s*(auto|scroll)/.test(rule.body),
  ).flatMap((rule) => rule.selectors);
}

/** Whether `flex`/`flex-shrink` in `body` leaves the item unshrinkable. */
function keepsSize(body: string): boolean {
  const shrink = /(?:^|;)\s*flex-shrink\s*:\s*0(?:\D|$)/.exec(body);
  if (shrink) return true;
  const flex = /(?:^|;)\s*flex\s*:\s*([^;]+)/.exec(body);
  if (!flex) return false;
  const parts = flex[1].trim().split(/\s+/);
  // `flex: none` and `flex: <grow> <shrink> <basis>` — the shrink factor is the
  // second number when the shorthand is written out in full.
  if (parts[0] === 'none') return true;
  return parts.length >= 2 && parts[1] === '0';
}

test('scrollable columns keep their children from shrinking', () => {
  const containers = scrollColumns();
  // The invariant is only worth asserting if it has something to constrain.
  assert.ok(containers.length > 0, 'expected at least one scrolling column in chat.css');

  for (const container of containers) {
    const guard = RULES.filter(
      (rule) =>
        rule.selectors.some(
          (selector) => selector === `${container} > *` || selector === `${container} *`,
        ) && keepsSize(rule.body),
    );
    assert.ok(
      guard.length > 0,
      `${container} scrolls its children and must stop them shrinking ` +
        `(add \`${container} > * { flex: 0 0 auto; }\`)`,
    );
  }
});

test('the transcript is the scrollable part of the panel', () => {
  const transcript = bodiesOf('.transcript').join(';');

  assert.match(bodiesOf('html').join(';'), /height:\s*100%/);
  assert.match(bodiesOf('body').join(';'), /height:\s*100%/);
  assert.match(bodiesOf('body').join(';'), /display:\s*flex/);
  assert.match(bodiesOf('body').join(';'), /flex-direction:\s*column/);

  assert.match(transcript, /min-height:\s*0/);
  assert.match(transcript, /overflow-y:\s*auto/);
  assert.equal(valueOf('.transcript', 'flex')?.split(/\s+/)[0], '1');
});
