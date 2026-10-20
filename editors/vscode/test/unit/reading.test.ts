/**
 * The reading view, formulas and export, checked where they cannot be run.
 *
 * The webview has no test window here: `media/chat.js` is a browser script, and
 * what can be pinned is the properties that would fail silently — a page that
 * fetched a script from the network, a renderer that built its output as HTML,
 * a focus view that hid the approval card, a reload that forgot which view was
 * on, or an export that opened a dialog it could not write to.
 *
 * The KaTeX assertions read the vendored bundle itself rather than trusting the
 * version string: the point of the dependency is that it draws mathematics, and
 * the point of the rule is that it never turns host text into markup.
 */

import { strict as assert } from 'node:assert';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_CSS = readFileSync(join(ROOT, 'media', 'chat.css'), 'utf8');
const CHAT_PANEL = readFileSync(join(ROOT, 'src', 'chatPanel.ts'), 'utf8');
const EXTENSION = readFileSync(join(ROOT, 'src', 'extension.ts'), 'utf8');
const HUB = readFileSync(join(ROOT, 'src', 'hub.ts'), 'utf8');
const PACKAGE = readFileSync(join(ROOT, 'package.json'), 'utf8');
const VENDOR_SCRIPT = readFileSync(join(ROOT, 'scripts', 'vendor-katex.mjs'), 'utf8');

test('the page fetches nothing: the CSP keeps its single source per directive', () => {
  const csp = /content="([^"]+)"/.exec(CHAT_PANEL)?.[1] ?? '';
  assert.match(csp, /^default-src 'none';/);
  // Inline style attributes are what KaTeX's HTML output needs, and the reason
  // the renderer is used in MathML mode instead. `unsafe-inline` would be the
  // cheap way past it; it is not taken.
  assert.doesNotMatch(csp, /unsafe-inline|unsafe-eval/);
  assert.doesNotMatch(csp, /https?:\/\//);
  assert.match(csp, /script-src 'nonce-\$\{nonce\}'/);
});

test('the formula renderer ships with the extension, not from a CDN', () => {
  assert.match(CHAT_PANEL, /src="\$\{asset\('vendor\/katex\/katex\.min\.js'\)\}"/);
  assert.ok(existsSync(join(ROOT, 'media', 'vendor', 'katex', 'katex.min.js')));
  assert.ok(existsSync(join(ROOT, 'media', 'vendor', 'katex', 'LICENSE')));
  // The bundle that ships is the one the script vendors: the version in
  // package.json, the version in VERSION, and the version the script checks.
  const version = /"katex": "([^"]+)"/.exec(PACKAGE)?.[1];
  assert.ok(version, 'katex is a devDependency');
  assert.match(VENDOR_SCRIPT, new RegExp(`EXPECTED = '${version.replace(/\./g, '\\.')}'`));
  assert.match(readFileSync(join(ROOT, 'media', 'vendor', 'katex', 'VERSION'), 'utf8'), new RegExp(`^${version}`));
});

