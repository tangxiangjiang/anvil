/**
 * The transcript's link policy, checked across the two files that implement it.
 *
 * Typesetting the model's reply put a new kind of value on screen: a URL. Every
 * other field the panel renders is text, and text cannot do anything — which is
 * why `innerHTML` is banned everywhere and why that ban was enough until now. A
 * URL is not text. `<a href="javascript:...">` is live in a webview whatever the
 * CSP says, so the renderer refuses anything but `http:` and `https:` and the
 * extension refuses them again before `vscode.env.openExternal` hands the string
 * to the operating system.
 *
 * Neither half can be exercised here — there is no DOM and no extension host in
 * this suite — so what is checked is the property that *is* checkable and that a
 * tidy-up would quietly remove: that both halves still exist, and that they
 * agree on what they allow. A single-sided check is not half the protection; it
 * is none, because the side that keeps it is not the side an attacker reaches.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');

/**
 * Every literal pattern in `source` that looks like a URL scheme test.
 *
 * A regex over a regex: the search finds a `/…/` literal in the file and returns
 * its body. `[^\n]*` is greedy and is meant to be — with several `/` on the line
 * it lands on the last one, the closing delimiter, rather than stopping at an
 * escaped `\/` in the middle of the pattern.
 */
function schemePatterns(source: string): string[] {
  return [...source.matchAll(/\/(\^https\?[^\n]*)\/([a-z]*)/g)].map((match) => match[1]);
}

test('the webview refuses to render a link it will not open', () => {
  // No `href` is ever set, so there is nothing for the browser to follow even by
  // accident — a middle-click or a context-menu "open" on a real anchor would
  // navigate the webview away and take the conversation with it. The reconciler
  // compares two spans with `.href ===`, which is why this has to single out an
  // assignment rather than any mention.
  assert.doesNotMatch(CHAT_JS, /\.href\s*=(?!=)/, 'chat.js must not assign an href');
  assert.doesNotMatch(CHAT_JS, /setAttribute\(\s*'href'/, 'nor set the attribute');
  assert.match(CHAT_JS, /setAttribute\(\s*'role'/, 'a link is a span wearing a link\'s role');
  const patterns = schemePatterns(CHAT_JS);
  assert.equal(patterns.length, 1, 'chat.js must test the scheme in exactly one place');
});

test('the extension tests the scheme again before opening anything', () => {
  const patterns = schemePatterns(CHAT_PANEL);
  assert.equal(patterns.length, 1, 'chatPanel.ts must test the scheme in exactly one place');
  assert.match(CHAT_PANEL, /openExternal/, 'the extension is the side that opens it');
});

test('both halves allow exactly the same schemes', () => {
  // The duplication is deliberate — the webview's check is presentation and the
  // extension's is the boundary — but a divergence would be invisible: the
  // extension would simply stop opening links the transcript offers, or start
  // opening ones it used to refuse.
  assert.deepEqual(schemePatterns(CHAT_JS), schemePatterns(CHAT_PANEL));
});

test('opening a link is a message to the extension, not a navigation', () => {
  // If the webview ever opened links itself, the extension's check would become
  // decorative and this test's premise would be false.
  assert.match(CHAT_JS, /postMessage\(\{\s*type:\s*'openLink'/);
  assert.match(CHAT_PANEL, /case 'openLink'/);
});
