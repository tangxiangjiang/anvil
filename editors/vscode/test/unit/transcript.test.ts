/**
 * A stored conversation, folded into the messages the panel can already draw.
 *
 * The replay's whole value is that it reuses the live renderers, which means the
 * folding has to produce exactly the shapes they expect — and the two ways it
 * can go wrong are both quiet. A user record the loop wrote to the model, shown
 * as a turn, puts words in the user's mouth; a tool call whose arguments were
 * dropped is a card that cannot open its change. Neither throws.
 */

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { TranscriptEntry } from '../../src/protocol';
import { ReplayStep, replayOf } from '../../src/transcript';

function kinds(steps: ReplayStep[]): string[] {
  return steps.map((step) => step.kind);
}

test('a stored conversation replays oldest first, in the shapes the tab draws', () => {
  const entries: TranscriptEntry[] = [
    { kind: 'message', run_id: 'r1', role: 'user', content: 'add a line' },
    { kind: 'message', run_id: 'r1', role: 'assistant', content: 'I will.' },
    {
      kind: 'message',
      run_id: 'r1',
      role: 'assistant',
      tool_calls: [{ id: 't1', name: 'write_file', args: { path: 'a.txt', content: 'x' } }],
    },
    {
      kind: 'message',
      run_id: 'r1',
      role: 'tool',
      tool_call_id: 't1',
      name: 'write_file',
      content: 'wrote 1 bytes',
    },
    { kind: 'message', run_id: 'r1', role: 'assistant', content: 'Done.' },
    { kind: 'run_end', run_id: 'r1', status: 'finished', steps: 3, usage: { input_tokens: 1, output_tokens: 2, total_tokens: 3 } },
  ];

  const steps = replayOf(entries, 's1');
  assert.deepEqual(kinds(steps), ['user', 'event', 'event', 'event', 'event', 'runEnd']);

  const user = steps[0];
  assert.equal(user.kind === 'user' && user.text, 'add a line');

  // The assistant text is an `assistant` event, which is what the Markdown
  // renderer is fed live, so replayed prose is typeset the same way.
  const prose = steps[1];
  assert.ok(prose.kind === 'event');
  assert.equal(prose.event.type, 'assistant');
  assert.equal(prose.event.text, 'I will.');
  assert.equal(prose.event.session_id, 's1');
  assert.equal(prose.event.run_id, 'r1');

  // The tool call travels with its arguments, because the card's review is
  // derived from them and a replayed call has nothing else.
  const call = steps[2];
  assert.ok(call.kind === 'event');
  assert.equal(call.event.type, 'tool_call');
  assert.equal(call.event.tool_call_id, 't1');
  assert.equal(call.event.name, 'write_file');
  assert.deepEqual(call.event.args, { path: 'a.txt', content: 'x' });

  // The result is keyed by the same id, which is how the card finds its ending.
  const result = steps[3];
  assert.ok(result.kind === 'event');
  assert.equal(result.event.type, 'tool_result');
  assert.equal(result.event.tool_call_id, 't1');
  assert.equal(result.event.content, 'wrote 1 bytes');
  // And it claims nothing about failure. Whether the call failed was never
  // written down; an `is_error` invented here would be a card that says "done"
  // or "failed" on no evidence at all.
  assert.equal(result.event.is_error, undefined);

  const end = steps[5];
  assert.ok(end.kind === 'runEnd');
  assert.equal(end.done.status, 'finished');
  assert.equal(end.done.steps, 3);
  assert.equal(end.done.usage?.total_tokens, 3);
  assert.equal(end.done.session_id, 's1');
});

test('a result whose call fell outside the window gets the row it should have had', () => {
  // A cap that lands inside a run leaves a result with no call before it. The
  // renderer has no card to draw it into, and an error block is the wrong
  // shape: the model got this result and continued. The replay synthesizes the
  // row from the only part of the call the store kept, its name.
  const steps = replayOf(
    [
      { kind: 'message', run_id: 'r1', role: 'tool', tool_call_id: 't9', name: 'read_file', content: 'contents' },
    ],
    's1',
  );
  assert.deepEqual(kinds(steps), ['event', 'event']);
  const call = steps[0];
  assert.ok(call.kind === 'event');
  assert.equal(call.event.type, 'tool_call');
  assert.equal(call.event.tool_call_id, 't9');
  assert.equal(call.event.name, 'read_file');
  // No arguments: the store keeps the result's tool name, not the call's JSON,
  // and an invented IN would claim more than it holds.
  assert.equal(call.event.args, undefined);
  const result = steps[1];
  assert.ok(result.kind === 'event');
  assert.equal(result.event.type, 'tool_result');
  assert.equal(result.event.tool_call_id, 't9');
});

