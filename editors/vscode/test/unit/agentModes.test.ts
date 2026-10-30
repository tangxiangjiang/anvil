import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const JS = readFileSync(join(ROOT, 'media', 'chat.js'), 'utf8');
const PANEL = readFileSync(join(ROOT, 'src', 'chatPanel.ts'), 'utf8');

test('Agent mode selector offers all three SDK modes with concise cost descriptions', () => {
  assert.match(PANEL, /data-collaboration="single"/);
  assert.match(PANEL, /data-collaboration="multi"/);
  assert.match(PANEL, /data-collaboration="complex"/);
  assert.doesNotMatch(PANEL, /data-collaboration="complex"[^>]*disabled/);
  assert.match(PANEL, /Single<\/strong><small>One Agent works directly · lowest cost/);
  assert.match(PANEL, /Multi<\/strong><small>Main Agent delegates focused work · higher cost/);
  assert.match(PANEL, /Complex<\/strong><small>Decision → Analyze → Workers · highest cost/);
  assert.match(JS, /vscode\.postMessage\(\{ type: 'collaboration', mode:/);
  assert.match(PANEL, /agentModeOptions\(mode\)/);
});

test('a mode change during a run is deferred to the next turn', () => {
  assert.match(PANEL, /if \(this\.session\?\.currentState === 'running'\) \{/);
  assert.match(PANEL, /will apply to the next turn; the running turn is unchanged/);
  assert.match(PANEL, /agentMode: this\.collaboration/);
  assert.match(PANEL, /current\.agent_mode === agentOptions\.agent_mode/);
  assert.match(JS, /collaborationButton\.disabled = false/);
});

test('Agent Map uses a modal and loads child transcript detail lazily', () => {
  assert.match(JS, /el\('dialog', 'agent-map-dialog'\)/);
  assert.match(JS, /vscode\.postMessage\(\{ type: 'agentMap' \}\)/);
  assert.match(JS, /vscode\.postMessage\(\{ type: 'agentDetail', agentId: node\.agentId \}\)/);
  assert.match(PANEL, /await this\.hub\.transcript\(agentId, 200\)/);
  assert.doesNotMatch(JS, /agentMap[\s\S]{0,1200}\.innerHTML\s*=/);
});

test('collaboration activity dot follows subagent lifecycle and menu anchors to its trigger', () => {
  assert.match(JS, /const activeSubagents = new Set\(\)/);
  assert.match(JS, /collaborationMode !== 'single' && activeSubagents\.size > 0/);
  assert.match(JS, /task\.kind === 'agent' && task\.status === 'running'/);
  assert.match(JS, /activeSubagents\.delete\('spawn:' \+ event\.tool_call_id\)/);
  assert.match(JS, /completedSubagentSpawns\.add\(event\.tool_call_id\)/);
  assert.match(JS, /!completedSubagentSpawns\.has\(e\.parent_tool_call_id\)/);
  assert.match(JS, /collaborationDot\.classList\.toggle\('active', working\)/);
  assert.match(JS, /collaborationButton\.getBoundingClientRect\(\)/);
  assert.match(JS, /window\.innerHeight - trigger\.top \+ 6/);
});

test('the current Agent mode is exposed to keyboard and screen readers', () => {
  assert.match(PANEL, /role="menuitemradio" data-collaboration="single"/);
  assert.match(PANEL, /role="menuitemradio" data-collaboration="multi"/);
  assert.match(PANEL, /role="menuitemradio" data-collaboration="complex"/);
  assert.match(JS, /row\.setAttribute\('aria-checked', selected \? 'true' : 'false'\)/);
  assert.match(JS, /setAttribute\('aria-label', 'Agent mode: ' \+ label\)/);
  assert.match(JS, /event\.key === 'ArrowDown'/);
  assert.match(JS, /event\.key === 'ArrowUp'/);
});
