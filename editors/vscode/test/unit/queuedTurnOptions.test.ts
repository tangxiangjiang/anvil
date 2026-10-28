import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const CHAT = readFileSync(join(ROOT, 'src', 'chatPanel.ts'), 'utf8');
const CHAT_JS = readFileSync(join(ROOT, 'media', 'chat.js'), 'utf8');

test('queued turns capture and apply their model, effort, and permission mode', () => {
  assert.match(CHAT, /interface QueuedTurn[\s\S]*?model: string;[\s\S]*?effort: Effort;[\s\S]*?permissionMode: PermissionMode \| null;/);
  assert.match(CHAT, /model: this\.overrides\.model \?\? ''/);
  assert.match(CHAT, /effort: this\.overrides\.effort \?\? 'medium'/);
  assert.match(CHAT, /permissionMode: this\.mode \?\? null/);
  assert.match(CHAT, /private async applyTurnOptions\(turn: QueuedTurn\)/);
  assert.match(CHAT, /session\.reopen\(\{ model, effort: turn\.effort, permission_mode: permissionMode \}\)/);
  assert.match(CHAT, /await this\.applyTurnOptions\(turn\)/);
});

test('model, effort, and permission mode stay enabled while a turn runs', () => {
  assert.match(CHAT_JS, /if \(modeButton\) modeButton\.disabled = false/);
  assert.match(CHAT_JS, /if \(modelPicker\) modelPicker\.disabled = false/);
  assert.doesNotMatch(CHAT_JS, /modeButton\.disabled = running/);
  assert.doesNotMatch(CHAT_JS, /modelPicker\.disabled = running/);
});
