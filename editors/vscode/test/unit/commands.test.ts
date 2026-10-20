/**
 * The slash catalogue and the routing of a typed line.
 *
 * What is being pinned is the set of rules that decide whether typed text is a
 * command at all: a built-in is never shadowed by a skill with the same name, a
 * path that happens to start with a slash is still a sentence, and every
 * built-in routes to the session capability it names rather than to a prompt.
 */

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import {
  BUILTIN_COMMANDS,
  commandCatalogue,
  findCommand,
  matchCommands,
  parseSlash,
  route,
  skillCommands,
  slashQuery,
} from '../../src/commands';
import { SkillEntry } from '../../src/protocol';

function skill(over: Partial<SkillEntry> & { id: string }): SkillEntry {
  return { name: over.id, ...over };
}

const SKILLS: SkillEntry[] = [
  skill({ id: 'commit-style', description: 'Write commit messages the way this repo does' }),
  skill({ id: 'release-notes', always: true }),
  skill({ id: 'plan', description: 'A skill that collides with a built-in' }),
];

test('parseSlash splits a command from its arguments', () => {
  assert.deepEqual(parseSlash('/plan fix the parser'), { name: 'plan', args: 'fix the parser' });
  assert.deepEqual(parseSlash('/clear'), { name: 'clear', args: '' });
  assert.deepEqual(parseSlash('  /help  '), { name: 'help', args: '' });
  // Only the first token is ever a name: everything after it is one blob, so a
  // multi-line argument survives intact.
  assert.deepEqual(parseSlash('/plan line one\nline two'), {
    name: 'plan',
    args: 'line one\nline two',
  });
});

test('parseSlash refuses what is not a command', () => {
  assert.equal(parseSlash('explain this'), undefined);
  assert.equal(parseSlash('/'), undefined);
  assert.equal(parseSlash('   '), undefined);
  assert.equal(parseSlash('and/or that'), undefined);
});

test('a leading path is not a command', () => {
  // The composer sees this all the time, and answering it as an unknown command
  // would be an error message where a prompt belonged.
  const action = route('/usr/bin/env is missing from PATH', commandCatalogue([]));
  assert.deepEqual(action, { kind: 'prompt', text: '/usr/bin/env is missing from PATH' });
});

test('the catalogue lists built-ins first and a skill never shadows one', () => {
  const catalogue = commandCatalogue(SKILLS);
  const names = catalogue.map((command) => command.name);
  assert.deepEqual(names.slice(0, BUILTIN_COMMANDS.length), [
    'plan',
    'model',
    'resume',
    'clear',
    'compact',
    'tasks',
    'mcp',
    'help',
  ]);
  assert.equal(names.filter((name) => name === 'plan').length, 1);
  // The colliding skill is dropped, not renamed: a command that did something
  // other than what its name says would be worse than one that is missing.
  assert.equal(findCommand(catalogue, 'plan')?.source, 'builtin');
  assert.equal(names.includes('commit-style'), true);
  assert.equal(names.includes('release-notes'), true);
});

test('an always-on skill is still listed', () => {
  // It is already in the session, so hiding it would make the catalogue
  // disagree with the session it describes.
  const commands = skillCommands(SKILLS);
  const always = commands.find((command) => command.name === 'release-notes');
  assert.equal(always?.description, 'Always-on skill');
});

test('built-ins match case-insensitively, a skill id matches as a path', () => {
  const catalogue = commandCatalogue(SKILLS);
  assert.equal(findCommand(catalogue, 'PLAN')?.name, 'plan');
  assert.equal(findCommand(catalogue, 'commit-style')?.skillId, 'commit-style');
  assert.equal(findCommand(catalogue, 'Commit-Style')?.skillId, 'commit-style');
  assert.equal(findCommand(catalogue, 'nope'), undefined);
});

