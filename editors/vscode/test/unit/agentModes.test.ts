import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const JS = readFileSync(join(ROOT, 'media', 'chat.js'), 'utf8');
const PANEL = readFileSync(join(ROOT, 'src', 'chatPanel.ts'), 'utf8');

test('collaboration selector offers Single and Multi while Complex stays disabled', () => {
  assert.match(PANEL, /data-collaboration="single"/);
  assert.match(PANEL, /data-collaboration="multi"/);
  assert.match(
    PANEL,
    /data-collaboration="complex"[^>]*aria-disabled="true"[^>]*disabled/,
  );
  assert.match(JS, /vscode\.postMessage\(\{ type: 'collaboration', mode:/);
  assert.match(PANEL, /applyOptions\(\{ subagents: mode === 'multi' \}\)/);
});

test('collaboration mode cannot switch while a run is active', () => {
  assert.match(PANEL, /if \(this\.session\?\.currentState === 'running'\) \{/);
  assert.match(JS, /collaborationButton\.disabled = running/);
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
  assert.match(JS, /collaborationMode === 'multi' && activeSubagents\.size > 0/);
  assert.match(JS, /task\.kind === 'agent' && task\.status === 'running'/);
  assert.match(JS, /activeSubagents\.delete\('spawn:' \+ event\.tool_call_id\)/);
  assert.match(JS, /completedSubagentSpawns\.add\(event\.tool_call_id\)/);
  assert.match(JS, /!completedSubagentSpawns\.has\(e\.parent_tool_call_id\)/);
  assert.match(JS, /collaborationDot\.classList\.toggle\('active', working\)/);
  assert.match(JS, /collaborationButton\.getBoundingClientRect\(\)/);
  assert.match(JS, /window\.innerHeight - trigger\.top \+ 6/);
});
