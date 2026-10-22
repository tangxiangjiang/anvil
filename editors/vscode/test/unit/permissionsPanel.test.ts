/**
 * The permissions manager's wiring, checked as text.
 *
 * There is no webview and no host child in this suite, so what can be checked is
 * where each half is wired and what it must not do. Four of those are the ones
 * whose failure is silent: a second write verb beside `permissions.set` (two
 * writers of one file), an `innerHTML` anywhere near a rule, a `bypass` option in
 * a select that writes a project file, and a Save that stays enabled over a file
 * the parser refused.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const ROOT = join(EXTENSION_ROOT, '..', '..');
const PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'permissionsPanel.ts'), 'utf8');
const DOC = readFileSync(join(EXTENSION_ROOT, 'media', 'permissions.js'), 'utf8');
const HUB = readFileSync(join(EXTENSION_ROOT, 'src', 'hub.ts'), 'utf8');
const MANAGER = readFileSync(join(EXTENSION_ROOT, 'src', 'hostManager.ts'), 'utf8');
const EXTENSION = readFileSync(join(EXTENSION_ROOT, 'src', 'extension.ts'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');
const PROTOCOL_TS = readFileSync(join(EXTENSION_ROOT, 'src', 'protocol.ts'), 'utf8');
const PROTOCOL_GO = readFileSync(join(ROOT, 'source', 'internal', 'editorhost', 'proto.go'), 'utf8');
const SERVER_GO = readFileSync(join(ROOT, 'source', 'internal', 'editorhost', 'server.go'), 'utf8');
const PACKAGE = JSON.parse(readFileSync(join(EXTENSION_ROOT, 'package.json'), 'utf8')) as {
  contributes: { commands: Array<{ command: string; title: string }> };
};

test('the manager writes with permissions.set and no other verb', () => {
  // The panel asks the hub; the hub asks the manager; the manager sends the one
  // method. A second write verb in this path would be a second writer of one
  // file, and the two would eventually disagree about its shape.
  assert.match(PANEL, /savePermissionRules\(/);
  assert.doesNotMatch(PANEL, /permissions\.add|PermissionsAdd|Method\./);
  assert.match(HUB, /savePermissionRules\(params: PermissionsSetParams\): Promise<PermissionsResult> \{\s*return this\.manager\.savePermissionRules\(params\);/);
  assert.match(MANAGER, /client\.request<PermissionsResult>\(Method\.PermissionsSet, params\)/);
  // Both halves name the method the same way, and the host has it.
  assert.match(PROTOCOL_TS, /PermissionsSet: 'permissions\.set'/);
  assert.match(PROTOCOL_GO, /MethodPermissionsSet = "permissions\.set"/);
  assert.match(SERVER_GO, /case MethodPermissionsSet:/);
});

test('the document renders data, never markup', () => {
  // The comment says the word too, so the check is for a use of it rather than
  // for the word: no assignment, no append.
  assert.doesNotMatch(DOC, /\.innerHTML/);
  assert.doesNotMatch(DOC, /innerHTML\s*[=+]/);
  assert.match(DOC, /textContent/);
  assert.match(DOC, /acquireVsCodeApi/);
  // Rule values travel as an input's value or a text node; they are never
  // concatenated into a tag.
  assert.match(DOC, /input\.value = rule\[name\] \|\| ''/);
});

test('a project file cannot be given bypass from the form', () => {
  // The select is built from the protocol's ladder minus bypass, and the document
  // skips the word even if a host sent it.
  assert.match(PANEL, /PermissionModes\.filter\(\(mode\) => mode !== 'bypass'\)/);
  assert.match(DOC, /mode === 'bypass'\) continue;/);
  // The authoritative refusal is the host's, and it is the sentence get and the
  // session result use.
  assert.match(SERVER_GO, /return nil, errInvalidParams\("permissions\.set: " \+ refusedMode\(mode\)\)/);
  assert.match(SERVER_GO, /which a project file may not grant/);
});

test('a file that will not parse is read-only, Save included', () => {
  // A problem in the state message turns that layer's controls off; its Save is
  // then disabled by the same flag, so the form cannot overwrite the text it is
  // showing. The two layers are refused independently (0.46): a broken user file
  // must not freeze the workspace's rules, and the other way round.
  assert.match(DOC, /editable\.workspace = !problem && !failure;/);
  assert.match(DOC, /editable\.user = hasUser && userExists && !userProblem && !failure;/);
  assert.match(DOC, /save\.disabled = !editable\[scope\] \|\| saving \|\| !dirty\[scope\];/);
  assert.match(DOC, /Editing is off so the file is not overwritten/);
  assert.match(DOC, /if \(!editable\[scope\]\)/);
  assert.match(DOC, /'Read-only until the problem is fixed\.'/);
  // And the host refuses independently: a client that skipped the form does not
  // get to replace a file this build could not read. Both layers are named.
  assert.match(SERVER_GO, /anvil\.LoadPermissionRuleFile\(cwd\); err != nil \{\s*return nil, errCode\(CodeInvalidRequest/);
  assert.match(SERVER_GO, /anvil\.LoadUserPermissionRuleFile\(\); err != nil \{\s*return nil, errCode\(CodeInvalidRequest/);
});

test('the two rule files are drawn as two sections, saved one at a time', () => {
  // permissions.get answers one list with each rule's source; the panel splits
  // it where the grouping decision belongs, and a host without scopes (no
  // source) reads as the workspace's only layer.
  assert.match(PANEL, /userRules: rowsFor\(state\.rules, 'user'\)/);
  assert.match(PANEL, /workspaceRules: rowsFor\(state\.rules, 'workspace'\)/);
  assert.match(PANEL, /rule\.source === 'user' : rule\.source !== 'user'/);
  assert.match(DOC, /hasUser = userPath !== ''/);
  assert.match(DOC, /if \(hasUser\) userSection\(msg, userPath, userExists, userProblem\);/);
  assert.match(DOC, /el\('section', 'scope user'\)/);
  assert.match(DOC, /el\('section', 'scope workspace'\)/);

  // A save names the layer it replaces. The user layer carries protected globs
  // and never a starting mode; the workspace layer is the reverse — the host
  // refuses each other's field, so the document must not send both shapes.
  assert.match(PANEL, /source: 'user',\s*rules,\s*protected: protectedPaths/);
  assert.match(PANEL, /source: 'workspace',\s*rules,\s*default_mode:/);
  assert.match(DOC, /type: 'save',\s*source: 'user',\s*rules,\s*protected:/);
  assert.match(DOC, /type: 'save',\s*source: 'workspace',\s*rules,\s*defaultMode:/);

  // A missing user file is read-only until a person asks for it: the create
  // step is its own message with create:true, and the host refuses a save that
  // did not ask.
  assert.match(DOC, /if \(!userExists && !userProblem\)/);
  assert.match(DOC, /source: 'user', create: true, rules: \[\], protected: \[\]/);
  assert.match(DOC, /Create user file/);
  assert.match(SERVER_GO, /p\.Create/);
});

test('protected paths are a user-file field and are shown as one', () => {
  // The form offers them only where the host accepts them, and says what the
  // fixed list already covers — a person adding "secrets/**" should not think
  // they have to add ".git" too.
  assert.match(DOC, /id = 'protected-paths'/);
  assert.match(DOC, /\.git, \.anvil\/checkpoints\/ and the Anvil home are always protected/);
  assert.match(DOC, /function protectedPaths\(\)/);
  assert.match(PANEL, /protected \?\? \[\]/);
});

test('both entry points exist and the composer opens the manager', () => {
  const commands = PACKAGE.contributes.commands.map((entry) => entry.command);
  assert.ok(commands.includes('anvil.openPermissionRules'), 'the manager command is contributed');
  assert.ok(commands.includes('anvil.openPermissionRulesFile'), 'the file command is contributed');
  assert.match(EXTENSION, /registerCommand\('anvil\.openPermissionRules', async \(\) => \{\s*hub\.openPermissionRules\(\);/);
  assert.match(EXTENSION, /registerCommand\('anvil\.openPermissionRulesFile', async \(\) => \{\s*await hub\.openPermissionRulesFile\(\);/);
  // The panel is what the manager command opens; the old file-opening path is
  // still the Open JSON button's.
  assert.match(HUB, /openPermissionRules\(\): void \{\s*this\.permissions\.open\(\);/);
  assert.match(DOC, /vscode\.postMessage\(\{ type: 'openFile', source: openSource \}\)/);
  assert.match(
    PANEL,
    /await this\.host\.openPermissionRulesFile\(\s*message\.source === 'user' \? 'user' : 'workspace',/,
  );
  // The composer's Permissions button is the manager, not the file.
  assert.match(CHAT_PANEL, /case 'openPermissions':\s*await this\.hub\.openPermissionRules\(\);/);
});

test('a workspace that does not read the file says so in the panel', () => {
  assert.match(DOC, /anvil\.permissions is off, so these files are not read/);
  assert.match(HUB, /loadPermissionsEnabled\(\): boolean \{\s*return readSettings\(\)\.session\.load_permissions !== false;/);
  assert.match(PANEL, /loadPermissions: this\.host\.loadPermissionsEnabled\(\)/);
});

test('the manager shares the host guard rather than starting a child of its own', () => {
  // Nothing in the panel spawns or connects: an untrusted workspace is refused by
  // the one gate the chat panel uses, because the call reaches `startClient`.
  assert.doesNotMatch(PANEL, /spawn|HostClient|child_process/);
  assert.match(HUB, /permissionRules\(\): Promise<PermissionsResult> \{\s*return this\.manager\.permissionRules\(\);/);
  assert.match(MANAGER, /HostManager\.assertTrusted\(\)/);
});
