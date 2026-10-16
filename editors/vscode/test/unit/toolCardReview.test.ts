/**
 * The button on a finished tool card, and the three files it has to keep in step.
 *
 * A click on a tool card travels through code no test in this suite can run:
 * the renderer needs a DOM, `chatPanel.ts` needs an extension host, and the
 * diff it opens needs a real editor. What *is* checkable is the agreement
 * between them, and that is what this file is for — the same shape as
 * `permissionKinds.test.ts` and `linkPolicy.test.ts`, for the same reason: every
 * failure here is silent at runtime. A button placed inside the summary would
 * collapse the card it opened; a `reviewCall` the panel does not route would be
 * a click that does nothing; a tool the renderer offers a review for that the
 * derivation does not read would be a button that answers "nothing to review".
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');
const EDIT_DIFF = readFileSync(join(EXTENSION_ROOT, 'src', 'editDiff.ts'), 'utf8');
const MENTIONS = readFileSync(join(EXTENSION_ROOT, 'src', 'mentions.ts'), 'utf8');

/** A top-level function of the renderer, from its declaration to the next one. */
function bodyOf(source: string, name: string): string {
  const start = source.indexOf(`\n  function ${name}(`);
  assert.ok(start >= 0, `${name} is in chat.js`);
  const rest = source.slice(start + 1);
  const next = rest.indexOf('\n  function ');
  return next >= 0 ? rest.slice(0, next) : rest;
}

/**
 * A private method of the panel, from its declaration to the next one. Slicing
 * on the closing brace would not do: a parameter object closes with one too.
 */
function methodOf(source: string, signature: string): string {
  const start = source.indexOf(signature);
  assert.ok(start >= 0, `${signature} is in chatPanel.ts`);
  const rest = source.slice(start);
  const next = rest.indexOf('\n  private ');
  assert.ok(next > 0, `${signature} is followed by another member`);
  return rest.slice(0, next);
}

