/**
 * The words the two halves of the mode ladder have to agree on.
 *
 * A mode is a wire value with three names: the host's constant in `proto.go`,
 * the client's entry in `protocol.ts`, and the label a person reads. None of
 * them is generated from another, and every way they can disagree is quiet — a
 * mode the Go side adds is a mode nobody can choose, a mode with no `<option>`
 * is one the select cannot show, and a mode with no label renders as its own
 * wire value, which reads like a bug rather than a mode. This is the same kind
 * of check as `permissionKinds.test.ts`, for the same reason: the failures are
 * silent, and a test is cheaper than being careful.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const ROOT = join(EXTENSION_ROOT, '..', '..');
const PROTOCOL_GO = readFileSync(join(ROOT, 'source', 'internal', 'editorhost', 'proto.go'), 'utf8');
const PROTOCOL_TS = readFileSync(join(EXTENSION_ROOT, 'src', 'protocol.ts'), 'utf8');
const PANEL_TS = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');

/** Every mode the host has, in the order its own list declares them. */
function hostModes(): string[] {
  const list = /var PermissionModes = \[\]string\{([^}]*)\}/.exec(PROTOCOL_GO)?.[1];
  assert.ok(list, 'PermissionModes is a list in proto.go');
  return list
    .split(',')
    .map((name) => name.trim())
    .filter(Boolean)
    .map((name) => {
      const constant = new RegExp(`\\b${name}\\s*=\\s*"(\\w+)"`).exec(PROTOCOL_GO)?.[1];
      assert.ok(constant, `${name} is a constant in proto.go`);
      return constant;
    });
}

/** Every mode the client knows, in the order its own list declares them. */
function clientModes(): string[] {
  const list = /export const PermissionModes = \[([^\]]*)\] as const/.exec(PROTOCOL_TS)?.[1];
  assert.ok(list, 'PermissionModes is a list in protocol.ts');
  return [...list.matchAll(/'([\w]+)'/g)].map((m) => m[1]);
}

test('the ladder is the same list, in the same order, on both sides', () => {
  const modes = hostModes();
  // Pinned rather than derived: this is the milestone's whole content, and a
  // mode appearing or disappearing without a docs change is what the pin is for.
  assert.deepEqual(modes, ['ask', 'accept_edits', 'plan', 'dont_ask', 'bypass']);
  assert.deepEqual(clientModes(), modes);
});

test('every mode has a label for the user and an option in the select', () => {
  const table = /const MODE_LABELS: Record<PermissionMode, string> = \{([\s\S]*?)\n\};/.exec(PANEL_TS)?.[1];
  assert.ok(table, 'the label table is in chatPanel.ts');
  const labelled = [...table.matchAll(/^\s*(\w+):/gm)].map((m) => m[1]);
  const modes = hostModes();
  assert.deepEqual(labelled.sort(), [...modes].sort(), 'every mode is labelled, and nothing else is');

  // The select offers each mode by its wire value, plus the empty entry that
  // means "let the workspace decide" — which is not a mode and has no label.
  const select = /<select id="mode"[\s\S]*?<\/select>/.exec(PANEL_TS)?.[0];
  assert.ok(select, 'the mode select is in the panel markup');
  const options = [...select.matchAll(/<option value="([\w]*)"/g)].map((m) => m[1]);
  assert.deepEqual(options.filter((v) => v !== '').sort(), [...modes].sort(), 'every mode is selectable');
  assert.equal(options[0], '', 'the workspace-default entry comes first');
});

test('the two mirrors say the same thing, in the same words', () => {
  // media/ cannot import from src/, so the labels exist twice. Repeating a list
  // is one thing; letting the repeats drift is another, and this is the line
  // that stops it.
  const panel = /const MODE_LABELS: Record<PermissionMode, string> = \{([\s\S]*?)\n\};/.exec(PANEL_TS)?.[1] ?? '';
  const script = /const modeLabels = \{([\s\S]*?)\n {2}\};/.exec(CHAT_JS)?.[1] ?? '';
  const entries = (block: string) => [...block.matchAll(/^\s*(\w+):\s*'([^']*)'/gm)].map((m) => `${m[1]}=${m[2]}`).sort();
  assert.ok(panel && script, 'both label tables are present');
  assert.deepEqual(entries(script), entries(panel));
});

test('the mode message carries both the choice and what it resolved to', () => {
  // The client's stored choice and the mode in force are different things — the
  // workspace's permissions.json can supply the second — so a renderer given
  // only one of them cannot show the select and the banner at the same time.
  assert.match(PANEL_TS, /type: 'mode'; mode: PermissionMode \| ''; resolved: PermissionMode/);
  assert.match(CHAT_JS, /msg\.mode/);
  assert.match(CHAT_JS, /msg\.resolved/);
  // The no-asking banner is the mirror of the bypass one, and is shown from the
  // resolved mode rather than the stored one: a workspace that declared it is
  // still running under it.
  assert.match(PANEL_TS, /id="dontAsk"/);
  assert.match(CHAT_JS, /dontAskStrip/);
});

test('the refusal fields the host sends are the ones the client reads', () => {
  for (const field of ['permission_mode_source', 'permission_mode_refused', 'default_mode', 'default_mode_refused']) {
    assert.match(PROTOCOL_GO, new RegExp(`"${field}`), `${field} in proto.go`);
    assert.match(PROTOCOL_TS, new RegExp(`\\b${field}\\b`), `${field} in protocol.ts`);
  }
  // Where a mode came from is a closed set on both sides: `resolveMode` returns
  // one of these three words, and a fourth would be a source the client cannot
  // render.
  const sources = hostSourceConstants().sort();
  assert.deepEqual(sources, ['default', 'file', 'request']);
  const client = [...PROTOCOL_TS.matchAll(/export const ModeSource\w+ = '(\w+)'/g)].map((m) => m[1]).sort();
  assert.deepEqual(client, sources, 'the client knows the same three sources');
});

/** The ModeSource* constants proto.go declares, by value. */
function hostSourceConstants(): string[] {
  return [...PROTOCOL_GO.matchAll(/ModeSource\w+ = "(\w+)"/g)].map((m) => m[1]);
}
