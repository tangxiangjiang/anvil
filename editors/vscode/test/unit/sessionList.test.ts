/**
 * What the session sidebar shows.
 *
 * The model is the half of the sidebar with no `vscode` in it, which is why it
 * is the half that can be tested at all — the provider above it only turns these
 * nodes into `TreeItem`s. What is pinned here is mostly what the model refuses
 * to do: invent a title the store did not report, guess a status it does not
 * recognise, or fold an undated session into a bucket it does not belong in.
 */

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { SessionInfo } from '../../src/protocol';
import {
  SessionGroup,
  SessionNode,
  SessionRow,
  buildSessionView,
  sessionNote,
} from '../../src/sessionList';

/** A fixed clock, mid-afternoon, so the recency buckets are decided. */
const NOW = new Date(2026, 9, 16, 12, 0, 0).getTime();

/** An instant on a local calendar day, as the wire reports it. */
function at(year: number, month: number, day: number, hour = 9): string {
  return new Date(year, month, day, hour, 0, 0).toISOString();
}

function session(overrides: Partial<SessionInfo> & { agent_id: string }): SessionInfo {
  return { cwd: 'E:\\ws', provider: 'openai', model: 'gpt-5.2', runs: 1, ...overrides };
}

/** The rows of a group, or the node itself when the list is flat. */
function rows(nodes: SessionNode[]): SessionRow[] {
  return nodes.flatMap((node) => (node.kind === 'group' ? node.rows : node.kind === 'session' ? [node] : []));
}

function groupOf(nodes: SessionNode[], label: string): SessionGroup {
  const group = nodes.find((node) => node.kind === 'group' && node.label === label);
  assert.ok(group, `expected a group labelled ${label}`);
  return group as SessionGroup;
}

function rowFor(nodes: SessionNode[], agentId: string): SessionRow {
  const row = rows(nodes).find((candidate) => candidate.agentId === agentId);
  assert.ok(row, `expected a row for ${agentId}`);
  return row as SessionRow;
}

test('sessions are grouped by local recency, newest first', () => {
  const nodes = buildSessionView(
    [
      session({ agent_id: 'old', updated_at: at(2026, 8, 1) }),
      session({ agent_id: 'today', updated_at: at(2026, 9, 16, 8) }),
      session({ agent_id: 'week', updated_at: at(2026, 9, 10) }),
      session({ agent_id: 'yesterday', updated_at: at(2026, 9, 15) }),
    ],
    { now: NOW },
  );

  assert.deepEqual(
    nodes.map((node) => (node.kind === 'group' ? node.label : node.kind)),
    ['Today', 'This week', 'Earlier'],
  );
  // October 10 is the seventh local day back, so it is inside the week; the
  // 1st is not. Both are ordered newest first within their bucket.
  assert.deepEqual(groupOf(nodes, 'Today').rows.map((row) => row.agentId), ['today']);
  assert.deepEqual(groupOf(nodes, 'This week').rows.map((row) => row.agentId), [
    'yesterday',
    'week',
  ]);
  assert.deepEqual(groupOf(nodes, 'Earlier').rows.map((row) => row.agentId), ['old']);
});

test('a bucket with no rows is not shown', () => {
  const nodes = buildSessionView([session({ agent_id: 'a', updated_at: at(2026, 9, 16, 8) })], {
    now: NOW,
  });
  assert.equal(nodes.length, 1);
  assert.equal((nodes[0] as SessionGroup).label, 'Today');
});

test('created_at is the fallback when there is no updated_at', () => {
  const nodes = buildSessionView(
    [session({ agent_id: 'created', created_at: at(2026, 9, 16, 8) })],
    { now: NOW },
  );
  assert.equal(groupOf(nodes, 'Today').rows[0].agentId, 'created');
});

test('a session with no timestamp is listed last and not folded into a bucket', () => {
  const nodes = buildSessionView(
    [
      session({ agent_id: 'undated' }),
      session({ agent_id: 'today', updated_at: at(2026, 9, 16, 8) }),
    ],
    { now: NOW },
  );
  assert.deepEqual(groupOf(nodes, 'Today').rows.map((row) => row.agentId), ['today']);
  assert.deepEqual(groupOf(nodes, 'Undated').rows.map((row) => row.agentId), ['undated']);
});

