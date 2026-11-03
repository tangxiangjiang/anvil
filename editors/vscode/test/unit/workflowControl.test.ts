import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const EXTENSION = readFileSync(join(ROOT, 'src', 'extension.ts'), 'utf8');
const TREE = readFileSync(join(ROOT, 'src', 'workflowTree.ts'), 'utf8');
const DOCUMENT = readFileSync(join(ROOT, 'src', 'workflowDocument.ts'), 'utf8');
const PACKAGE = readFileSync(join(ROOT, 'package.json'), 'utf8');

function functionBody(source: string, signature: string): string {
  const start = source.indexOf(signature);
  assert.ok(start >= 0, `${signature} exists`);
  return source.slice(start);
}

test('the Workflow view and explicit decision commands are contributed and registered', () => {
  for (const name of [
    'anvil.workflow',
    'anvil.refreshWorkflow',
    'anvil.filterWorkflow',
    'anvil.approveWorkflowSelection',
    'anvil.denyWorkflowSelection',
  ]) {
    assert.ok(PACKAGE.includes(`"${name}"`), `${name} is contributed`);
  }
  assert.match(EXTENSION, /new WorkflowTreeProvider\(hub\)/);
  assert.match(EXTENSION, /canSelectMany: true/);
  assert.match(EXTENSION, /onDidChangeCheckboxState/);
});

test('navigation and the tree provider have no mutation capability', () => {
  assert.doesNotMatch(TREE, /decideWorkflow|workflow\.decide/);
  for (const command of ['anvil.workflow.openArtifact', 'anvil.workflow.openSession']) {
    const body = functionBody(EXTENSION, `registerCommand('${command}'`);
    const end = body.indexOf('\n    vscode.commands.registerCommand(', 1);
    assert.doesNotMatch(end > 0 ? body.slice(0, end) : body, /decideWorkflow/);
  }
});

test('a modal confirmation precedes the only decision call', () => {
  const body = functionBody(EXTENSION, 'async function decideWorkflowSelection(');
  const confirmation = body.indexOf('showWarningMessage');
  const mutation = body.indexOf('hub.decideWorkflow');
  assert.ok(confirmation >= 0 && mutation > confirmation);
  assert.match(body.slice(confirmation, mutation), /modal: true/);
  assert.match(body.slice(confirmation, mutation), /if \(answer !== verb\) return/);
  assert.equal(body.split('hub.decideWorkflow').length, 2);
  assert.match(body, /A denial reason is required/);
  assert.match(body, /if \(workflowDecisionPending\)/);
  assert.match(body, /workflowDecisionPending = true/);
  assert.match(body, /finally \{[\s\S]*workflowDecisionPending = false/);
  assert.match(PACKAGE, /anvil\.workflowSelection && !anvil\.workflowDecisionPending/);
});

test('a stale decision refreshes authoritative state and is never retried', () => {
  const body = functionBody(EXTENSION, 'async function decideWorkflowSelection(');
  const stale = body.slice(body.indexOf('err.code === -32006'));
  assert.match(stale, /tree\.refreshAgent\(selection\.agentId\)/);
  assert.match(stale, /nothing was retried/);
  assert.doesNotMatch(stale, /hub\.decideWorkflow/);
});

test('workflow plan bodies are opened through a read-only content provider', () => {
  assert.match(DOCUMENT, /registerTextDocumentContentProvider/);
  assert.doesNotMatch(DOCUMENT, /registerFileSystemProvider|writeFile|WorkspaceEdit/);
  assert.match(TREE, /This Host does not support the Workflow view/);
});
