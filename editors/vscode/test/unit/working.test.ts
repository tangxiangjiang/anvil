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
const CHAT_PANEL = readFileSync(join(ROOT, 'src', 'chatPanel.ts'), 'utf8');

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
  // Every stroke is at least a pixel on screen: 1.4 units is about 1.6px at 18px.
  assert.match(glyph, /'stroke-width': '1\.4'/);
  // The ink reaches the box's edges rather than sitting in the middle of it.
  assert.match(glyph, /M1\.2 9\.8h10\.2v2H1\.2z/);
  assert.match(glyph, /M3\.8 13\.4h7\.2v1\.6H3\.8z/);
  assert.match(glyph, /x: '8\.2',\s*y: '0\.8',\s*width: '6\.2'/);
  assert.match(glyph, /x: '10\.6',\s*y: '2\.6',\s*width: '1\.6'/);
  // Nodes, never markup.
  assert.doesNotMatch(glyph, /innerHTML|outerHTML|insertAdjacentHTML|document\.write/);
  assert.match(bodyOf(CHAT_JS, 'svgNode'), /createElementNS\(SVG_NS/);
});

test('the line is built once, and its two halves never show together', () => {
  assert.match(CHAT_JS, /const statusText = el\('span', 'status-text'\)/);
  assert.match(CHAT_JS, /const workingLine = el\('span', 'working'\)/);
  assert.match(CHAT_JS, /el\('span', 'working-word', DEFAULT_WORKING_WORD\)/);
  assert.match(CHAT_JS, /const workingMeta = el\('span', 'working-meta'\)/);
  assert.match(CHAT_JS, /statusLine\.appendChild\(workingLine\)/);
  assert.match(CHAT_JS, /statusLine\.appendChild\(statusText\)/);

  // The summary and the progress note share the other half, and the footprint
  // is hidden only when both halves and the root line are empty.
  const status = bodyOf(CHAT_JS, 'setStatus');
  assert.match(status, /statusText\.textContent = text/);
  assert.match(status, /syncFootprint\(\)/);
  const sync = bodyOf(CHAT_JS, 'syncFootprint');
  assert.match(
    sync,
    /footprint\.hidden = rootLine\.hidden && meter\.hidden && workingLine\.hidden && statusText\.hidden/,
  );
  // Nothing writes the status element's text directly any more: that would
  // wipe the animation's nodes on the next update.
  assert.doesNotMatch(CHAT_JS, /statusLine\.textContent/);
});

test('the clock starts with the turn and stops with it', () => {
  const start = bodyOf(CHAT_JS, 'startWorking');
  assert.match(start, /workingSince = Date\.now\(\)/);
  assert.match(start, /workingWord\.textContent = DEFAULT_WORKING_WORD/);
  assert.match(start, /workingLine\.hidden = false/);
  assert.match(start, /syncFootprint\(\)/);
  assert.match(start, /setInterval\(renderWorkingMeta, 1000\)/);
  // Starting twice must not restart the clock a reopened tab is already on.
  assert.match(start, /if \(workingTimer !== null\) return/);
  // The line is in the scroll content now, so the write puts the reader back.
  assert.match(start, /scrollIfPinned\(pinned\)/);

  const stop = bodyOf(CHAT_JS, 'stopWorking');
  assert.match(stop, /clearInterval\(workingTimer\)/);
  assert.match(stop, /animation\.cancel\(\)/);
  assert.match(stop, /workingLine\.hidden = true/);
  assert.match(stop, /syncFootprint\(\)/);

  const running = bodyOf(CHAT_JS, 'setRunning');
  assert.match(running, /if \(running\) startWorking\(\)/);
  assert.match(running, /else stopWorking\(\)/);
});

test('the parenthetical says escape, clock, step and live counts', () => {
  const meta = bodyOf(CHAT_JS, 'renderWorkingMeta');
  assert.match(meta, /'esc to interrupt'/);
  // Minutes only once they are earned.
  assert.match(meta, /seconds < 60/);
  assert.match(meta, /Math\.floor\(seconds \/ 60\)/);
  // The step is the host's, and absent when it said nothing. Its spelling
  // lives in `stepLabel` so the counter can be its own node (p_0.46.26).
  assert.match(meta, /workingStep > 0/);
  const label = bodyOf(CHAT_JS, 'stepLabel');
  assert.match(label, /'step ' \+ workingStep/);
  assert.match(label, /workingStep \+ '\/' \+ workingMax/);
  // The step is appended as a node, so a landing step has something to
  // pulse and the parenthetical keeps one truncation box around it. Its
  // separator is a text node of its own, so the pulse moves the counter and
  // not the punctuation in front of it.
  assert.match(meta, /createTextNode\('\\u00a0· '\)/);
  assert.match(meta, /workingMeta\.appendChild\(workingStepNode\)/);
  assert.match(meta, /workingMeta\.appendChild\(meter\)/);
  assert.match(meta, /document\.createTextNode\('\)'\)/);
  // The tick skips a tab nobody is looking at, like the cache clock does; the
  // next tick after the tab returns brings the elapsed time up to date.
  assert.match(meta, /if \(document\.hidden\) return/);
});

