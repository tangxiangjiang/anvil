import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const HUB = readFileSync(join(ROOT, 'src', 'hub.ts'), 'utf8');
const CHAT = readFileSync(join(ROOT, 'src', 'chatPanel.ts'), 'utf8');
const EXTENSION = readFileSync(join(ROOT, 'src', 'extension.ts'), 'utf8');
const PACKAGE = JSON.parse(readFileSync(join(ROOT, 'package.json'), 'utf8')) as {
  contributes: {
    commands: Array<{ command: string }>;
    menus: Record<string, Array<{ command: string }>>;
  };
};

test('every stored session open path reveals its existing chat tab', () => {
  assert.match(
    HUB,
    /openTab\(agentId\?: string\): ChatPanel \{[\s\S]*?findByAgent\(agentId\)[\s\S]*?existing\.reveal\(\)[\s\S]*?return existing/,
  );
  assert.match(HUB, /async openSession\(agentId: string\): Promise<void> \{\s*this\.openTab\(agentId\)/);
  assert.match(
    CHAT,
    /private async resumeAgent\(agentId: string\)[\s\S]*?findByAgent\(agentId\)[\s\S]*?existing\.reveal\(\)[\s\S]*?return/,
  );
  assert.match(
    EXTENSION,
    /const existing = agentId \? hub\.findByAgent\(agentId\) : undefined;[\s\S]*?webviewPanel\.dispose\(\)/,
  );
});

test('the duplicate-session-tab command is no longer contributed or registered', () => {
  assert.equal(
    PACKAGE.contributes.commands.some((entry) => entry.command === 'anvil.openSessionInNewTab'),
    false,
  );
  assert.equal(
    Object.values(PACKAGE.contributes.menus)
      .flat()
      .some((entry) => entry.command === 'anvil.openSessionInNewTab'),
    false,
  );
  assert.doesNotMatch(EXTENSION, /openSessionInNewTab/);
});

test('listed and renamed session titles drive the chat tab title', () => {
  assert.match(HUB, /const title = session\.title\?\.trim\(\);[\s\S]*?next\.set\(session\.agent_id, title\)/);
  assert.match(HUB, /this\.sessionTitles\.set\(agentId, renamed\.title\)/);
  assert.match(HUB, /panel\.sessionTitleChanged\(\)/);
  assert.match(
    CHAT,
    /const title = agentId \? this\.hub\.sessionTitle\(agentId\) \?\? `Anvil — \$\{agentId\.slice\(0, 8\)\}` : 'Anvil'/,
  );
});
