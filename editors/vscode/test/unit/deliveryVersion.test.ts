import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const PRE_CHANGE_VERSION = '0.52.19';
const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const REPOSITORY_ROOT = join(EXTENSION_ROOT, '..', '..');

function patchAfter(version: string): string {
  const parts = version.split('.').map(Number);
  assert.equal(parts.length, 3);
  assert.ok(parts.every(Number.isSafeInteger));
  return `${parts[0]}.${parts[1]}.${parts[2] + 1}`;
}

test('the shipped product version advances by exactly one patch and stays synchronized', () => {
  const pkg = JSON.parse(readFileSync(join(EXTENSION_ROOT, 'package.json'), 'utf8'));
  const lock = JSON.parse(readFileSync(join(EXTENSION_ROOT, 'package-lock.json'), 'utf8'));
  const expected = patchAfter(PRE_CHANGE_VERSION);
  assert.equal(pkg.version, expected);
  assert.equal(lock.version, expected);
  assert.equal(lock.packages[''].version, expected);

  const sdkVersion = readFileSync(join(REPOSITORY_ROOT, 'source', 'version.go'), 'utf8');
  assert.match(sdkVersion, new RegExp(`const Version = "${expected.replaceAll('.', '\\.')}"`));
  const hostProtocol = readFileSync(
    join(REPOSITORY_ROOT, 'source', 'internal', 'editorhost', 'proto.go'),
    'utf8',
  );
  assert.match(hostProtocol, /var Version = anvil\.Version/);

  for (const relative of [
    'README.md',
    'doc/roadmaps/v0.x/README.md',
    'doc/roadmaps/v0.x/m_0.46_editor_polish.vscode.md',
    'doc/roadmaps/v0.x/m_0.54_durable_agent_runs.vscode.md',
  ]) {
    assert.match(readFileSync(join(REPOSITORY_ROOT, relative), 'utf8'), new RegExp(expected.replaceAll('.', '\\.')));
  }
});
