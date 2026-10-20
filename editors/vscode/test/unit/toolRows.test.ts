/**
 * A tool row's sentence: what the call is called, what it touched, and what a
 * reader can do with what it touched.
 *
 * The renderer cannot be run here — there is no DOM and no extension host — so
 * what is checkable is the agreement between the two halves of every cross-file
 * promise: a title map that never reaches the row, a `fileLink` that posts a
 * message no handler routes, or an `openPath` that resolves a path the webview
 * only drew. Each of those fails silently in a real window, which is why each
 * is stated here as source.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_CSS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.css'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');
const EXTENSION = readFileSync(join(EXTENSION_ROOT, 'src', 'extension.ts'), 'utf8');
const HUB = readFileSync(join(EXTENSION_ROOT, 'src', 'hub.ts'), 'utf8');

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

test('a tool is called by the name a reader knows', () => {
  const titles = /const TOOL_TITLES = \{([\s\S]*?)\r?\n {2}\};/.exec(CHAT_JS);
  assert.ok(titles, 'the title map is in chat.js');
  assert.match(titles[1], /read_file:\s*'Read'/);
  assert.match(titles[1], /edit_file:\s*'Edit'/);
  assert.match(titles[1], /write_file:\s*'Write'/);
  assert.match(titles[1], /shell:\s*'Bash'/);
  assert.match(titles[1], /grep:\s*'Grep'/);
  assert.match(titles[1], /spawn_agent:\s*'Agent'/);

  const title = bodyOf(CHAT_JS, 'toolTitle');
  assert.match(title, /if \(TOOL_TITLES\[name\]\) return TOOL_TITLES\[name\]/);
  // A tool the map has never seen is de-slugged rather than shown raw.
  assert.match(title, /name\.replace\(\/_\/g, ' '\)/);
  assert.match(title, /words\.charAt\(0\)\.toUpperCase\(\) \+ words\.slice\(1\)/);

  // The row and the approval card both go through it, so neither surface shows
  // the SDK's identifier as a title.
  const call = bodyOf(CHAT_JS, 'addToolCall');
  assert.match(call, /el\('span', 'name', toolTitle\(event\.name\)\)/);
  assert.doesNotMatch(call, /event\.name \|\| 'tool'/);
  assert.match(CHAT_JS, /el\('span', 'tool-name', toolTitle\(request\.tool\)\)/);
  assert.match(CHAT_JS, /announceAlert\('Approval needed: ' \+ toolTitle\(request\.tool\)/);
});

test('a read names its file and the lines it used', () => {
  const detail = bodyOf(CHAT_JS, 'toolDetail');
  assert.match(detail, /case 'read_file'/);
  // offset is 1-based and limit counts lines: the host's reading, not a guess.
  assert.match(detail, /numberArg\(args, 'offset'\)/);
  assert.match(detail, /numberArg\(args, 'limit'\)/);
  assert.match(detail, /offset \+ limit - 1/);
  assert.match(detail, /kind: 'meta', text: `\(lines \$\{offset\}-\$\{offset \+ limit - 1\}\)`/);
  assert.match(detail, /kind: 'meta', text: `\(from line \$\{offset\}\)`/);
  assert.match(detail, /kind: 'meta', text: `\(first \$\{limit\} lines\)`/);

  // The other arguments that name what a call touched.
  assert.match(detail, /case 'write_file'/);
  assert.match(detail, /case 'edit_file'/);
  assert.match(detail, /case 'grep'/);
  assert.match(detail, /textArg\(args, 'pattern'\)/);
  assert.match(detail, /case 'shell'/);
  assert.match(detail, /textArg\(args, 'command'\)/);
  assert.match(detail, /case 'apply_patch'/);
  assert.ok(detail.includes('\\*\\*\\* (?:Update|Add|Delete) File:'));

  // A call whose arguments say nothing falls back to the old one-line hint,
  // rather than to an empty row.
  assert.match(detail, /const fallback = callHint\(name, args\)/);

  const call = bodyOf(CHAT_JS, 'addToolCall');
  assert.match(call, /for \(const part of toolDetail\(event\.name, event\.args\)\)/);
  assert.match(call, /hint\.appendChild\(fileLink\(part\.text, part\.line\)\)/);
  assert.match(call, /hint\.appendChild\(el\('span', part\.kind, part\.text\)\)/);
});

test('the path is a link, and the extension resolves it under the root', () => {
  const link = bodyOf(CHAT_JS, 'fileLink');
  // A span wearing a link's role, never an anchor: a real `href` would let a
  // middle click navigate the webview away from the conversation.
  assert.match(link, /setAttribute\('role', 'link'\)/);
  assert.match(link, /link\.tabIndex = 0/);
  assert.doesNotMatch(link, /\.href\s*=/);
  // Following the path must not also toggle the `<details>` it lives in.
  assert.match(link, /event\.preventDefault\(\)/);
  assert.match(link, /event\.stopPropagation\(\)/);
  assert.match(link, /type: 'openPath', path/);
  // The keyboard reaches the same path as the mouse.
  assert.match(link, /event\.key === 'Enter' \|\| event\.key === ' '/);

  assert.match(CHAT_PANEL, /case 'openPath':/);
  const open = methodOf(CHAT_PANEL, 'private async openPath(');
  // A relative path is joined to the chosen root; an absolute one is accepted
  // only when it is already inside it. Neither `..` nor another drive passes.
  assert.match(open, /isAbsolute\(trimmed\)/);
  assert.match(open, /this\.fileUri\(trimmed\.replace\(\/\\\\\/g, '\/'\)\)/);
  assert.match(open, /relative\(root\.fsPath, uri\.fsPath\)/);
  assert.match(open, /inside\.startsWith\('\.\.'\)/);
  assert.match(open, /openTextDocument\(uri\)/);
  assert.match(open, /showTextDocument\(document/);
  assert.match(open, /new vscode\.Range\(first - 1, 0, first - 1, 0\)/);
});

test('an edit says how much changed above the comparison', () => {
  const note = bodyOf(CHAT_JS, 'changeNote');
  assert.match(note, /count === 1 \? '1 line' : count \+ ' lines'/);
  assert.match(note, /'Added ' \+ lines\(adds\)/);
  assert.match(note, /'removed ' : 'Removed '/);

  const diff = bodyOf(CHAT_JS, 'renderEditDiff');
  assert.match(diff, /el\('div', 'note', changeNote\(adds, dels\)\)/);
  // The path moved into the row, where a reader looks first; the diff keeps
  // the counts and loses the duplicate heading.
  assert.doesNotMatch(diff, /el\('span', 'file'/);
  assert.match(CHAT_CSS, /\.tool \.edit-diff \.note \{/);
  assert.doesNotMatch(CHAT_CSS, /\.tool \.edit-diff \.path/);
});

test('the operation is the loud part of the row', () => {
  assert.match(
    CHAT_CSS,
    /\.tool \.name \{[^}]*font-weight:\s*600[^}]*color:\s*var\(--vscode-foreground\)/,
  );
  assert.match(CHAT_CSS, /\.tool > summary \{[^}]*font-size:\s*1em/);
  // The path is the link colour; the range beside it is muted and does not
  // shrink away with the file.
  assert.match(CHAT_CSS, /\.tool \.hint \.file \{[^}]*--vscode-textLink-foreground/);
  assert.match(CHAT_CSS, /\.tool \.hint \.meta \{[^}]*flex:\s*0 0 auto/);
});

test('a result offers its whole output in a tab', () => {
  // The caption counts lines, not characters: "3 lines of output" is how a
  // reader decides whether to open it. A trailing newline is not a line.
  const count = bodyOf(CHAT_JS, 'lineCount');
  assert.match(count, /replace\(\/\\r\?\\n\+\$\/, ''\)/);
  assert.match(count, /split\(\/\\r\?\\n\/\)\.length/);

  const link = bodyOf(CHAT_JS, 'addOutputLink');
  assert.match(link, /const count = lineCount\(content\)/);
  assert.match(link, /count === 1 \? '1 line of output' : count \+ ' lines of output'/);
  // It is a sibling of the details, not a child: a folded row hides its
  // children, and this caption has to be readable while the row is closed.
  assert.match(link, /card\.parentNode\.insertBefore\(note, card\.nextSibling\)/);
  assert.match(link, /type: 'openOutput'/);
  assert.match(link, /id: event\.tool_call_id/);
  assert.match(link, /text: content/);
  assert.match(link, /syncRail\(card\.parentElement\)/);

  assert.match(bodyOf(CHAT_JS, 'addToolResult'), /addOutputLink\(card, event, content\)/);

  const note = /\.output-link \{\r?\n {2}display: block;([\s\S]*?)\}/.exec(CHAT_CSS);
  assert.ok(note, 'the caption has a rule');
  assert.match(note[1], /font-size:\s*0\.85em/);
  assert.match(note[1], /color:\s*var\(--ui-muted\)/);
  assert.match(note[1], /padding:\s*2px 6px 2px 22px/);
  // It belongs to the reply's rail, so the line does not break around it.
  assert.match(CHAT_CSS, /\.output-link\.rail::after/);

  // The extension opens a document the webview cannot: the provider is created
  // once for the window and reaches every tab through the hub.
  assert.match(CHAT_PANEL, /case 'openOutput':/);
  const open = methodOf(CHAT_PANEL, 'private async openOutput(');
  assert.match(open, /typeof text !== 'string' \|\| text === ''/);
  assert.match(open, /this\.outputs\.open\(tool, call, text\)/);
  assert.match(EXTENSION, /new ToolOutputDocuments\(\)/);
  assert.match(EXTENSION, /context\.subscriptions\.push\(outputs\)/);
  assert.match(EXTENSION, /new Hub\(context, log, previews, plans, outputs\)/);
  assert.match(HUB, /private readonly outputs: ToolOutputDocuments/);
  assert.match(
    HUB,
    /new ChatPanel\(this\.context, this\.log, this\.previews, this\.plans, this\.outputs, this, agentId, focus\)/,
  );
});
