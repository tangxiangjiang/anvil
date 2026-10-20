/**
 * A plan as a document, checked where it cannot be shown.
 *
 * The naming rules are pure and stated as data. What is left is the agreement
 * the compiler cannot check: the provider the window registers, the tab that
 * holds the text, the card that offers the three choices, and the two decisions
 * whose order matters — the mode changes before the sentence is sent, and a
 * refusal sends nothing at all. Each of those failing is silent: a document
 * that never appears, an approval that arrives while the write tools are still
 * withheld, a rejection that starts a turn anyway.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import { PLAN_APPROVED_PROMPT, planPath, planQuery, planTarget, planTitle } from '../../src/plan';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');
const PLAN_DOCUMENT = readFileSync(join(EXTENSION_ROOT, 'src', 'planDocument.ts'), 'utf8');
const EXTENSION = readFileSync(join(EXTENSION_ROOT, 'src', 'extension.ts'), 'utf8');
const PACKAGE = readFileSync(join(EXTENSION_ROOT, 'package.json'), 'utf8');

/** A top-level function of the renderer, from its declaration to the next one. */
function bodyOf(source: string, name: string): string {
  const start = source.indexOf(`\n  function ${name}(`);
  assert.ok(start >= 0, `${name} is in chat.js`);
  const rest = source.slice(start + 1);
  const next = rest.indexOf('\n  function ');
  return next >= 0 ? rest.slice(0, next) : rest;
}

/** A member of the panel, from its declaration to the next comment or member. */
function memberOf(source: string, signature: string): string {
  const start = source.indexOf(signature);
  assert.ok(start >= 0, `${signature} is in chatPanel.ts`);
  const rest = source.slice(start);
  const ends = ['\n  /**', '\n  private ', '\n  async ', '\n  readonly ']
    .map((marker) => rest.indexOf(marker, 1))
    .filter((at) => at > 0);
  return ends.length === 0 ? rest : rest.slice(0, Math.min(...ends));
}

test('a tab title is the plan’s first line, without its markdown', () => {
  assert.equal(planTitle('## Plan: rename the parser\n\nThen the body.'), 'Plan: rename the parser');
  assert.equal(planTitle('**Bold** plan'), 'Bold plan');
  assert.equal(planTitle('> quoted first line\n\nsecond'), 'quoted first line');
  // Blank lines are skipped rather than becoming the title: a plan routinely
  // opens with one after a heading that was stripped to nothing.
  assert.equal(planTitle('\n\n   \n# Real heading'), 'Real heading');
  assert.equal(planTitle(''), 'Plan');
});

