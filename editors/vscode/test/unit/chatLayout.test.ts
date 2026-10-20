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
 *
 * The floating composer adds a third kind of check, and it is the one a rename
 * would break silently: the height the script measures and the height the
 * stylesheet reserves are the same variable, spelled the same way on both
 * sides. That is checked across the two files at the bottom of this file.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const CSS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.css'), 'utf8');
const JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');

/** The stylesheet with its comments dropped, so prose cannot look like a rule. */
const SOURCE = CSS.replace(/\/\*[\s\S]*?\*\//g, '');

interface Rule {
  selectors: string[];
  body: string;
}

/**
 * Every rule in the stylesheet. Comments are dropped first, and blocks are
 * split on `}` — `chat.css` has no at-rules, so a rule never contains one.
 */
function parseRules(): Rule[] {
  return SOURCE.split('}')
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

test('the composer is out of the flow, and the transcript still fills the body', () => {
  // Absolute positioning is what makes the dock not a flex item, which is what
  // keeps the height chain above intact. Were it in flow, the transcript would
  // be sized against a body that also contains the composer, and the panel —
  // not the transcript — would be the thing that overflowed.
  assert.match(bodiesOf('body').join(';'), /position:\s*relative/);
  assert.equal(valueOf('.dock', 'position'), 'absolute');
  assert.equal(valueOf('.dock', 'bottom'), '0');

  // The dock spans the full width at the bottom of the tab, so its own box must
  // not take clicks: it would swallow the scrollbar drag and any text selection
  // that ends near the bottom edge. Only the card inside it is interactive.
  assert.equal(valueOf('.dock', 'pointer-events'), 'none');
  assert.equal(valueOf('.dock > *', 'pointer-events'), 'auto');
});

test('the transcript reserves the composer height, and the script measures it', () => {
  const reserve = valueOf('.transcript', 'padding-bottom');
  assert.ok(reserve, '.transcript must reserve room for the floating composer');
  assert.match(reserve, /var\(--anvil-composer-h\)/);

  // A declaration in `:root`, not just the `var()` fallback: the reserve has to
  // be a real length on the first paint, before the observer has run, and a
  // variable with no declaration is the kind of thing a rename loses quietly.
  const declared = valueOf(':root', '--anvil-composer-h');
  assert.ok(declared, ':root must declare --anvil-composer-h');
  assert.match(declared, /^\d+(\.\d+)?px$/);
});

test('the reading column is centred and stops widening', () => {
  for (const selector of ['.transcript', '.dock']) {
    const padding = valueOf(selector, 'padding-inline');
    assert.ok(padding, `${selector} must be inset to the reading column`);
    // Inside a `max()`, so a narrow window collapses to the gutters rather than
    // to a negative inset.
    assert.match(padding, /max\(/);
    assert.match(padding, /var\(--anvil-read-width\)/);
  }
});

test('the composer is a card, and the tab is themed as an editor', () => {
  assert.ok(valueOf('.composer', 'border-radius'));
  assert.ok(valueOf('.composer', 'box-shadow'));
  assert.match(bodiesOf('.composer').join(';'), /background/);

  // A sidebar tint is right for a view and wrong for a tab.
  const body = bodiesOf('body').join(';');
  assert.match(body, /--vscode-editor-background/);
  assert.doesNotMatch(body, /--vscode-sideBar-background/);
});

test('the error banner can be hidden', () => {
  // `.banner` is `display: flex`, which beats the user-agent rule for the
  // hidden attribute. Without this override the dismiss button sets `hidden`
  // and the bar stays on screen.
  assert.match(bodiesOf('.banner[hidden]').join(';'), /display:\s*none\s*!important/);
});

test('the assistant body reflows, and a code block does not', () => {
  // The rest of `.body` is `pre-wrap`, which is right for text the user typed and
  // wrong for Markdown: it would keep every line break the model wrote and stop
  // paragraphs from reflowing when the window is resized. The override is the
  // whole reason the class is `body md` rather than `body`.
  assert.equal(valueOf('.msg .body.md', 'white-space'), 'normal');

  // And the opposite inside a code block: a wrapped code line is a changed line.
  assert.equal(valueOf('.body.md .md-code code', 'white-space'), 'pre');
});

test('the stylesheet has no at-rules, which is what lets it be parsed as text', () => {
  // `parseRules()` above splits the stylesheet on `}` and reads the text before
  // each `{` as a selector, so a `@media` block would silently turn into rules
  // with names like `@media (min-width: 600px) { .x`. Every invariant in this
  // file would then be checking something that does not exist. The assumption is
  // stated in a comment up there; this is what enforces it.
  assert.doesNotMatch(SOURCE, /@[a-z-]+\s*[({]/, 'chat.css must stay free of at-rules');
});

test('an edit diff is two columns, and a body class narrows it without an at-rule', () => {
  assert.match(
    SOURCE,
    /\.tool \.edit-diff \.diff \.row \{[^}]*grid-template-columns:\s*minmax\(0, 1fr\) minmax\(0, 1fr\)/,
  );
  assert.match(
    SOURCE,
    /body\.diff-narrow \.tool \.edit-diff \.diff \.row \{[^}]*display:\s*block/,
  );
  assert.match(
    SOURCE,
    /body\.diff-narrow \.tool \.edit-diff \.diff \.cell\.empty \{[^}]*display:\s*none/,
  );
  assert.match(
    SOURCE,
    /body\.diff-narrow \.tool \.edit-diff \.diff \.row\.ctx \.cell\.new \{[^}]*display:\s*none/,
  );

  // The script is the other half of the agreement: one threshold, one class,
  // and one throttled listener rather than a layout per card.
  assert.match(JS, /const DIFF_SIDE_MIN = \d+;/);
  assert.match(JS, /function syncDiffWidth\(\)/);
  assert.match(JS, /addEventListener\('resize', queueDiffWidth\)/);
  assert.match(JS, /classList\.toggle\('diff-narrow'/);
  assert.match(JS, /requestAnimationFrame\(\(\) => \{[\s\S]{0,200}syncDiffWidth\(\)/);
});

test('the diff takes its line colours from the editor theme', () => {
  // Whitespace after `var(` is allowed: a nested fallback is written across
  // lines, and the point is which variables the stylesheet reads.
  assert.match(SOURCE, /var\(\s*--vscode-diffEditor-insertedLineBackground/);
  assert.match(SOURCE, /var\(\s*--vscode-diffEditor-removedLineBackground/);
  assert.match(SOURCE, /var\(\s*--vscode-diffEditor-insertedTextBackground/);
  assert.match(SOURCE, /var\(\s*--vscode-diffEditor-removedTextBackground/);
});

test('a tool is a row, not a card, and its output is inset', () => {
  const tool = bodiesOf('.tool').join(';');
  assert.match(tool, /border:\s*0/);
  assert.match(tool, /background:\s*transparent/);
  assert.doesNotMatch(tool, /border-radius:\s*var\(--anvil-radius\)/);

  assert.match(bodiesOf('.tool > summary').join(';'), /padding:\s*3px 4px/);
  assert.match(bodiesOf('.tool .pane').join(';'), /padding:\s*0 0 6px 18px/);
  assert.match(
    bodiesOf('.tool .section pre').join(';'),
    /border-left:\s*1px solid var\(--ui-hairline\)/,
  );
  assert.match(
    bodiesOf('.msg .think .body').join(';'),
    /border-left:\s*1px solid var\(--ui-hairline\)/,
  );
});

test('the textarea grows to fit instead of being dragged', () => {
  // A manual resize grip on a box whose height the layout reserves would leave
  // the reservation describing a size the script overwrites on the next
  // keystroke. The cap lives in CSS, once.
  assert.notEqual(valueOf('.composer textarea', 'resize'), 'vertical');
  assert.ok(valueOf('.composer textarea', 'max-height'));
});

test('every length the script writes is one the stylesheet declares and reads', () => {
  const written = new Set(
    [...JS.matchAll(/setProperty\(\s*['"](--[a-z0-9-]+)['"]/gi)].map((match) => match[1]),
  );
  const read = new Set([...SOURCE.matchAll(/var\(\s*(--[a-z0-9-]+)/g)].map((match) => match[1]));
  const declared = new Set(
    [...SOURCE.matchAll(/(?:^|[;{])\s*(--[a-z0-9-]+)\s*:/g)].map((match) => match[1]),
  );

  // A variable the script writes but the stylesheet never heard of is the
  // silent failure this test exists for: the reserve falls back to its declared
  // default and the last message hides behind the composer card.
  for (const name of written) {
    assert.ok(declared.has(name), `${name} is written by chat.js but never declared in chat.css`);
    assert.ok(read.has(name), `${name} is written by chat.js but never read in chat.css`);
  }

  // And the other direction: an `--anvil-*` the stylesheet reads has to come
  // from somewhere — either the script measures it or the stylesheet declares
  // it. Theme variables are VS Code's and are none of this test's business.
  for (const name of read) {
    if (!name.startsWith('--anvil-')) continue;
    assert.ok(
      written.has(name) || declared.has(name),
      `${name} is read in chat.css but nothing writes or declares it`,
    );
  }
});
