/**
 * A tool result's tab: what it is called, and that it is a document rather
 * than a file.
 *
 * The naming is a pure function, so it is run here; the provider needs a
 * window, so its promises are read as source. Together they cover the two ways
 * this feature fails silently: a title that a URI cannot hold, and a document
 * shown before its text was registered.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

import { TOOL_OUTPUT_SCHEME, outputPath, outputSuffix } from '../../src/toolOutputName';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const PROVIDER = readFileSync(join(EXTENSION_ROOT, 'src', 'toolOutput.ts'), 'utf8');

test('the tab is named after the call, in a path a URI can hold', () => {
  const path = outputPath('Grep', 'call_z83pum');
  assert.match(path, /^\/Grep output \([a-z0-9]{6}\)$/);
  // The same call always names the same tab; a different call does not.
  assert.equal(outputPath('Grep', 'call_z83pum'), path);
  assert.notEqual(outputPath('Grep', 'call_other'), path);

  // A slash would make the title a nested path, and the tab would show one
  // segment of it; the query and fragment markers would end the title early.
  assert.match(outputPath('Read src/parser.ts', 'x'), /^\/Read src-parser\.ts output /);
  assert.match(outputPath('Ask #1?', 'x'), /^\/Ask -1- output /);

  // An unnamed or absurdly long tool still names a tab.
  assert.equal(outputPath('', 'x'), `/Tool output (${outputSuffix('x')})`);
  assert.equal(outputPath('   ', 'x'), `/Tool output (${outputSuffix('x')})`);
  assert.ok(outputPath('x'.repeat(80), 'id').length <= 60);
});

test('the suffix is short, stable, and made of path-safe characters', () => {
  assert.equal(outputSuffix('call_z83pum').length, 6);
  assert.match(outputSuffix('call_z83pum'), /^[a-z0-9]{6}$/);
  assert.equal(outputSuffix(''), outputSuffix(''));
  assert.match(outputSuffix(''), /^[a-z0-9]{6}$/);
  assert.notEqual(outputSuffix('a'), outputSuffix('b'));
});

test('the provider serves a read-only document, one tab per call', () => {
  assert.equal(TOOL_OUTPUT_SCHEME, 'anvil-tool-output');
  assert.match(
    PROVIDER,
    /registerTextDocumentContentProvider\(TOOL_OUTPUT_SCHEME, this\)/,
  );
  assert.match(PROVIDER, /provideTextDocumentContent\(uri: vscode\.Uri\): string/);
  // The text is registered before the document is shown, so the first render
  // cannot race an empty answer.
  const open = PROVIDER.slice(PROVIDER.indexOf('async open('));
  assert.ok(open.indexOf('this.contents.set(at, text)') < open.indexOf('showTextDocument'));
  // The call id is part of the URI, so a second click reveals the same tab.
  assert.match(PROVIDER, /\?id=\$\{encodeURIComponent\(key\)\}/);
  assert.match(PROVIDER, /preview: true/);
  assert.match(PROVIDER, /showTextDocument\(uri/);
  // Kept documents are bounded; an aged-out URI answers, empty, rather than
  // throwing from the provider.
  assert.match(PROVIDER, /const KEPT = 16/);
  assert.match(open, /this\.contents\.size > KEPT/);
  assert.match(PROVIDER, /this\.contents\.get\(uri\.toString\(\)\) \?\? ''/);
  // Nothing writes: with no FileSystemProvider the scheme is read-only.
  assert.doesNotMatch(PROVIDER, /writeFile|createFileSystemProvider/);
});
