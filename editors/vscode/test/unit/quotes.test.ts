import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_CSS = readFileSync(join(ROOT, 'media', 'chat.css'), 'utf8');
const PANEL = readFileSync(join(ROOT, 'src', 'chatPanel.ts'), 'utf8');
const CONTEXT = readFileSync(join(ROOT, 'src', 'editorContext.ts'), 'utf8');
const PROTOCOL = readFileSync(join(ROOT, 'src', 'protocol.ts'), 'utf8');

test('user messages and completed assistant replies offer the same quote action', () => {
  assert.match(CHAT_JS, /who\.appendChild\(quoteAction\(candidate, 'Quote this message'\)\)/);
  assert.match(CHAT_JS, /state\.head\.appendChild\(quoteAction\(state\.quote, 'Quote this reply'\)\)/);
  assert.match(CHAT_JS, /event\.stopPropagation\(\)/);
  assert.match(CHAT_CSS, /\.message-quote \{/);
  assert.doesNotMatch(CHAT_JS, /\.innerHTML\s*=/);
});

test('the add menu and ref command open one conversation picker', () => {
  assert.match(PANEL, /id="quote"[\s\S]*?Reference conversation[\s\S]*?\/ref/);
  assert.match(CHAT_JS, /quoteButton\.addEventListener\('click', openQuotePicker\)/);
  assert.match(CHAT_JS, /function quotePaletteQuery\(\)/);
  assert.match(CHAT_JS, /\/ref\[ \\t\]\+/);
  assert.match(CHAT_JS, /quoteCandidates[\s\S]*?\.reverse\(\)/);
  assert.match(CHAT_JS, /command\.kind === 'quote'/);
});

test('selected quotes are removable chips and travel as prompt context', () => {
  assert.match(CHAT_JS, /function quoteChip\(quote\)/);
  assert.match(CHAT_JS, /Stop quoting this message/);
  assert.match(CHAT_JS, /quotes: sentQuotes\.map/);
  assert.match(PANEL, /msg\.quotes \?\? \[\]/);
  assert.match(CONTEXT, /context\.quotes = quotes/);
  assert.match(PROTOCOL, /quotes\?: Quote\[\]/);
  assert.match(CHAT_JS, /'↳ ' \+ \(quote\.role === 'user' \? 'you' : 'anvil'\)/);
});

test('a transcript selection is split by narrative region and speaker', () => {
  assert.match(CHAT_JS, /body\.dataset\.quoteRole = 'user'/);
  assert.match(CHAT_JS, /body\.dataset\.quoteRole = 'assistant'/);
  assert.match(CHAT_JS, /function selectedTextWithin\(range, node\)/);
  assert.match(CHAT_JS, /transcript\.querySelectorAll\('\[data-quote-role\]'\)/);
  assert.match(CHAT_JS, /role: node\.dataset\.quoteRole === 'user' \? 'user' : 'assistant'/);
  assert.match(CHAT_JS, /for \(const part of selected\)[\s\S]*?quotes\.push/);
});

test('selection quoting has a floating action, context menu, and shortcut', () => {
  assert.match(CHAT_JS, /el\('button', 'selection-quote'\)/);
  assert.match(CHAT_JS, /Quote selection \(Ctrl\+Shift\+Q\)/);
  assert.match(CHAT_JS, /transcript\.addEventListener\('contextmenu'/);
  assert.match(CHAT_JS, /el\('div', 'selection-context-menu'\)/);
  assert.match(CHAT_JS, /'Quote selection'/);
  assert.match(CHAT_JS, /event\.ctrlKey[\s\S]*?event\.shiftKey[\s\S]*?event\.key\.toLowerCase\(\) !== 'q'/);
  assert.match(CHAT_CSS, /\.selection-quote\s*\{/);
  assert.match(CHAT_CSS, /\.selection-context-menu\s*\{/);
});

test('selection context copy uses the existing editor clipboard path', () => {
  assert.match(CHAT_JS, /type: 'copy', text: selectedCopyText/);
  assert.match(PANEL, /case 'copy':[\s\S]*?this\.copy\(msg\.text\)/);
  // Paste remains owned by the composer image path; selected text never hooks it.
  assert.match(CHAT_JS, /input\.addEventListener\('paste'[\s\S]*?addImage\(dataUrl\)/);
});
