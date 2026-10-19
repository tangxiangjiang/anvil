/**
 * The replay's wiring, across the three files no test in this suite can run.
 *
 * `src/transcript.ts` folds a stored conversation into shapes the tab draws; the
 * panel decides when to ask for it and where to send it; `media/chat.js` draws
 * the two lines that have no live equivalent. Each file is tested on its own
 * elsewhere — this one is for the agreements between them, the same shape as
 * `toolCardReview.test.ts`, and for the same reason: every failure here is
 * silent. A replay posted before the reset stacks two conversations in one
 * document; a replayed call that goes through `onEvent` claims a pre-image of
 * the *present* turn; a renderer case the panel never posts is a conversation
 * that stops halfway with no error anywhere.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');

/** A top-level function of the renderer, from its declaration to the next one. */
function bodyOf(source: string, name: string): string {
  const start = source.indexOf(`\n  function ${name}(`);
  assert.ok(start >= 0, `${name} is in chat.js`);
  const rest = source.slice(start + 1);
  const next = rest.indexOf('\n  function ');
  return next >= 0 ? rest.slice(0, next) : rest;
}

/** A private method of the panel, from its declaration to the next one. */
function methodOf(source: string, signature: string): string {
  const start = source.indexOf(signature);
  assert.ok(start >= 0, `${signature} is in chatPanel.ts`);
  const rest = source.slice(start);
  const next = rest.indexOf('\n  private ');
  assert.ok(next > 0, `${signature} is followed by another member`);
  return rest.slice(0, next);
}

test('a replayed call goes through the one door that decides its review', () => {
  const body = methodOf(CHAT_PANEL, 'private async replay');
  // Through `postEvent`, which is where `reviewable` is computed for a live
  // event too: a replay that posted its own tool call would be a card with no
  // way into its change, and the forgetting would be invisible.
  assert.match(body, /this\.postEvent\(step\.event\)/);
  assert.doesNotMatch(body, /type: 'event', event/);

  // And never through `onEvent`. That path snapshots the change tracker, which
  // reads the file as it is *now*: a replayed call's pre-image is long gone, so
  // the snapshot would file today's contents under a historical call's id and
  // list those files under the present turn's changed files.
  assert.doesNotMatch(body, /snapshotToolCall/);
  assert.doesNotMatch(body, /this\.onEvent/);
});