test('with nothing dated at all, the rows are listed without headings', () => {
  const nodes = buildSessionView(
    [session({ agent_id: 'a' }), session({ agent_id: 'b' })],
    { now: NOW },
  );
  assert.ok(nodes.every((node) => node.kind === 'session'));
  // A single "Undated" heading over every session says less than the rows do.
  assert.deepEqual(nodes.map((node) => (node as SessionRow).agentId), ['a', 'b']);
});

test('an unparseable timestamp is treated as absent, not as the epoch', () => {
  const nodes = buildSessionView([session({ agent_id: 'odd', updated_at: 'not a date' })], {
    now: NOW,
  });
  assert.ok(nodes.every((node) => node.kind === 'session'));
  assert.equal((nodes[0] as SessionRow).agentId, 'odd');
});

test('a session with no title is labelled with its shortened id, whole in the tooltip', () => {
  // The fallback, and the reason the id never leaves the tooltip: a title is a
  // label, and a label is not what `resume` takes.
  const agentId = 'agent-0123456789abcdef';
  const nodes = buildSessionView([session({ agent_id: agentId, updated_at: at(2026, 9, 16, 8) })], {
    now: NOW,
  });
  const row = rowFor(nodes, agentId);
  assert.equal(row.label, agentId.slice(0, 12));
  assert.equal(row.id, agentId);
  assert.match(row.tooltip, new RegExp(agentId));
});

test('a session is labelled with its title, and still names its agent id in the tooltip', () => {
  const agentId = 'agent-0123456789abcdef';
  const nodes = buildSessionView(
    [session({ agent_id: agentId, updated_at: at(2026, 9, 16, 8), title: '看一下这是个什么工程' })],
    { now: NOW },
  );
  const row = rowFor(nodes, agentId);
  assert.equal(row.label, '看一下这是个什么工程');
  assert.equal(row.id, agentId);
  assert.match(row.tooltip, new RegExp(agentId));
});

test('a title of only whitespace falls back to the agent id', () => {
  const nodes = buildSessionView(
    [session({ agent_id: 'blank', updated_at: at(2026, 9, 16, 8), title: '   \n ' })],
    { now: NOW },
  );
  assert.equal(rowFor(nodes, 'blank').label, 'blank');
});

test('the description carries the model and the run count, and pluralises it', () => {
  const nodes = buildSessionView(
    [
      session({ agent_id: 'one', updated_at: at(2026, 9, 16, 8), runs: 1 }),
      session({ agent_id: 'many', updated_at: at(2026, 9, 16, 7), runs: 3, model: '' }),
    ],
    { now: NOW },
  );
  assert.equal(rowFor(nodes, 'one').description, 'gpt-5.2 · 1 run');
  // No model reported means no model in the description, not an empty slot.
  assert.equal(rowFor(nodes, 'many').description, '3 runs');
});

test('the icon follows the last status, and an unknown one is not guessed', () => {
  const nodes = buildSessionView(
    [
      session({ agent_id: 'ok', updated_at: at(2026, 9, 16, 8), last_status: 'finished' }),
      session({ agent_id: 'bad', updated_at: at(2026, 9, 16, 7), last_status: 'error' }),
      session({ agent_id: 'stop', updated_at: at(2026, 9, 16, 6), last_status: 'cancelled' }),
      session({ agent_id: 'none', updated_at: at(2026, 9, 16, 5) }),
      session({ agent_id: 'weird', updated_at: at(2026, 9, 16, 4), last_status: 'quantum' }),
    ],
    { now: NOW },
  );
  assert.equal(rowFor(nodes, 'ok').icon, 'check');
  assert.equal(rowFor(nodes, 'bad').icon, 'error');
  assert.equal(rowFor(nodes, 'stop').icon, 'circle-slash');
  assert.equal(rowFor(nodes, 'none').icon, 'history');
  assert.equal(rowFor(nodes, 'weird').icon, 'history');
  // The status the icon could not render is still reported, as unknown.
  assert.match(rowFor(nodes, 'weird').tooltip, /Last status quantum/);
  assert.match(rowFor(nodes, 'none').tooltip, /Last status unknown/);
});

