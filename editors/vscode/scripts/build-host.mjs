/*
 * Builds the `anvil-host` sidecar into `bin/`, and records what it built in
 * `bin/hosts.json`.
 *
 * `bin/` is where both sides look for it: a developer's F5 window resolves the
 * bundle before it looks on `PATH` (src/hostBundle.ts), and `vsce package` picks
 * the same file up for a packaged extension. Building to one place is the point
 * — the binary the developer ran is byte-for-byte the binary we ship.
 *
 *   node scripts/build-host.mjs                    # this machine's platform
 *   node scripts/build-host.mjs --target linux-x64 # cross-compile one target
 *   node scripts/build-host.mjs --all              # every target, in turn
 *
 * `bin/` is git-ignored: a binary is a build product.
 */

import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const extensionRoot = resolve(here, '..');
// editors/vscode/scripts -> editors/vscode -> editors -> the repository root.
const repoRoot = resolve(extensionRoot, '..', '..');
const goDir = join(repoRoot, 'source');
const binDir = join(extensionRoot, 'bin');
const manifestFile = join(binDir, 'hosts.json');
/** Schema revision, matching MANIFEST_VERSION in src/hostBundle.ts. */
const MANIFEST_VERSION = 1;

/**
 * Kept in step with HOST_TARGETS in `src/hostBundle.ts`: same triples, same
 * `GOOS`/`GOARCH`, same file names. `test/unit/hostBundle.test.ts` reads the
 * manifest this script writes and fails if a name here is one the extension
 * would not look for.
 */
const TARGETS = {
  'win32-x64': { goos: 'windows', goarch: 'amd64', file: 'anvil-host.exe' },
  'win32-arm64': { goos: 'windows', goarch: 'arm64', file: 'anvil-host.exe' },
  'linux-x64': { goos: 'linux', goarch: 'amd64', file: 'anvil-host' },
  'linux-arm64': { goos: 'linux', goarch: 'arm64', file: 'anvil-host' },
  'darwin-x64': { goos: 'darwin', goarch: 'amd64', file: 'anvil-host' },
  'darwin-arm64': { goos: 'darwin', goarch: 'arm64', file: 'anvil-host' },
};

function fail(message) {
  console.error(`build-host: ${message}`);
  process.exit(1);
}

const go = process.env.ANVIL_GO ?? 'go';

/** The target this machine is, when no target was asked for. */
function nativeTarget() {
  const id = `${process.platform}-${process.arch}`;
  if (!(id in TARGETS)) {
    fail(`no host target for ${id}; pass --target (${Object.keys(TARGETS).join(', ')})`);
  }
  return id;
}

function parseArgs(argv) {
  const targets = [];
  let all = false;
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--help' || arg === '-h') {
      console.log('usage: node scripts/build-host.mjs [--target <id>] [--all]');
      console.log(`targets: ${Object.keys(TARGETS).join(', ')}`);
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
  const chosen = all ? Object.keys(TARGETS) : targets.length > 0 ? targets : [nativeTarget()];
  for (const id of chosen) {
    if (!(id in TARGETS)) fail(`unknown target ${id}; known: ${Object.keys(TARGETS).join(', ')}`);
  }
  return chosen;
}

/**
 * The extension and the sidecar are two halves of one wire format, so a build
 * that would pair mismatched revisions is a build worth refusing. This is a
 * cheap static check on the two constants; `initialize` enforces the same
 * agreement at runtime.
 */
function assertProtocolAligned() {
  const goFile = join(goDir, 'internal', 'editorhost', 'proto.go');
  const tsFile = join(extensionRoot, 'src', 'protocol.ts');
  let goRev;
  let tsRev;
  try {
    goRev = /ProtocolVersion\s*=\s*(\d+)/.exec(readFileSync(goFile, 'utf8'))?.[1];
    tsRev = /PROTOCOL_VERSION\s*=\s*(\d+)/.exec(readFileSync(tsFile, 'utf8'))?.[1];
  } catch (err) {
    fail(`could not read the protocol revision: ${err.message}`);
  }
  if (goRev === undefined || tsRev === undefined) {
    fail(`could not find the protocol revision in ${goFile} or ${tsFile}`);
  }
  if (goRev !== tsRev) {
    fail(`protocol mismatch: source/internal/editorhost speaks ${goRev}, src/protocol.ts speaks ${tsRev}`);
  }
  return goRev;
}

function goVersion() {
  const result = spawnSync(go, ['version'], { encoding: 'utf8' });
  return /go version (go\S+)/.exec(result.stdout ?? '')?.[1];
}

/** Builds one target into `bin/` and returns its manifest entry. */
function buildTarget(id, protocol) {
  const target = TARGETS[id];
  mkdirSync(binDir, { recursive: true });
  // One binary per package: a stale one for another platform would be shipped
  // silently, and the manifest is rewritten for the target being built alone.
  for (const [other, entry] of Object.entries(TARGETS)) {
    if (other !== id) rmSync(join(binDir, entry.file), { force: true });
  }
  const out = join(binDir, target.file);

  // No shell: `go` resolves to go.exe on Windows, and this keeps the argument
  // list out of a command line that would need quoting rules per platform.
  // CGO off, so the shipped binary depends on nothing the user's machine
  // happens to have: a Linux VSIX must not need a glibc of the right vintage.
  const result = spawnSync(go, ['build', '-o', out, './cmd/anvil-host'], {
    cwd: goDir,
    stdio: 'inherit',
    env: { ...process.env, GOOS: target.goos, GOARCH: target.goarch, CGO_ENABLED: '0' },
  });
  if (result.error) fail(`could not run go: ${result.error.message}`);
  if (result.status !== 0) process.exit(result.status ?? 1);

  const bytes = readFileSync(out);
  const entry = {
    file: target.file,
    sha256: createHash('sha256').update(bytes).digest('hex'),
    size: statSync(out).size,
    go: goVersion(),
    protocol,
    built: new Date().toISOString(),
  };
  console.log(`build-host: ${id} -> ${out} (${entry.sha256.slice(0, 12)}, ${entry.size} bytes)`);
  return entry;
}

if (!existsSync(join(goDir, 'go.mod'))) {
  fail(`no Go module at ${goDir}`);
}

const targets = parseArgs(process.argv.slice(2));
const protocol = assertProtocolAligned();

for (const id of targets) {
  const entry = buildTarget(id, protocol);
  writeFileSync(
    manifestFile,
    `${JSON.stringify({ manifest: MANIFEST_VERSION, hosts: { [id]: entry } }, null, 2)}\n`,
  );
  console.log(`build-host: ${manifestFile}`);
}
