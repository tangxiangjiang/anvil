/**
 * Whether a reloaded window comes back to its session.
 *
 * The decision is three-valued rather than a boolean because "forget this id"
 * and "there is nothing to do" are different actions, and only one of them
 * writes to the workspace state. Merging them would either resurrect an agent
 * that is gone or forget one that is not.
 */

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { shouldReattach } from '../../src/reattach';

test('nothing remembered means nothing to come back to', () => {
  assert.equal(shouldReattach(undefined, []), 'none');
  assert.equal(shouldReattach('', [{ agent_id: 'a1' }]), 'none');
});

test('a remembered agent the workspace still stores is resumed', () => {
  assert.equal(shouldReattach('a1', [{ agent_id: 'a1' }, { agent_id: 'a2' }]), 'resume');
});

test('a remembered agent the workspace no longer has is forgotten', () => {
  // The one case that cannot be reached by hand in a test, and the reason this
  // is a function of two arguments: an id that outlived its agent would
  // otherwise be resumed on every reload, failing every time.
  assert.equal(shouldReattach('a1', []), 'clear');
  assert.equal(shouldReattach('a1', [{ agent_id: 'a2' }]), 'clear');
});

test('a listing that could not be read is not evidence the agent is gone', () => {
  // No host, a host too old to answer, a workspace that would not start one —
  // all arrive as `undefined`, and none of them says anything about the agent.
  // Clearing here would forget a session that is perfectly intact.
  assert.equal(shouldReattach('a1', undefined), 'none');
});
