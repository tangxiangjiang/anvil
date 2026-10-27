import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { TurnQueue } from '../../src/turnQueue';

test('queued turns leave in FIFO order', () => {
  const queue = new TurnQueue<string>();
  queue.enqueue('one');
  queue.enqueue('two');

  assert.equal(queue.count, 2);
  assert.equal(queue.next(), 'one');
  assert.equal(queue.next(), 'two');
  assert.equal(queue.next(), undefined);
});

test('a priority turn runs before queued work without losing it', () => {
  const queue = new TurnQueue<string>();
  queue.enqueue('one');
  queue.enqueue('two');
  queue.prioritize('now');

  assert.equal(queue.interrupting, true);
  assert.deepEqual([queue.next(), queue.next(), queue.next()], ['now', 'one', 'two']);
});

test('a newer priority turn preserves the earlier priority request', () => {
  const queue = new TurnQueue<string>();
  queue.enqueue('queued');
  queue.prioritize('first-now');
  queue.prioritize('latest-now');

  assert.deepEqual([queue.next(), queue.next(), queue.next()], ['latest-now', 'first-now', 'queued']);
});

test('clear drops FIFO and priority turns together', () => {
  const queue = new TurnQueue<string>();
  queue.enqueue('queued');
  queue.prioritize('now');
  queue.clear();

  assert.equal(queue.count, 0);
  assert.equal(queue.interrupting, false);
  assert.equal(queue.next(), undefined);
});
