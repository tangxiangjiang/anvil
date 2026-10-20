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
  assert.match(CHAT_JS, /const todoList = document\.getElementById\('todoList'\)/);
  assert.match(CHAT_CSS, /\.todos\s*\{/);
  // The head is outside the scrolling list: a close control that scrolls away
  // with a long plan is a control a reader has to hunt for.
  assert.match(CHAT_PANEL, /<div class="todos-head">[\s\S]*?id="todosClose"[\s\S]*?<\/div>\s*<div id="todoList"/);
  assert.match(CHAT_CSS, /\.todo-list\s*\{[^}]*max-height/);
});

test('a todo event replaces the list; an empty one removes the block', () => {
  assert.match(CHAT_JS, /e\.type === 'todo'\) setTodos\(e\.items\)/);
  const set = bodyOf(CHAT_JS, 'setTodos');
  // Replace, not append: every call carries the whole list, and appending would
  // leave the previous pass on screen above the current one.
  assert.match(set, /todoList\.textContent = ''/);
  assert.match(set, /todoBlock\.hidden = true/);
  assert.match(set, /todoList\.childNodes\.length === 0/);
  // A new conversation is a new checklist: the block is cleared with the
  // transcript rather than surviving into a session it does not describe.
  assert.match(bodyOf(CHAT_JS, 'clearTranscript'), /setTodos\(\[\]\)/);
});

test('a closed plan stays closed until the plan itself changes', () => {
  const set = bodyOf(CHAT_JS, 'setTodos');
  // The signature is the rows' text, not their statuses: the model moving a
  // row to done is the same plan being worked.
  assert.match(set, /const signature = list[\s\S]*?\.join\('\\n'\)/);
  assert.match(set, /todoSignature = signature/);
  assert.match(set, /signature === dismissedTodos && dismissedEpoch === todoEpoch/);
  // An empty list ends the plan, and the dismissal with it.
  assert.match(set, /dismissedTodos = ''/);
  // A later turn is a new plan even when the same words come back, so a
  // dismissal cannot outlive the turn it was made in.
  assert.match(CHAT_JS, /todoEpoch \+= 1;/);
  // Closing records the plan on screen and hides the block; the button that had
  // focus is gone, so focus goes to a control that still does something.
  const close = /todosClose\.addEventListener\('click'[\s\S]*?\n {4}\}\)/.exec(CHAT_JS);
  assert.ok(close, 'the close button is wired');
  assert.match(close[0], /dismissedTodos = todoSignature/);
  assert.match(close[0], /dismissedEpoch = todoEpoch/);
  assert.match(close[0], /todoBlock\.hidden = true/);
  assert.match(close[0], /sendButton\.focus\(\)/);
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
  // The union member is somewhere in the declaration, not necessarily last: a
  // later phase may append its own. A comment inside the type may carry a
  // semicolon of its own, so the whole type is sliced by its end, not a `;`.
  const eventType = PROTOCOL.slice(
    PROTOCOL.indexOf('export type EventType ='),
    PROTOCOL.indexOf('/** One streamed unit of a run. */'),
  );
  assert.match(eventType, /\|\s*'todo'/);
  const hostEvent = PROTOCOL.slice(PROTOCOL.indexOf('export interface HostEvent'));
  assert.match(hostEvent.slice(0, hostEvent.indexOf('\n}')), /items\?: TodoItem\[\]/);
  assert.match(PROTOCOL, /export interface TodoItem \{/);
});
