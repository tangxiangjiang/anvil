import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { LineFramer } from '../../src/hostClient';

test('a line split across chunks is emitted once, whole', () => {
  const framer = new LineFramer();
  assert.deepEqual(framer.push('{"a"'), []);
  assert.deepEqual(framer.push(':1}'), []);
  assert.deepEqual(framer.push('\n'), ['{"a":1}']);
});

test('several lines in one chunk come out in order', () => {
  const framer = new LineFramer();
  assert.deepEqual(framer.push('one\ntwo\nthree\n'), ['one', 'two', 'three']);
});

test('a trailing partial line is held until its newline arrives', () => {
  const framer = new LineFramer();
  assert.deepEqual(framer.push('done\n{"partial"'), ['done']);
  assert.deepEqual(framer.push('}\n'), ['{"partial"}']);
});

test('CRLF endings do not leave a carriage return in the payload', () => {
  const framer = new LineFramer();
  assert.deepEqual(framer.push('{"a":1}\r\n{"b":2}\r\n'), ['{"a":1}', '{"b":2}']);
});

test('blank lines are dropped rather than parsed', () => {
  const framer = new LineFramer();
  assert.deepEqual(framer.push('\n\n  \n{"a":1}\n'), ['{"a":1}']);
});

test('a split CRLF is not left as a stray carriage return', () => {
  const framer = new LineFramer();
  assert.deepEqual(framer.push('{"a":1}\r'), []);
  assert.deepEqual(framer.push('\n'), ['{"a":1}']);
});
