/**
 * The input layer: Escape, drops, the image picker, and Copy.
 *
 * There is no browser and no editor host in this suite, so what can be checked
 * is where each affordance is wired and what it must not do: an idle Escape
 * leaving the draft alone, a picker producing the same data URL a paste does, a
 * drop that resolves paths on the side that knows the workspace root, and a
 * clipboard write with no HTML anywhere near it.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_CSS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.css'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');
const ATTACHMENTS = readFileSync(join(EXTENSION_ROOT, 'src', 'attachments.ts'), 'utf8');
const PACKAGE_JSON = readFileSync(join(EXTENSION_ROOT, 'package.json'), 'utf8');
const SETTINGS = readFileSync(join(EXTENSION_ROOT, 'src', 'settings.ts'), 'utf8');

test('vision is on by default, and the setting is the switch that turns it off', () => {
  // A screenshot pasted into a chat is a request to send it (p_0.46.9). The
  // declaration and the reader must agree: the declaration is what the settings
  // UI shows, the fallback is what a session gets before anyone opened it.
  const declaration = /"anvil\.vision": \{[\s\S]*?\n {8}\}/.exec(PACKAGE_JSON);
  assert.ok(declaration, 'anvil.vision is declared');
  assert.match(declaration[0], /"default": true/);
  assert.match(SETTINGS, /session\.vision = cfg\.get<boolean>\('vision', true\) \?\? true/);
  // Off is still a choice the user can make, and the composer still says so.
  assert.match(CHAT_JS, /if \(!vision\) \{[\s\S]{0,200}'Images are off for this session/);
});

test('Escape interrupts only when a turn is running', () => {
  const handler = /window\.addEventListener\('keydown', \(event\) => \{[\s\S]*?\n {2}\}\);/.exec(CHAT_JS);
  assert.ok(handler, 'chat.js must listen for keydown on the window');
  const body = handler[0];
  assert.match(body, /event\.key !== 'Escape' \|\| !running/);
  assert.match(body, /requestStop\(\)/);
  // Nothing in that handler touches the composer: an idle Escape belongs to the
  // palette and to the draft.
  assert.doesNotMatch(body, /input\.value/);
  // The send button — the same control that stops a running turn — and Escape
  // are one action, so the two paths cannot drift.
  assert.match(CHAT_JS, /sendButton\.addEventListener\('click', \(\) => \{[\s\S]*?if \(running\) \{[\s\S]*?requestStop\(\)/);
  assert.match(CHAT_JS, /function requestStop\(\) \{\s*sendButton\.disabled = true;\s*vscode\.postMessage\(\{ type: 'cancel' \}\)/);
  assert.match(CHAT_JS, /vscode\.postMessage\(\{ type: 'cancel' \}\)/);
});

test('the picker button sits beside the attach button and asks the host', () => {
  assert.match(CHAT_PANEL, /id="attach"[\s\S]*?id="image"[\s\S]*?id="send"/);
  assert.match(CHAT_JS, /imageButton\.addEventListener\('click', \(\) => vscode\.postMessage\(\{ type: 'pickImages' \}\)\)/);
  // Locked with the rest of the composer while a turn runs.
  assert.match(CHAT_JS, /if \(imageButton\) imageButton\.disabled = running;/);
});

test('the host opens the image dialog and answers with data URLs', () => {
  assert.match(CHAT_PANEL, /case 'pickImages':\s*await this\.pickImages\(\);/);
  assert.match(CHAT_PANEL, /showOpenDialog\(\{[\s\S]*?filters: \{ Images: \['png', 'jpg', 'jpeg', 'gif', 'webp'\] \}/);
  assert.match(CHAT_PANEL, /vscode\.workspace\.fs\.readFile\(uri\)/);
  assert.match(CHAT_PANEL, /dataUrlFromBytes\(bytes, mimeForImagePath\(uri\.path\)\)/);
  assert.match(CHAT_PANEL, /this\.post\(\{ type: 'images', images \}\)/);
  // The data URL is built on the host, in one place, with the paste path's cap.
  assert.match(ATTACHMENTS, /export function dataUrlFromBytes/);
  assert.match(ATTACHMENTS, /Math\.ceil\(bytes\.byteLength \/ 3\) \* 4 > MAX_ATTACHMENT_BASE64/);
});

test('a picked image lands in the same list a paste fills', () => {
  const imagesCase = /case 'images':[\s\S]*?return;/.exec(CHAT_JS);
  assert.ok(imagesCase);
  assert.match(imagesCase[0], /addImage\(dataUrl\)/);
  // Paste and picker share the one adder, so the chips and the send see no
  // difference between them.
  assert.match(CHAT_JS, /reader\.onload = \(\) => \{[\s\S]*?addImage\(reader\.result\)/);
  assert.match(CHAT_JS, /function addImage\(dataUrl\) \{[\s\S]*?images\.push\(dataUrl\)/);
  assert.match(CHAT_JS, /dataUrl\.indexOf\('data:image\/'\) !== 0/);
});

test('a drop that carries files is accepted and the panel says so', () => {
  assert.match(CHAT_JS, /document\.addEventListener\('dragover'/);
  assert.match(CHAT_JS, /document\.addEventListener\('drop'/);
  assert.match(CHAT_JS, /document\.body\.classList\.add\('drop-target'\)/);
  assert.match(CHAT_CSS, /body\.drop-target \{/);
  // Only a file drag: a text selection dropped on the transcript is not ours.
  assert.match(CHAT_JS, /indexOf\.call\(types, 'Files'\) !== -1/);
});

test('images are attached from a drop, everything else is resolved as a mention', () => {
  const drop = /document\.addEventListener\('drop'[\s\S]*?\n {2}\}\);/.exec(CHAT_JS);
  assert.ok(drop);
  assert.match(drop[0], /file\.type\.indexOf\('image\/'\) === 0[\s\S]*?readImage\(file\)/);
  assert.match(drop[0], /vscode\.postMessage\(\{ type: 'dropFiles', paths, uris \}\)/);
  // The host resolves the paths: only it knows the workspace root. A multi-root
  // window without a root yet asks for one rather than taking a folder (0.46).
  assert.match(CHAT_PANEL, /case 'dropFiles':\s*await this\.dropFiles\(msg\.paths, msg\.uris\);/);
  assert.match(CHAT_PANEL, /folder = await resolveWorkspaceRoot\(this\.context\)/);
  assert.match(CHAT_PANEL, /relativeMention\(folder\.uri\.fsPath, candidate\)/);
  assert.match(CHAT_PANEL, /this\.post\(\{ type: 'mentions', paths: unique \}\)/);
  // A file outside the root is refused with a sentence, not silently.
  assert.match(CHAT_PANEL, /outside this window's root folder, so it was not added/);
});

test('a refused turn is reported, not swallowed', () => {
  const send = /private async sendQuestion[\s\S]*?\n {2}\}/.exec(CHAT_PANEL);
  assert.ok(send, 'sendQuestion must be findable');
  // The webview has already drawn the bubble, so a failure that is only
  // logged looks like a turn that vanished. The host's vision refusal is the
  // common case, and it names the setting to turn on.
  assert.match(send[0], /type: 'notice',\s*level: 'error'/);
  assert.match(send[0], /The turn was not sent/);
  // The optimistic `running` state has to be undone, or the composer stays
  // locked with nothing running.
  assert.match(send[0], /this\.postState\(\)/);
  assert.doesNotMatch(send[0], /catch\(\(\) => undefined\)/);
});

test('Copy is offered on a finished answer and writes through the host', () => {
  assert.match(CHAT_JS, /if \(round && round\.text\.trim\(\) !== ''\) addCopy\(round\.wrap, round\.text\)/);
  assert.match(CHAT_JS, /vscode\.postMessage\(\{ type: 'copy', text \}\)/);
  assert.match(CHAT_PANEL, /case 'copy':\s*await this\.copy\(msg\.text\);/);
  assert.match(CHAT_PANEL, /await vscode\.env\.clipboard\.writeText\(text\)/);
  // The action is built with textContent like everything else in this file.
  const addCopy = /function addCopy\(wrap, text\) \{[\s\S]*?\n {2}\}/.exec(CHAT_JS);
  assert.ok(addCopy);
  assert.doesNotMatch(addCopy[0], /innerHTML|insertAdjacentHTML/);
  assert.match(CHAT_CSS, /\.msg \.copy \{/);
});