test('the renderer is used so that no HTML is assigned', () => {
  const bundle = readFileSync(join(ROOT, 'media', 'vendor', 'katex', 'katex.min.js'), 'utf8');
  assert.doesNotMatch(bundle, /\binnerHTML\b/);
  // The transcript's own rule, over the whole renderer. The property is spelled
  // as a use rather than a word, because `chat.js` names it in its header — the
  // comment explaining that it is never assigned must not fail the rule that it
  // is never assigned.
  assert.doesNotMatch(CHAT_JS, /\.innerHTML|\[['"]innerHTML['"]\]|insertAdjacentHTML\s*\(|document\.write\s*\(/);

  // MathML output writes no inline styles, which is what lets the CSP stay as
  // strict as it is; `trust: false` removes the commands that could write a
  // link or a class from a formula.
  assert.match(CHAT_JS, /output: 'mathml'/);
  assert.match(CHAT_JS, /trust: false/);
  assert.match(CHAT_JS, /throwOnError: true/);
  // A formula the renderer refuses is its source, not an error box.
  assert.match(CHAT_JS, /node\.textContent = display \? '\$\$' \+ value \+ '\$\$' : '\$' \+ value \+ '\$';/);
});

test('the focus view hides the agent’s side and keeps the question', () => {
  assert.match(CHAT_CSS, /body\.focus-view \.tool,\s*\nbody\.focus-view \.think \{/);
  // An approval card is a question to the reader. Hiding it would leave a turn
  // waiting behind a view the reader cannot see through.
  assert.doesNotMatch(CHAT_CSS, /body\.focus-view \.approval/);
  // The script's anchor rule has to agree with the stylesheet's, or the reader
  // is re-anchored to an element with no box.
  assert.match(CHAT_JS, /node\.classList\.contains\('tool'\) \|\| node\.classList\.contains\('think'\)/);
});

test('the focus flag is the document’s, merged into its saved state', () => {
  assert.match(CHAT_JS, /function remember\(patch\)/);
  assert.match(CHAT_JS, /vscode\.setState\(Object\.assign\(\{\}, state && typeof state === 'object' \? state : \{\}, patch\)\)/);
  assert.match(CHAT_JS, /remember\(\{ focus: on \}\)/);
  // Both writers merge: the agent id and the view are written at different
  // moments, and either replacing the object would erase the other.
  assert.match(CHAT_JS, /remember\(\{ agentId: msg\.agentId \|\| undefined \}\)/);
  assert.doesNotMatch(CHAT_JS, /vscode\.setState\(\{ agentId/);

  // The initial value comes from the page the host rendered, which is how a
  // reload comes back in the same view.
  assert.match(CHAT_JS, /let focusView = document\.body\.classList\.contains\('focus-view'\)/);
  assert.match(CHAT_PANEL, /<body\$\{focus \? ' class="focus-view"' : ''\}>/);
  assert.match(CHAT_PANEL, /renderHtml\(panel\.webview, this\.context\.extensionUri, this\.focusView\)/);
});

test('the palette command and Ctrl+Alt+F ask the document to flip', () => {
  assert.match(EXTENSION, /registerCommand\('anvil\.toggleFocusView'/);
  assert.match(EXTENSION, /hub\.activePanel\(\)\?\.toggleFocusView\(\)/);
  assert.match(CHAT_PANEL, /toggleFocusView\(\): void \{\s*\n\s*this\.post\(\{ type: 'toggleFocus' \}\);/);
  assert.match(CHAT_JS, /case 'toggleFocus':[\s\S]{0,160}setFocus\(!focusView\)/);

  assert.match(PACKAGE, /"command": "anvil\.toggleFocusView"/);
  assert.match(PACKAGE, /"key": "ctrl\+alt\+f"/);
  // Scoped to the chat document, so the key is not taken from an editor that
  // has nothing to toggle.
  assert.match(PACKAGE, /"when": "activeWebviewPanelId == anvil\.chat"/);
});

test('a restored tab comes back in the view it was left in', () => {
  assert.match(EXTENSION, /state: \{ agentId\?: unknown; focus\?: unknown \}/);
  assert.match(EXTENSION, /hub\.createPanel\(agentId, state\?\.focus === true\)/);
  assert.match(HUB, /createPanel\(agentId\?: string, focus = false\)/);
});

test('/export writes Markdown through the workspace, not the screen', () => {
  assert.match(CHAT_PANEL, /case 'export':\s*\n\s*await this\.exportConversation\(\);/);
  const start = CHAT_PANEL.indexOf('private async exportConversation(');
  assert.ok(start >= 0);
  const body = CHAT_PANEL.slice(start, CHAT_PANEL.indexOf('\n  /** Adopts a named agent', start));
  // The store is the source: the screen may hold only part of a replayed
  // conversation.
  assert.match(body, /this\.hub\.transcript\(agentId\)/);
  assert.match(body, /showSaveDialog\(/);
  assert.match(body, /anvil-conversation-\$\{date\}\.md/);
  assert.match(body, /vscode\.workspace\.fs\.writeFile\(target, Buffer\.from\(text, 'utf8'\)\)/);
  // A cancelled dialog writes nothing and is not an error.
  assert.match(body, /if \(!target\) return;/);
});
