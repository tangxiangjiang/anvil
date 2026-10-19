/**
 * The search picker's two decisions, without a window: what a query keeps, and
 * how the kept rows are labelled.
 */

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { SessionInfo } from '../../src/protocol';
import { filterSessions, sessionChoices } from '../../src/sessionSearch';

function session(agentId: string, title?: string, updatedAt?: string): SessionInfo {
  const info: SessionInfo = { agent_id: agentId, cwd: '/w', provider: 'openai', model: 'm', runs: 1 };
  if (title !== undefined) info.title = title;
  if (updatedAt !== undefined) info.updated_at = updatedAt;
  return info;
}

const sessions = [
  session('agent-0a1b2c3d4e5f', 'Fix the flaky test', '2026-01-01T00:00:00Z'),
  session('agent-9f8e7d6c5b4a', 'Write the release notes', '2026-01-03T00:00:00Z'),
  session('agent-112233445566'),
];

test('a query matches a title regardless of case', () => {
  assert.deepEqual(
    filterSessions(sessions, 'FLAKY').map((row) => row.agent_id),
    ['agent-0a1b2c3d4e5f'],
  );
});

test('a query matches the agent id', () => {
  assert.deepEqual(
    filterSessions(sessions, '9f8e').map((row) => row.agent_id),
    ['agent-9f8e7d6c5b4a'],
  );
});

test('several terms must all match, across title and id', () => {
  // One term from the title, one from the id: a person narrowing a list types
  // what they remember, and they do not remember it all from one field.
  assert.deepEqual(
    filterSessions(sessions, 'release 9f8e').map((row) => row.agent_id),
    ['agent-9f8e7d6c5b4a'],
  );
  assert.deepEqual(filterSessions(sessions, 'release flaky'), []);
});

test('an empty or whitespace query offers everything', () => {
  assert.deepEqual(filterSessions(sessions, '').length, 3);
  assert.deepEqual(filterSessions(sessions, '   ').length, 3);
});

test('choices are labelled by name and described by the full id, newest first', () => {
  const choices = sessionChoices(sessions);
  assert.deepEqual(
    choices.map((choice) => choice.agentId),
    ['agent-9f8e7d6c5b4a', 'agent-0a1b2c3d4e5f', 'agent-112233445566'],
  );
  assert.equal(choices[0].label, 'Write the release notes');
  assert.equal(choices[0].description, 'agent-9f8e7d6c5b4a');
  // A session nobody named is offered under its shortened id, because there is
  // no name to offer — not under a placeholder that would be mistaken for one.
  assert.equal(choices[2].label, 'agent-112233', 'the unnamed row falls back to the id');
});

test('the choices keep the order the filter handed back', () => {
  const choices = sessionChoices(filterSessions(sessions, 'notes'));
  assert.deepEqual(choices.map((choice) => choice.label), ['Write the release notes']);
});
