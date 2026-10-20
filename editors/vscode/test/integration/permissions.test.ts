/**
 * The permission-rule surface, end to end against a scripted host.
 *
 * The Go tests cover what a rule decides; what is covered here is the wire the
 * extension reads and writes — the rule carried on an approval and on a tool
 * result, the "always allow" shapes that come with a card, and the two calls
 * that read and amend the file. The panel's own glue is not exercised (it needs
 * a window); the requests below are the ones it sends, so a renamed field or a
 * changed shape fails here rather than in a user's session.
 */

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { startFakeHost, waitFor } from '../helpers/harness';
import { Method, PermissionsAddResult, PermissionsResult, RuleSpec } from '../../src/protocol';

test('an approval carries the rule that asked and the choices that would decide it', async (t) => {
  const harness = await startFakeHost('permissions');
  t.after(() => harness.stop());

  await harness.session.send('check the working tree');
  await waitFor(() => harness.approvals.length === 1, 'the approval card');
  const request = harness.approvals[0];

  assert.equal(request.tool, 'shell');
  // A rule that insisted on asking is part of why the card is there, and the
  // sentence is the SDK's own — the card and a denial have to spell it alike.
  assert.equal(request.rule?.index, 1);
  assert.equal(request.rule?.effect, 'ask');
  assert.match(request.rule?.describe ?? '', /rule 1 \(ask shell "git:\*"\)/);

  // Every choice the host offers was proven against this call, so the card can
  // render them without deciding anything itself.
  assert.deepEqual(
    (request.remember ?? []).map((option) => option.kind),
    ['exact', 'prefix', 'readonly'],
  );
  const prefix = (request.remember ?? []).find((option) => option.kind === 'prefix');
  assert.deepEqual(prefix?.rule, { effect: 'allow', tool: 'shell', pattern: 'git status:*' });
});

test('a remembered rule settles the card, and the tool result names it', async (t) => {
  const harness = await startFakeHost('permissions');
  t.after(() => harness.stop());

  await harness.session.send('check the working tree');
  await waitFor(() => harness.approvals.length === 1, 'the approval card');
  const request = harness.approvals[0];
  const chosen = (request.remember ?? []).find((option) => option.kind === 'prefix');
  assert.ok(chosen);

  // What the panel sends when the user picks "always allow this prefix".
  const added = await harness.client.request<PermissionsAddResult>(Method.PermissionsAdd, {
    rule: chosen.rule,
    request_id: request.request_id,
  });
  assert.equal(added.approved, true);
  assert.equal(added.index, 1);
  assert.equal(added.decided_by?.pattern, 'git status:*');
  assert.match(added.rules[0].describe ?? '', /permissions\.json rule 1/);

  // No approval.response was sent — the host settled the call because the file
  // now decides it — and the run moves on to the result.
  await waitFor(() => harness.dones.length === 1, 'the run to finish');
  assert.equal(harness.dones[0].done.status, 'finished');
  const output = harness.events.find((event) => event.type === 'tool_result');
  assert.equal(output?.is_error, false);
  // The rule that allowed it: how a call that never asked says why it did not.
  assert.equal(output?.rule?.pattern, 'git status:*');
});

test('the rule file is asked about, and its state survives the wire', async (t) => {
  const harness = await startFakeHost('permissions');
  t.after(() => harness.stop());

  const empty = await harness.client.request<PermissionsResult>(Method.PermissionsGet, {});
  assert.equal(empty.exists, false);
  assert.deepEqual(empty.rules, []);
  assert.equal(empty.problem, undefined);
  // Absolute, so the client can open the file before it exists.
  assert.match(empty.path, /\.anvil[\\/]permissions\.json$/);
  // The user layer is reported beside it (0.47): where it would be, whether it
  // is there, and the globs it adds. A person with no preferences is a missing
  // file, not a problem.
  assert.match(empty.user_path ?? '', /\.anvil[\\/]permissions\.json$/);
  assert.equal(empty.user_exists, false);
  assert.equal(empty.user_problem, undefined);
  assert.deepEqual(empty.protected, []);

  const rule: RuleSpec = { tool: 'shell', pattern: 'git status:*' };
  const added = await harness.client.request<PermissionsAddResult>(Method.PermissionsAdd, { rule });
  assert.equal(added.index, 1);
  // No request_id: a rule written from the rules file is not about a call, and
  // nothing is settled by it. A client that names no layer writes the
  // workspace's, which is what every client before scopes meant.
  assert.equal(added.approved, false);
  assert.equal(added.source, 'workspace');

  const after = await harness.client.request<PermissionsResult>(Method.PermissionsGet, {});
  assert.equal(after.exists, true);
  // An empty effect means allow, which is the contract the host documents.
  assert.equal(after.rules[0].effect, 'allow');
  assert.equal(after.rules[0].pattern, 'git status:*');
  // One-based, and the number the rule's own sentence quotes.
  assert.equal(after.rules[0].index, 1);
  assert.equal(after.rules[0].source, 'workspace');
});