test('the activity word follows real stream, tool and approval events', () => {
  assert.doesNotMatch(CHAT_JS, /WORKING_WORDS|Math\.floor\(seconds \/ 3\)/);
  const activity = bodyOf(CHAT_JS, 'toolActivity');
  assert.match(activity, /'read_file'.*return 'Reading…'/s);
  assert.match(activity, /'grep'.*return 'Searching…'/s);
  assert.match(activity, /'write_file'.*return 'Editing…'/s);
  assert.match(activity, /name === 'shell'.*return 'Running Bash…'/s);

  const render = bodyOf(CHAT_JS, 'renderEvent');
  assert.match(render, /setWorkingActivity\('Responding…'\)/);
  assert.match(render, /setWorkingActivity\(toolActivity\(e\.name\)\)/);
  assert.match(render, /setWorkingActivity\(DEFAULT_WORKING_WORD\)/);

  const approval = bodyOf(CHAT_JS, 'showApproval');
  assert.match(approval, /setWorkingActivity\('Waiting for ' \+ toolTitle\(request\.tool\) \+ ' approval…'\)/);
  // The one-second clock redraws metadata only; it cannot rotate the word while
  // an approval is waiting.
  assert.doesNotMatch(bodyOf(CHAT_JS, 'renderWorkingMeta'), /workingWord|setWorkingActivity|flowText/);
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
  // Emptying the transcript detaches the block, which is chrome rather than a
  // turn; it goes back before the next conversation starts.
  assert.match(clear, /transcript\.appendChild\(footprint\)/);
  assert.match(clear, /syncFootprint\(\)/);
});

test('a progress event feeds the line with what the host reported', () => {
  // One renderer branch owns progress now; it no longer writes the status text
  // directly and then hides the working half it just filled.
  const progress = CHAT_JS.slice(CHAT_JS.indexOf("e.type === 'progress'"));
  assert.match(progress, /typeof e\.step === 'number' && e\.step > 0/);
  assert.match(progress, /workingMax = typeof e\.maxSteps === 'number'/);
  assert.match(progress, /setWorkingActivity\(e\.text\)/);
  assert.match(progress, /else setStatus\(e\.text\)/);
});

