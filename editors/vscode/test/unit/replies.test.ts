/**
 * A reply's beginning, and the fold around the turn.
 *
 * p_0.47.16 gave a turn an ending. A turn that can be read back also needs a
 * beginning, and a long conversation needs a way to close the turns already
 * read: the reply is one `<details>` from its start marker to its summary.
 *
 * The renderer is a browser script with no test window here, so the decisions
 * are pinned in the source and the shape in the stylesheet — the same way the
 * rail, the folded Thought and the summary ending are.
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
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return new RegExp(`${escaped} \\{([\\s\\S]*?)\\}`).exec(source)?.[1] ?? '';
}

test('a turn opens a reply with a start dot, its speaker and a fold', () => {
  const reply = bodyOf(CHAT_JS, 'ensureReply');
  // A details element, open while it is the turn being read.
  assert.match(reply, /el\('details', 'reply'\)/);
  assert.match(reply, /node\.open = true/);
  // The header: the hollow start dot, the speaker's name, the folded-face hint.
  assert.match(reply, /el\('summary', 'reply-head'\)/);
  assert.match(reply, /el\('span', 'start-dot'\)/);
  assert.match(reply, /el\('span', 'reply-name', 'anvil'\)/);
  assert.match(reply, /el\('span', 'reply-hint'\)/);
  // The body is the same column the transcript was, so the rhythm survives.
  assert.match(reply, /el\('div', 'reply-body'\)/);
  // The turn goes in at the end of the conversation but in front of the
  // transcript's footprint, so the document order matches the drawn order
  // (p_0.47.26).
  assert.match(reply, /addToEnd\(transcript, node\)/);
  // The reply is built from nodes, never from markup.
  assert.doesNotMatch(reply, /innerHTML|outerHTML|insertAdjacentHTML|document\.write/);
});

test('blocks go into the open turn, and a new turn starts one', () => {
  const target = bodyOf(CHAT_JS, 'targetFor');
  // A spawn card holds a child's conversation, which is not this transcript's
  // turn and has no reply of its own.
  assert.match(target, /if \(active !== mainScope\) return active\.container/);
  // While a reply is open everything goes in it, so a card the host sends after
  // a run ends cannot land after the turn it belongs to.
  assert.match(target, /if \(reply\) return reply\.body/);
  // With no reply open, only a block that begins a turn creates one.
  assert.match(target, /if \(!inReply\(node\)\) return active\.container/);
  assert.match(target, /return ensureReply\(\)\.body/);

  const inReply = bodyOf(CHAT_JS, 'inReply');
  assert.match(inReply, /classList\.contains\('tool'\)/);
  assert.match(inReply, /classList\.contains\('approval'\)/);
  assert.match(inReply, /classList\.contains\('assistant'\)/);
  assert.match(inReply, /classList\.contains\('result-note'\)/);
});

test('a user message closes the turn it follows, unless the reader touched it', () => {
  const close = bodyOf(CHAT_JS, 'closeReply');
  assert.match(close, /if \(reply\.node\.dataset\.pinned !== '1'\) reply\.node\.open = false/);
  assert.match(close, /reply = null/);

  const user = bodyOf(CHAT_JS, 'addUser');
  assert.match(user, /if \(active === mainScope\) closeReply\(\)/);
  // The close comes before the message is appended, so the message itself is
  // never inside a reply.
  assert.ok(user.indexOf('closeReply()') < user.indexOf('append(wrap)'));

  // A click on the header is the reader's decision; the automatic fold after it
  // must not undo it.
  const reply = bodyOf(CHAT_JS, 'ensureReply');
  assert.match(reply, /head\.addEventListener\('click'/);
  assert.match(reply, /node\.dataset\.pinned = '1'/);
});

test('a new conversation forgets the turn on screen', () => {
  const clear = bodyOf(CHAT_JS, 'clearTranscript');
  assert.match(clear, /reply = null/);
});

test('the folded face carries the conclusion, and never invents one', () => {
  const mark = bodyOf(CHAT_JS, 'markSummary');
  assert.match(mark, /round\.wrap\.closest\('\.reply'\)/);
  assert.match(mark, /querySelector\('\.reply-hint'\)/);
  assert.match(mark, /hint\.textContent = parts\.title/);
  // The hint is written only by the summary: a run that did not conclude leaves
  // it empty rather than borrowing a line from somewhere else.
  const writers = CHAT_JS.split('\n').filter((line) => line.includes("querySelector('.reply-hint')"));
  assert.equal(writers.length, 1, 'only markSummary writes the hint');
});

test('an approval belongs to the turn that is waiting for it', () => {
  // Approvals do not go through `append`, so the one route they do take has to
  // enter the reply as well — a card outside the `<details>` would read after
  // the turn's later blocks.
  assert.match(
    CHAT_JS,
    /const target = active === mainScope && reply \? reply\.body : transcript;/,
  );
});

test('the reply is a header with a hollow dot, a hint and a chevron', () => {
  const body = ruleBody('.reply > .reply-body');
  assert.match(body, /display:\s*flex/);
  assert.match(body, /flex-direction:\s*column/);
  assert.match(body, /gap:\s*var\(--anvil-gap\)/);

  const head = ruleBody('.reply > .reply-head');
  assert.match(head, /display:\s*flex/);
  assert.match(head, /cursor:\s*pointer/);
  assert.match(head, /list-style:\s*none/);
  assert.match(head, /padding:\s*4px 6px/);

  // The start dot is the end dot's ring: 8px drawn, 6px laid out, the same
  // centre as the rail, and no fill.
  const dot = ruleBody('.reply > .reply-head .start-dot');
  assert.match(dot, /width:\s*8px/);
  assert.match(dot, /height:\s*8px/);
  assert.match(dot, /margin:\s*-1px/);
  assert.match(dot, /border:\s*2px solid var\(--vscode-charts-purple/);
  assert.match(dot, /background:\s*transparent/);
  assert.match(dot, /z-index:\s*1/);

  // The folded face is one line of conclusion, clipped rather than wrapped, and
  // hidden once the turn is open.
  const hint = ruleBody('.reply > .reply-head .reply-hint');
  assert.match(hint, /text-overflow:\s*ellipsis/);
  assert.match(hint, /white-space:\s*nowrap/);
  assert.match(ruleBody('.reply[open] > .reply-head .reply-hint'), /display:\s*none/);

  // The chevron turns with the fold, and the rail starts under the dot.
  assert.match(ruleBody('.reply > .reply-head::after'), /content:\s*'›'/);
  assert.match(ruleBody('.reply[open] > .reply-head::after'), /transform:\s*rotate\(90deg\)/);
  const line = ruleBody('.reply[open] > .reply-head::before');
  assert.match(line, /left:\s*9px/);
  assert.match(line, /width:\s*1px/);
});

test('the turn says its speaker once, and the ending keeps its own header', () => {
  assert.match(
    ruleBody('.reply-body > .msg.assistant:not(.summary) > .who'),
    /display:\s*none/,
  );
  // The summary's `.who` is the end marker: the rule above leaves it alone, and
  // its own rule still lays the dot and title out.
  assert.match(ruleBody('.msg.assistant.summary > .who'), /display:\s*flex/);
});
