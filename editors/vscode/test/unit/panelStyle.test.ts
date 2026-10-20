/**
 * The five webview documents share one visual grammar, checked at the source.
 *
 * Four manager panels each used to declare the same page — the box model, the
 * body, the heading, the path line, the banner, the controls, the buttons — and
 * four copies drifted. `media/tokens.css` holds the constants and
 * `media/panel.css` the shell; a panel stylesheet keeps only what is its own.
 * What can go wrong here is silent: a panel that loads its own stylesheet before
 * the shell loses on load order, one that redeclares a `--ui-*` value splits the
 * grammar again, and one that still frames every section puts the cards back.
 *
 * There is no browser in this suite, so this is the agreement between files;
 * the visual pass — every panel opened in light, dark and high contrast — is
 * recorded in the phase document.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const read = (...parts: string[]): string => readFileSync(join(ROOT, ...parts), 'utf8');

const TOKENS = read('media', 'tokens.css');
const PANEL = read('media', 'panel.css');
const PANEL_SOURCES: Record<string, string> = {
  permissions: read('src', 'permissionsPanel.ts'),
  tasks: read('src', 'tasksPanel.ts'),
  mcp: read('src', 'mcpPanel.ts'),
  review: read('src', 'reviewPanel.ts'),
};

const PANEL_CSS: Record<string, string> = {
  permissions: read('media', 'permissions.css'),
  tasks: read('media', 'tasks.css'),
  mcp: read('media', 'mcp.css'),
  review: read('media', 'review.css'),
};

test('every panel loads tokens, then the shell, then itself', () => {
  for (const [name, source] of Object.entries(PANEL_SOURCES)) {
    const tokens = source.indexOf(`asset('tokens.css')`);
    const shell = source.indexOf(`asset('panel.css')`);
    const own = source.indexOf(`asset('${name}.css')`);
    assert.ok(tokens > 0, `${name} loads tokens.css`);
    assert.ok(shell > tokens, `${name} loads panel.css after tokens.css`);
    assert.ok(own > shell, `${name} loads its own stylesheet after panel.css`);
  }

  // The chat panel is the fifth document and shares the tokens only: its layout
  // is a full-height column, which the managers' page shell would undo.
  const chat = read('src', 'chatPanel.ts');
  const tokens = chat.indexOf(`asset('tokens.css')`);
  const own = chat.indexOf(`asset('chat.css')`);
  assert.ok(tokens > 0 && own > tokens, 'chat loads tokens.css before chat.css');
  assert.doesNotMatch(chat, /asset\('panel\.css'\)/);
});

test('the tokens are declared once, as variables and not as rules', () => {
  for (const name of [
    'gap',
    'gap-tight',
    'radius',
    'radius-sm',
    'line',
    'line-tight',
    'mono',
    'hairline',
    'muted',
  ]) {
    assert.match(TOKENS, new RegExp(`--ui-${name}\\s*:`), `--ui-${name} is declared`);
  }
  // A token file that starts styling elements is a second shell nobody owns.
  assert.doesNotMatch(TOKENS, /\{[^}]*\b(?:margin|padding|background|color)\s*:/);
  assert.doesNotMatch(TOKENS, /@[a-z-]+\s*[({]/);
});

test('no panel redeclares the shell or a shared token', () => {
  for (const [name, css] of Object.entries(PANEL_CSS)) {
    assert.doesNotMatch(css, /:root\s*\{/, `${name}.css declares no :root tokens`);
    assert.doesNotMatch(css, /(?:^|\n)\*\s*\{/, `${name}.css declares no box model`);
    assert.doesNotMatch(css, /(?:^|\n)body\s*\{/, `${name}.css declares no body rule`);
    assert.doesNotMatch(css, /(?:^|\n)button\s*\{/, `${name}.css declares no button base`);
    assert.doesNotMatch(css, /(?:^|\n)(?:select|input)[^{]*\{/, `${name}.css declares no control base`);
    assert.doesNotMatch(css, /(?:^|\n)\s*--ui-[a-z-]+\s*:/, `${name}.css redefines no --ui-* token`);
  }

  // The shell itself is the one place these appear.
  assert.match(PANEL, /(?:^|\n)\*\s*\{/);
  assert.match(PANEL, /(?:^|\n)body\s*\{/);
  assert.match(PANEL, /(?:^|\n)button\s*\{/);
});

test('a section is divided by a hairline, not framed by a border', () => {
  for (const [name, css] of Object.entries(PANEL_CSS)) {
    assert.match(css, /var\(--ui-hairline\)/, `${name}.css separates with the shared hairline`);
  }

  // The card frame each panel used to draw is gone. `--vscode-panel-border` may
  // still appear through `--ui-hairline`; what this forbids is a panel writing
  // the frame itself, which is how the four cards came back last time.
  for (const [name, css] of Object.entries(PANEL_CSS)) {
    assert.doesNotMatch(
      css,
      /border:\s*1px solid var\(--vscode-panel-border\)/,
      `${name}.css draws no card frame`,
    );
  }
});

test('the review still reads its line colours from the editor theme', () => {
  const css = PANEL_CSS.review;
  assert.match(css, /var\(\s*--vscode-diffEditor-insertedTextBackground/);
  assert.match(css, /var\(\s*--vscode-diffEditor-removedTextBackground/);
});

test('a finished row is quiet, and only a running or failed one is not', () => {
  // The transcript's rule, applied to the tasks list: success is the normal
  // case, so it is drawn in the muted colour rather than in green.
  const css = PANEL_CSS.tasks;
  assert.match(css, /\.status-finished\s*\{[^}]*var\(--ui-muted\)/);
  assert.match(css, /\.status-failed\s*\{[^}]*var\(--vscode-errorForeground\)/);
  assert.match(css, /\.status-running\s*\{[^}]*var\(--vscode-charts-blue/);
});