test('the user layer is written by naming it, one layer per save', async (t) => {
  const harness = await startFakeHost('permissions');
  t.after(() => harness.stop());

  // Appending to a file that does not exist creates it, and the answer names
  // the file it wrote rather than the workspace's.
  const added = await harness.client.request<PermissionsAddResult>(Method.PermissionsAdd, {
    source: 'user',
    rule: { effect: 'deny', tool: 'shell', pattern: 'rm -rf:*' },
  });
  assert.equal(added.source, 'user');
  assert.match(added.path, /\.anvil[\\/]permissions\.json$/);
  assert.match(added.rules[0].describe ?? '', /user permissions\.json rule 1/);

  // A whole-layer save carries the user file's own field, and gets back a
  // reading with both layers and their sources.
  const saved = await harness.client.request<PermissionsResult>(Method.PermissionsSet, {
    source: 'user',
    create: true,
    rules: [{ effect: 'allow', tool: 'write_file', pattern: 'src/**' }],
    protected: ['secrets/**', ' *.pem ', ''],
  });
  assert.equal(saved.user_exists, true);
  assert.deepEqual(saved.protected, ['secrets/**', '*.pem']);
  assert.equal(saved.rules[0].source, 'user');
  assert.equal(saved.rules[0].index, 1);

  // The one field that belongs to the workspace is refused on the user layer,
  // and the other way round — neither is silently dropped.
  await assert.rejects(
    harness.client.request(Method.PermissionsSet, {
      source: 'user',
      rules: [],
      default_mode: 'accept_edits',
    }),
    /no starting mode/,
  );
  await assert.rejects(
    harness.client.request(Method.PermissionsSet, {
      rules: [],
      protected: ['secrets/**'],
    }),
    /belong to the user file/,
  );
});

test('a malformed user file is reported, read-only and never replaced', async (t) => {
  const harness = await startFakeHost('permissions', {
    env: { FAKE_HOST_USER_RULES: '1', FAKE_HOST_USER_RULES_BROKEN: '1' },
  });
  t.after(() => harness.stop());

  const state = await harness.client.request<PermissionsResult>(Method.PermissionsGet, {});
  // The layer is there and will not compile: the same report the workspace file
  // gets, with the place, and no rules from it.
  assert.equal(state.user_exists, true);
  assert.equal(state.user_problem?.line, 3);
  assert.equal(state.user_problem?.column, 9);
  assert.match(state.user_problem?.message ?? '', /not one of allow, ask, deny/);

  for (const method of [Method.PermissionsAdd, Method.PermissionsSet]) {
    await assert.rejects(
      harness.client.request(method, { source: 'user', rule: { tool: 'shell' } }),
      (err: Error) => {
        assert.match(err.message, /permissions\.json:3:9/);
        return true;
      },
    );
  }
});

test('a rule file that will not compile is reported with its place, not as a bare failure', async (t) => {
  const harness = await startFakeHost('permissions', { env: { FAKE_HOST_RULES_BROKEN: '1' } });
  t.after(() => harness.stop());

  // The read is a result rather than an error: the client asked about the state
  // of a file somebody may be halfway through editing.
  const state = await harness.client.request<PermissionsResult>(Method.PermissionsGet, {});
  assert.equal(state.exists, true);
  assert.equal(state.problem?.line, 4);
  assert.equal(state.problem?.column, 12);
  assert.match(state.problem?.message ?? '', /not one of allow, ask, deny/);

  // The write refuses instead of replacing a policy it cannot read, and says
  // where — the same place, in the shape a session failure would carry.
  await assert.rejects(
    harness.client.request(Method.PermissionsAdd, { rule: { tool: 'shell', pattern: 'ls' } }),
    (err: Error) => {
      assert.match(err.message, /permissions\.json:4:12/);
      return true;
    },
  );
});
