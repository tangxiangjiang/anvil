/**
 * One root per window, checked as text.
 *
 * There is no VS Code in this suite, so what can be checked is the property the
 * phase is about: no module picks a folder for itself. A `workspaceFolders[0]`
 * anywhere outside the resolver is a conversation silently bound to whichever
 * folder the window happened to list first, and in a multi-root window that is
 * the wrong folder half the time.
 *
 * The rest of the file pins the decisions that are invisible until they go
 * wrong: the picker is asked once and not by every concurrent caller, a cancel
 * creates nothing, a single-folder window never asks, and the header names the
 * root without offering to change it.
 */

import { strict as assert } from 'node:assert';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const SRC = join(EXTENSION_ROOT, 'src');
const RESOLVER = readFileSync(join(SRC, 'workspaceRoot.ts'), 'utf8');
const MANAGER = readFileSync(join(SRC, 'hostManager.ts'), 'utf8');
const CHAT_PANEL = readFileSync(join(SRC, 'chatPanel.ts'), 'utf8');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_CSS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.css'), 'utf8');
const EXTENSION = readFileSync(join(SRC, 'extension.ts'), 'utf8');
const CHANGES = readFileSync(join(SRC, 'changes.ts'), 'utf8');
const EDITOR_CONTEXT = readFileSync(join(SRC, 'editorContext.ts'), 'utf8');
const HUB = readFileSync(join(SRC, 'hub.ts'), 'utf8');

function sources(): Array<{ name: string; text: string }> {
  return readdirSync(SRC)
    .filter((name) => name.endsWith('.ts'))
    .map((name) => ({ name, text: readFileSync(join(SRC, name), 'utf8') }));
}

test('no module chooses a folder for itself', () => {
  // The resolver is the one place allowed to read the window's folder list. A
  // first-folder fallback elsewhere would be the exact bug this phase removes.
  const offenders = sources()
    .filter((file) => file.name !== 'workspaceRoot.ts')
    .filter((file) => /workspaceFolders\s*\??\.\s*\[0\]/.test(file.text))
    .map((file) => file.name);
  assert.deepEqual(offenders, []);
  // And every path decision goes through it.
  for (const [file, text] of [
    ['changes.ts', CHANGES],
    ['editorContext.ts', EDITOR_CONTEXT],
    ['hub.ts', HUB],
    ['chatPanel.ts', CHAT_PANEL],
    ['hostManager.ts', MANAGER],
  ] as const) {
    assert.match(text, /from '\.\/workspaceRoot'/, `${file} does not use the resolver`);
  }
  assert.match(CHANGES, /const root = rootUri\(\);\s*if \(!root\) return undefined;\s*[\s\S]*?joinPath\(root, \.\.\.segments\)/);
  assert.match(EDITOR_CONTEXT, /const folder = rootFolder\(\);/);
  assert.match(HUB, /const folder = rootUri\(\);\s*if \(!folder \|\| folder\.scheme !== 'file'\) return undefined;/);
});

test('a single-folder window never asks', () => {
  // The single-root case is the common one and has no decision in it: the
  // folder is the window. Checked before the picker is even mentioned.
  const single = /if \(all\.length === 1\) \{[\s\S]*?return Promise\.resolve\(all\[0\]\);[\s\S]*?\}/.exec(RESOLVER);
  assert.ok(single, 'resolveWorkspaceRoot does not answer the single-folder case first');
  assert.doesNotMatch(single[0], /showQuickPick|workspaceState/);
  // The synchronous read agrees, so a path decision before any session is still
  // the only folder there is.
  assert.match(RESOLVER, /if \(all\.length === 1\) return all\[0\];/);
});

