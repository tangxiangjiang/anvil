/**
 * Where the sidecar binary comes from, and whether it is the one we shipped.
 *
 * Three sources, in order: an explicit `anvil.hostPath`, the `anvil-host`
 * bundled inside the extension, and `anvil-host` on `PATH`. The middle one is
 * what makes a packaged extension work on a machine that has never seen a Go
 * toolchain; the last is the escape hatch a contributor with their own build
 * uses.
 *
 * This module imports no `vscode`, so the whole resolution order — and the
 * provenance check on top of it — is testable under plain Node. The check is
 * the part most worth testing and the part whose failure is hardest to notice:
 * a stale or swapped binary presents as a working host right up until it does
 * something the extension did not ask for.
 */

import { createHash } from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import { isAbsolute, join } from 'node:path';

/** Directory inside the extension that holds the bundled host. */
export const BUNDLE_DIR = 'bin';
/** Provenance manifest written by `scripts/build-host.mjs`, beside the binary. */
export const MANIFEST_FILE = 'hosts.json';
/** Manifest schema revision. A reader that does not know a revision rejects it. */
export const MANIFEST_VERSION = 1;
/** The bare name looked up on `PATH` when nothing is bundled or configured. */
export const PATH_HOST = 'anvil-host';

/** One platform the extension can be packaged for. */
export interface HostTarget {
  /** VS Code's target triple, as `vsce --target` spells it. */
  readonly target: string;
  /** Go's `GOOS`. */
  readonly goos: string;
  /** Go's `GOARCH`. */
  readonly goarch: string;
  /** File name inside `bin/`. */
  readonly file: string;
}

/**
 * The targets we build. Kept as one list so the build script, the packaging
 * script, and the runtime lookup cannot disagree about what a platform is
 * called or what its binary is named.
 */
export const HOST_TARGETS: readonly HostTarget[] = [
  { target: 'win32-x64', goos: 'windows', goarch: 'amd64', file: 'anvil-host.exe' },
  { target: 'win32-arm64', goos: 'windows', goarch: 'arm64', file: 'anvil-host.exe' },
  { target: 'linux-x64', goos: 'linux', goarch: 'amd64', file: 'anvil-host' },
  { target: 'linux-arm64', goos: 'linux', goarch: 'arm64', file: 'anvil-host' },
  { target: 'darwin-x64', goos: 'darwin', goarch: 'amd64', file: 'anvil-host' },
  { target: 'darwin-arm64', goos: 'darwin', goarch: 'arm64', file: 'anvil-host' },
];

/**
 * The target for a platform / arch pair, or undefined when we do not build it.
 *
 * The pair is both VS Code's platform and Node's `process.arch` (so `win32` +
 * `x64`), which is also how the target triples are spelled, so the whole of it
 * is a lookup rather than a translation table — one fewer place for the build
 * script and the extension to disagree.
 */
export function targetFor(platform: string, arch: string): HostTarget | undefined {
  return HOST_TARGETS.find((entry) => entry.target === `${platform}-${arch}`);
}

/** What the build script recorded about the binary it produced. */
export interface HostManifestEntry {
  file: string;
  /** Lowercase hex sha256 of the binary as it was written. */
  sha256: string;
  size?: number;
  /** The Go toolchain that produced it, for support. */
  go?: string;
  /** The editor-host protocol revision the binary was built against. */
  protocol?: number;
  /** When it was built, ISO-8601 UTC. */
  built?: string;
}

/** The provenance manifest: one entry per target, keyed by target triple. */
export interface HostManifest {
  manifest: number;
  hosts: Record<string, HostManifestEntry>;
}

/** Reads `bin/hosts.json`, or undefined when it is missing or unreadable. */
export function readHostManifest(dir: string): HostManifest | undefined {
  const file = join(dir, MANIFEST_FILE);
  if (!existsSync(file)) return undefined;
  try {
    const parsed = JSON.parse(readFileSync(file, 'utf8')) as HostManifest;
    if (parsed === null || typeof parsed !== 'object') return undefined;
    if (typeof parsed.hosts !== 'object' || parsed.hosts === null) return undefined;
    return parsed;
  } catch {
    // A manifest we cannot parse is a manifest we cannot vouch for; the caller
    // treats the missing entry as a failed check rather than as permission.
    return undefined;
  }
}

/** Which of the three sources won. */
export type HostSource = 'configured' | 'bundled' | 'path';

/** The binary to run, where it came from, and what it should hash to. */
export interface ResolvedHost {
  command: string;
  source: HostSource;
  /** Set for a bundled host: the target triple it was built for. */
  target?: string;
  /** Set for a bundled host: the digest the manifest recorded. */
  expectedSha256?: string;
}

export interface ResolveHostInput {
  /** `ExtensionContext.extensionUri.fsPath`. */
  extensionPath: string;
  /** The raw `anvil.hostPath` setting; empty means "not set". */
  configured?: string;
  /** The first workspace folder, for a relative `anvil.hostPath`. */
  workspaceRoot?: string;
  platform?: string;
  arch?: string;
}