test('the palette matches names first and descriptions second', () => {
  const catalogue = commandCatalogue(SKILLS);
  assert.deepEqual(
    matchCommands(catalogue, 're').map((command) => command.name),
    ['resume', 'release-notes'],
  );
  // Nothing starts with "commit" except the skill, but "messages" only appears
  // in a description, so the fallback is what makes it findable at all.
  assert.deepEqual(
    matchCommands(catalogue, 'messages').map((command) => command.name),
    ['commit-style'],
  );
  assert.equal(matchCommands(catalogue, '').length, catalogue.length);
  assert.deepEqual(matchCommands(catalogue, 'zzz'), []);
});

test('slashQuery is open only while the command name is still being typed', () => {
  assert.equal(slashQuery('/'), '');
  assert.equal(slashQuery('/pl'), 'pl');
  assert.equal(slashQuery('/plan fix'), undefined);
  assert.equal(slashQuery('hello'), undefined);
});

test('every built-in routes to the capability it names', () => {
  const catalogue = commandCatalogue(SKILLS);
  assert.deepEqual(route('/plan', catalogue), { kind: 'mode', mode: 'plan', args: '' });
  assert.deepEqual(route('/plan look at main.go', catalogue), {
    kind: 'mode',
    mode: 'plan',
    args: 'look at main.go',
  });
  assert.deepEqual(route('/model gpt-5.2', catalogue), { kind: 'model', model: 'gpt-5.2' });
  assert.deepEqual(route('/resume', catalogue), { kind: 'resume', agentId: '' });
  assert.deepEqual(route('/resume a7', catalogue), { kind: 'resume', agentId: 'a7' });
  assert.deepEqual(route('/clear', catalogue), { kind: 'clear' });
  assert.deepEqual(route('/tasks', catalogue), { kind: 'tasks' });
  // The manager covers both layers, so nothing after `/mcp` is carried: there is
  // no per-conversation filter for it to be one.
  assert.deepEqual(route('/mcp', catalogue), { kind: 'mcp' });
  assert.deepEqual(route('/MCP servers', catalogue), { kind: 'mcp' });
  assert.deepEqual(route('/help', catalogue), { kind: 'help' });
});

test('/compact is a host request, never a prompt', () => {
  const catalogue = commandCatalogue(SKILLS);
  assert.deepEqual(route('/compact', catalogue), { kind: 'compact' });
  assert.deepEqual(route('/COMPACT', catalogue), { kind: 'compact' });
  // Whatever follows the command is neither a prompt nor an argument: there is
  // nothing for the model to be asked, so the text is not carried anywhere.
  assert.deepEqual(route('/compact keep the last turn', catalogue), { kind: 'compact' });
  // It is a built-in, so a skill with the same id cannot take the name.
  const colliding = commandCatalogue([skill({ id: 'compact', description: 'not this' })]);
  assert.equal(findCommand(colliding, 'compact')?.source, 'builtin');
  assert.equal(findCommand(colliding, 'compact')?.description.includes('Summarize'), true);
});

test('a skill routes to the skill, with whatever followed it as the prompt', () => {
  const catalogue = commandCatalogue(SKILLS);
  assert.deepEqual(route('/commit-style', catalogue), {
    kind: 'skill',
    skillId: 'commit-style',
    args: '',
  });
  assert.deepEqual(route('/commit-style write the message', catalogue), {
    kind: 'skill',
    skillId: 'commit-style',
    args: 'write the message',
  });
});

test('text that names nothing stays a prompt, verbatim', () => {
  const catalogue = commandCatalogue(SKILLS);
  assert.deepEqual(route('what does this do', catalogue), {
    kind: 'prompt',
    text: 'what does this do',
  });
  // A slash that names nothing is not stripped and not answered as an error:
  // the model sees what the user typed.
  assert.deepEqual(route('/etc/hosts is wrong', catalogue), {
    kind: 'prompt',
    text: '/etc/hosts is wrong',
  });
});