test('the picker is asked once and cancelled choice creates nothing', () => {
  // One in-flight question: two callers awaiting one answer, not two pickers
  // racing to overwrite each other's state.
  assert.match(RESOLVER, /if \(!choosing\) \{\s*choosing = ask\(context, all\)\.finally/);
  assert.match(RESOLVER, /return choosing;/);
  // Dismissing the picker is a distinct outcome, not an empty folder: the
  // callers that must stay quiet can tell it apart with `instanceof`.
  assert.match(RESOLVER, /class RootChoiceCancelled extends Error/);
  assert.match(RESOLVER, /if \(!choice\) throw new RootChoiceCancelled\(\);/);
  assert.match(RESOLVER, /this\.name = 'RootChoiceCancelled';/);
  // The remembered folder is preselected, so the second window is one keypress.
  assert.match(RESOLVER, /picked: remembered !== undefined && folder\.uri\.toString\(\) === remembered\.uri\.toString\(\)/);
  // The choice is stored only after one was made, and it is the folder's URI so
  // a renamed folder is not a different folder.
  assert.match(RESOLVER, /active = choice\.folder;\s*await context\.workspaceState\.update\(STATE_KEY, active\.uri\.toString\(\)\);/);
  // A remembered entry for a folder this window does not have is ignored; the
  // picker asks rather than carrying the path somewhere it cannot go.
  assert.match(RESOLVER, /return folders\(\)\.find\(\(folder\) => folder\.uri\.toString\(\) === value\);/);
});

test('the host is spawned at the window root, and a cancel starts nothing', () => {
  assert.match(MANAGER, /const folder = await resolveWorkspaceRoot\(this\.context\);/);
  assert.match(MANAGER, /if \(!folder\) throw new Error\('open a folder to use Anvil'\);/);
  assert.match(MANAGER, /const cwd = folder\.uri\.fsPath;/);
  // The resolver runs before the child exists: a cancelled picker throws with no
  // process, no handshake and no session behind it.
  const spawn = /private async spawn\(\): Promise<HostClient> \{[\s\S]*?\n {2}\}/.exec(MANAGER);
  assert.ok(spawn);
  const resolved = spawn[0].indexOf('await resolveWorkspaceRoot');
  const spawned = spawn[0].indexOf('new HostClient(');
  assert.ok(resolved > 0 && spawned > resolved, 'the root is resolved before the child is created');
  // And the panel is quiet about the cancel: the drop path narrows it out.
  assert.match(CHAT_PANEL, /if \(err instanceof RootChoiceCancelled\) return undefined;/);
});

test('the header names the root and offers no switch', () => {
  // The extension half: the root travels once, and again when the session that
  // chose it exists.
  assert.match(CHAT_PANEL, /\| \{ type: 'root'; name: string; path: string; multi: boolean \}/);
  assert.match(CHAT_PANEL, /private postRoot\(\): void \{[\s\S]*?type: 'root', name: info\.name, path: info\.path, multi: info\.multi/);
  assert.match(CHAT_PANEL, /this\.postAgentId\(\);\s*this\.postRoot\(\);/);
  assert.match(CHAT_PANEL, /onSession = \(result: SessionResult\): void => \{[\s\S]*?this\.postRoot\(\);/);
  // The document has a line, not a control: one `<div id="root">` and no
  // select or button that could move the conversation's folder.
  assert.match(CHAT_PANEL, /<div id="root" class="root" hidden><\/div>/);
  const line = /<div id="root"[\s\S]*?<\/div>/.exec(CHAT_PANEL);
  assert.ok(line);
  assert.doesNotMatch(line[0], /<select|<button/);
  // The webview renders it with textContent, keeps the path in the tooltip, and
  // says the one-folder rule only where it is not obvious.
  assert.match(CHAT_JS, /case 'root':\s*renderRoot\(msg\);/);
  const render = /function renderRoot\(msg\) \{[\s\S]*?\n {2}\}/.exec(CHAT_JS);
  assert.ok(render);
  assert.doesNotMatch(render[0], /innerHTML/);
  assert.match(render[0], /rootLine\.textContent = '';/);
  assert.match(render[0], /folder\.title = path;/);
  assert.match(render[0], /if \(msg\.multi === true\) \{[\s\S]*?'one folder per window'/);
  assert.match(render[0], /other folders are not part of it; open another window/);
  // And it is styled as one muted line above the status.
  assert.match(CHAT_CSS, /^\.root \{/m);
  assert.match(CHAT_CSS, /\.root \.root-name \{/);
});

test('the extension installs the root before anything can be restored', () => {
  // A restored panel asks for its root during activation, and `workspaceState`
  // is read there: an install after the first `await` would miss it.
  assert.match(EXTENSION, /import \{ initWorkspaceRoot \} from '\.\/workspaceRoot';/);
  const activate = /export function activate\(context: vscode\.ExtensionContext\): void \{[\s\S]*?const hub = new Hub\(/.exec(EXTENSION);
  assert.ok(activate);
  assert.match(activate[0], /initWorkspaceRoot\(context\);/);
  assert.doesNotMatch(activate[0], /await /);
  // The listener drops a folder the window no longer has, so the next question
  // is asked rather than answered with a path that is gone.
  assert.match(RESOLVER, /onDidChangeWorkspaceFolders\(\(\) => \{[\s\S]*?active = undefined;/);
});
