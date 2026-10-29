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
const PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');

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

test('live task plan is rendered before the composer', () => {
  const todos = PANEL.indexOf('<div id="todos" class="todos collapsed"');
  const composer = PANEL.indexOf('<form id="composer" class="composer">');
  assert.ok(todos >= 0, 'todo plan is present');
  assert.ok(composer >= 0, 'composer is present');
  assert.ok(todos < composer, 'todo plan precedes the composer');
  // The borders overlap into one joined Plan/composer surface.
  assert.equal(valueOf('.todos', 'margin-bottom'), '-1px');
  assert.equal(valueOf('.todos', 'border-radius'), '10px 10px 0 0');
  assert.equal(valueOf('.todos:not([hidden]):not(.collapsed) + .composer', 'border-top-left-radius'), '0');
  assert.equal(valueOf('.todos', 'margin-top'), undefined);
  assert.equal(
    valueOf('.dock:has(.composer:focus-within) .todos:not([hidden]):not(.collapsed)', 'border-color'),
    'var(--vscode-focusBorder)',
  );
  assert.doesNotMatch(
    valueOf('.dock:has(.todos:not([hidden]):not(.collapsed)) .composer:focus-within', 'box-shadow') ?? '',
    /0 0 0 1px/,
  );
  assert.equal(valueOf('.todos.collapsed', 'height'), '0');
  assert.equal(valueOf('.todos.collapsed', 'border'), '0');
  assert.equal(valueOf('.todos.collapsed .todos-head', 'display'), 'none');
});

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

