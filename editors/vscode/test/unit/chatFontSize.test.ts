/**
 * The chat's text size, agreed between the setting, the panel and the document.
 *
 * The size is a display-only setting with three links that have to agree and
 * one that is easy to get wrong: `package.json` declares it, `settings.ts`
 * clamps it, `chatPanel.ts` renders it onto the body and re-announces it, and
 * the window's configuration listener has to treat it as *not* a reason to
 * restart the host. A restart here would end a run in progress to change a
 * number of pixels, and nothing in a type check would notice.
 *
 * There is no browser in this suite, so this is the agreement between files;
 * the visual pass at both ends of the range is recorded in the phase document.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const read = (...parts: string[]): string => readFileSync(join(ROOT, ...parts), 'utf8');

const PACKAGE = JSON.parse(read('package.json')) as {
  contributes: { configuration: { properties: Record<string, Record<string, unknown>> } };
};
const SETTINGS = read('src', 'settings.ts');
const PANEL = read('src', 'chatPanel.ts');
const HUB = read('src', 'hub.ts');
const EXTENSION = read('src', 'extension.ts');
const CHAT_JS = read('media', 'chat.js');

test('the size is a setting with a range and a default two larger than the UI font', () => {
  const setting = PACKAGE.contributes.configuration.properties['anvil.chatFontSize'];
  assert.ok(setting, 'anvil.chatFontSize is declared');
  assert.equal(setting.type, 'number');
  assert.equal(setting.default, 15);
  assert.equal(setting.minimum, 10);
  assert.equal(setting.maximum, 24);
});

test('the reader clamps the number before it becomes pixels', () => {
  assert.match(SETTINGS, /cfg\.get<number>\('chatFontSize', 15\)/);
  assert.match(SETTINGS, /Math\.min\(24, Math\.max\(10, Math\.round\(fontSizeRaw\)\)\)/);
  // Every resolved reading carries it, so the panel has one source for the
  // value it renders and the value it posts.
  assert.match(SETTINGS, /fontSize: number;/);
  assert.match(SETTINGS, /\r?\n\s*fontSize,\r?\n/);
});

test('the setting is excluded from the restart comparison', () => {
  // `settingsKey` is the host-relevant half of a reading. A display setting
  // that stayed in the key would make every size change look like a session
  // change and restart the host.
  assert.match(SETTINGS, /export function settingsKey\(settings: ResolvedSettings\): string/);
  assert.match(SETTINGS, /delete hostRelevant\.fontSize;/);
  assert.match(EXTENSION, /settingsKey\(previous\) !== settingsKey\(next\)/);
  assert.match(EXTENSION, /hub\.setFontSize\(next\.fontSize\)/);
});

test('a live change reaches every open document', () => {
  // The panel posts the message; the hub broadcasts it; a document that has
  // not started listening queues it like every other post.
  assert.match(PANEL, /\{ type: 'fontSize'; size: number \}/);
  assert.match(PANEL, /setFontSize\(size: number\): void/);
  assert.match(HUB, /setFontSize\(size: number\): void/);
  assert.match(HUB, /for \(const panel of this\.panels\) panel\.setFontSize\(size\)/);
  // First paint and reload: the value is on the body and re-sent at `ready`.
  assert.match(PANEL, /data-font-size="\$\{fontSize\}"/);
  assert.match(PANEL, /renderHtml\(\s*panel\.webview,\s*this\.context\.extensionUri,\s*this\.focusView,\s*readSettings\(\)\.fontSize,?\s*\)/);
  assert.match(PANEL, /this\.post\(\{ type: 'fontSize', size: readSettings\(\)\.fontSize \}\)/);
  // The document applies it and ignores anything outside the range.
  assert.match(CHAT_JS, /case 'fontSize':/);
  assert.match(CHAT_JS, /px < 10 \|\| px > 24/);
});

test('an asset URL carries a content digest, so a reinstall cannot be served from cache', () => {
  // `asWebviewUri` builds the same URL for the same path whatever the file now
  // holds. A reinstall that changes `media/chat.js` without changing the version
  // therefore left the webview fetching a cached copy, and a shipped fix
  // installed and appeared to do nothing (p_0.46.26). One builder serves all
  // five documents, so a panel cannot keep the stale-cache behaviour.
  const ASSET = read('src', 'webviewAsset.ts');
  for (const file of ['chatPanel.ts', 'mcpPanel.ts', 'permissionsPanel.ts', 'reviewPanel.ts', 'tasksPanel.ts']) {
    assert.match(read('src', file), /const asset = mediaAsset\(webview, extensionUri\);/);
  }
  assert.match(ASSET, /createHash\('sha256'\)/);
  assert.match(ASSET, /readFileSync\(file\.fsPath\)/);
  assert.match(ASSET, /uri\.with\(\{ query: `v=\$\{digest\}` \}\)/);
  // A file that cannot be read still yields a URL rather than failing the panel.
  assert.match(ASSET, /return uri;/);
});