/** Resolves the sidecar in precedence order: setting, bundle, `PATH`. */
export function resolveHost(input: ResolveHostInput): ResolvedHost {
  const configured = (input.configured ?? '').trim();
  if (configured !== '') {
    return { command: resolveAgainst(configured, input.workspaceRoot), source: 'configured' };
  }

  const target = targetFor(input.platform ?? process.platform, input.arch ?? process.arch);
  if (target) {
    const dir = join(input.extensionPath, BUNDLE_DIR);
    const file = join(dir, target.file);
    if (existsSync(file)) {
      const entry = readHostManifest(dir)?.hosts?.[target.target];
      return { command: file, source: 'bundled', target: target.target, expectedSha256: entry?.sha256 };
    }
  }

  // Nothing bundled for this platform: fall back to a name and let the spawn
  // decide. A missing binary then fails with the spawn's own error, which is
  // the honest one for this case.
  return { command: PATH_HOST, source: 'path' };
}

/** A relative configured path is relative to the workspace, not to the editor. */
function resolveAgainst(command: string, workspaceRoot: string | undefined): string {
  if (isAbsolute(command) || workspaceRoot === undefined) return command;
  return join(workspaceRoot, command);
}

/** Lowercase hex sha256 of a file. Reads it whole: this runs once per spawn. */
export function sha256File(file: string): string {
  return createHash('sha256').update(readFileSync(file)).digest('hex');
}

/**
 * Checks a resolved host against the manifest, returning a refusal message or
 * undefined when it is the binary we shipped.
 *
 * Only a bundled host is checked. A configured path or a `PATH` lookup is the
 * user's own build, and the extension has nothing truthful to compare it with —
 * refusing those would make the developer loop impossible while proving
 * nothing.
 */
export function verifyHost(host: ResolvedHost): string | undefined {
  if (host.source !== 'bundled') return undefined;
  if (!host.expectedSha256) {
    return (
      `the bundled Anvil host ${host.command} is not listed in ${MANIFEST_FILE} for ` +
      `${host.target}: the extension package is incomplete. Reinstall it, or set ` +
      'anvil.hostPath to the binary you meant to run.'
    );
  }
  let actual: string;
  try {
    actual = sha256File(host.command);
  } catch (err) {
    return `could not read the bundled Anvil host ${host.command}: ${(err as Error).message}`;
  }
  if (actual !== host.expectedSha256) {
    return (
      `the bundled Anvil host ${host.command} does not match the checksum recorded when it ` +
      `was built (expected ${short(host.expectedSha256)}, found ${short(actual)}). Reinstall ` +
      'the extension, or set anvil.hostPath to the binary you meant to run.'
    );
  }
  return undefined;
}

function short(hash: string): string {
  return hash.slice(0, 12);
}

/** One line naming the source, for the output channel. */
export function describeHost(host: ResolvedHost): string {
  switch (host.source) {
    case 'configured':
      return `anvil.hostPath: ${host.command}`;
    case 'bundled':
      return `bundled for ${host.target}: ${host.command}`;
    case 'path':
      return `on PATH: ${host.command}`;
  }
}

/**
 * The host's own release version, out of its `anvil-host <version>` banner, or
 * undefined when the banner is not one we recognize.
 */
export function hostVersion(banner: string): string | undefined {
  const match = /^anvil-host\s+v?(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)$/.exec(banner.trim());
  return match?.[1];
}

/**
 * Whether a host belongs to a different release line than this extension.
 *
 * The wire contract is the protocol revision, which `initialize` enforces, so
 * this is deliberately not fatal: a patch difference is fine and a client that
 * refused it would strand anyone running a newer sidecar. But a *minor*
 * difference means the binary in hand is from another release of the product,
 * which is the failure the packaging work makes easy — several platforms, one
 * of them stale — and the one a version banner can catch before a tool call
 * behaves oddly.
 */
export function versionDrift(banner: string, extensionVersion: string): string | undefined {
  const host = hostVersion(banner);
  if (host === undefined) return undefined;
  const hostLine = minor(host);
  const extensionLine = minor(extensionVersion);
  if (hostLine === undefined || extensionLine === undefined || hostLine === extensionLine) {
    return undefined;
  }
  return `${banner} is from Anvil ${hostLine} but this extension is ${extensionVersion}; reinstall the extension, or point anvil.hostPath at a matching anvil-host.`;
}

/** `0.35.7` → `0.35`; undefined when the input is not a dotted version. */
function minor(version: string): string | undefined {
  const match = /^(\d+)\.(\d+)\./.exec(version);
  return match ? `${match[1]}.${match[2]}` : undefined;
}

/** What to suggest when the spawn fails, which depends on where we looked. */
export function hostFixHint(host: ResolvedHost): string {
  switch (host.source) {
    case 'configured':
      return 'check anvil.hostPath';
    case 'bundled':
      return 'the bundled host could not be run; reinstall the extension for this platform';
    case 'path':
      return 'no bundled host for this platform: install anvil-host on PATH, or build it and set anvil.hostPath';
  }
}
