import { strict as assert } from 'node:assert';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_PANEL = readFileSync(join(ROOT, 'src', 'chatPanel.ts'), 'utf8');
const PACKAGE_JSON = readFileSync(join(ROOT, 'package.json'), 'utf8');

test('closed code blocks use Shiki tokens without inserting markup', () => {
  assert.match(CHAT_JS, /fillCode\(code, block\.text \|\| '', block\.lang \|\| '', block\.closed === true\)/);
  assert.match(CHAT_JS, /function fillCode\(code, text, language, closed\)/);
  assert.match(CHAT_JS, /document\.createTextNode/);
  assert.match(CHAT_JS, /el\('span', 'shiki-token', token\.content\)/);
  assert.match(CHAT_JS, /highlighter\.codeToTokens\(text, \{ lang, theme \}\)/);
  assert.doesNotMatch(CHAT_JS, /\.innerHTML\s*=/);
});

test('streaming stays plain and stale asynchronous highlights are discarded', () => {
  const start = CHAT_JS.indexOf('function fillCode(');
  const body = CHAT_JS.slice(start, CHAT_JS.indexOf('\n  function ', start + 1));
  assert.match(body, /setText\(code, text\);\s*if \(!closed\) return/);
  assert.match(body, /highlightClosedCode\(code, text, language, generation\)\.catch/);
  assert.match(CHAT_JS, /code\.dataset\.highlightGeneration !== generation/);
  assert.match(CHAT_JS, /if \(!code\.isConnected/);
});

test('the offline Shiki bundle carries VS Code themes and common language aliases', () => {
  assert.match(CHAT_PANEL, /vendor\/shiki\/shiki\.min\.js/);
  assert.match(CHAT_PANEL, /script-src 'nonce-\$\{nonce\}' 'wasm-unsafe-eval'/);
  assert.match(CHAT_JS, /themes: \['dark-plus', 'light-plus'\]/);
  assert.match(CHAT_JS, /cpp: 'cpp'/);
  assert.match(CHAT_JS, /typescript: 'typescript'/);
  assert.match(CHAT_JS, /python: 'python'/);
  assert.match(CHAT_JS, /vscode-high-contrast-light/);
  assert.match(PACKAGE_JSON, /"vendor:shiki": "node scripts\/vendor-shiki\.mjs"/);
  for (const file of ['shiki.min.js', 'LICENSE', 'VERSION']) {
    assert.ok(existsSync(join(ROOT, 'media', 'vendor', 'shiki', file)), `${file} is vendored`);
  }
});

test('an unlabelled fence conservatively recognizes common code, including C++ interfaces', () => {
  assert.match(CHAT_JS, /function inferShikiLanguage\(text\)/);
  assert.match(CHAT_JS, /\\benum\\s\+class\\b\|\\bvirtual\\b/);
  assert.match(CHAT_JS, /return 'cpp'/);
  assert.match(
    CHAT_JS,
    /requested === '' \|\| requested === 'text' \|\| requested === 'plaintext' \|\| requested === 'plain'/,
  );
  assert.match(CHAT_JS, /resolveShikiLanguage\(language, text\)/);
});
