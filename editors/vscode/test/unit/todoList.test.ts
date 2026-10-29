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

test('the collapsible block exists above the composer', () => {
  const form = CHAT_PANEL.indexOf('<form id="composer"');
  const block = CHAT_PANEL.indexOf('<div id="todos" class="todos collapsed" hidden');
  assert.ok(block >= 0 && block < form, 'the checklist is above the composer');
  assert.match(CHAT_JS, /const todoBlock = document\.getElementById\('todos'\)/);
  assert.match(CHAT_JS, /const todoList = document\.getElementById\('todoList'\)/);
  assert.match(CHAT_CSS, /\.todos\s*\{/);
  assert.match(CHAT_PANEL, /id="todosToggle"[^>]*aria-expanded="false"[^>]*aria-controls="todosBody"/);
  assert.match(CHAT_PANEL, /id="todosBody"[\s\S]*?id="todoList"[\s\S]*?id="todoHistory"/);
  assert.match(CHAT_CSS, /\.todo-list\s*\{[^}]*max-height/);
  assert.match(CHAT_CSS, /\.todo-history-list\s*\{[^}]*max-height/);
});

test('a todo event replaces Current and a conversation reset clears history', () => {
  assert.match(CHAT_JS, /e\.type === 'todo'\) setTodos\(e\.items, e\.run_id\)/);
  const set = bodyOf(CHAT_JS, 'setTodos');
  assert.match(set, /drawTodoRows\(todoList, list\)/);
  // Clearing Current keeps the archive reachable when it has entries.
  assert.match(set, /todoBlock\.hidden = todoHistoryEntries\.length === 0/);
  assert.match(bodyOf(CHAT_JS, 'clearTranscript'), /clearTodoHistory\(\)/);
  assert.match(bodyOf(CHAT_JS, 'clearTodoHistory'), /todoHistoryEntries\.length = 0/);
});

test('distinct plans are bounded while status changes update one entry', () => {
  const set = bodyOf(CHAT_JS, 'setTodos');
  assert.match(set, /const signature = list\.map\(\(item\) => item\.text\)\.join\('\\n'\)/);
  assert.match(set, /const key = `\$\{owner\}\\n\$\{signature\}`/);
  assert.match(set, /if \(existing\) existing\.items = list/);
  assert.match(set, /todoHistoryEntries\.push\(\{ key, items: list \}\)/);
  assert.match(set, /todoHistoryEntries\.length > TODO_HISTORY_LIMIT/);
  const history = bodyOf(CHAT_JS, 'renderTodoHistory');
  assert.match(history, /\.filter\(\(\{ entry \}\) => entry\.key !== currentTodoKey\)/);
  assert.match(history, /\.reverse\(\)/);
});

test('collapse keeps the Plan header as the way back', () => {
  const collapse = bodyOf(CHAT_JS, 'setTodosCollapsed');
  assert.match(collapse, /classList\.toggle\('collapsed', collapsed\)/);
  assert.match(collapse, /todosBody\.hidden = collapsed/);
  assert.match(collapse, /setAttribute\('aria-expanded'/);
  assert.match(collapse, /setAttribute\('aria-label'/);
  assert.match(collapse, /todosToggle\.title = collapsed \? 'Expand plan' : 'Collapse plan'/);
  const toggle = /todosToggle\.addEventListener\('click'[\s\S]*?\n {4}\}\)/.exec(CHAT_JS);
  assert.ok(toggle, 'the plan toggle is wired');
  assert.match(toggle[0], /setTodosCollapsed\(!todoBlock\.classList\.contains\('collapsed'\)\)/);
  assert.doesNotMatch(toggle[0], /todoBlock\.hidden = true/);
});

test('startup and replay stay folded while a distinct live plan expands', () => {
  assert.match(CHAT_PANEL, /id="todos" class="todos collapsed" hidden/);
  assert.match(CHAT_PANEL, /id="todosBody" class="todos-body" hidden/);
  assert.match(bodyOf(CHAT_JS, 'clearTodoHistory'), /setTodosCollapsed\(true\)/);
  const set = bodyOf(CHAT_JS, 'setTodos');
  assert.match(set, /if \(existing\) existing\.items = list/);
  assert.match(set, /else \{[\s\S]*?if \(!replaying\) setTodosCollapsed\(false\)/);
});

test('the collapse control is a flat handle on the joined composer edge', () => {
  assert.doesNotMatch(CHAT_PANEL, /id="todosClose"|>×<\/button>/);
  assert.match(CHAT_PANEL, /id="todosToggle"[\s\S]*?<svg[\s\S]*?<\/button>/);
  assert.match(CHAT_CSS, /\.todos\s*\{[^}]*position:\s*relative[^}]*margin-bottom:\s*-1px[^}]*border-radius:\s*10px 10px 0 0/);
  assert.match(CHAT_CSS, /\.todos-toggle\s*\{[^}]*position:\s*absolute[^}]*bottom:\s*-7px[^}]*width:\s*44px[^}]*height:\s*14px/);
  assert.match(CHAT_CSS, /\.todos:not\(\[hidden\]\):not\(\.collapsed\) \+ \.composer\s*\{[^}]*border-top-left-radius:\s*0/);
  assert.match(CHAT_CSS, /\.todos\.collapsed \.todos-toggle svg\s*\{[^}]*rotate\(180deg\)/);
  assert.match(CHAT_CSS, /\.todos\.collapsed\s*\{[^}]*height:\s*0[^}]*border:\s*0/);
  assert.match(CHAT_CSS, /\.todos\.collapsed \.todos-head\s*\{[^}]*display:\s*none/);
});

test('the rows are text, and every status has a mark', () => {
  const draw = bodyOf(CHAT_JS, 'drawTodoRows');
  assert.doesNotMatch(draw, /\.innerHTML\s*(\+)?=(?!=)/);
  assert.doesNotMatch(draw, /insertAdjacentHTML\s*\(/);
  assert.match(draw, /el\('span', 'todo-text', item\.text\)/);
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