test('the open session and the running one are marked, and running wins', () => {
  const sessions = [
    session({ agent_id: 'idle', updated_at: at(2026, 9, 16, 8) }),
    session({ agent_id: 'open', updated_at: at(2026, 9, 16, 7) }),
    session({ agent_id: 'busy', updated_at: at(2026, 9, 16, 6) }),
  ];
  const nodes = buildSessionView(sessions, {
    now: NOW,
    openAgentIds: ['open'],
    runningAgentIds: ['busy'],
  });

  assert.equal(rowFor(nodes, 'idle').contextValue, 'session');
  assert.equal(rowFor(nodes, 'open').contextValue, 'session.open');
  assert.match(rowFor(nodes, 'open').description, /open$/);
  assert.equal(rowFor(nodes, 'busy').contextValue, 'session.running');
  assert.match(rowFor(nodes, 'busy').description, /running$/);
  assert.equal(rowFor(nodes, 'busy').icon, 'sync~spin');

  // A session that is both open and running reads as running: it is the one
  // changing under the user.
  const both = buildSessionView(sessions, { now: NOW, openAgentIds: ['busy'], runningAgentIds: ['busy'] });
  assert.equal(rowFor(both, 'busy').contextValue, 'session.running');
  assert.doesNotMatch(rowFor(both, 'busy').description, /open/);
});

test('markers are not applied to a session with a different id', () => {
  const nodes = buildSessionView([session({ agent_id: 'a', updated_at: at(2026, 9, 16, 8) })], {
    now: NOW,
    openAgentIds: ['b'],
    runningAgentIds: ['c'],
  });
  assert.equal(rowFor(nodes, 'a').contextValue, 'session');
});

test('several rows can be open at once', () => {
  const sessions = [
    session({ agent_id: 'a', updated_at: at(2026, 9, 16, 8) }),
    session({ agent_id: 'b', updated_at: at(2026, 9, 16, 7) }),
    session({ agent_id: 'c', updated_at: at(2026, 9, 16, 6) }),
  ];
  const nodes = buildSessionView(sessions, { now: NOW, openAgentIds: ['a', 'c'], runningAgentIds: ['c'] });

  assert.equal(rowFor(nodes, 'a').contextValue, 'session.open');
  assert.equal(rowFor(nodes, 'b').contextValue, 'session');
  // Running still wins over open for a row that is both.
  assert.equal(rowFor(nodes, 'c').contextValue, 'session.running');
});

test('an empty list is a note, not an empty group', () => {
  const nodes = buildSessionView([], { now: NOW });
  assert.equal(nodes.length, 1);
  assert.equal(nodes[0].kind, 'note');
  assert.equal(nodes[0].id, 'note:empty');
});

test('each note says why the sidebar is empty, and only some offer a retry', () => {
  assert.equal(sessionNote('untrusted').retry, false);
  assert.equal(sessionNote('loading').retry, false);
  assert.equal(sessionNote('empty').retry, true);
  assert.equal(sessionNote('error', 'spawn failed').retry, true);
  // The host's own words, because "could not start the host" and a failed
  // digest check are different problems with different fixes.
  assert.equal(sessionNote('error', 'spawn failed').tooltip, 'spawn failed');
  assert.equal(sessionNote('error').tooltip.length > 0, true);
});

test('the tooltip reports only what the store actually returned', () => {
  const nodes = buildSessionView(
    [
      session({
        agent_id: 'a',
        updated_at: at(2026, 9, 16, 8),
        created_at: at(2026, 9, 16, 7),
        title: 'Fix the flaky test',
        runs: 4,
        last_status: 'finished',
        usage: { input_tokens: 10, output_tokens: 20, total_tokens: 30 },
      }),
    ],
    { now: NOW },
  );
  const tooltip = rowFor(nodes, 'a').tooltip;
  assert.match(tooltip, /Agent a/);
  assert.match(tooltip, /Workspace E:\\ws/);
  assert.match(tooltip, /Model openai\/gpt-5\.2/);
  assert.match(tooltip, /Runs 4/);
  assert.match(tooltip, /Tokens 30/);
  // The title is the label; it is not repeated in the tooltip, which is where
  // the id a command takes lives.
  assert.doesNotMatch(tooltip, /Fix the flaky test/);

  // A session with no usage line does not gain one.
  const bare = buildSessionView([session({ agent_id: 'b', updated_at: at(2026, 9, 16, 8) })], {
    now: NOW,
  });
  assert.doesNotMatch(rowFor(bare, 'b').tooltip, /Tokens/);
});
