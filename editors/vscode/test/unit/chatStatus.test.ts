/**
 * The window's chat status-bar item: which state wins, and what it says.
 *
 * One item describes every tab, so the rule for combining them is the whole
 * question: an approval waiting in a tab the user is not looking at is the one
 * thing that has to reach them, and a run making progress does not.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

import { ChatPhase, chatStatusOf, phaseOf } from '../../src/chatStatus';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const HUB = readFileSync(join(EXTENSION_ROOT, 'src', 'hub.ts'), 'utf8');
const EXTENSION = readFileSync(join(EXTENSION_ROOT, 'src', 'extension.ts'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');
const STATUS_BAR = readFileSync(join(EXTENSION_ROOT, 'src', 'statusBar.ts'), 'utf8');
const PACKAGE = JSON.parse(readFileSync(join(EXTENSION_ROOT, 'package.json'), 'utf8'));

test('no tabs is idle', () => {
  assert.equal(phaseOf([]), 'idle');
});

test('an idle tab is idle, a busy one is running', () => {
  assert.equal(phaseOf([{ busy: false, awaiting: false }]), 'idle');
  assert.equal(phaseOf([{ busy: true, awaiting: false }]), 'running');
});

test('a waiting tab outranks a running one, wherever it is', () => {
  const tabs = [
    { busy: true, awaiting: false },
    { busy: false, awaiting: true },
  ];
  assert.equal(phaseOf(tabs), 'waiting');
  // Order must not matter: the tabs are a set, not a ranking.
  assert.equal(phaseOf([...tabs].reverse()), 'waiting');
});

test('a tab that is running and waiting is waiting', () => {
  // An approval is raised inside a run, so both are true at once; the question
  // is the state that needs the user.
  assert.equal(phaseOf([{ busy: true, awaiting: true }]), 'waiting');
});

test('every phase has a non-empty text and tooltip, and waiting is distinct', () => {
  const phases: ChatPhase[] = ['idle', 'running', 'waiting'];
  const texts = new Set<string>();
  for (const phase of phases) {
    const { text, tooltip } = chatStatusOf(phase);
    assert.ok(text.includes('Anvil'), phase);
    assert.ok(tooltip.length > 0, phase);
    texts.add(text);
  }
  assert.equal(texts.size, phases.length);
});

test('the waiting and running icons are codicon animations, not text', () => {
  assert.match(chatStatusOf('running').text, /\$\(sync~spin\)/);
  assert.match(chatStatusOf('waiting').text, /\$\(bell\)/);
});

test('the hub answers the phase from the panels rather than a copy of them', () => {
  assert.match(HUB, /get phase\(\): ChatPhase/);
  assert.match(HUB, /phaseOf\(\[\.\.\.this\.panels\]\.map\(\(panel\) => \(\{ busy: panel\.busy, awaiting: panel\.awaitingApproval\(\) \}\)\)\)/);
  // A click goes to the work: the waiting tab, then the front tab, then any
  // open one, and only then a new tab.
  assert.match(HUB, /revealChat\(\): void \{/);
});

test('the panel reports a waiting approval from its session', () => {
  assert.match(CHAT_PANEL, /awaitingApproval\(\): boolean \{\s*return this\.pendingApprovals\(\)\.length > 0;/);
  // Answering, withdrawing, and a rule that decided the call all change it, and
  // each tells the hub so the item cannot go stale.
  assert.match(CHAT_PANEL, /onApproval = [\s\S]*?this\.hub\.notifyRunning\(\);/);
  assert.match(CHAT_PANEL, /forgetApproval\(requestId\)/);
});

test('the status bar is one item, created with the extension and disposed with it', () => {
  assert.match(STATUS_BAR, /createStatusBarItem\(vscode\.StatusBarAlignment\.Right, 90\)/);
  assert.match(STATUS_BAR, /hub\.onRunningChanged/);
  assert.match(STATUS_BAR, /hub\.onTabsChanged/);
  assert.match(EXTENSION, /new ChatStatusBar\(hub\)/);
  assert.ok(
    PACKAGE.contributes.commands.some((command: { command: string }) => command.command === 'anvil.focusChat'),
    'the item’s command must be contributed',
  );
});
