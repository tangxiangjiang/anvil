/**
 * The task checklist: where it is drawn, and that it replaces rather than
 * appends.
 *
 * The arithmetic and the plumbing live on the Go side, where the tool validates
 * what it stores; what this file checks is the half a screenshot cannot: that
 * every `todo` event replaces the block, that clearing hides it, and that the
 * rows are built with `textContent` like every other renderer in this webview.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_CSS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.css'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');
const PROTOCOL = readFileSync(join(EXTENSION_ROOT, 'src', 'protocol.ts'), 'utf8');

/** A top-level function of the renderer, from its declaration to the next one. */
function bodyOf(source: string, name: string): string {
  const start = source.indexOf(`\n  function ${name}(`);
  assert.ok(start >= 0, `${name} is in chat.js`);
  const rest = source.slice(start + 1);
  const next = rest.indexOf('\n  function ');
  return next >= 0 ? rest.slice(0, next) : rest;
}

test('the block exists below the composer, and an empty one is hidden', () => {
  const form = CHAT_PANEL.indexOf('<form id="composer"');
  const block = CHAT_PANEL.indexOf('<div id="todos" class="todos" hidden');
  assert.ok(block > form, 'the checklist is below the composer');
  assert.match(CHAT_JS, /const todoBlock = document\.getElementById\('todos'\)/);
  assert.match(CHAT_CSS, /\.todos\s*\{/);
});

test('a todo event replaces the list; an empty one removes the block', () => {
  assert.match(CHAT_JS, /e\.type === 'todo'\) setTodos\(e\.items\)/);
  const set = bodyOf(CHAT_JS, 'setTodos');
  // Replace, not append: every call carries the whole list, and appending would
  // leave the previous pass on screen above the current one.
  assert.match(set, /todoBlock\.textContent = ''/);
  assert.match(set, /todoBlock\.hidden = true/);
  assert.match(set, /todoBlock\.childNodes\.length === 0/);
  // A new conversation is a new checklist: the block is cleared with the
  // transcript rather than surviving into a session it does not describe.
  assert.match(bodyOf(CHAT_JS, 'clearTranscript'), /setTodos\(\[\]\)/);
});

test('the rows are text, and every status has a mark', () => {
  const set = bodyOf(CHAT_JS, 'setTodos');
  assert.doesNotMatch(set, /\.innerHTML\s*(\+)?=(?!=)/);
  assert.doesNotMatch(set, /insertAdjacentHTML\s*\(/);
  assert.match(set, /el\('span', 'todo-text', text\)/);
  assert.match(CHAT_JS, /todoMarks = \{[^}]*pending[^}]*in_progress[^}]*done[^}]*\}/);
  // Done rows are struck through in CSS, so the plan keeps what was finished
  // visible instead of silently dropping it.
  assert.match(CHAT_CSS, /\.todo-row\[data-status='done'\] \.todo-text\s*\{[^}]*line-through/);
});

test('the protocol carries the event and its items', () => {
  // The union member ends the declaration; a comment inside it may carry a
  // semicolon of its own, so the whole type is not sliced at the first one.
  assert.match(PROTOCOL, /\|\s*'todo';/);
  const hostEvent = PROTOCOL.slice(PROTOCOL.indexOf('export interface HostEvent'));
  assert.match(hostEvent.slice(0, hostEvent.indexOf('\n}')), /items\?: TodoItem\[\]/);
  assert.match(PROTOCOL, /export interface TodoItem \{/);
});
