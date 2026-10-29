/**
 * The end of a reply, checked where it cannot be drawn.
 *
 * A summary block is built out of three decisions, and each fails quietly: a
 * title taken from a line of syntax, a body that still carries the title, a
 * block that marks prose the run did not end on. The renderer is a browser
 * script with no test window here, so the properties are pinned in the source
 * and in the stylesheet — the same way the rail and the folded Thought are.
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

test('a title is prose taken off a line, or nothing at all', () => {
  const title = bodyOf(CHAT_JS, 'summaryTitle');
  // Syntax is not a title. A final answer that opens on a fence, a rule or a
  // table keeps the ordinary ending rather than getting a header made of it.
  assert.match(title, /\^\(`\{3,\}\|~\{3,\}\)/);
  assert.match(title, /\^\[-\*_\]\{3,\}\$/);
  assert.match(title, /text\.startsWith\('\|'\)/);
  // Markdown comes off: headings, quotes, list markers, image and link
  // wrappers, inline emphasis and a trailing colon.
  assert.match(title, /replace\(\/\^#\{1,6\}\\s\*\//);
  assert.match(title, /replace\(\/\^>\\s\*\//);
  assert.match(title, /replace\(\/\^\[-\*\+\]\\s\+\//);
  assert.match(title, /replace\(\/\^\\d\+\[\.\)\]\\s\+\//);
  assert.match(title, /replace\(\/!\?\\\[/);
  assert.match(title, /replace\(\/\[\*_`~\]\/g, ''\)/);
  // A long title is clipped rather than allowed to run the width of the panel.
  assert.match(title, /title\.length > 80/);
  assert.match(title, /slice\(0, 79\) \+ '…'/);
});

test('the body removes a complete title but preserves a clipped first line', () => {
  const parts = bodyOf(CHAT_JS, 'summaryParts');
  // Leading blank lines are skipped before the title is chosen.
  assert.match(parts, /while \(i < lines\.length && lines\[i\]\.trim\(\) === ''\) i \+= 1/);
  assert.match(parts, /const title = summaryTitle\(lines\[i\]\)/);
  assert.match(parts, /if \(title === ''\) return null/);
  // A short title is removed, but a clipped title is only a preview: the full
  // source line stays in the body so rendering and Copy carry the same answer.
  assert.match(parts, /const bodyFrom = title\.endsWith\('…'\) \? i : i \+ 1/);
  assert.match(parts, /lines\.slice\(bodyFrom\)\.join\('\\n'\)/);
  assert.match(parts, /replace\(\/\^\(\?:\[ \\t\]\*\\r\?\\n\)\+\/, ''\)/);
  assert.match(parts, /return \{ title, body \}/);
});

test('only a finished run whose last block is prose becomes a summary', () => {
  const mark = bodyOf(CHAT_JS, 'markSummary');
  // An empty round has nothing to title, and a round that is not the last block
  // is prose the run went on from.
  assert.match(mark, /round\.text\.trim\(\) === ''/);
  assert.match(mark, /isLastBlock\(round\.wrap\)/);
  assert.match(mark, /summaryParts\(round\.text\)/);

  // The header is the end dot, the word, and the title — built as text nodes.
  assert.match(mark, /el\('span', 'end-dot'\)/);
  assert.match(mark, /el\('span', 'summary-label', 'Summary'\)/);
  assert.match(mark, /el\('span', 'summary-title', parts\.title\)/);
  assert.match(mark, /classList\.add\('summary'\)/);
  assert.doesNotMatch(mark, /innerHTML|outerHTML|insertAdjacentHTML|document\.write/);

  // The body is re-rendered from the remainder, so the title is not repeated
  // under its own header. It is then moved after the reply details so folding
  // the work cannot hide the conclusion, and the old body rail is recomputed.
  assert.match(mark, /round\.text = parts\.body/);
  assert.match(mark, /round\.md = \{ node: round\.body, settled: \[\], offset: 0, rendered: 0 \}/);
  assert.match(mark, /round\.body\.textContent = ''/);
  assert.match(mark, /paintMarkdown\(round\.md, round\.text\)/);
  assert.match(mark, /insertBefore\(round\.wrap, turn\.nextSibling\)/);
  assert.match(mark, /classList\.remove\('rail', 'rail-up', 'rail-down'\)/);
  assert.match(mark, /syncRail\(body\)/);
});

test('the two endings call it by their own status rules', () => {
  const live = bodyOf(CHAT_JS, 'finishRun');
  assert.match(live, /const round = currentRound\(\)/);
  // A clean finish arrives as `{kind: 'none'}`, never as no failure at all, so
  // the live path tests the kind. Testing the object is what made a live run
  // draw no summary while its replay did.
  assert.match(
    live,
    /if \(!failure \|\| failure\.kind === 'none'\) markSummary\(round, done && done\.run_id\)/,
  );
  assert.doesNotMatch(live, /if \(!failure\) markSummary/);
  assert.ok(
    live.indexOf('markSummary(round,') < live.indexOf('finishReply()'),
    'the live reply folds after its Summary is moved out',
  );

  // A replay knows the stored status: failed and stopped runs are not
  // conclusions, and the summary is marked before the run-end line is appended
  // so it is still the last block the run drew.
  const replay = bodyOf(CHAT_JS, 'addRunEnd');
  assert.match(replay, /const round = currentRound\(\)/);
  assert.match(
    replay,
    /if \(status !== 'error' && status !== 'cancelled'\) markSummary\(round, done && done\.run_id\)/,
  );
  assert.ok(
    replay.indexOf('markSummary(round,') < replay.indexOf('finishReply()')
      && replay.indexOf('finishReply()') < replay.indexOf("el('div', 'msg run-end')"),
    'the Summary is moved out and the work folded before the run-end line',
  );
});

test('a run’s changed-file list is placed directly after its Summary', () => {
  const mark = bodyOf(CHAT_JS, 'markSummary');
  assert.match(mark, /runSummaries\.set\(runId, round\.wrap\)/);

  const changes = bodyOf(CHAT_JS, 'showChanges');
  assert.match(changes, /Changed files · \$\{files\.length\}/);
  assert.match(changes, /runSummaries\.get\(typeof runId === 'string' \? runId : ''\)/);
  assert.match(changes, /summary\.insertAdjacentElement\('afterend', card\)/);
  assert.match(changes, /vscode\.postMessage\(\{ type: 'reviewChange', path: file\.path \}\)/);
});

test('the summary header is an end dot, a word and a clipped title', () => {
  const who = /\.msg\.assistant\.summary > \.who \{([\s\S]*?)\}/.exec(CHAT_CSS)?.[1] ?? '';
  assert.ok(who, 'the summary header has a rule');
  assert.match(who, /display:\s*flex/);
  assert.match(who, /gap:\s*10px/);
  assert.match(who, /padding-left:\s*6px/);

  const dot = /\.msg\.assistant\.summary > \.who \.end-dot \{([\s\S]*?)\}/.exec(CHAT_CSS)?.[1] ?? '';
  assert.ok(dot, 'the end dot has a rule');
  // The same 8px drawn, 6px laid out as the tool dot, so it sits on the rail
  // and covers it; the ending colour is neither the operation green nor the
  // failure red nor the link blue.
  assert.match(dot, /width:\s*8px/);
  assert.match(dot, /height:\s*8px/);
  assert.match(dot, /margin:\s*-1px/);
  assert.match(dot, /z-index:\s*1/);
  assert.match(dot, /--vscode-charts-purple/);

  const title = /\.msg\.assistant\.summary > \.who \.summary-title \{([\s\S]*?)\}/.exec(CHAT_CSS)?.[1] ?? '';
  assert.ok(title, 'the title has a rule');
  assert.match(title, /text-overflow:\s*ellipsis/);
  assert.match(title, /white-space:\s*nowrap/);

  // The line stops inside the dot rather than running beside the summary body.
  const rail = /\.msg\.assistant\.summary::after \{([\s\S]*?)\}/.exec(CHAT_CSS)?.[1] ?? '';
  assert.ok(rail, 'the summary rail has a rule');
  assert.match(rail, /bottom:\s*auto/);
  assert.match(rail, /height:\s*10px/);
  const up = /\.msg\.assistant\.summary\.rail-up::after \{([\s\S]*?)\}/.exec(CHAT_CSS)?.[1] ?? '';
  assert.ok(up, 'a summary with a block above reaches up');
  assert.match(up, /top:\s*-10px/);
  assert.match(up, /height:\s*20px/);
});
