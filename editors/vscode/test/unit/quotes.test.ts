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
