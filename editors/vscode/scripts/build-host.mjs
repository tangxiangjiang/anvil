/*
 * Builds the `anvil-host` sidecar into `out/`, next to the compiled extension.
 *
 * The extension never ships a Go toolchain; this exists so a developer working
 * in this repository can go from a clean checkout to a runnable panel with one
 * command (`npm test` depends on it). A packaged extension points `anvil.hostPath`
 * at a binary built elsewhere.
 */

import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const extensionRoot = resolve(here, '..');
// editors/vscode/scripts -> editors/vscode -> editors -> the repository root.
const repoRoot = resolve(extensionRoot, '..', '..');
const goDir = join(repoRoot, 'source');
const outDir = join(extensionRoot, 'out');
const binary = join(outDir, process.platform === 'win32' ? 'anvil-host.exe' : 'anvil-host');

if (!existsSync(goDir)) {
  console.error(`build-host: no Go module at ${goDir}`);
  process.exit(1);
}

mkdirSync(outDir, { recursive: true });

// No shell: `go` resolves to go.exe on Windows, and this keeps the argument
// list out of a command line that would need quoting rules per platform.
const go = process.env.ANVIL_GO ?? 'go';
const result = spawnSync(go, ['build', '-o', binary, './cmd/anvil-host'], {
  cwd: goDir,
  stdio: 'inherit',
});

if (result.error) {
  console.error(`build-host: could not run go: ${result.error.message}`);
  process.exit(1);
}
if (result.status !== 0) {
  process.exit(result.status ?? 1);
}

console.log(`build-host: ${binary}`);
