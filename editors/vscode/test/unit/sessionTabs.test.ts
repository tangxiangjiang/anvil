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

test('a restoring session keeps its tab identity during the temporary host open', () => {
  assert.match(HUB, /findByAgent\(agentId: string\)[\s\S]*?panel\.matchesAgent\(agentId\)/);
  assert.match(
    CHAT,
    /currentAgentId\(\): string \| undefined \{\s*return this\.resumingAgentId \?\? this\.agentId \?\? this\.session\?\.agent/,
  );
  assert.match(
    CHAT,
    /matchesAgent\(agentId: string\)[\s\S]*?agentId === this\.resumingAgentId[\s\S]*?agentId === this\.agentId[\s\S]*?agentId === this\.session\?\.agent/,
  );
  const resume = /private async resumeAgent\(agentId: string\)[\s\S]*?\n {2}\}/.exec(CHAT);
  assert.ok(resume);
  assert.ok(
    resume[0].indexOf('this.resumingAgentId = agentId') < resume[0].indexOf('await this.ensureSession()'),
    'the destination is claimed before host work starts',
  );
  assert.match(resume[0], /catch \(err\) \{\s*this\.resumingAgentId = undefined/);
  assert.match(CHAT, /if \(this\.resumingAgentId && agentId !== this\.resumingAgentId\) return/);
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
