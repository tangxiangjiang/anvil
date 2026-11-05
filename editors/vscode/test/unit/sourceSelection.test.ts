import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const PACKAGE = JSON.parse(readFileSync(join(ROOT, 'package.json'), 'utf8'));
const EXTENSION = readFileSync(join(ROOT, 'src', 'extension.ts'), 'utf8');
const HUB = readFileSync(join(ROOT, 'src', 'hub.ts'), 'utf8');
const PANEL = readFileSync(join(ROOT, 'src', 'chatPanel.ts'), 'utf8');
const CONTEXT = readFileSync(join(ROOT, 'src', 'editorContext.ts'), 'utf8');
const CHAT = readFileSync(join(ROOT, 'media', 'chat.js'), 'utf8');

test('source selections use an Anvil shortcut without competing for the editor context menu', () => {
  const command = PACKAGE.contributes.commands.find(
    (candidate: { command: string }) => candidate.command === 'anvil.addSelectionToChat',
  );
  assert.equal(command?.title, 'Anvil: Attach Selected Code');

  const binding = PACKAGE.contributes.keybindings.find(
    (candidate: { command: string }) => candidate.command === 'anvil.addSelectionToChat',
  );
  assert.equal(binding?.key, 'ctrl+alt+l');
  assert.equal(binding?.mac, 'cmd+alt+l');
  assert.match(binding?.when ?? '', /editorTextFocus/);
  assert.match(binding?.when ?? '', /editorHasSelection/);

  assert.equal(PACKAGE.contributes.menus['editor/context'], undefined);
});

test('the command refuses an empty selection before choosing or creating a chat', () => {
  const registration = /registerCommand\('anvil\.addSelectionToChat'[\s\S]*?\n {4}\}\),/.exec(EXTENSION);
  assert.ok(registration);
  assert.match(registration[0], /const selection = currentSelection\(\)/);
  assert.match(registration[0], /if \(!selection\)/);
  assert.match(registration[0], /hub\.addSelectionToChat\(selection\)/);
  assert.ok(
    registration[0].indexOf('if (!selection)') < registration[0].indexOf('hub.addSelectionToChat'),
    'selection validation happens before a chat is selected',
  );
});

test('the most recently active chat receives an explicitly attached source snapshot', () => {
  assert.match(HUB, /private lastActivePanel: ChatPanel \| undefined/);
  assert.match(HUB, /this\.activePanel\(\) \?\? this\.lastActivePanel \?\? \[\.\.\.this\.panels\]\[0\] \?\? this\.openTab\(\)/);
  assert.match(PANEL, /pendingSourceSelection = \{\s*\.\.\.selection,\s*range: \{ \.\.\.selection\.range \}/);
  assert.match(PANEL, /selection,\s*model: this\.overrides\.model/);
  assert.match(PANEL, /buildPromptContext\(dedupeMentions\(mentions\), quotes, selection\)/);
  assert.match(CONTEXT, /const selection = explicitSelection \?\? undefined/);
  assert.doesNotMatch(CONTEXT, /explicitSelection === undefined \? currentSelection\(\)/);
  assert.match(CONTEXT, /let lastSourceEditor: vscode\.TextEditor \| undefined/);
  assert.match(EXTENSION, /onDidChangeTextEditorSelection\(\(event\) => rememberSourceEditor\(event\.textEditor\)\)/);
});

test('the composer displays, removes, and clears the submitted source reference', () => {
  assert.match(CHAT, /let sourceSelection = null/);
  assert.match(CHAT, /if \(sourceSelection\) chipsBar\.appendChild\(sourceSelectionChip\(sourceSelection\)\)/);
  assert.match(CHAT, /vscode\.postMessage\(\{ type: 'clearSourceSelection' \}\)/);
  assert.match(CHAT, /const sentSelection = sourceSelection/);
  assert.match(CHAT, /sourceSelection = null;\s*closePalette\(\);\s*renderChips\(\)/);
  assert.match(CHAT, /addUser\(text, sentMentions, sentImages, sentQuotes, undefined, sentSelection\)/);
  assert.match(PANEL, /case 'clearSourceSelection':\s*this\.pendingSourceSelection = undefined/);
  assert.match(PANEL, /this\.sourceSelectionTouched = true/);
});

test('typing @attach selected turns the current editor selection into a chip', () => {
  assert.match(CHAT, /function referencePaletteQuery\(\)/);
  assert.match(CHAT, /name: 'Attach selected code'/);
  assert.match(CHAT, /vscode\.postMessage\(\{ type: 'attachSourceSelection' \}\)/);
  assert.match(PANEL, /case 'attachSourceSelection': \{/);
  assert.match(PANEL, /const selection = currentSelection\(\)/);
  assert.match(PANEL, /this\.addSourceSelection\(selection\)/);
});

test('new chats open beside source code and stay together when started from Anvil', () => {
  assert.match(PANEL, /activeChat\.viewColumn\(\) \?\? vscode\.ViewColumn\.Beside/);
  assert.match(PANEL, /vscode\.window\.activeTextEditor\s*\?\s*vscode\.ViewColumn\.Beside\s*:\s*vscode\.ViewColumn\.Active/);
  assert.match(PANEL, /createWebviewPanel\(\s*ChatPanel\.viewType,\s*'Anvil',\s*targetColumn/);
});
