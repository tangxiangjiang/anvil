/**
 * Where the sidecar is found, and whether it is the one we shipped.
 *
 * The resolution order is the whole of this module's behaviour and it is easy
 * to get subtly wrong (a bundled binary that loses to an unset setting, a
 * relative path resolved against the editor instead of the workspace), so each
 * rung is pinned on its own. The provenance check is pinned against a real
 * file on disk with a real digest, including the case that matters: a binary
 * swapped after the manifest was written.
 */

import { strict as assert } from 'node:assert';
import { createHash } from 'node:crypto';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { test } from 'node:test';
import {
  BUNDLE_DIR,
  HOST_TARGETS,
  MANIFEST_FILE,
  MANIFEST_VERSION,
  PATH_HOST,
  describeHost,
  hostVersion,
  readHostManifest,
  resolveHost,
  sha256File,
  targetFor,
  verifyHost,
  versionDrift,
} from '../../src/hostBundle';

/** A fake extension directory, with whatever the test wants in `bin/`. */
function fakeExtension(
  files: Record<string, string | Buffer>,
): { root: string; cleanup: () => void } {
  const root = mkdtempSync(join(tmpdir(), 'anvil-bundle-'));
  for (const [name, body] of Object.entries(files)) {
    const file = join(root, name);
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(file, body);
  }
  return { root, cleanup: () => rmSync(root, { recursive: true, force: true }) };
}

/** A manifest naming one file and its true digest — what the build writes. */
function manifestFor(target: string, file: string, body: string): string {
  return JSON.stringify({
    manifest: MANIFEST_VERSION,
    hosts: { [target]: { file, sha256: createHash('sha256').update(body).digest('hex') } },
  });
}

test('a target is the platform and arch we build, and nothing else', () => {
  assert.deepEqual(targetFor('win32', 'x64'), {
    target: 'win32-x64',
    goos: 'windows',
    goarch: 'amd64',
    file: 'anvil-host.exe',
  });
  assert.equal(targetFor('linux', 'arm64')?.goarch, 'arm64');
  assert.equal(targetFor('darwin', 'x64')?.goos, 'darwin');
  // A platform we do not build is undefined rather than a guess: the caller
  // then falls back to PATH, instead of looking for a binary that cannot exist.
  assert.equal(targetFor('freebsd', 'x64'), undefined);
  assert.equal(targetFor('linux', 'riscv64'), undefined);
  // Every target names the same file name per platform family, which is what
  // lets a package carry exactly one binary.
  for (const entry of HOST_TARGETS) {
    assert.equal(entry.file, entry.goos === 'windows' ? 'anvil-host.exe' : 'anvil-host');
  }
});

test('the setting wins over everything, and is resolved against the workspace', () => {
  const fake = fakeExtension({ [`${BUNDLE_DIR}/anvil-host.exe`]: 'bundled' });
  try {
    const input = { extensionPath: fake.root, platform: 'win32', arch: 'x64' };
    // An absolute path is used as given.
    assert.deepEqual(resolveHost({ ...input, configured: 'E:\\tools\\anvil-host.exe' }), {
      command: 'E:\\tools\\anvil-host.exe',
      source: 'configured',
    });
    // A relative one belongs to the workspace, not to the extension: that is
    // where a repository's own build lands.
    assert.equal(
      resolveHost({ ...input, configured: 'bin/anvil-host.exe', workspaceRoot: 'E:\\repo' }).command,
      join('E:\\repo', 'bin', 'anvil-host.exe'),
    );
    // Whitespace is not a setting.
    assert.equal(resolveHost({ ...input, configured: '  ' }).source, 'bundled');
  } finally {
    fake.cleanup();
  }
});

test('a bundled host is used when nothing is configured', () => {
  const body = 'a sidecar, allegedly';
  const fake = fakeExtension({
    [`${BUNDLE_DIR}/anvil-host.exe`]: body,
    [`${BUNDLE_DIR}/${MANIFEST_FILE}`]: manifestFor('win32-x64', 'anvil-host.exe', body),
  });
  try {
    const host = resolveHost({ extensionPath: fake.root, platform: 'win32', arch: 'x64' });
    assert.equal(host.source, 'bundled');
    assert.equal(host.target, 'win32-x64');
    assert.equal(host.command, join(fake.root, BUNDLE_DIR, 'anvil-host.exe'));
    assert.equal(host.expectedSha256, sha256File(host.command));
    assert.equal(verifyHost(host), undefined, 'the binary the manifest describes is accepted');
  } finally {
    fake.cleanup();
  }
});

test('another platform in the same directory is not this platform', () => {
  // The packaged layout for Linux on a Windows machine: no `.exe`, so nothing
  // is bundled here and the fallback is PATH rather than a binary that cannot
  // run.
  const fake = fakeExtension({ [`${BUNDLE_DIR}/anvil-host`]: 'linux sidecar' });
  try {
    assert.deepEqual(resolveHost({ extensionPath: fake.root, platform: 'win32', arch: 'x64' }), {
      command: PATH_HOST,
      source: 'path',
    });
    // And asking *as* Linux finds it.
    assert.equal(
      resolveHost({ extensionPath: fake.root, platform: 'linux', arch: 'x64' }).source,
      'bundled',
    );
  } finally {
    fake.cleanup();
  }
});

