import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { classifyCode, classifyDone, transportFailure } from '../../src/errors';
import { DoneParams } from '../../src/protocol';

function done(over: Partial<DoneParams>): DoneParams {
  return { session_id: 's1', run_id: 'r1', status: 'error', ...over };
}

test('a finished run has nothing to say', () => {
  const failure = classifyDone(done({ status: 'finished', result: 'ok' }));
  assert.equal(failure.kind, 'none');
  assert.equal(failure.message, '');
  assert.equal(failure.isError, false);
});

test('a cancelled run is reported as stopped, not as a failure', () => {
  const failure = classifyDone(done({ status: 'cancelled' }));
  assert.equal(failure.kind, 'cancelled');
  assert.equal(failure.message, 'Stopped.');
  assert.equal(failure.isError, false);
});

test('each host code maps to its own message', () => {
  const cases: Array<[string, string]> = [
    ['max_steps', 'max_steps'],
    ['provider', 'provider'],
    ['empty_response', 'empty_response'],
    ['finish_protocol', 'finish_protocol'],
    ['max_wall_time', 'wall_time'],
    ['context_deadline', 'deadline'],
    ['context_overflow', 'context'],
  ];
  const messages = new Set<string>();
  for (const [code, kind] of cases) {
    const failure = classifyDone(done({ code, err: `detail for ${code}` }));
    assert.equal(failure.kind, kind, code);
    assert.equal(failure.isError, true, code);
    assert.ok(failure.message.length > 0, code);
    assert.ok(failure.message.includes(`detail for ${code}`), `keeps the host's words for ${code}`);
    messages.add(failure.message);
  }
  // The whole point of the mapping: a user can tell these apart.
  assert.equal(messages.size, cases.length);
});

test('an exhausted empty response does not blame credentials', () => {
  const detail = 'model provider returned 5 consecutive empty responses';
  const failure = classifyDone(done({ code: 'empty_response', err: detail }));
  assert.equal(failure.kind, 'empty_response');
  assert.equal(failure.message, detail);
  assert.doesNotMatch(failure.message, /API key|model id|baseUrl/i);
});

test('an unknown code keeps whatever the host said', () => {
  const failure = classifyDone(done({ code: 'weird', err: 'something specific broke' }));
  assert.equal(failure.kind, 'unknown');
  assert.equal(failure.message, 'something specific broke');
});

test('an error with no code and no message still says something', () => {
  const failure = classifyDone(done({}));
  assert.equal(failure.message, 'The run failed.');
});

test('code matching is case-insensitive', () => {
  assert.equal(classifyCode('MAX_STEPS'), 'max_steps');
  assert.equal(classifyCode(undefined), 'unknown');
});

test('a transport failure is presented as an error with its own text', () => {
  const failure = transportFailure(new Error('the host exited (code 1)'));
  assert.equal(failure.isError, true);
  assert.equal(failure.message, 'the host exited (code 1)');
});
