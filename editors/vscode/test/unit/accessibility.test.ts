/**
 * Accessibility, checked at the source because there is no browser here.
 *
 * What can fail silently is exactly what these pin: a page whose transcript is
 * itself the live region (every token announced), an approval card that appears
 * with no announcement and nothing focused, an icon-only button whose only name
 * is a tooltip, or a focus ring drawn from a colour that disappears on a dark
 * theme. None of those throw; all of them make the panel unusable without a
 * mouse, which is the contract p_0.47.5 makes.
 *
 * The manual pass — a real screen reader on a dark and a high-contrast theme —
 * cannot be run here and is recorded in the phase document instead.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_CSS = readFileSync(join(ROOT, 'media', 'chat.css'), 'utf8');
const CHAT_PANEL = readFileSync(join(ROOT, 'src', 'chatPanel.ts'), 'utf8');

test('the transcript is a log, and the announcements have their own regions', () => {
  // Two regions with two urgencies: an answer or the end of a turn waits its
  // turn; an approval or an error does not.
  assert.match(CHAT_PANEL, /<div id="srStatus" class="sr-only" role="status" aria-live="polite"/);
  assert.match(CHAT_PANEL, /<div id="srAlert" class="sr-only" role="alert" aria-live="assertive"/);
  assert.match(CHAT_JS, /getElementById\('srStatus'\)/);
  assert.match(CHAT_JS, /getElementById\('srAlert'\)/);

  // The transcript keeps its log role but is not live: every chunk of an answer
  // mutates it, and a live region there is a reader asked to hear every token.
  assert.match(CHAT_PANEL, /id="transcript" class="transcript" role="log" aria-live="off"/);
  assert.doesNotMatch(CHAT_PANEL, /id="transcript"[^>]*aria-live="polite"/);
});

test('announcements are throttled at half a second and sent once per block', () => {
  assert.match(CHAT_JS, /const ANNOUNCE_INTERVAL_MS = 500;/);
  const progress = CHAT_JS.slice(CHAT_JS.indexOf('function announceProgress('));
  assert.match(progress.slice(0, 400), /ANNOUNCE_INTERVAL_MS/);
  // Streaming text reaches the polite region through the throttle; a finished
  // block is said whole, once.
  assert.match(CHAT_JS, /if \(active === mainScope\) announceProgress\(e\.text\);/);
  assert.match(CHAT_JS, /const said = round\.body\.textContent\.trim\(\);/);
  assert.match(CHAT_JS, /announce\(said\);/);
  // Completion is its own sentence after the last block.
  assert.match(CHAT_JS, /announce\(failure && failure\.kind === 'cancelled' \? 'Turn stopped\.' : 'Answer finished\.'\)/);
});

test('a replayed conversation is drawn quietly', () => {
  // A replay is history, not a turn in flight. Without the bracket the regions
  // would read the whole stored conversation aloud as it is drawn.
  assert.match(CHAT_PANEL, /this\.post\(\{ type: 'replayStart' \}\);/);
  assert.match(CHAT_PANEL, /finally \{[\s\S]{0,500}type: 'replayEnd'/);
  assert.match(CHAT_JS, /let replaying = false;/);
  assert.match(CHAT_JS, /function announce\(text\) \{\s*if \(!srStatus \|\| !text \|\| replaying\) return;/);
  assert.match(CHAT_JS, /function announceAlert\(text\) \{\s*if \(!srAlert \|\| !text \|\| replaying\) return;/);
  assert.match(CHAT_JS, /case 'replayStart':\s*replaying = true;/);
  assert.match(CHAT_JS, /case 'replayEnd':\s*replaying = false;/);
});

test('the assertive region carries approvals and errors, and only those', () => {
  const alertUses = CHAT_JS.match(/announceAlert\(/g) ?? [];
  // Definition, approval, notice, banner, remember failure: the places that
  // interrupt. A count is deliberate — a new use should be a decision, not a
  // drifting default of "say everything urgently".
  assert.equal(alertUses.length, 5);
  assert.match(CHAT_JS, /announceAlert\('Approval needed: ' \+ request\.tool/);
  assert.match(CHAT_JS, /function addNotice\(text\)[\s\S]{0,200}announceAlert\(text\)/);
  assert.match(CHAT_JS, /if \(level === 'error'\) announceAlert\(message\);/);
});

test('an approval request takes focus, and a decision gives it back', () => {
  // The composer is disabled while a turn runs, so nothing else owns the
  // keyboard when the card appears; tabbing through the transcript is not a
  // path a blocking question can depend on.
  assert.match(CHAT_JS, /const first = buttons\.querySelector\('button'\);[\s\S]{0,80}first\.focus\(\);/);
  // The pressed button is disabled by the answer; focus goes somewhere that
  // still does something rather than to the document.
  assert.match(CHAT_JS, /const hadFocus = card\.contains\(document\.activeElement\);/);
  assert.match(CHAT_JS, /function refocusAfter\(hadFocus\)[\s\S]{0,80}sendButton\.focus\(\)/);
  // A turn starting disables the textarea; focus moves to the stop control.
  assert.match(CHAT_JS, /const hadInputFocus = document\.activeElement === input;/);
  assert.match(CHAT_JS, /if \(running && hadInputFocus\) sendButton\.focus\(\);/);
});

test('every icon-only control has a name that is not a tooltip', () => {
  // "×" and "✕" are pictures; `title` is a tooltip and is not reliably the
  // accessible name.
  assert.match(CHAT_JS, /button\.setAttribute\('aria-label', title\);/);
  assert.match(CHAT_JS, /dismiss\.setAttribute\('aria-label', 'Dismiss'\);/);
  // "@" likewise.
  assert.match(CHAT_PANEL, /id="attach"[^>]*aria-label="Reference a file or folder"/);
  // Send is an SVG; its label changes with what the button does.
  assert.match(CHAT_JS, /sendButton\.setAttribute\('aria-label', running \? 'Stop' : 'Send'\);/);
});

test('approval controls are a group named by the question above them', () => {
  assert.match(CHAT_JS, /const headId = 'approval-head-' \+ \+\+approvalSeq;/);
  assert.match(CHAT_JS, /head\.id = headId;/);
  assert.match(CHAT_JS, /buttons\.setAttribute\('role', 'group'\);/);
  assert.match(CHAT_JS, /buttons\.setAttribute\('aria-labelledby', headId\);/);
  // The task list's label was on a generic div, where it is not exposed.
  assert.match(CHAT_PANEL, /id="todos" class="todos" role="list"/);
  assert.match(CHAT_JS, /row\.setAttribute\('role', 'listitem'\);/);
});

test('focus is visible in the ordinary and the high-contrast themes', () => {
  // The live regions have to stay in the accessibility tree, so they are
  // clipped, not hidden.
  assert.match(CHAT_CSS, /\.sr-only \{[\s\S]{0,200}clip-path: inset\(50%\)/);
  assert.doesNotMatch(CHAT_CSS, /\.sr-only \{[^}]*display: none/);

  // One ring, from the theme's focus colour, and only when a keyboard put the
  // focus there.
  assert.match(CHAT_CSS, /button:focus-visible,\s*\nselect:focus-visible,\s*\ntextarea:focus-visible/);
  assert.match(CHAT_CSS, /outline: 1px solid var\(--vscode-focusBorder\);/);
  assert.match(CHAT_CSS, /body\.vscode-high-contrast button:focus-visible[\s\S]{0,900}outline: 2px solid var\(--vscode-contrastActiveBorder, var\(--vscode-focusBorder\)\)/);
  assert.match(CHAT_CSS, /body\.vscode-high-contrast-light button:focus-visible/);
  // A hand-drawn ring replaced by none would be the failure this guards.
  assert.doesNotMatch(CHAT_CSS, /outline: none/);
});

test('the keyboard path through the composer and the slash menu is intact', () => {
  // Arrows, Enter/Tab and Escape on the palette; Enter sends; Escape stops a
  // run from anywhere.
  assert.match(CHAT_JS, /if \(event\.key === 'ArrowDown'\) \{\s*\n\s*event\.preventDefault\(\);\s*\n\s*movePalette\(1\);/);
  assert.match(CHAT_JS, /if \(event\.key === 'Enter' \|\| event\.key === 'Tab'\) \{[\s\S]{0,80}acceptPalette\(paletteIndex\)/);
  assert.match(CHAT_JS, /event\.key === 'Escape'[\s\S]{0,60}closePalette\(\)/);
  assert.match(CHAT_JS, /event\.key === 'Enter' && !event\.shiftKey[\s\S]{0,120}dispatchEvent\(new Event\('submit'/);
  assert.match(CHAT_JS, /if \(event\.key !== 'Escape' \|\| !running\) return;/);
});