test('an empty extension falls back to PATH', () => {
  const fake = fakeExtension({});
  try {
    assert.deepEqual(resolveHost({ extensionPath: fake.root, platform: 'linux', arch: 'x64' }), {
      command: PATH_HOST,
      source: 'path',
    });
    // A platform we do not build never looks inside the bundle at all.
    assert.equal(
      resolveHost({ extensionPath: fake.root, platform: 'freebsd', arch: 'amd64' }).source,
      'path',
    );
  } finally {
    fake.cleanup();
  }
});

test('a swapped binary is refused, and a missing manifest entry too', () => {
  const body = 'the binary we built';
  const fake = fakeExtension({
    [`${BUNDLE_DIR}/anvil-host.exe`]: body,
    [`${BUNDLE_DIR}/${MANIFEST_FILE}`]: manifestFor('win32-x64', 'anvil-host.exe', body),
  });
  try {
    const input = { extensionPath: fake.root, platform: 'win32', arch: 'x64' };
    // Same path, different bytes: the manifest is what makes this visible.
    writeFileSync(join(fake.root, BUNDLE_DIR, 'anvil-host.exe'), 'something else entirely');
    const refusal = verifyHost(resolveHost(input));
    assert.ok(refusal !== undefined);
    assert.match(refusal, /does not match the checksum/);
    assert.match(refusal, /anvil\.hostPath/, 'the message says how to run a binary of your own');

    // A manifest that does not name the binary at all is not permission: a
    // package missing its own manifest is a package we cannot vouch for.
    rmSync(join(fake.root, BUNDLE_DIR, MANIFEST_FILE));
    const unlisted = verifyHost(resolveHost(input));
    assert.ok(unlisted !== undefined);
    assert.match(unlisted, /not listed in hosts\.json/);

    // And the escape hatch: a configured path is never checked, because there
    // is nothing truthful to check it against.
    const configured = resolveHost({ ...input, configured: 'my-own-build' });
    assert.equal(verifyHost(configured), undefined);
  } finally {
    fake.cleanup();
  }
});

test('the manifest is read defensively', () => {
  const fake = fakeExtension({
    [`${BUNDLE_DIR}/${MANIFEST_FILE}`]: '{ this is not json',
  });
  try {
    // Unparseable reads as absent; the caller then refuses rather than allows.
    assert.equal(readHostManifest(join(fake.root, BUNDLE_DIR)), undefined);
    assert.equal(readHostManifest(join(fake.root, 'nowhere')), undefined);
    writeFileSync(join(fake.root, BUNDLE_DIR, MANIFEST_FILE), JSON.stringify({ manifest: 1 }));
    assert.equal(readHostManifest(join(fake.root, BUNDLE_DIR)), undefined, 'no hosts map, no manifest');
  } finally {
    fake.cleanup();
  }
});

test('the sources are described in terms a person can act on', () => {
  assert.match(describeHost({ command: 'anvil-host', source: 'path' }), /on PATH: anvil-host/);
  assert.match(
    describeHost({ command: 'C:\\x\\anvil-host.exe', source: 'bundled', target: 'win32-x64' }),
    /bundled for win32-x64/,
  );
  assert.match(describeHost({ command: 'mine', source: 'configured' }), /anvil\.hostPath: mine/);
});

test('a version banner names a release line', () => {
  assert.equal(hostVersion('anvil-host 0.35.7'), '0.35.7');
  assert.equal(hostVersion('anvil-host v0.35.7'), '0.35.7');
  assert.equal(hostVersion('anvil-host 0.35.7-dev.1'), '0.35.7-dev.1');
  assert.equal(hostVersion('anvil-host'), undefined);
  assert.equal(hostVersion('something else 1.2.3'), undefined);

  // A patch difference is not drift: the protocol revision is the contract,
  // and refusing a newer sidecar would strand the user for no reason.
  assert.equal(versionDrift('anvil-host 0.35.9', '0.35.7'), undefined);
  assert.equal(versionDrift('anvil-host 0.35.7', '0.35.7'), undefined);
  // A different release line is worth saying out loud, because the packaging
  // story makes shipping the wrong one easy.
  for (const banner of ['anvil-host 0.34.2', 'anvil-host 0.36.0']) {
    const drift = versionDrift(banner, '0.35.7');
    assert.ok(drift !== undefined, `${banner} is another release line`);
    assert.match(drift, /but this extension is 0\.35\.7/);
  }
  assert.match(versionDrift('anvil-host 0.34.2', '0.35.7') ?? '', /from Anvil 0\.34/);
  assert.equal(versionDrift('not a banner', '0.35.7'), undefined);
});

test('the manifest the build script writes describes a target we know', () => {
  // The build script keeps its own copy of the target table, so the file it
  // actually wrote is the thing to check: a name it wrote that this module
  // would not look for is a package whose host is never found.
  const bundle = join(__dirname, '..', '..', '..', 'bin');
  const manifest = readHostManifest(bundle);
  if (!manifest) return; // no host built in this checkout; `npm run build:host` makes one
  const targets = Object.keys(manifest.hosts);
  assert.equal(targets.length, 1, 'a package carries exactly one platform host');
  const entry = manifest.hosts[targets[0]];
  const known = HOST_TARGETS.find((candidate) => candidate.target === targets[0]);
  assert.ok(known !== undefined, `${targets[0]} is not a target this extension resolves`);
  assert.equal(entry.file, known.file, 'the file name is the one the resolver looks for');
  assert.equal(manifest.manifest, MANIFEST_VERSION, 'the schema revision is the one this reader knows');
  assert.match(entry.sha256, /^[0-9a-f]{64}$/, 'a real digest, so the spawn check can compare');
  assert.equal(entry.sha256, sha256File(join(bundle, entry.file)), 'and it is the digest of the binary beside it');
});
