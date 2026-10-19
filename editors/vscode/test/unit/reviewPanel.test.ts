/**
 * The wiring of the per-hunk review, checked where it cannot be run.
 *
 * The arithmetic is `hunks.test.ts`. What is left is agreement between four
 * files that no test in this suite can exercise at once: the card that offers
 * the button, the panel that opens the document, the document that answers, and
 * the answer that rewrites a call. Every failure in that chain is silent at
 * runtime — a button that posts a type nobody routes, a panel whose document is
 * never closed, an answer that settles the card but never reaches the host — so
 * the names on both sides are compared here.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');
const REVIEW_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'review.js'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');
const REVIEW_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'reviewPanel.ts'), 'utf8');

/** A top-level function of the renderer, from its declaration to the next one. */
function bodyOf(source: string, name: string): string {
  const start = source.indexOf(`\n  function ${name}(`);
  assert.ok(start >= 0, `${name} is in chat.js`);
  const rest = source.slice(start + 1);
  const next = rest.indexOf('\n  function ');
  return next >= 0 ? rest.slice(0, next) : rest;
}

/** A private method of the panel, from its declaration to the next one. */
function methodOf(source: string, signature: string): string {
  const start = source.indexOf(signature);
  assert.ok(start >= 0, `${signature} is in chatPanel.ts`);
  const rest = source.slice(start);
  const next = rest.indexOf('\n  private ');
  assert.ok(next > 0, `${signature} is followed by another member`);
  return rest.slice(0, next);
}

test('the review document renders only text, never markup', () => {
  // The document shows file content. That is the one kind of input this
  // extension must never let build a node from a string.
  assert.doesNotMatch(REVIEW_JS, /\.innerHTML\s*(\+)?=(?!=)/);
  assert.doesNotMatch(REVIEW_JS, /\.outerHTML\s*=/);
  assert.doesNotMatch(REVIEW_JS, /insertAdjacentHTML\s*\(/);
  assert.doesNotMatch(REVIEW_JS, /document\.write\s*\(/);
  assert.match(REVIEW_JS, /textContent/);
});

test('the panel document loads only the extension assets, under a nonce', () => {
  assert.match(REVIEW_PANEL, /cspSource/);
  assert.match(REVIEW_PANEL, /script-src 'nonce-\$\{nonce\}'/);
  assert.match(REVIEW_PANEL, /asset\('review\.css'\)/);
  assert.match(REVIEW_PANEL, /asset\('review\.js'\)/);
  // `default-src 'none'` is what makes the rest of the policy a whitelist.
  assert.match(REVIEW_PANEL, /default-src 'none'/);
});

test('the card offers a partial review for the two tools whose bodies it carries', () => {
  const card = bodyOf(CHAT_JS, 'describePreview');
  assert.match(card, /type: 'reviewPartial'/);
  assert.match(card, /request\.tool === 'write_file' \|\| request\.tool === 'edit_file'/);
  // A patch spans files and has no single body; offering parts would be a
  // promise the preview cannot keep.
  assert.doesNotMatch(card, /apply_patch/);
});

test('the panel routes the card to a document that is scoped to the approval', () => {
  assert.match(CHAT_PANEL, /case 'reviewPartial':/);
  const open = methodOf(CHAT_PANEL, 'private async openPartialReview(');
  // The proof, not the shape, decides: `partialDiff` fails closed.
  assert.match(open, /partialDiff\(request\.tool, request\.preview\)/);
  assert.match(open, /this\.partialReviews\.open\(requestId/);
});

test('a partial answer is an allow carrying the rewritten call', () => {
  const answer = methodOf(CHAT_PANEL, 'private async answerPartial(');
  assert.match(answer, /applyAccepted\(partial\.before, partial\.hunks, accepted\)/);
  assert.match(answer, /rewriteArgs\(request\.tool, request\.args, partial\.before, partial\.after, text\)/);
  assert.match(answer, /session\.respond\(request\.request_id, 'allow', \{ args \}\)/);
  // A refusal to compute is not a guess and not an allow.
  assert.match(answer, /nothing was sent/);

  const reject = methodOf(CHAT_PANEL, 'private async rejectPartial(');
  assert.match(reject, /session\.respond\(request\.request_id, 'deny'/);
});

test('one question has one set of surfaces, closed together', () => {
  const close = methodOf(CHAT_PANEL, 'private async closeReview(');
  assert.match(close, /this\.partialReviews\.close\(requestId\)/);
  assert.match(CHAT_PANEL, /this\.partialReviews\.dispose\(\)/);
});

test('the card settles on the review answer without answering again', () => {
  // The panel already sent `session.respond`. A card that posted its own
  // `answer` here would name a request that is no longer pending.
  const settle = bodyOf(CHAT_JS, 'settleFromReview');
  assert.doesNotMatch(settle, /vscode\.postMessage/);
  assert.match(settle, /msg\.kept/);
  assert.match(CHAT_JS, /case 'approvalSettled':/);
});
