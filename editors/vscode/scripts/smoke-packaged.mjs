/*
 * Opens a built VSIX, checks what is inside it, and runs the host it carries.
 *
 *   node scripts/smoke-packaged.mjs --vsix anvil-vscode-<version>-win32-x64.vsix
 *
 * This is the packaging acceptance test that can run without a window: the
 * extension host API is not available here, but the two things that actually
 * break in a package are — the tree (.vscodeignore shipping `src/`, forgetting
 * the binary) and the sidecar (a binary that is not the one the manifest
 * records, or one that cannot start on a machine without a Go toolchain).
 *
 * It extracts to a temporary directory and runs the binary from *there*, which
 * is the closest thing to "installed on another machine" that a script can be:
 * nothing in the extension's own build tree is in scope.
 */

import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));

/** Files that must be in the package, relative to `extension/`. */
const REQUIRED = [
  'package.json',
  'out/src/extension.js',
  'out/src/hostBundle.js',
  'media/chat.js',
  'media/chat.css',
  // A plain script, loaded by a second `script` tag in the panel's document. It
  // is not imported by anything, so nothing in the build or the tests fails when
  // it is missing from the package -- the transcript would just render as
  // unstyled text. This line is the only thing that would notice.
  'media/markdown.js',
  'bin/hosts.json',
];
/**
 * Prefixes that must not be. The first four are build inputs rather than the
 * product; `.anvil/` is the workspace's own session state, which is the one
 * thing here that is not merely wasteful to ship — it is somebody's
 * conversations, and a packaged extension has no business carrying them.
 */
const FORBIDDEN = ['src/', 'test/', 'scripts/', 'node_modules/', 'out/test/', '.anvil/'];

function fail(message) {
  console.error(`smoke-packaged: ${message}`);
  process.exit(1);
}

/**
 * Removes a temporary tree, and does not fail the run if it cannot.
 *
 * On Windows the binary that was just executed can still be held for a moment
 * after its process exits, and `rmSync` then throws EPERM with no retries by
 * default — which turned a passing check into a non-zero exit. The package is
 * what is under test; a leftover directory in `%TEMP%` is not, so a failure
 * here is a warning at most.
 */
