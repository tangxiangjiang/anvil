/**
 * The words the two halves of the rule feature have to agree on.
 *
 * `src/protocol.ts` is a mirror of the Go protocol and `media/chat.js` is a
 * renderer over it, and neither is generated: a name the Go side renames is a
 * silent `undefined` here, and a kind the host invents is a button with no
 * label. Both failures are quiet, both are cheap to catch statically, and both
 * have happened in this repository before — which is why the check is a test
 * rather than a comment asking people to be careful.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

/** The extension, then the repository the SDK lives in beside it. */
const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const ROOT = join(EXTENSION_ROOT, '..', '..');
const PROTOCOL_GO = readFileSync(join(ROOT, 'source', 'internal', 'editorhost', 'proto.go'), 'utf8');
const PROTOCOL_TS = readFileSync(join(EXTENSION_ROOT, 'src', 'protocol.ts'), 'utf8');
const RULES_GO = readFileSync(join(ROOT, 'source', 'permissionrules.go'), 'utf8');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');

test('the permission methods travel under the names both sides spell', () => {
  for (const method of ['permissions.get', 'permissions.add']) {
    assert.match(PROTOCOL_GO, new RegExp(`"${method}"`), `${method} in proto.go`);
    assert.match(PROTOCOL_TS, new RegExp(`'${method}'`), `${method} in protocol.ts`);
  }
});

test('every "always allow" kind the SDK offers has a label in the card', () => {
  // The kinds are the SDK's; the labels are the card's. A kind with no label
  // renders as the generic fallback, and a user offered "always allow this"
  // cannot tell what they are agreeing to.
  const kinds = [...RULES_GO.matchAll(/PermissionOption\w+\s*=\s*"(\w+)"/g)].map((m) => m[1]);
  assert.deepEqual(kinds.sort(), ['exact', 'path', 'prefix', 'readonly', 'tool']);

  const table = /const REMEMBER_LABELS = \{([\s\S]*?)\n {2}\};/.exec(CHAT_JS)?.[1];
  assert.ok(table, 'the label table is in chat.js');
  const labelled = [...table.matchAll(/^\s*(\w+):/gm)].map((m) => m[1]);
  assert.deepEqual(labelled.sort(), kinds.sort());
});

test('the fields a card and a tool card read are the ones the host sends', () => {
  // `remember` on an approval and `rule` on an event are the two additions this
  // phase turns on; both are read by name in the renderer.
  assert.match(CHAT_JS, /request\.remember/);
  assert.match(CHAT_JS, /event\.rule/);
  for (const field of ['remember', 'decided_by', 'describe']) {
    assert.match(PROTOCOL_GO, new RegExp(`"${field}`), `${field} in proto.go`);
    assert.match(PROTOCOL_TS, new RegExp(`\\b${field}\\b`), `${field} in protocol.ts`);
  }
});
