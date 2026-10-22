/**
 * A diff scrolls by the side, not by the line.
 *
 * p_0.46.10 let every cell scroll its own long line, which put a horizontal
 * scrollbar under every line of both sides. The overflow now belongs to a pane
 * per side: one bar at the foot of each, invisible until the comparison is
 * pointed at, and the two panes move together.
 *
 * The renderer is a browser script with no test window here, so the decisions
 * are pinned in the source and the shape in the stylesheet — the same way the
 * rail, the fold and the summary ending are.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_CSS = readFileSync(join(ROOT, 'media', 'chat.css'), 'utf8');

/** A top-level function of the renderer, from its declaration to the next one. */
function bodyOf(source: string, name: string): string {
  const start = source.indexOf(`\n  function ${name}(`);
  assert.ok(start >= 0, `${name} is in chat.js`);
  const rest = source.slice(start + 1);
  const next = rest.indexOf('\n  function ');
  return next >= 0 ? rest.slice(0, next) : rest;
}

/**
 * A declaration block from the stylesheet, without its comments. The rules are
 * split on `}` (the stylesheet has no at-rules), and a selector list is split
 * on `,`, so a rule grouped with the narrow shape can still be read by name.
 */
function ruleBody(selector: string): string {
  const source = CHAT_CSS.replace(/\/\*[\s\S]*?\*\//g, '');
  for (const block of source.split('}')) {
    const open = block.indexOf('{');
    if (open < 0) continue;
    const selectors = block
      .slice(0, open)
      .split(',')
      .map((part) => part.trim());
    if (selectors.includes(selector)) return block.slice(open + 1);
  }
  return '';
}

test('a comparison is one pane per side', () => {
  const diff = bodyOf(CHAT_JS, 'renderEditDiff');
  // Each side is a pane; the rows of that side live in it.
  assert.match(diff, /el\('div', 'pane ' \+ side\)/);
  assert.match(diff, /pre\.appendChild\(pane\)/);
  // A side with nothing on a line keeps the line, so the panes stay in step.
  assert.match(diff, /el\('div', 'row empty'\)/);
  // The cell is still the box that carries the row's colour and the
  // high-contrast edge; only the clipping element moved out of it.
  assert.match(diff, /el\('div', 'row ' \+ row\.kind\)/);
  assert.match(diff, /el\('span', 'cell ' \+ side/);
  assert.match(diff, /el\('span', 'ln', String\(row\.line\)\)/);
  assert.match(diff, /el\('span', 'code'/);
  // The panes are what the scroll positions are read from and written to.
  assert.match(diff, /syncDiffScroll\(pre\)/);
  assert.doesNotMatch(diff, /innerHTML|outerHTML|insertAdjacentHTML|document\.write/);
});

test('the narrow shape is painted from the pairs, not hidden from the panes', () => {
  const diff = bodyOf(CHAT_JS, 'renderEditDiff');
  assert.match(diff, /document\.body\.classList\.contains\('diff-narrow'\)/);
  assert.match(diff, /pre\.className = 'diff side'/);
  // A context line is said once; the two sides of a change stack.
  assert.match(diff, /side === 'new' && pair\.kind === 'ctx'/);
  // The painter is kept so a width flip can run it again.
  assert.match(diff, /paint\(\);/);
  assert.match(diff, /diffPainters\.set\(pre, paint\)/);
});

test('a flip repaints every comparison on screen', () => {
  const repaint = bodyOf(CHAT_JS, 'repaintDiffs');
  assert.match(repaint, /querySelectorAll\('\.tool \.edit-diff \.diff'\)/);
  assert.match(repaint, /diffPainters\.get\(pre\)/);
  assert.match(repaint, /paint\(\)/);

  const width = bodyOf(CHAT_JS, 'syncDiffWidth');
  // Only a change of shape repaints; a resize that does not cross the
  // threshold must not throw away a reader's scroll position.
  assert.match(
    width,
    /if \(document\.body\.classList\.contains\('diff-narrow'\) !== narrow\) \{/,
  );
  assert.ok(
    width.indexOf('repaintDiffs()') > width.indexOf("classList.toggle('diff-narrow'"),
    'the repaint follows the class',
  );
});

test('the two panes move as one', () => {
  const sync = bodyOf(CHAT_JS, 'syncDiffScroll');
  assert.match(sync, /pre\.querySelectorAll\('\.pane'\)/);
  assert.match(sync, /panes\.length !== 2/);
  assert.match(sync, /addEventListener\('scroll'/);
  // Both offsets are copied, so a vertical scroll keeps the rows level and a
  // horizontal one keeps the character column.
  assert.match(sync, /to\.scrollTop !== from\.scrollTop/);
  assert.match(sync, /to\.scrollLeft !== from\.scrollLeft/);
  // Each pane follows the other.
  const calls = sync.match(/follow\(panes/g) ?? [];
  assert.equal(calls.length, 2, 'both directions are listened to');
});

test('the pane carries the overflow, and the bar waits for the pointer', () => {
  const pane = ruleBody('.tool .edit-diff .diff > .pane');
  assert.match(pane, /flex:\s*1 1 0/);
  assert.match(pane, /min-width:\s*0/);
  assert.match(pane, /max-height:\s*18em/);
  // `scroll` rather than `auto` keeps the two feet level when only one side
  // overflows, so the rows stay aligned.
  assert.match(pane, /overflow:\s*scroll/);

  // The divider is the pane's, not the cell's.
  assert.match(
    ruleBody('.tool .edit-diff .diff > .pane.old'),
    /border-right:\s*1px solid var\(--ui-hairline\)/,
  );

  // A long line widens its pane instead of being clipped inside the cell.
  const row = ruleBody('.tool .edit-diff .diff .row');
  assert.match(row, /width:\s*max-content/);
  assert.match(row, /min-width:\s*100%/);
  const code = ruleBody('.tool .edit-diff .diff .cell .code');
  assert.doesNotMatch(code, /overflow-x/);

  // Transparent until the comparison is hovered, then the editor's colours.
  assert.match(
    ruleBody('.tool .edit-diff .diff > .pane::-webkit-scrollbar-thumb'),
    /background:\s*transparent/,
  );
  assert.match(
    ruleBody('.tool .edit-diff .diff:hover > .pane::-webkit-scrollbar-thumb'),
    /--vscode-scrollbarSlider-background/,
  );
  // One vertical bar for two synced panes: the old side keeps none.
  assert.match(
    ruleBody('.tool .edit-diff .diff > .pane.old::-webkit-scrollbar:vertical'),
    /width:\s*0/,
  );
  // The narrow shape is one column that scrolls as a whole.
  const narrow = ruleBody('.tool .edit-diff .diff.narrow');
  assert.match(narrow, /display:\s*block/);
  assert.match(narrow, /overflow:\s*auto/);
});
