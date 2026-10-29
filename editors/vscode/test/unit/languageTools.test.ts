/**
 * The settings-to-wire mapping for language servers and the two commands.
 *
 * There is no `vscode` here, which is the point: the narrowing from loose
 * settings values to the exact shapes `session.new` accepts is pure, so the
 * cases that matter can be stated as data. The source assertions pin the other
 * half — that `readSettings` actually reads the four keys and only sets a field
 * when there is something to send.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import { argv, lspParams } from '../../src/languageTools';
import { configurationProperties } from '../helpers/package';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const SETTINGS = readFileSync(join(EXTENSION_ROOT, 'src', 'settings.ts'), 'utf8');
const PACKAGE = JSON.parse(readFileSync(join(EXTENSION_ROOT, 'package.json'), 'utf8')) as {
  contributes: { configuration: { properties: Record<string, unknown> } };
};

test('lspParams sends nothing when the switch is off', () => {
  assert.equal(lspParams(false, [{ language: 'go', command: 'gopls' }]), undefined);
});

test('lspParams drops entries that could never start a process', () => {
  assert.equal(lspParams(true, []), undefined);
  assert.equal(lspParams(true, [{ language: 'go' }]), undefined);
  assert.equal(lspParams(true, [{ command: 'gopls' }]), undefined);
  assert.equal(lspParams(true, [{ language: 'go', command: '  ' }]), undefined);
  assert.equal(lspParams(true, 'not an array'), undefined);
});

test('lspParams maps the fields the host expects and keeps args verbatim', () => {
  assert.deepEqual(
    lspParams(true, [
      { language: 'go', pattern: '**/*.go', command: ' gopls ', args: ['-remote=auto', ''] },
    ]),
    { enabled: true, servers: [{ language: 'go', pattern: '**/*.go', command: 'gopls', args: ['-remote=auto', ''] }] },
  );
});

test('lspParams omits an absent selector rather than sending an empty string', () => {
  assert.deepEqual(lspParams(true, [{ language: 'typescript', command: 'tsserver' }]), {
    enabled: true,
    servers: [{ command: 'tsserver', language: 'typescript' }],
  });
  assert.equal(lspParams(true, [{ command: 'tsserver' }]), undefined);
});

test('lspParams keeps the valid rows beside an invalid one', () => {
  assert.deepEqual(lspParams(true, [{ language: 'go' }, { pattern: '**/*.ts', command: 'tsserver' }]), {
    enabled: true,
    servers: [{ command: 'tsserver', pattern: '**/*.ts' }],
  });
});

test('argv is a command only when its first word is one', () => {
  assert.equal(argv([]), undefined);
  assert.equal(argv(['  ', 'x']), undefined);
  assert.equal(argv('go test'), undefined);
  assert.equal(argv(['go', 3]), undefined);
  assert.deepEqual(argv(['go', 'test', './...']), ['go', 'test', './...']);
  assert.deepEqual(argv(['echo', '']), ['echo', '']);
});

test('settings.ts reads the four keys and sends each only when it has a value', () => {
  assert.match(SETTINGS, /cfg\.get<boolean>\('lsp\.enabled', false\)/);
  assert.match(SETTINGS, /cfg\.get<unknown>\('lsp\.servers', \[\]\)/);
  assert.match(SETTINGS, /cfg\.get<unknown>\('testCommand', \[\]\)/);
  assert.match(SETTINGS, /cfg\.get<unknown>\('buildCommand', \[\]\)/);
  assert.match(SETTINGS, /if \(lsp\) session\.lsp = lsp;/);
  assert.match(SETTINGS, /if \(testCommand\) session\.test_command = testCommand;/);
  assert.match(SETTINGS, /if \(buildCommand\) session\.build_command = buildCommand;/);
});

test('package.json declares the settings with off/empty defaults', () => {
  const props = configurationProperties(PACKAGE);
  const enabled = props['anvil.lsp.enabled'] as { default?: unknown };
  const servers = props['anvil.lsp.servers'] as { default?: unknown };
  const testCommand = props['anvil.testCommand'] as { default?: unknown };
  const buildCommand = props['anvil.buildCommand'] as { default?: unknown };
  assert.equal(enabled?.default, false);
  assert.deepEqual(servers?.default, []);
  assert.deepEqual(testCommand?.default, []);
  assert.deepEqual(buildCommand?.default, []);
});
