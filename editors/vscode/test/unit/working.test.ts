/**
 * The working line: an anvil, a hammer and the facts of a run in flight.
 *
 * A turn used to leave the status line empty until it ended, so a reader
 * watching a long step could not tell a working agent from a hung one. The line
 * now carries a drawn mark, a word and a dim parenthetical, in the shape the
 * CLI gives one — and hands the same element to the run's summary at the end.
 *
 * The renderer is a browser script with no test window here, so the decisions
 * are pinned in the source and the shape in the stylesheet.
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

/** A declaration block from the stylesheet, without its comments. */
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

test('the mark is an anvil, a hammer and a spark, built as nodes', () => {
  const glyph = bodyOf(CHAT_JS, 'hammerGlyph');
  assert.match(glyph, /svgNode\('svg'/);
  assert.match(glyph, /class: 'working-glyph'/);
  // A decoration: the word beside it already says what is happening.
  assert.match(glyph, /'aria-hidden': 'true'/);
  assert.match(glyph, /class: 'anvil'/);
  assert.match(glyph, /svgNode\('g', \{ class: 'hammer' \}\)/);
  assert.match(glyph, /svgNode\('g', \{ class: 'spark' \}\)/);
  // The spark is keyed to the strike, so it starts painted out.
  assert.match(glyph, /class: 'spark'/);
  // Nodes, never markup.
  assert.doesNotMatch(glyph, /innerHTML|outerHTML|insertAdjacentHTML|document\.write/);
  assert.match(bodyOf(CHAT_JS, 'svgNode'), /createElementNS\(SVG_NS/);
});

test('the line is built once, and its two halves never show together', () => {
  assert.match(CHAT_JS, /const statusText = el\('span', 'status-text'\)/);
  assert.match(CHAT_JS, /const workingLine = el\('span', 'working'\)/);
  assert.match(CHAT_JS, /el\('span', 'working-word', 'Forging…'\)/);
  assert.match(CHAT_JS, /const workingMeta = el\('span', 'working-meta'\)/);
  assert.match(CHAT_JS, /statusLine\.appendChild\(workingLine\)/);
  assert.match(CHAT_JS, /statusLine\.appendChild\(statusText\)/);

  // The summary and the progress note share the other half, and the line is
  // hidden only when both halves are empty.
  const status = bodyOf(CHAT_JS, 'setStatus');
  assert.match(status, /statusText\.textContent = text/);
  assert.match(status, /statusLine\.hidden = workingLine\.hidden && text === ''/);
  // Nothing writes the status element's text directly any more: that would
  // wipe the animation's nodes on the next update.
  assert.doesNotMatch(CHAT_JS, /statusLine\.textContent/);
});

test('the clock starts with the turn and stops with it', () => {
  const start = bodyOf(CHAT_JS, 'startWorking');
  assert.match(start, /workingSince = Date\.now\(\)/);
  assert.match(start, /workingWord\.textContent = 'Forging…'/);
  assert.match(start, /workingLine\.hidden = false/);
  assert.match(start, /statusLine\.hidden = false/);
  assert.match(start, /setInterval\(renderWorkingMeta, 1000\)/);
  // Starting twice must not restart the clock a reopened tab is already on.
  assert.match(start, /if \(workingTimer !== null\) return/);

  const stop = bodyOf(CHAT_JS, 'stopWorking');
  assert.match(stop, /clearInterval\(workingTimer\)/);
  assert.match(stop, /animation\.cancel\(\)/);
  assert.match(stop, /workingLine\.hidden = true/);

  const running = bodyOf(CHAT_JS, 'setRunning');
  assert.match(running, /if \(running\) startWorking\(\)/);
  assert.match(running, /else stopWorking\(\)/);
});

test('the parenthetical says escape, the clock and the step', () => {
  const meta = bodyOf(CHAT_JS, 'renderWorkingMeta');
  assert.match(meta, /'esc to interrupt'/);
  // Minutes only once they are earned.
  assert.match(meta, /seconds < 60/);
  assert.match(meta, /Math\.floor\(seconds \/ 60\)/);
  // The step is the host's, and absent when it said nothing.
  assert.match(meta, /workingStep > 0/);
  assert.match(meta, /'step ' \+ workingStep/);
  assert.match(meta, /workingStep \+ '\/' \+ workingMax/);
  assert.match(meta, /workingMeta\.textContent = '\(' \+ parts\.join\(' · '\) \+ '\)'/);
  // The tick skips a tab nobody is looking at, like the cache clock does; the
  // next tick after the tab returns brings the elapsed time up to date.
  assert.match(meta, /if \(document\.hidden\) return/);
});

test('the head swings from script, and only when motion is welcome', () => {
  const animate = bodyOf(CHAT_JS, 'animateHammer');
  assert.match(animate, /rotate\(-72deg\)/);
  assert.match(animate, /iterations: Infinity/);
  assert.match(animate, /spark\.animate\(/);
  assert.match(CHAT_JS, /prefers-reduced-motion: reduce/);
  // The animation runs only while the line is up: starting the clock is what
  // starts the swing, and stopping it cancels both animations.
  const start = bodyOf(CHAT_JS, 'startWorking');
  assert.match(start, /if \(!reduceMotion\) workingAnimations = animateHammer\(\)/);
});

test('the summary takes the line when the run ends', () => {
  const finish = bodyOf(CHAT_JS, 'finishRun');
  assert.match(finish, /setStatus\(parts\.join\(' · '\)\)/);
  // A replayed run never had a working line; its end is still the same helper.
  const clear = bodyOf(CHAT_JS, 'clearTranscript');
  assert.match(clear, /stopWorking\(\)/);
  assert.match(clear, /setStatus\(''\)/);
});

test('a progress event feeds the line with what the host reported', () => {
  // One renderer branch owns progress now; it no longer writes the status text
  // directly and then hides the working half it just filled.
  const progress = CHAT_JS.slice(CHAT_JS.indexOf("e.type === 'progress'"));
  assert.match(progress, /typeof e\.step === 'number' && e\.step > 0/);
  assert.match(progress, /workingMax = typeof e\.maxSteps === 'number'/);
  assert.match(progress, /workingWord\.textContent = e\.text/);
  assert.match(progress, /else setStatus\(e\.text\)/);
});

test('the line is one row of mark and text, with a pivot in the stylesheet', () => {
  const line = ruleBody('.status .working');
  assert.match(line, /display:\s*inline-flex/);
  assert.match(line, /align-items:\s*center/);
  assert.match(line, /gap:\s*6px/);
  // `hidden` has to beat the display rule above.
  assert.match(ruleBody('.status .working[hidden]'), /display:\s*none/);

  const glyph = ruleBody('.status .working-glyph');
  assert.match(glyph, /width:\s*14px/);
  assert.match(glyph, /height:\s*14px/);

  const pivot = ruleBody('.status .working-glyph .hammer');
  assert.match(pivot, /transform-box:\s*view-box/);
  assert.match(pivot, /transform-origin:\s*12px 9px/);

  assert.match(ruleBody('.status .working-glyph .spark'), /opacity:\s*0/);
  assert.match(ruleBody('.status .working-meta'), /opacity:\s*0\.75/);
});