function cleanup(path) {
  try {
    rmSync(path, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
  } catch (err) {
    console.error(`smoke-packaged: could not remove ${path}: ${err.message}`);
  }
}

function parseArg(argv, name) {
  const i = argv.indexOf(name);
  if (i < 0) return undefined;
  const value = argv[i + 1];
  if (!value) fail(`${name} needs a value`);
  return value;
}

/**
 * Extracts with whatever this machine has. `unzip` covers Linux and Git Bash;
 * macOS and Windows 10+ need neither installed, so the fallbacks are real
 * rather than defensive.
 */
function extract(vsix, dir) {
  const attempts = [
    ['unzip', ['-q', '-o', vsix, '-d', dir]],
    ['tar', ['-xf', vsix, '-C', dir]],
    ['powershell', ['-NoProfile', '-NonInteractive', '-Command',
      `Expand-Archive -LiteralPath '${vsix}' -DestinationPath '${dir}' -Force`]],
  ];
  const tried = [];
  for (const [command, args] of attempts) {
    const result = spawnSync(command, args, { stdio: 'ignore' });
    if (result.error) {
      tried.push(`${command}: ${result.error.message}`);
      continue;
    }
    if (result.status === 0 && existsSync(join(dir, 'extension', 'package.json'))) return;
    tried.push(`${command}: exit ${result.status}`);
  }
  fail(`could not extract ${vsix} (${tried.join('; ')})`);
}

/** What the extension resolves and what it refuses, re-implemented on the tree. */
function checkTree(root) {
  for (const required of REQUIRED) {
    if (!existsSync(join(root, required))) fail(`the package is missing ${required}`);
  }
  // The host binary is the one file whose name depends on the target, so it is
  // read from the manifest rather than guessed.
  const manifest = JSON.parse(readFileSync(join(root, 'bin', 'hosts.json'), 'utf8'));
  const entries = Object.entries(manifest.hosts ?? {});
  if (entries.length !== 1) {
    fail(`bin/hosts.json should describe exactly one target, found ${entries.length}`);
  }
  const [target, entry] = entries[0];
  const binary = join(root, 'bin', entry.file);
  if (!existsSync(binary)) fail(`the manifest names ${entry.file}, which is not in the package`);

  const actual = createHash('sha256').update(readFileSync(binary)).digest('hex');
  if (actual !== entry.sha256) {
    fail(`the packaged ${entry.file} is not the binary the manifest recorded`);
  }
  console.log(`smoke-packaged: packaged ${target} host, ${entry.size} bytes, sha256 ${actual.slice(0, 12)}`);
  return { target, binary, version: JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version };
}

/**
 * Speaks the handshake by hand: no build output, no dependencies, just lines.
 * Resolves with the `initialize` result once the host has been asked to leave —
 * the child must be gone before the temporary directory holding it is removed,
 * and on Windows a running executable cannot be deleted.
 */
function handshake(binary, workspace, version) {
  return new Promise((resolveHandshake, rejectHandshake) => {
    const env = { ...process.env };
    // No key: this must work, and must reach no provider, without one.
    for (const key of ['ANVIL_API_KEY', 'OPENAI_API_KEY', 'ANTHROPIC_API_KEY']) delete env[key];
    const child = spawn(binary, [], { cwd: workspace, env, stdio: ['pipe', 'pipe', 'pipe'] });
    let stdout = '';
    let stderr = '';
    let result;
    let settled = false;

    const settle = (err) => {
      if (settled) return;
      settled = true;
      clearTimeout(deadline);
      if (err) rejectHandshake(err);
      else resolveHandshake(result);
    };
    const deadline = setTimeout(() => {
      child.kill();
      settle(new Error(`the packaged host did not answer initialize within 20s (stderr: ${stderr.trim()})`));
    }, 20_000);

    const send = (message) => child.stdin.write(`${JSON.stringify(message)}\n`);

    child.on('error', (err) => settle(err));
    child.stdout.setEncoding('utf8');
    child.stdout.on('data', (chunk) => {
      stdout += chunk;
      for (;;) {
        const nl = stdout.indexOf('\n');
        if (nl < 0) break;
        const line = stdout.slice(0, nl);
        stdout = stdout.slice(nl + 1);
        if (!line.trim()) continue;
        const msg = JSON.parse(line);
        if (msg.id !== 1) continue;
        if (msg.error) {
          child.kill();
          settle(new Error(`initialize failed: ${JSON.stringify(msg.error)}`));
          return;
        }
        result = msg.result;
        // The extension's own order: answer the handshake, then ask it to stop.
        send({ jsonrpc: '2.0', method: 'initialized' });
        send({ jsonrpc: '2.0', id: 2, method: 'shutdown' });
        // A host that ignores `shutdown` is not what this test is about; stop
        // waiting on it either way.
        setTimeout(() => child.kill(), 5000).unref();
      }
    });
    child.stderr.setEncoding('utf8');
    child.stderr.on('data', (chunk) => {
      stderr += chunk;
    });
    child.on('exit', (code) => {
      if (result) settle();
      else settle(new Error(`the packaged host exited with code ${code} (stderr: ${stderr.trim()})`));
    });

    send({
      jsonrpc: '2.0',
      id: 1,
      method: 'initialize',
      params: { protocol: 1, cwd: workspace, client: { name: 'anvil-smoke', version } },
    });
  });
}

const arg = parseArg(process.argv, '--vsix');
if (!arg) fail('pass --vsix <file>');
// A relative path is relative to the extension, where the packaging script
// puts the VSIX, rather than to wherever the caller happens to be.
const vsix = resolve(here, '..', arg);
if (!existsSync(vsix)) fail(`no such file: ${vsix}`);

const dir = mkdtempSync(join(tmpdir(), 'anvil-smoke-'));
const workspace = mkdtempSync(join(tmpdir(), 'anvil-workspace-'));
try {
  extract(vsix, dir);
  const root = join(dir, 'extension');
  for (const forbidden of FORBIDDEN) {
    if (existsSync(join(root, forbidden))) fail(`the package contains ${forbidden}, which .vscodeignore should have dropped`);
  }
  const { target, binary, version } = checkTree(root);

  // A package for another platform is still worth opening — the tree and the
  // provenance are the same checks — but its binary cannot run here, and saying
  // so is better than an ENOEXEC that looks like a broken package.
  if (target !== `${process.platform}-${process.arch}`) {
    console.log(`smoke-packaged: ${target} is not this machine (${process.platform}-${process.arch}); tree and checksum checked, binary not run`);
  } else {
    const result = await handshake(binary, workspace, version);
    if (result.protocol !== 1) fail(`the packaged host speaks protocol ${result.protocol}`);
    if (!String(result.host).startsWith('anvil-host ')) fail(`unexpected host banner ${result.host}`);
    console.log(`smoke-packaged: ${result.host} answered for ${target}, protocol ${result.protocol}`);
  }
  console.log(`smoke-packaged: OK ${vsix}`);
} finally {
  cleanup(dir);
  cleanup(workspace);
}
