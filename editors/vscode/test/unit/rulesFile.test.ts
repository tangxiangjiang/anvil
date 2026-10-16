/**
 * The bytes the extension writes for a rules file, and the place it points at
 * when one will not compile.
 *
 * Both are contracts with something outside the extension: the file is read by
 * `permissions.Load` in the SDK, and the place is what makes an error message
 * clickable in the editor. Neither needs a window, which is why they live in a
 * module with no `vscode` import and are tested here.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import {
  RULES_FILE_VERSION,
  describeProblem,
  emptyRulesFile,
  problemPosition,
} from '../../src/rulesFile';

/** The extension, then the repository the SDK lives in beside it. */
const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const ROOT = join(EXTENSION_ROOT, '..', '..');

test('a new rules file is one the SDK reads', () => {
  const body = emptyRulesFile();
  const parsed = JSON.parse(body) as { version: number; rules: unknown[] };
  // The version the SDK's loader accepts; a file written without one, or with
  // a version it refuses, would be rejected the first time it is read.
  assert.equal(parsed.version, RULES_FILE_VERSION);
  const go = readFileSync(join(ROOT, 'source', 'internal', 'permissions', 'rule.go'), 'utf8');
  assert.match(go, new RegExp(`Version\\s*=\\s*${RULES_FILE_VERSION}\\b`));
  // A file rather than nothing at all: an empty policy is a state the user
  // asked to see, and an empty editor is not that state.
  assert.deepEqual(parsed.rules, []);
  assert.ok(body.endsWith('\n'), 'a text file ends with a newline');
});

test('a problem is reported at the place the parser counted from one', () => {
  // The parser counts lines and columns from one; a `vscode.Position` counts
  // from zero. Off by one here is a squiggle on the wrong line, which is worse
  // than none: it points somewhere the user then edits.
  assert.deepEqual(problemPosition({ message: 'x', line: 4, column: 12 }), { line: 3, column: 11 });
});

test('a problem with no place points at the top of the file', () => {
  // A version this build does not read, or a key it does not know: the parser
  // reports no position, and the top of the file is where a reader starts.
  assert.deepEqual(problemPosition({ message: 'unknown version' }), { line: 0, column: 0 });
  assert.deepEqual(problemPosition({ message: 'unknown version', line: 0, column: 0 }), {
    line: 0,
    column: 0,
  });
});

test('a problem reads as one line naming the file and the place', () => {
  const path = '/w/.anvil/permissions.json';
  assert.equal(
    describeProblem(path, { message: 'effect "maybe" is not one of allow, ask, deny', line: 4, column: 12 }),
    `${path}:4:12: effect "maybe" is not one of allow, ask, deny`,
  );
  // With no place, the path alone — and no stray ":0:0" for the reader to
  // wonder about.
  assert.equal(describeProblem(path, { message: 'unknown version' }), `${path}: unknown version`);
});