test('a floating latest-message button returns the transcript to its bottom', () => {
  assert.match(
    JS,
    /const scrollBottomButton = document\.getElementById\('scrollBottom'\)/,
  );
  assert.match(
    JS,
    /scrollBottomButton\.addEventListener\('click', scrollConversationToBottom\)/,
  );
  assert.match(JS, /transcript\.addEventListener\('scroll', syncScrollBottomButton/);
  assert.match(
    JS,
    /function scrollConversationToBottom\(\) \{\s*transcript\.scrollTop = transcript\.scrollHeight;/,
  );

  assert.equal(valueOf('.scroll-bottom', 'position'), 'absolute');
  assert.match(valueOf('.scroll-bottom', 'bottom') ?? '', /100%/);
  assert.equal(valueOf('.scroll-bottom[hidden]', 'display'), 'none');
});

test('auto-scroll is an explicit persistent preference that can preserve review position', () => {
  assert.match(PANEL, /id="autoScroll"[^>]*aria-pressed="true"/);
  assert.match(JS, /const autoScrollButton = document\.getElementById\('autoScroll'\)/);
  assert.match(JS, /restoredDocumentState\.autoScroll === false/);
  assert.match(JS, /remember\(\{ autoScroll \}\)/);
  assert.match(
    JS,
    /autoScrollButton\.addEventListener\('click', \(\) => setAutoScroll\(!autoScroll\)\)/,
  );
  // On means every layout write follows. Off falls back to the old pinned
  // behavior, so a reader already upstream keeps that exact review position.
  assert.match(
    JS,
    /function scrollIfPinned\(wasPinned\) \{\s*if \(autoScroll \|\| wasPinned\) transcript\.scrollTop = transcript\.scrollHeight;/,
  );
  // Re-enabling is immediate rather than waiting for the next model chunk.
  assert.match(JS, /if \(autoScroll\) scrollConversationToBottom\(\)/);

  assert.equal(valueOf('.auto-scroll', 'position'), 'absolute');
  assert.match(bodiesOf('.auto-scroll').join(';'), /right:\s*max\([^;]*40px/);
  assert.match(valueOf(".auto-scroll[aria-pressed='true']", 'border-color') ?? '', /focusBorder/);
});

test('the running composer stays editable and offers a split queue mode plus stop', () => {
  assert.match(PANEL, /id="stop"[^>]*aria-label="Stop current response"/);
  assert.match(PANEL, /id="send"[^>]*aria-label="Send"/);
  assert.match(PANEL, /id="sendMode"[^>]*aria-controls="sendModeMenu"/);
  assert.match(PANEL, /data-send-mode="enqueue"[\s\S]*?>Enqueue</);
  assert.match(PANEL, /data-send-mode="steer"[\s\S]*?>Steer</);
  assert.doesNotMatch(PANEL, /id="prioritySend"/);
  assert.doesNotMatch(JS, /input\.disabled\s*=\s*running/);
  assert.doesNotMatch(JS, /attachButton\.disabled\s*=\s*running/);
  assert.match(JS, /runningSendMode === 'steer' \? 'sendPriority' : 'enqueue'/);
  assert.match(JS, /row\.dataset\.sendMode === 'steer'/);
  assert.match(JS, /composer\.classList\.toggle\('send-menu-open', opening\)/);
  assert.match(bodiesOf('.composer.send-menu-open').join(';'), /overflow:\s*visible/);
  assert.match(JS, /case 'dispatchQueued':\s*dispatchQueued\(msg\)/);
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

test('the composer is one compact surface with a two-storey layout', () => {
  const composer = bodiesOf('.composer').join(';');
  assert.match(composer, /border-radius:\s*10px/);
  assert.match(composer, /padding:\s*0/);
  assert.match(composer, /gap:\s*0/);
  assert.match(composer, /overflow:\s*hidden/);

  const input = bodiesOf('.composer textarea').join(';');
  assert.match(input, /min-height:\s*2\.8em/);
  assert.match(input, /background:\s*transparent/);
  assert.match(input, /border:\s*0/);

  const actions = bodiesOf('.composer .actions').join(';');
  assert.match(actions, /border-top:\s*1px solid var\(--ui-hairline\)/);
  assert.match(actions, /min-height:\s*36px/);
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

test('the user speaks from a green bubble on the right', () => {
  const user = bodiesOf('.msg.user .body').join(';');
  assert.match(user, /align-self:\s*flex-end/);
  assert.match(user, /width:\s*fit-content/);
  assert.match(user, /max-width:\s*66\.666%/);
  assert.match(user, /text-align:\s*left/);
  assert.match(user, /testing-iconPassed|charts-green/);
  assert.match(user, /border-radius:\s*14px 14px 4px 14px/);
});

test('no rail reaches past the end of the reply it belongs to', () => {
  // `rail-down` means "another block is below me", and it draws its segment 10px
  // past its own box to meet the next one across the reply body's 12px gap. The
  // reply's last block has no next segment, and what sits 10px under it is the
  // transcript's gap and then the next entry — which is how a stub of rail came
  // to hang below the last block, and at the bottom of a scrolled conversation
  // it read as a line floating under the transcript (p_0.46.26).
  assert.match(
    SOURCE,
    /\.reply-body > :last-child\.rail-down::after \{\s*bottom: 0;/,
    'the last block of a reply must not reach below itself',
  );

  // The marker is already correct at the source: the last block of a run is
  // never given `rail-down`, so the rule above is a guard for the states the
  // marker does not produce (an interrupted run, a replayed turn).
  assert.match(JS, /block\.classList\.toggle\('rail-down', i < run\.length - 1\)/);
});

test('a reply with an empty body draws no segment below its header', () => {
  // `reply-head::before` reaches 2px past the header to meet the first block's
  // own line, and that overlap is right the moment a first block exists. It is
  // a guard rather than a reported defect: `append` fills the body in the same
  // JS turn that `targetFor` creates it, so the state is not painted in the
  // normal path (p_0.46.26). A hoisted-ending reply body, and any future caller
  // that opens a reply before it has content, are the cases it covers.
  assert.match(
    SOURCE,
    /\.reply\[open\]:not\(:has\(> \.reply-body > \*\)\) > \.reply-head::before \{\s*bottom: 0;/,
    'an empty reply body must not reach below its header',
  );
});

test('the footprint stays the transcript’s last entry in the document too', () => {
  // The stylesheet draws the footprint under every turn with `order: 1`, but a
  // block appended after it in the DOM would still read out before the whole
  // conversation in a copy or a screen reader. So the document order says the
  // same thing the layout does: new entries go in front of the footprint
  // (p_0.46.26).
  assert.match(JS, /function addToEnd\(target, node\)/);
  assert.match(JS, /transcript\.insertBefore\(node, footprint\)/);
  assert.match(JS, /if \(target === transcript && footprint && footprint\.parentNode === transcript\)/);
  // The two producers of a top-level entry go through it: the reply that opens
  // a turn, and an approval card that arrives with no reply open.
  assert.match(JS, /addToEnd\(transcript, node\)/);
  assert.match(JS, /addToEnd\(target, card\)/);
  assert.doesNotMatch(JS, /transcript\.appendChild\(node\)/);
});

test('the dock paints above every dot in the transcript', () => {
  // The dot carries `z-index: 1` so it covers the rail its own block draws, and
  // that value lives in the root stacking context — the block around the dot is
  // positioned but creates none. The dock had no order of its own, so a row
  // scrolled under the composer left its green dot floating on the card
  // (p_0.46.27).
  const dock = Number(valueOf('.dock', 'z-index'));
  assert.ok(Number.isFinite(dock), 'the dock declares a stacking order');
  for (const selector of [
    '.tool > summary::before',
    '.reply > .reply-head .start-dot',
    '.msg.assistant.summary > .who .end-dot',
  ]) {
    const dot = Number(valueOf(selector, 'z-index'));
    assert.ok(dot > 0, `${selector} still covers its rail`);
    assert.ok(dock > dot, `${selector} must paint under the dock`);
  }
});

test('the model menu uses themed surfaces and contains the effort control', () => {
  assert.match(bodiesOf('.model-menu').join(';'), /--vscode-editorWidget-background/);
  assert.match(bodiesOf('.model-menu-row').join(';'), /--vscode-foreground/);
  assert.match(PANEL, /id="modelMenu"[\s\S]*?id="modelMenuList"[\s\S]*?id="effortControl"/);
});

test('the stylesheet has no at-rules, which is what lets it be parsed as text', () => {
  // `parseRules()` above splits the stylesheet on `}` and reads the text before
  // each `{` as a selector, so a `@media` block would silently turn into rules
  // with names like `@media (min-width: 600px) { .x`. Every invariant in this
  // file would then be checking something that does not exist. The assumption is
  // stated in a comment up there; this is what enforces it.
  assert.doesNotMatch(SOURCE, /@[a-z-]+\s*[({]/, 'chat.css must stay free of at-rules');
});

test('an edit diff scrolls by the side, and a body class narrows it without an at-rule', () => {
  // Each side is one pane, so the overflow belongs to the side rather than to
  // every line: the pane is what carries the scrollbar now.
  assert.match(SOURCE, /\.tool \.edit-diff \.diff \{[^}]*display:\s*flex/);
  assert.match(
    SOURCE,
    /\.tool \.edit-diff \.diff > \.pane \{[^}]*flex: 1 1 0[^}]*max-height: 18em[^}]*overflow: scroll/,
  );
  // A long line widens its own pane; a short row still paints across it.
  assert.match(SOURCE, /\.tool \.edit-diff \.diff \.row \{[^}]*width: max-content/);
  assert.match(SOURCE, /\.tool \.edit-diff \.diff \.row \{[^}]*min-width: 100%/);
  // The bars are invisible until the comparison is pointed at.
  assert.match(
    SOURCE,
    /\.tool \.edit-diff \.diff > \.pane::-webkit-scrollbar-thumb,[\s\S]*?\{[^}]*background: transparent/,
  );
  assert.match(
    SOURCE,
    /\.tool \.edit-diff \.diff:hover > \.pane::-webkit-scrollbar-thumb,[\s\S]*?\{[^}]*--vscode-scrollbarSlider-background/,
  );
  // The narrow shape is a painted unified column, not a hidden cell.
  assert.match(SOURCE, /\.tool \.edit-diff \.diff\.narrow \{[^}]*display: block/);

  // The script is the other half of the agreement: one threshold, one class,
  // and one throttled listener rather than a layout per card.
  assert.match(JS, /const DIFF_SIDE_MIN = \d+;/);
  // Two columns stop paying for themselves sooner at a larger text size.
  assert.match(JS, /const DIFF_SIDE_BASE = 15;/);
  assert.match(JS, /DIFF_SIDE_MIN \* \(font \/ DIFF_SIDE_BASE\)/);
  assert.match(JS, /function syncDiffWidth\(\)/);
  assert.match(JS, /addEventListener\('resize', queueDiffWidth\)/);
  assert.match(JS, /classList\.toggle\('diff-narrow'/);
  assert.match(JS, /requestAnimationFrame\(\(\) => \{[\s\S]{0,200}syncDiffWidth\(\)/);
  // A flip repaints what is on screen; a comparison drawn afterwards reads the
  // class when it paints.
  assert.match(JS, /repaintDiffs\(\)/);
});

test('the diff takes its line colours from the editor theme', () => {
  // Whitespace after `var(` is allowed: a nested fallback is written across
  // lines, and the point is which variables the stylesheet reads.
  assert.match(SOURCE, /var\(\s*--vscode-diffEditor-insertedLineBackground/);
  assert.match(SOURCE, /var\(\s*--vscode-diffEditor-removedLineBackground/);
  assert.match(SOURCE, /var\(\s*--vscode-diffEditor-insertedTextBackground/);
  assert.match(SOURCE, /var\(\s*--vscode-diffEditor-removedTextBackground/);
});

test('a tool is a row, not a card, and its output is framed', () => {
  const tool = bodiesOf('.tool').join(';');
  assert.match(tool, /border:\s*0/);
  assert.match(tool, /background:\s*transparent/);
  assert.doesNotMatch(tool, /border-radius:\s*var\(--anvil-radius\)/);

  assert.match(bodiesOf('.tool > summary').join(';'), /padding:\s*4px 6px/);
  // The opened call's two halves sit in one hairline frame, aligned with the
  // row's name, instead of floating under it.
  const pane = bodiesOf('.tool .pane').join(';');
  assert.match(pane, /margin:\s*2px 0 6px 22px/);
  assert.match(pane, /padding:\s*8px 10px/);
  assert.match(pane, /border:\s*1px solid var\(--ui-hairline\)/);
  // A spawn's pane holds a whole conversation and is not boxed.
  assert.match(bodiesOf('.tool.spawn .pane').join(';'), /border:\s*0/);
  // IN and OUT are divided by a hairline; the frame replaces the pre's own rule.
  assert.match(
    bodiesOf('.tool .section + .section').join(';'),
    /border-top:\s*1px solid var\(--ui-hairline\)/,
  );
  assert.match(bodiesOf('.tool .section pre').join(';'), /padding:\s*0/);
  assert.doesNotMatch(bodiesOf('.tool .section pre').join(';'), /border-left/);
  // Thinking has one line — the rail — and no second bar beside its text.
  assert.match(bodiesOf('.msg .think .body').join(';'), /padding:\s*4px 0 4px 22px/);
  assert.doesNotMatch(bodiesOf('.msg .think .body').join(';'), /border-left/);
});

test('a row is marked by a state dot, not a caret', () => {
  const dot = bodiesOf('.tool > summary::before').join(';');
  assert.match(dot, /content:\s*''/);
  assert.match(dot, /width:\s*8px/);
  assert.match(dot, /height:\s*8px/);
  // 8px drawn, 6px laid out: the centre stays at 9px where the rail is, and the
  // negative margins keep the name at 22px.
  assert.match(dot, /margin:\s*-1px/);
  assert.match(dot, /border-radius:\s*50%/);
  // The dot is painted after the block's generated rail unless it is ordered
  // above it, which is what makes the dot cover the line rather than the line
  // cross the dot.
  assert.match(dot, /position:\s*relative/);
  assert.match(dot, /z-index:\s*1/);

  // A call is an operation while and after it runs: the dot is the editor's
  // green, a little quieter when finished. Failure is the error colour, and a
  // row that never got a result is a ring. The row gains no second word or
  // icon for any of them.
  assert.match(
    bodiesOf('.tool > summary::before').join(';'),
    /--dot:\s*var\(--vscode-testing-iconPassed, var\(--vscode-charts-green/,
  );
  assert.match(bodiesOf('.tool.done > summary::before').join(';'), /opacity:\s*0\.85/);
  assert.match(
    bodiesOf('.tool.failed > summary::before').join(';'),
    /--dot:\s*var\(--vscode-errorForeground\)/,
  );
  assert.match(bodiesOf('.tool.stopped > summary::before').join(';'), /box-shadow:\s*inset 0 0 0 2px/);
  assert.match(bodiesOf('.tool[open] > summary::before').join(';'), /box-shadow:\s*inset 0 0 0 2px/);

  // The caret is gone from both grammars, so a search for it in the built
  // stylesheet finds nothing a user could still see.
  assert.doesNotMatch(SOURCE, /25B8|25BE/);
  const think = bodiesOf('.msg .think > summary::before').join(';');
  assert.match(think, /width:\s*8px/);
  assert.match(think, /margin:\s*-1px/);
  assert.match(think, /z-index:\s*1/);
  assert.match(think, /border-radius:\s*50%/);
});

test('a reply is one rail, prose included', () => {
  // The segment is a pseudo-element on the block, so the markup stays flat and
  // the replay, the spawn scopes and the focus view need to learn nothing.
  const rail = bodiesOf('.tool::after').join(';');
  assert.match(rail, /content:\s*''/);
  assert.match(rail, /left:\s*9px/);
  assert.match(rail, /width:\s*1px/);
  assert.match(rail, /display:\s*none/);
  // Prose and the output caption are blocks of the reply too.
  assert.match(bodiesOf('.msg.assistant::after').join(';'), /left:\s*9px/);
  assert.match(bodiesOf('.output-link::after').join(';'), /left:\s*9px/);
  assert.match(bodiesOf('.tool.rail::after').join(';'), /display:\s*block/);
  assert.match(bodiesOf('.msg.assistant.rail::after').join(';'), /display:\s*block/);
  // Every block reaches 10px into the gap: two messages are 20px apart and a
  // message and a row 16px, so the first is met exactly and the second is
  // over-covered.
  assert.match(bodiesOf('.tool.rail-up::after').join(';'), /top:\s*-10px/);
  assert.match(bodiesOf('.tool.rail-down::after').join(';'), /bottom:\s*-10px/);
  assert.match(bodiesOf('.msg.assistant.rail-up::after').join(';'), /top:\s*-10px/);
  assert.match(bodiesOf('.msg.assistant.rail-down::after').join(';'), /bottom:\s*-10px/);

  // Prose sits right of the line, where a row's name starts; the copy action
  // belongs to the message and moves with it.
  assert.match(
    bodiesOf('.msg.assistant.rail > .body').join(';'),
    /padding-left:\s*22px/,
  );
  assert.match(
    bodiesOf('.msg.assistant.rail > .who').join(';'),
    /padding-left:\s*22px/,
  );

  // The class pass: an assistant message is a block of the reply rather than
  // an ending, and a block with a neighbour on a side reaches into that gap.
  assert.match(JS, /function syncRail\(container\)/);
  assert.match(JS, /child\.classList\.contains\('msg'\) && child\.classList\.contains\('assistant'\)/);
  assert.match(JS, /child\.classList\.contains\('output-link'\)/);
  assert.match(JS, /block\.classList\.add\('rail'\)/);
  assert.match(JS, /block\.classList\.toggle\('rail-up', i > 0\)/);
  assert.match(JS, /block\.classList\.toggle\('rail-down', i < run\.length - 1\)/);
  assert.match(JS, /const target = targetFor\(node\)/);
  assert.match(JS, /syncRail\(target\)/);
  // Prose no longer ends the run, so the old think-only step walk is gone.
  assert.doesNotMatch(JS, /:scope > \.think/);
});

test('the transcript is a tight rhythm, and a state word says its state', () => {
  // A message's name, Thought and answer sit close; the transcript's own 12px
  // gap still separates one step from the next.
  const msg = bodiesOf('.msg').join(';');
  assert.match(msg, /gap:\s*4px/);
  assert.match(msg, /margin-block:\s*4px/);
  // The caption belongs to the row above it, not to the gap below it.
  assert.match(bodiesOf('.output-link').join(';'), /margin:\s*-8px 0 0/);

  // Finished is green, stopped is a warning, failed is an error: the word and
  // the dot tell the same story, and no state relies on italics alone.
  assert.match(bodiesOf('.tool.done .state').join(';'), /--vscode-testing-iconPassed/);
  assert.match(bodiesOf('.tool.stopped .state').join(';'), /--vscode-editorWarning-foreground/);
  assert.doesNotMatch(bodiesOf('.tool.stopped .state').join(';'), /font-style/);
});

test('a thought is folded behind a grey chevron', () => {
  // The dot says "reasoning", not "operation": it stays grey in every theme.
  const dot = bodiesOf('.msg .think > summary::before').join(';');
  assert.match(dot, /--dot:\s*var\(--ui-muted\)/);
  assert.doesNotMatch(dot, /testing-iconPassed|charts-green/);

  // The chevron is the fold affordance; a pseudo-element keeps the summary's
  // accessible name as "Thinking".
  assert.match(bodiesOf('.msg .think > summary::after').join(';'), /content:\s*'›'/);
  assert.match(
    bodiesOf('.msg .think[open] > summary::after').join(';'),
    /transform:\s*rotate\(90deg\)/,
  );
  assert.match(JS, /think\.open = false/);
});

test('the chat text size is a variable the script writes and the body reads', () => {
  // Declared so the first paint, before the script has run, is the editor's
  // own size; overwritten by the script from `anvil.chatFontSize`.
  assert.match(SOURCE, /--anvil-font-size:\s*var\(--vscode-font-size/);
  assert.equal(valueOf('body', 'font-size'), 'var(--anvil-font-size)');
  assert.match(JS, /function applyFontSize\(size\)/);
  assert.match(JS, /setProperty\('--anvil-font-size', px \+ 'px'\)/);
  assert.match(JS, /case 'fontSize':/);
  // The rendered value is applied before `ready` is posted, so a document that
  // waited through a settings change is not painted at the old size first.
  assert.match(JS, /applyFontSize\(document\.body\.dataset\.fontSize\)/);
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