test('a title is safe to use as a document path', () => {
  // A slash would turn the title into a nested path, and the tab would show
  // only the last segment. A colon is legal in a URI path and stays, so a
  // heading reads as the heading.
  assert.equal(planTitle('Rename src/parser.ts'), 'Rename src-parser.ts');
  assert.equal(planTitle('Plan: rename the parser'), 'Plan: rename the parser');
  const hostile = planTitle('Fix <the> "quoted" heading?');
  assert.doesNotMatch(hostile, /[<>"{}|\\^`?#%/]/);
  assert.equal(planTitle('  spaced   out  '), 'spaced out');
});

test('a long first line is clipped rather than shown whole', () => {
  const title = planTitle('x'.repeat(200));
  assert.equal(title.length, 60);
  assert.ok(title.endsWith('…'));
});

test('a plan document is addressed by path and by the run that made it', () => {
  assert.equal(planPath('A plan'), '/A plan.md');
  assert.equal(planQuery('a1', 'r2'), 'agent=a1&run=r2');
  assert.deepEqual(planTarget(planQuery('a1', 'r2')), { agentId: 'a1', runId: 'r2' });
  // Both ids are required: a document naming only one belongs to no turn.
  assert.equal(planTarget(''), undefined);
  assert.equal(planTarget('agent=a1'), undefined);
  assert.equal(planTarget('run=r2'), undefined);
  assert.equal(planTarget('agent=&run=r2'), undefined);
});

test('the document is registered read-only and disposed with the window', () => {
  assert.match(PLAN_DOCUMENT, /registerTextDocumentContentProvider\(PLAN_SCHEME, this\)/);
  assert.match(PLAN_DOCUMENT, /this\.registration\.dispose\(\)/);
  assert.match(PLAN_DOCUMENT, /this\.contents\.clear\(\)/);
  // Content-only is the safety property: a file system provider would make the
  // document a file, and a file is something that can be written.
  assert.doesNotMatch(PLAN_DOCUMENT, /registerFileSystemProvider|writeFile|createFileSystemWatcher/);

  // One provider, created once in activate and released by the subscription.
  assert.equal(EXTENSION.split('new PlanDocuments()').length, 2);
  assert.match(EXTENSION, /const plans = new PlanDocuments\(\);/);
  assert.match(EXTENSION, /context\.subscriptions\.push\(plans\)/);
  assert.match(EXTENSION, /new Hub\(context, log, previews, plans, outputs\)/);
});

test('the three commands exist in both halves', () => {
  for (const name of ['anvil.openPlan', 'anvil.approvePlan', 'anvil.rejectPlan']) {
    assert.ok(EXTENSION.includes(`registerCommand('${name}'`), `${name} is registered`);
    assert.ok(PACKAGE.includes(`"command": "${name}"`), `${name} is contributed`);
  }
});

test('the plan text is the plan-mode turn’s own words, in order', () => {
  const track = memberOf(CHAT_PANEL, 'private trackPlanText(');
  // A child agent's steps are not the plan, and its tool's words are not either.
  assert.match(track, /if \(event\.parent_tool_call_id\) return;/);
  assert.match(track, /event\.run_id !== this\.planTurn\.run/);
  assert.match(track, /event\.type === 'tool_call'/);

  const finished = memberOf(CHAT_PANEL, 'private planFinished(');
  assert.match(finished, /mode !== 'plan'/);
  assert.match(finished, /this\.post\(\{ type: 'plan', runId, text \}\)/);
  assert.match(memberOf(CHAT_PANEL, 'onDone ='), /this\.planFinished\(done\.run_id\)/);
});

test('entering plan mode records what it replaced', () => {
  const setMode = memberOf(CHAT_PANEL, 'private async setMode(');
  assert.match(setMode, /chosen === 'plan' && this\.mode !== 'plan'/);
  assert.match(setMode, /this\.modeBeforePlan = this\.mode/);
});

test('approval changes the mode before anything is asked to run', () => {
  const approve = memberOf(CHAT_PANEL, 'async approvePlan(');
  const options = approve.indexOf('applyOptions({ permission_mode: back })');
  const send = approve.indexOf('sendQuestion(PLAN_APPROVED_PROMPT');
  assert.ok(options >= 0, 'the mode is applied');
  assert.ok(send >= 0, 'the instruction is sent');
  assert.ok(options < send, 'the mode is in force before the turn starts');
  // The mode approved back to is the one that was replaced, and plan itself is
  // never the answer.
  assert.match(approve, /const back = this\.modeBeforePlan \?\? 'ask';/);

  // One fixed sentence, and no copy of the plan in it: the plan is already the
  // last answer in the history. The literal is pinned so an edit is a decision.
  assert.equal(PLAN_APPROVED_PROMPT, 'The plan is approved. Carry it out.');
  assert.match(CHAT_PANEL, /import \{ PLAN_APPROVED_PROMPT \} from '\.\/plan';/);
});

test('keeping the plan sends nothing', () => {
  const reject = memberOf(CHAT_PANEL, 'rejectPlan(runId: string): void {');
  assert.doesNotMatch(reject, /sendQuestion|applyOptions|prompt\(/);
  assert.match(reject, /type: 'planSettled'.*action: 'reject'/);
  assert.match(reject, /type: 'focus'/);
});

test('the card offers the three choices and draws text only', () => {
  const plan = bodyOf(CHAT_JS, 'showPlan');
  assert.match(plan, /'Open plan'/);
  assert.match(plan, /'Approve and run'/);
  assert.match(plan, /'Keep planning'/);
  assert.match(plan, /type: 'openPlan'/);
  assert.match(plan, /type: 'approvePlan'/);
  assert.match(plan, /type: 'rejectPlan'/);
  assert.doesNotMatch(plan, /innerHTML|outerHTML|insertAdjacentHTML|document\.write/);

  const settle = bodyOf(CHAT_JS, 'settlePlan');
  // A refusal is not drawn as an approval: the two answers settle differently.
  assert.match(settle, /msg\.action === 'approve'/);
  assert.match(settle, /Kept in plan mode/);
  assert.doesNotMatch(settle, /innerHTML|outerHTML/);
});

test('the provider answers for exactly the URI it was given content for', () => {
  // The map is the only source: a document whose text has aged out renders
  // empty rather than showing another plan, which is what a URI-derived answer
  // would risk.
  assert.match(PLAN_DOCUMENT, /return this\.contents\.get\(uri\.toString\(\)\) \?\? '';/);
  assert.match(PLAN_DOCUMENT, /const key = uri\.toString\(\);/);
  assert.match(PLAN_DOCUMENT, /this\.contents\.set\(key, text\)/);
});