test('the line is one row of mark and text, with a pivot in the stylesheet', () => {
  const line = ruleBody('.status');
  // A flex row, so the line's height is `max(min-height, tallest child)` and
  // not the strut plus whatever an inline-flex baseline adds (p_0.46.25).
  assert.match(line, /display:\s*flex/);
  assert.match(line, /align-items:\s*center/);
  const working = ruleBody('.status .working');
  assert.match(working, /display:\s*flex/);
  assert.match(working, /align-items:\s*center/);
  assert.match(working, /gap:\s*6px/);
  // `hidden` has to beat the display rule above.
  assert.match(ruleBody('.status .working[hidden]'), /display:\s*none/);
  // The mark and the word keep their size; the parenthetical is what truncates.
  const word = ruleBody('.status .working-word');
  assert.match(word, /flex:\s*0 0 auto/);
  assert.match(word, /font-size:\s*1\.12em/);
  assert.match(word, /font-weight:\s*650/);
  assert.match(word, /color:\s*var\(--vscode-foreground\)/);
  const meta = ruleBody('.status .working-meta');
  assert.match(meta, /min-width:\s*0/);
  assert.match(meta, /text-overflow:\s*ellipsis/);

  // Larger than the 0.85em line it labels; the view box does not change.
  const glyph = ruleBody('.status .working-glyph');
  assert.match(glyph, /width:\s*18px/);
  assert.match(glyph, /height:\s*18px/);
  assert.match(glyph, /color:\s*var\(--vscode-descriptionForeground\)/);

  // The hammer and its spark are the one orange in the line; the anvil stays
  // muted. `done` is green and `stopped` the warning colour, so orange cannot
  // be read as a state.
  const pivot = ruleBody('.status .working-glyph .hammer');
  assert.match(pivot, /transform-box:\s*view-box/);
  // The face's top-right corner, where the handle meets the anvil.
  assert.match(pivot, /transform-origin:\s*11\.4px 9\.8px/);
  assert.match(pivot, /color:\s*var\(--vscode-charts-orange, #e8912d\)/);

  const spark = ruleBody('.status .working-glyph .spark');
  assert.match(spark, /opacity:\s*0/);
  assert.match(spark, /color:\s*var\(--vscode-charts-orange, #e8912d\)/);

  assert.match(ruleBody('.status .working-meta'), /opacity:\s*0\.75/);
});

test('the session’s lines scroll with the conversation, as one entry', () => {
  // The block is in the transcript rather than the floating dock (p_0.46.24):
  // in the flow, a floating line's reserved height is measured a frame late and
  // the conversation is painted across it.
  const transcript = CHAT_PANEL.indexOf('id="transcript"');
  const block = CHAT_PANEL.indexOf('id="footprint"');
  const dock = CHAT_PANEL.indexOf('id="composerDock"');
  assert.ok(transcript > 0 && block > transcript, 'the footprint is in the transcript');
  assert.ok(dock > block, 'the footprint is before the dock');

  // The dock no longer owns any footprint line.
  const dockBody = CHAT_PANEL.slice(dock, CHAT_PANEL.indexOf('<form id="composer"', dock));
  assert.doesNotMatch(dockBody, /id="status"|id="root"|id="meter"/);

  // The meter starts hidden in the footprint and is moved into the working
  // parenthetical when a run starts; it is never a visible row of its own.
  const inner = CHAT_PANEL.slice(block, dock);
  const status = inner.indexOf('id="status"');
  const meter = inner.indexOf('id="meter"');
  const root = inner.indexOf('id="root"');
  assert.ok(status >= 0 && meter > status && root > meter, 'status owns meter before root');
  assert.match(bodyOf(CHAT_JS, 'renderWorkingMeta'), /workingMeta\.appendChild\(meter\)/);

  // Moving an element means cutting it, not copying it.
  for (const id of ['footprint', 'status', 'root', 'meter']) {
    assert.equal(CHAT_PANEL.split(`id="${id}"`).length - 1, 1, `one #${id}`);
  }
});

test('the live total is inline with the working metadata', () => {
  const block = CHAT_PANEL.indexOf('id="footprint"');
  const status = CHAT_PANEL.indexOf('id="status"', block);
  const meter = CHAT_PANEL.indexOf('id="meter"', block);
  const root = CHAT_PANEL.indexOf('id="root"', block);
  const form = CHAT_PANEL.indexOf('<form id="composer"');
  assert.ok(block > 0 && status > block && meter > status && root > meter, 'meter is born beside status');
  assert.ok(meter < form, 'the meter is in the transcript, not the composer');
  assert.match(CHAT_PANEL, /<span id="meter" class="meter" hidden><\/span>/);

  const css = ruleBody('.footprint .meter');
  assert.match(css, /display:\s*inline/);
  assert.match(css, /white-space:\s*nowrap/);
  assert.doesNotMatch(css, /flex:|text-overflow/);
  assert.match(ruleBody('.footprint .meter[hidden]'), /display:\s*none/);
});

test('the footprint keeps the panel’s own rhythm, and reserves its first line', () => {
  const block = ruleBody('.footprint');
  assert.match(block, /display:\s*flex/);
  assert.match(block, /flex-direction:\s*column/);
  // The label-to-value gap `.msg` uses, not the transcript's 12px entry gap.
  assert.match(block, /gap:\s*4px/);
  assert.match(block, /margin-block:\s*4px/);
  // Sorts after every turn however one is appended.
  assert.match(block, /order:\s*1/);
  assert.match(ruleBody('.footprint[hidden]'), /display:\s*none/);

  // Both lines take the transcript's own inset rather than the dock's 13px.
  assert.match(ruleBody('.status'), /padding:\s*0;/);
  assert.match(ruleBody('.root'), /padding:\s*0;/);
  assert.match(ruleBody('.status'), /font-size:\s*0\.85em/);
  assert.match(ruleBody('.root'), /font-size:\s*0\.85em/);

  // Line one is exactly the taller of the text line and the mark, so a run
  // starting or ending changes the block's words, never its height.
  const status = ruleBody('.status');
  assert.match(status, /min-height:\s*max\(1\.5em, 18px\)/);
  assert.match(status, /overflow:\s*hidden/);
  // The summary truncates in its own box; `text-overflow` on the row would not
  // reach it across the flex-item boundary.
  const summary = ruleBody('.status-text');
  assert.match(summary, /min-width:\s*0/);
  assert.match(summary, /text-overflow:\s*ellipsis/);
});

test('the composer’s controls keep their size without the total', () => {
  assert.match(ruleBody('.composer .actions > button'), /flex:\s*0 0 auto/);
  assert.equal(CHAT_PANEL.slice(CHAT_PANEL.indexOf('<form id="composer"')).includes('id="meter"'), false);
});
