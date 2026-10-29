import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const TREE = readFileSync(join(ROOT, 'src', 'sessionTree.ts'), 'utf8');
const EXTENSION = readFileSync(join(ROOT, 'src', 'extension.ts'), 'utf8');
const PACKAGE = JSON.parse(readFileSync(join(ROOT, 'package.json'), 'utf8')) as {
  contributes: {
    commands: Array<{ command: string }>;
    menus: Record<string, Array<{ command: string; when?: string }>>;
  };
};

test('Session Manager is a collapsible function-group root', () => {
  assert.match(TREE, /kind: 'section'/);
  assert.match(TREE, /id: 'section:sessions'/);
  assert.match(TREE, /label: 'SESSION MANAGER'/);
  assert.match(
    TREE,
    /if \(node\.kind === 'section'\)[\s\S]*?vscode\.TreeItemCollapsibleState\.Expanded/,
  );
  assert.match(TREE, /if \(!element\) return \[SESSION_SECTION\]/);
  assert.match(TREE, /label: 'New session'[\s\S]*?command: 'anvil\.newSession'/);
  assert.match(TREE, /if \(element\.kind === 'section'\)[\s\S]*?return \[NEW_SESSION_ACTION, \.\.\.this\.view\(\)\]/);
  assert.equal(
    PACKAGE.contributes.menus['view/title'].some((entry) => entry.command === 'anvil.newSession'),
    false,
  );
});

test('the mistaken date-group title action is removed', () => {
  assert.equal(
    PACKAGE.contributes.commands.some((entry) => entry.command === 'anvil.expandSessionGroups'),
    false,
  );
  assert.equal(
    PACKAGE.contributes.menus['view/title'].some((entry) => entry.command === 'anvil.expandSessionGroups'),
    false,
  );
  assert.doesNotMatch(TREE, /expandAll\(/);
});

test('archive filing is workspace-local, reversible, and pruned against the listing', () => {
  assert.match(TREE, /const ARCHIVED_KEY = 'anvil\.archivedSessions'/);
  assert.match(TREE, /workspaceState\.get<unknown>\(ARCHIVED_KEY, \[\]\)/);
  assert.match(TREE, /async setArchived\(agentId: string, archived: boolean\)/);
  assert.match(TREE, /await this\.workspaceState\.update\(ARCHIVED_KEY, \[\.\.\.this\.archived\]\)/);
  assert.match(TREE, /const kept = \[\.\.\.this\.archived\]\.filter\(\(agentId\) => present\.has\(agentId\)\)/);
  assert.match(TREE, /archivedAgentIds: \[\.\.\.this\.archived\]/);
});

test('session context menus archive and restore without deleting', () => {
  assert.match(EXTENSION, /registerCommand\('anvil\.archiveSession'[\s\S]*?tree\.setArchived\(agentId, true\)/);
  assert.match(EXTENSION, /registerCommand\('anvil\.unarchiveSession'[\s\S]*?tree\.setArchived\(agentId, false\)/);
  const commands = new Set(PACKAGE.contributes.commands.map((entry) => entry.command));
  assert.ok(commands.has('anvil.archiveSession'));
  assert.ok(commands.has('anvil.unarchiveSession'));
  const menu = PACKAGE.contributes.menus['view/item/context'];
  assert.ok(menu.some((entry) => entry.command === 'anvil.archiveSession'));
  assert.ok(
    menu.some(
      (entry) => entry.command === 'anvil.unarchiveSession' && entry.when?.includes('session.archived'),
    ),
  );
});