test('a result whose call is already in the replay does not get a second row', () => {
  const steps = replayOf(
    [
      {
        kind: 'message',
        run_id: 'r1',
        role: 'assistant',
        tool_calls: [{ id: 't1', name: 'shell', args: { command: 'ls' } }],
      },
      { kind: 'message', run_id: 'r1', role: 'tool', tool_call_id: 't1', name: 'shell', content: 'a' },
    ],
    's1',
  );
  assert.equal(steps.filter((step) => step.kind === 'event' && step.event.type === 'tool_call').length, 1);
  // The call's own arguments survive; only a missing call is synthesized.
  const call = steps[0];
  assert.ok(call.kind === 'event');
  assert.deepEqual(call.event.args, { command: 'ls' });
});

test('the loop’s own notes to the model are not replayed as the user’s turns', () => {
  // The store keeps these under `role:"user"`, so the only thing that tells them
  // apart is where they sit: a run opens with the one message that started it,
  // and the loop's notes come after. Shown as a "you" bubble, a vision note or a
  // loop guard warning would read as something the user typed.
  const entries: TranscriptEntry[] = [
    { kind: 'message', run_id: 'r1', role: 'user', content: 'what is in this screenshot?' },
    { kind: 'message', run_id: 'r1', role: 'user', content: '(attached images from read_image)' },
    { kind: 'message', run_id: 'r1', role: 'assistant', content: 'A login form.' },
    { kind: 'run_end', run_id: 'r1', status: 'finished', steps: 2 },
  ];

  const steps = replayOf(entries, 's1');
  assert.deepEqual(kinds(steps), ['user', 'notice', 'event', 'runEnd']);
  const notice = steps[1];
  assert.ok(notice.kind === 'notice');
  assert.equal(notice.text, '(attached images from read_image)');
});

test('a second prompt is a user turn again, because a run end closes the first', () => {
  const entries: TranscriptEntry[] = [
    { kind: 'message', run_id: 'r1', role: 'user', content: 'first' },
    { kind: 'run_end', run_id: 'r1', status: 'finished', steps: 1 },
    { kind: 'message', run_id: 'r2', role: 'user', content: 'second' },
    { kind: 'message', run_id: 'r2', role: 'user', content: 'Loop guard: stop repeating.' },
  ];

  const steps = replayOf(entries, 's1');
  assert.deepEqual(kinds(steps), ['user', 'runEnd', 'user', 'notice']);
  const first = steps[0];
  const second = steps[2];
  assert.ok(first.kind === 'user' && second.kind === 'user');
  assert.equal(first.text, 'first');
  assert.equal(second.text, 'second');
});

test('replay hides the editor context envelope from the user bubble', () => {
  const entries: TranscriptEntry[] = [
    {
      kind: 'message',
      run_id: 'r1',
      role: 'user',
      content:
        '<context>\n<diagnostics count="1">\nsrc/main.ts:1:1 error: broken\n</diagnostics>\n</context>\n\n继续帮我实现下',
    },
  ];

  const steps = replayOf(entries, 's1');
  assert.deepEqual(steps, [{ kind: 'user', text: '继续帮我实现下' }]);
});

test('a turn that carried only an image is still a turn', () => {
  // The prompt is empty and the images were never stored, so this is all there
  // is: an empty user turn. Dropping it would lose the fact that it happened.
  const steps = replayOf([{ kind: 'message', run_id: 'r1', role: 'user', content: '' }], 's1');
  assert.deepEqual(kinds(steps), ['user']);
  const user = steps[0];
  assert.ok(user.kind === 'user' && user.text === '');
});

test('an empty assistant turn draws nothing, and a call without content still draws its card', () => {
  const steps = replayOf(
    [
      { kind: 'message', run_id: 'r1', role: 'assistant', content: '' },
      {
        kind: 'message',
        run_id: 'r1',
        role: 'assistant',
        tool_calls: [{ id: 't2', name: 'shell', args: { command: 'ls' } }],
      },
    ],
    's1',
  );
  assert.deepEqual(kinds(steps), ['event']);
  const call = steps[0];
  assert.ok(call.kind === 'event');
  assert.equal(call.event.type, 'tool_call');
  assert.equal(call.event.tool_call_id, 't2');
});

test('an entry kind this build does not know is skipped, and a missing field is not a crash', () => {
  // A newer host may add a kind; an older panel has no renderer for it. Reading
  // it as a message would draw the wrong thing, so it is left out.
  const steps = replayOf(
    [
      { kind: 'checkpoint' as unknown as TranscriptEntry['kind'], run_id: 'r1' },
      { kind: 'message', run_id: 'r1', role: 'system' as unknown as 'user', content: 'ignored' },
      { kind: 'message', run_id: 'r1', role: 'tool', content: '' },
    ],
    's1',
  );
  assert.deepEqual(kinds(steps), ['event']);
  const out = steps[0];
  assert.ok(out.kind === 'event');
  assert.equal(out.event.type, 'tool_result');
  assert.equal(out.event.content, '');
});

