/*
 * Builds a platform VSIX: compile the extension, build the sidecar for that
 * platform into `bin/`, then hand the whole directory to `vsce`.
 *
 *   node scripts/package-vsix.mjs                    # this machine's platform
 *   node scripts/package-vsix.mjs --target linux-x64
 *   node scripts/package-vsix.mjs --all              # one VSIX per target
 *
 * The result is a VSIX that installs and runs on a machine with no Go toolchain:
 * the sidecar travels inside it, and `src/hostBundle.ts` finds it before it
 * looks anywhere else.
 *
 * A platform VSIX carries exactly one binary, which is why the target is a
 * parameter here rather than a folder of binaries: `ExtensionContext.extensionUri`
 * says nothing about the machine it will run on, so the platform has to be
 * chosen when the package is built. `--all` therefore builds one package per
 * target, in sequence, each overwriting what the previous one left in `bin/`.
 */

import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const extensionRoot = resolve(here, '..');

/** Kept in step with HOST_TARGETS in src/hostBundle.ts. */
const TARGETS = ['win32-x64', 'win32-arm64', 'linux-x64', 'linux-arm64', 'darwin-x64', 'darwin-arm64'];

function fail(message) {
  console.error(`package-vsix: ${message}`);
  process.exit(1);
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: extensionRoot, stdio: 'inherit', ...options });
  if (result.error) fail(`could not run ${command}: ${result.error.message}`);
  if (result.status !== 0) process.exit(result.status ?? 1);
}

function parseArgs(argv) {
  const targets = [];
  let all = false;
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--help' || arg === '-h') {
      console.log('usage: node scripts/package-vsix.mjs [--target <id>] [--all]');
      console.log(`targets: ${TARGETS.join(', ')}`);
      process.exit(0);
    } else if (arg === '--all') {
      all = true;
    } else if (arg === '--target') {
      const id = argv[++i];
      if (!id) fail('--target needs a value');
      targets.push(id);
    } else if (arg.startsWith('--target=')) {
      targets.push(arg.slice('--target='.length));
    } else {
      fail(`unknown argument ${arg}`);
    }
  }
  if (all && targets.length > 0) fail('--all and --target are mutually exclusive');
  const chosen = all ? TARGETS : targets.length > 0 ? targets : [`${process.platform}-${process.arch}`];
  for (const id of chosen) {
    if (!TARGETS.includes(id)) fail(`unknown target ${id}; known: ${TARGETS.join(', ')}`);
  }
  return chosen;
}

/** `vsce` as a path, so the package does not have to be installed globally. */
function vsceBin() {
  const pkgFile = join(extensionRoot, 'node_modules', '@vscode', 'vsce', 'package.json');
  if (!existsSync(pkgFile)) fail('@vscode/vsce is not installed; run npm install');
  const pkg = JSON.parse(readFileSync(pkgFile, 'utf8'));
  return join(dirname(pkgFile), pkg.bin.vsce);
}

function version() {
  return JSON.parse(readFileSync(join(extensionRoot, 'package.json'), 'utf8')).version;
}

const targets = parseArgs(process.argv.slice(2));
const vsce = vsceBin();

// One compile for every target: the JavaScript inside a platform VSIX is the
// same in all of them, and only the sidecar differs.
//
// `tsc` and `vsce` are both run as JavaScript through this same `node`, rather
// than through their `node_modules/.bin` shims: a `.cmd` shim needs a shell on
// Windows, and a shell needs quoting rules that differ per platform, for a
// command that has no quoting to do.
run(process.execPath, [join(extensionRoot, 'node_modules', 'typescript', 'bin', 'tsc'), '-p', '.']);

for (const target of targets) {
  // The host build writes `bin/hosts.json` alongside the binary, which is what
  // the extension checks at spawn; packaging after it is what keeps the two in
  // the same package.
  run(process.execPath, [join(here, 'build-host.mjs'), '--target', target]);

  const out = join(extensionRoot, `anvil-vscode-${version()}-${target}.vsix`);
  run(process.execPath, [vsce, 'package', '--target', target, '--out', out]);
  console.log(`package-vsix: ${out} (${Math.round(statSync(out).size / 1024)} KiB)`);
}