test('the replay is best-effort, because an old host is a supported pair', () => {
  const body = methodOf(CHAT_PANEL, 'private async replay');
  // A host that predates `session.transcript` answers -32601. The tab has to
  // come out of that usable, with no replay and nothing said about it.
  assert.match(body, /catch \(err\)/);
  assert.match(body, /this\.log\(/);
  assert.doesNotMatch(body, /type: 'notice'/);
  // Nothing to draw means nothing is posted: an empty replay must not cost the
  // user the session marker the resume just put there.
  assert.match(body, /if \(steps\.length === 0\) return;/);
});

test('the replay lands after the reset and the marker, never before', () => {
  const body = methodOf(CHAT_PANEL, 'private async resumeAgent');
  const reset = body.indexOf("this.post({ type: 'reset' })");
  const marker = body.indexOf("this.post({ type: 'session'");
  const replay = body.indexOf('await this.replay(agentId)');
  assert.ok(reset >= 0 && marker >= 0 && replay >= 0, 'all three are in resumeAgent');
  // The reset takes the previous conversation off the screen; the marker is the
  // first line of the one that replaced it; the replay fills in the rest. Any
  // other order leaves two conversations stacked in one document, which reads
  // as corruption rather than as a switch.
  assert.ok(reset < marker && marker < replay, 'the order is reset, marker, replay');
});

test('a window asks to come back only when it has something to come back to', () => {
  const ready = CHAT_PANEL.slice(CHAT_PANEL.indexOf("if (msg.type === 'ready')"));
  assert.match(ready, /void this\.reattach\(\)/);

  const body = methodOf(CHAT_PANEL, 'private async reattach');
  // Keyed by the id the host gave, from the workspace's own state — not from
  // anything the tab holds, which is exactly what a reload took away.
  assert.match(body, /this\.context\.workspaceState\.get<string>\(SessionAgentKey\)/);
  assert.match(body, /if \(!stored\) return;/);
  // The three endings, and the middle one has to forget: an id whose agent is
  // gone would otherwise be resumed on every reload, and fail every time.
  assert.match(body, /shouldReattach\(stored, listed\)/);
  assert.match(body, /case 'resume':/);
  assert.match(body, /case 'clear':/);
  assert.match(body, /await this\.forgetAgent\(\)/);
  // A live session needs no resuming — only the transcript its document lost.
  assert.match(body, /this\.manager\?\.state\(\)\.agentId === stored/);
  // A listing that could not be read is `undefined`, and must reach the decision
  // as such rather than as an empty list.
  assert.match(body, /let listed: SessionInfo\[\] \| undefined;/);
});

test('the agent the tab is showing is remembered and forgotten where it changes', () => {
  // Written at the one place every session reports itself — new, resumed, and
  // re-opened for a model change — with the id the host answered, rather than
  // composed at each call site from what was asked for.
  const session = CHAT_PANEL.slice(CHAT_PANEL.indexOf('onSession = (result: SessionResult)'));
  assert.match(session, /this\.rememberAgent\(result\.agent_id\)/);
  const remember = methodOf(CHAT_PANEL, 'private rememberAgent');
  assert.match(remember, /workspaceState\.update\(SessionAgentKey, agentId\)/);

  const clear = methodOf(CHAT_PANEL, 'private async clearConversation');
  assert.match(clear, /await this\.forgetAgent\(\)/);
});

test('a replayed conversation is drawn by the renderers the live one uses', () => {
  // Each case hands off to an existing drawing path: the composer's own user
  // bubble, and the Markdown path for prose — both reached through the event
  // stream, so a replayed tool card gets its review button for free.
  assert.match(CHAT_JS, /case 'user':[\s\S]{0,200}addUser\(msg\.text \|\| ''\)/);
  assert.match(CHAT_JS, /case 'note':[\s\S]{0,200}addNote\(msg\.text \|\| ''\)/);
  assert.match(CHAT_JS, /case 'runEnd':[\s\S]{0,200}addRunEnd\(msg\.done\)/);
});

test('a run end settles anything still reading "running"', () => {
  // A call whose result never arrived — a dropped event, or a replay of a run
  // that ended first — would otherwise claim to be in flight forever, which
  // reads as a stuck turn rather than as a lost record. Both endings do it: the
  // live one, and the marker a replay leaves.
  assert.match(bodyOf(CHAT_JS, 'finishRun'), /stopRunningCards\(\)/);
  assert.match(bodyOf(CHAT_JS, 'addRunEnd'), /stopRunningCards\(\)/);

  const sweep = bodyOf(CHAT_JS, 'stopRunningCards');
  assert.match(sweep, /classList\.contains\('done'\)/);
  assert.match(sweep, /classList\.contains\('failed'\)/);
  // No review button for a call whose ending is unknown: whether it landed is
  // precisely what is not known, and the button's rule is that a change which
  // did not land is not a change.
  assert.doesNotMatch(sweep, /addReviewButton/);
});

test('a run says what it cost the same way in both places', () => {
  // The status line for the run in flight and the marker a replayed one leaves
  // are the same sentence, from one function: two counts of the same run's steps
  // would be two answers to one question.
  assert.match(bodyOf(CHAT_JS, 'finishRun'), /runSummary\(done\)/);
  assert.match(bodyOf(CHAT_JS, 'addRunEnd'), /runSummary\(done\)/);
});

test('Enter during an IME composition does not send the message', () => {
  // Confirming a candidate — an English word typed through a Chinese IME, or a
  // Chinese word — is an Enter. That key has to land the word in the box. The
  // keydown that follows compositionend often reports isComposing as false, so
  // the composition itself has to swallow it.
  const wiring = CHAT_JS.slice(CHAT_JS.indexOf("input.addEventListener('compositionstart'"));
  assert.match(wiring, /event\.isComposing \|\| event\.keyCode === 229 \|\| composing/);
  assert.match(wiring, /compositionend/);
  assert.match(wiring, /confirmEnter/);
  const guard = wiring.indexOf('event.isComposing');
  const send = wiring.indexOf("composer.dispatchEvent(new Event('submit'");
  assert.ok(guard >= 0 && send > guard, 'the composition check runs before submit');
});

test('the new transcript lines are built, not written', () => {
  // The webview's whole security story is that model output arrives as text.
  // The note is the one that most tempts a shortcut, because the text it draws
  // looks like a prompt.
  const note = bodyOf(CHAT_JS, 'addNote');
  assert.match(note, /el\('div', 'body', text\)/);
  assert.doesNotMatch(note, /innerHTML/);
  assert.doesNotMatch(bodyOf(CHAT_JS, 'addRunEnd'), /innerHTML/);
});