test('a stored todo_write replays as the checklist it left on screen', () => {
  // The checklist has no record of its own: the call's arguments are the whole
  // list, and the live path shows the same thing through the `todo` event. The
  // replay synthesises that event so the webview has one renderer for both.
  const entries: TranscriptEntry[] = [
    {
      kind: 'message',
      run_id: 'r1',
      role: 'assistant',
      tool_calls: [
        {
          id: 't1',
          name: 'todo_write',
          args: { items: [{ text: ' read it ', status: 'done' }, { text: 'fix it', status: 'in_progress' }] },
        },
      ],
    },
    { kind: 'message', run_id: 'r1', role: 'tool', tool_call_id: 't1', name: 'todo_write', content: '2 items' },
  ];

  const steps = replayOf(entries, 's1');
  assert.deepEqual(kinds(steps), ['event', 'event', 'event']);
  const todo = steps[1];
  assert.ok(todo.kind === 'event');
  assert.equal(todo.event.type, 'todo');
  assert.equal(todo.event.session_id, 's1');
  // Trimmed like the tool stored it, so a replayed row reads exactly as it did.
  assert.deepEqual(todo.event.items, [
    { text: 'read it', status: 'done' },
    { text: 'fix it', status: 'in_progress' },
  ]);
});

test('a todo_write the tool would have refused replays as no list', () => {
  // Failure is not recorded, so the replay has to decide from the arguments —
  // and it applies the tool's own rule. Drawing a list the host rejected would
  // show a plan the model never managed to send.
  for (const items of [
    [{ text: 'a', status: 'blocked' }],
    [{ text: '   ', status: 'pending' }],
    [{ status: 'pending' }],
  ]) {
    const steps = replayOf(
      [
        {
          kind: 'message',
          run_id: 'r1',
          role: 'assistant',
          tool_calls: [{ id: 't1', name: 'todo_write', args: { items } }],
        },
      ],
      's1',
    );
    assert.deepEqual(kinds(steps), ['event'], JSON.stringify(items));
    const only = steps[0];
    assert.ok(only.kind === 'event');
    assert.equal(only.event.type, 'tool_call');
  }
});

test('an empty todo_write replays as an empty list, which is how a call clears it', () => {
  const steps = replayOf(
    [
      {
        kind: 'message',
        run_id: 'r1',
        role: 'assistant',
        tool_calls: [{ id: 't1', name: 'todo_write', args: { items: [] } }],
      },
    ],
    's1',
  );
  assert.deepEqual(kinds(steps), ['event', 'event']);
  const todo = steps[1];
  assert.ok(todo.kind === 'event');
  assert.equal(todo.event.type, 'todo');
  assert.deepEqual(todo.event.items, []);
});

test('a compaction boundary replays as the same row the live event draws', () => {
  // The store keeps a summary record where the conversation was folded. It has
  // the boundary and the text, but not the counts — so the replayed row says
  // that it happened and nothing it cannot support.
  const entries: TranscriptEntry[] = [
    { kind: 'message', run_id: 'r1', role: 'user', content: 'first' },
    { kind: 'run_end', run_id: 'r1', status: 'finished', steps: 1 },
    { kind: 'summary', run_id: 'r1', content: 'Earlier turns were summarized.' },
    { kind: 'message', run_id: 'r2', role: 'user', content: 'second' },
  ];

  const steps = replayOf(entries, 's1');
  assert.deepEqual(kinds(steps), ['user', 'runEnd', 'event', 'user']);
  const boundary = steps[2];
  assert.ok(boundary.kind === 'event');
  assert.equal(boundary.event.type, 'compact');
  assert.equal(boundary.event.session_id, 's1');
  assert.equal(boundary.event.run_id, 'r1');
  // No counts, because the record has none: an invented "0 → 0" would be a
  // divider claiming a compaction that moved nothing.
  assert.equal(boundary.event.compact, undefined);
  // The user's turn after the boundary is still the user's turn: a summary
  // record is a note about the conversation, not a run.
  const after = steps[3];
  assert.ok(after.kind === 'user');
  assert.equal(after.text, 'second');
});

test('a run boundary that never said how it ended replays as one that finished', () => {
  // The status decides a word on screen, and there is always one: an absent
  // status would otherwise print "undefined" over the middle of a transcript.
  const steps = replayOf([{ kind: 'run_end', run_id: 'r1' }], 's1');
  assert.deepEqual(kinds(steps), ['runEnd']);
  const end = steps[0];
  assert.ok(end.kind === 'runEnd');
  assert.equal(end.done.status, 'finished');
  assert.equal(end.done.steps, undefined);
});

test('a stored terminal failure keeps its reason and code after replay', () => {
  const steps = replayOf([
    {
      kind: 'run_end',
      run_id: 'r1',
      status: 'error',
      steps: 13,
      err: 'model provider returned 5 consecutive empty responses',
      code: 'empty_response',
    },
  ], 's1');

  assert.deepEqual(kinds(steps), ['runEnd']);
  const end = steps[0];
  assert.ok(end.kind === 'runEnd');
  assert.equal(end.done.status, 'error');
  assert.equal(end.done.err, 'model provider returned 5 consecutive empty responses');
  assert.equal(end.done.code, 'empty_response');
});