test('the button is a sibling of the summary, never inside it', () => {
  // `<details>` toggles from its summary. A button inside it would collapse the
  // card on the same click that opens the diff, which is the bug this pins.
  const button = bodyOf(CHAT_JS, 'addReviewButton');
  assert.match(button, /card\.insertBefore\(open, card\.querySelector\('\.pane'\)\)/);
  assert.doesNotMatch(button, /summary\.appendChild/);
  assert.doesNotMatch(button, /setAttribute\(\s*'onclick'/);
});

test('a card stores the call facts only when the extension said there is a change', () => {
  // The renderer cannot read `editDiff.ts`, so it does not decide this: a second
  // list of "tool names that change a file" living in `chat.js` is a list that
  // goes stale the first time one is renamed. It stores the facts it is told to
  // keep, and nothing else.
  const call = bodyOf(CHAT_JS, 'addToolCall');
  assert.match(call, /function addToolCall\(event, reviewable\)/);
  assert.match(call, /if \(reviewable\) \{/);
  assert.match(call, /callFacts\.set\(event\.tool_call_id/);
  // The arguments are kept as they arrived, beside the id they belong to.
  assert.match(call, /name: event\.name/);
  assert.match(call, /args: event\.args/);
  assert.match(call, /runId: event\.run_id/);
});

test('the button waits for a result, and a failed call never gets one', () => {
  const result = bodyOf(CHAT_JS, 'addToolResult');
  assert.match(result, /if \(!failed\) addReviewButton\(card, event\.tool_call_id\)/);
  // And nothing here consults an approval. The calls that ran silently — under
  // accept_edits, under bypass, by an allow rule, or with no permissions file at
  // all — are exactly the ones nobody was shown, and so the ones worth a way
  // back to the change.
  assert.doesNotMatch(result, /approval/i);
});

test('every tool call the card gets carries the answer about its review', () => {
  // The answer is decided once, where the event is posted, rather than by each
  // caller: a caller that forgot would be a card with no way into its change,
  // and the forgetting would be silent. (p_0.38.3's replay posts its rebuilt
  // calls through the same door.)
  const from = CHAT_PANEL.indexOf('onEvent = (event: HostEvent)');
  assert.ok(from > 0, 'the panel has an onEvent');
  const listener = CHAT_PANEL.slice(from, CHAT_PANEL.indexOf('onApproval = ', from));
  assert.match(listener, /this\.postEvent\(event\)/);

  const body = methodOf(CHAT_PANEL, 'private postEvent');
  assert.match(body, /reviewOf\(event\.name, event\.args\) !== undefined/);
  assert.match(body, /this\.post\(\{ type: 'event', event, reviewable \}\)/);

  // The renderer believes the extension and nothing else: it reads the flag as
  // a boolean, so a field that went missing reads as "no review" rather than as
  // a fact about the call.
  assert.match(CHAT_JS, /addToolCall\(e, msg\.reviewable === true\)/);
});

test('the click hands back the call, not just its id', () => {
  // The panel keeps no copy of every call's body, so the facts have to come
  // back with the click. The id travels too, for the log line that says which
  // call had nothing to review.
  const button = bodyOf(CHAT_JS, 'addReviewButton');
  assert.match(button, /type: 'reviewCall'/);
  assert.match(button, /toolCallId/);
  assert.match(button, /runId: facts\.runId/);
  assert.match(button, /name: facts\.name/);
  assert.match(button, /args: facts\.args/);
});

test('the panel routes the click, and the review answers nothing', () => {
  assert.match(CHAT_PANEL, /case 'reviewCall':/);
  assert.match(CHAT_PANEL, /await this\.openCallReview\(msg\)/);

  const body = methodOf(CHAT_PANEL, 'private async openCallReview');
  assert.match(body, /reviewOf\(call\.name, call\.args\)/);
  assert.match(body, /planReview\(review, preImage\)/);
  // Every plan is executed: the three surfaces the module can choose.
  assert.match(body, /plan\.kind === 'text'/);
  assert.match(body, /plan\.kind === 'memory'/);
  assert.match(body, /plan\.deleted/);
  assert.match(body, /openText/);
  assert.match(body, /openPreview/);
  assert.match(body, /openAgainstDisk/);
  assert.match(body, /openDeleted/);
  // A record, not a request: the map the panel closes when an approval is
  // answered must not be touched here, or a review would vanish with a turn.
  assert.doesNotMatch(body, /this\.reviews/);
});

test('a pre-image is used only for the turn it came from', () => {
  // The same file can be written again in a later turn, and that turn's
  // pre-image shown under an older card's button would read as that call's
  // edit. The run id is what tells the two apart.
  const body = methodOf(CHAT_PANEL, 'private preImageFor');
  assert.match(body, /runId !== this\.lastChanges\.run/);
  assert.match(CHAT_PANEL, /this\.lastChanges = \{ run: runId, files:/);

  // And the renderer sends the run the call belonged to, which is the only way
  // the comparison can have an answer at all.
  assert.match(bodyOf(CHAT_JS, 'addToolCall'), /event\.run_id/);
});

test('the tools the review covers are the tools the tracker follows', () => {
  // `reviewOf` derives a call's paths with `toolCallPaths`, which is also what
  // decides which files a turn snapshots. A tool the review understood and the
  // tracker did not would be a card whose pre-image is never taken; the other
  // way round is a snapshot nothing can review. Both are silent.
  const derived = new Set(
    [...EDIT_DIFF.matchAll(/name === '(\w+)'/g)].map((match) => match[1]),
  );
  const tracked = new Set([
    ...(/(?:const )?SINGLE_PATH_TOOLS = new Set\(\[([^\]]*)\]/.exec(MENTIONS)?.[1] ?? '')
      .split(',')
      .map((entry) => entry.trim().replace(/['"]/g, ''))
      .filter((entry) => entry !== ''),
    'apply_patch',
  ]);
  assert.deepEqual([...derived].sort(), [...tracked].sort());
  assert.ok(derived.size >= 3, 'the derivation covers the file-writing tools');
});

test('nothing in the renderer writes innerHTML', () => {
  // The ban is the webview's whole security story, and this phase adds the one
  // thing that most tempts a renderer to break it: a button that opens a
  // window. It is written with createElement and textContent like everything
  // else in the transcript. The patterns single out an assignment or a call,
  // because the file's own header *names* the ban in prose.
  assert.doesNotMatch(CHAT_JS, /\.innerHTML\s*(\+)?=(?!=)/);
  assert.doesNotMatch(CHAT_JS, /\.outerHTML\s*=/);
  assert.doesNotMatch(CHAT_JS, /insertAdjacentHTML\s*\(/);
  assert.doesNotMatch(CHAT_JS, /document\.write\s*\(/);
});
