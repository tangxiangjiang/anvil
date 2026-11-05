import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const PACKAGE = JSON.parse(readFileSync(join(ROOT, 'package.json'), 'utf8'));
const EXTENSION = readFileSync(join(ROOT, 'src', 'extension.ts'), 'utf8');
const SIDEBAR = readFileSync(join(ROOT, 'src', 'chatSidebar.ts'), 'utf8');
const PANEL = readFileSync(join(ROOT, 'src', 'chatPanel.ts'), 'utf8');
const HUB = readFileSync(join(ROOT, 'src', 'hub.ts'), 'utf8');
const CHAT = readFileSync(join(ROOT, 'media', 'chat.js'), 'utf8');

test('Anvil contributes a native Secondary Side Bar chat peer', () => {
  const container = PACKAGE.contributes.viewsContainers.secondarySidebar.find(
    (candidate: { id: string }) => candidate.id === 'anvil-chat',
  );
  assert.equal(container?.title, 'Anvil');
  const view = PACKAGE.contributes.views['anvil-chat'].find(
    (candidate: { id: string }) => candidate.id === 'anvil.chatView',
  );
  assert.equal(view?.type, 'webview');
  assert.equal(view?.name, 'Chat');
  assert.ok(PACKAGE.activationEvents.includes('onView:anvil.chatView'));
});

test('the side-bar provider reuses the full chat document and restores its agent', () => {
  assert.match(EXTENSION, /registerWebviewViewProvider\(ChatSidebarProvider\.viewType, chatSidebar/);
  assert.match(EXTENSION, /retainContextWhenHidden: true/);
  assert.match(SIDEBAR, /context\.state\?\.agentId/);
  assert.match(SIDEBAR, /panel\.adoptView\(view\)/);
  assert.match(PANEL, /adoptView\(view: vscode\.WebviewView\)/);
  assert.match(PANEL, /this\.configureWebview\(view\.webview\)/);
});

test('session activation prefers the idle side-bar surface without duplicating an agent', () => {
  assert.match(HUB, /registerSidebarPanel\(panel: ChatPanel\)/);
  assert.match(HUB, /if \(this\.sidebarPanel && !this\.sidebarPanel\.busy\)/);
  assert.match(HUB, /await this\.sidebarPanel\.openAgent\(agentId\)/);
  assert.match(PANEL, /const existing = this\.hub\.findByAgent\(agentId\)/);
  assert.match(PANEL, /existing\.reveal\(\)/);
});

test('webview focus, not mere side-bar visibility, selects the command target', () => {
  assert.match(CHAT, /type: 'surfaceFocus'/);
  assert.match(CHAT, /type: 'surfaceBlur'/);
  assert.match(PANEL, /case 'surfaceFocus':/);
  assert.match(PANEL, /case 'surfaceBlur':/);
});
