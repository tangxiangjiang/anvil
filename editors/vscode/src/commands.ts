/**
 * Slash commands, as data.
 *
 * A command is a name and what it means; the dispatch that acts on it lives in
 * `chatPanel.ts`, because every one of them needs a window, a picker, or a
 * session. This module only decides what can be typed and what a typed line
 * parses to — no `vscode` import, so it is testable directly.
 *
 * Skills contribute commands: `.anvil/skills/<id>/SKILL.md` ([14]) is a package
 * the agent can be given, so its id is a command that adds that skill to the
 * conversation. That is the whole mapping — a skill is not a prompt template,
 * and pretending otherwise would invent a substitution syntax the SDK does not
 * have.
 */

import { PermissionMode, SkillEntry } from './protocol';

export interface SlashCommand {
  /** Without the leading slash. */
  name: string;
  description: string;
  source: 'builtin' | 'skill';
  /** Skill commands only: the id to hand to the session. */
  skillId?: string;
}

/**
 * The commands the extension provides itself. Every one of them maps onto
 * something the session can already do — none of them is a prompt trick.
 */
export const BUILTIN_COMMANDS: readonly SlashCommand[] = [
  {
    name: 'plan',
    description: 'Switch to plan mode; anything after the command is sent as the request',
    source: 'builtin',
  },
  {
    name: 'model',
    description: 'Choose the model this conversation uses',
    source: 'builtin',
  },
  {
    name: 'resume',
    description: 'Open a past session from this workspace',
    source: 'builtin',
  },
  {
    name: 'clear',
    description: 'Start a new conversation',
    source: 'builtin',
  },
  {
    name: 'tasks',
    description: 'Show background commands and subagents',
    source: 'builtin',
  },
  {
    name: 'help',
    description: 'List the available commands',
    source: 'builtin',
  },
];

/** Skill ids as commands. An always-on skill is listed too: hiding it would
 * make the catalogue disagree with what the session already has. */
export function skillCommands(skills: SkillEntry[]): SlashCommand[] {
  const out: SlashCommand[] = [];
  for (const skill of skills) {
    if (!skill || typeof skill.id !== 'string' || skill.id === '') continue;
    const description = (skill.description ?? '').trim();
    out.push({
      name: skill.id,
      description: description === '' ? (skill.always ? 'Always-on skill' : 'Skill') : description,
      source: 'skill',
      skillId: skill.id,
    });
  }
  return out;
}

/** Built-ins first, then skills: a built-in name is never shadowed by a skill. */
export function commandCatalogue(skills: SkillEntry[]): SlashCommand[] {
  const taken = new Set(BUILTIN_COMMANDS.map((command) => command.name));
  return [...BUILTIN_COMMANDS, ...skillCommands(skills).filter((command) => !taken.has(command.name))];
}

/**
 * Splits a composer line into a command name and its argument text.
 *
 * Returns undefined when the line is not a command at all, which is what keeps
 * a path like `/usr/bin/env is missing` an ordinary prompt: only the first
 * token is ever considered, and the caller checks it against the catalogue
 * before acting.
 */
export function parseSlash(text: string): { name: string; args: string } | undefined {
  const trimmed = text.trim();
  if (!trimmed.startsWith('/')) return undefined;
  const body = trimmed.slice(1);
  if (body === '') return undefined;
  const match = /^(\S+)(?:[ \t]+([\s\S]*))?$/.exec(body);
  if (!match) return undefined;
  return { name: match[1], args: (match[2] ?? '').trim() };
}

/**
 * Finds the command a parsed name refers to. Built-ins match case-insensitively
 * because they are the extension's own words; a skill id matches exactly first,
 * since on a case-sensitive filesystem it names a directory.
 */
export function findCommand(commands: SlashCommand[], name: string): SlashCommand | undefined {
  const lower = name.toLowerCase();
  const builtin = commands.find((command) => command.source === 'builtin' && command.name === lower);
  if (builtin) return builtin;
  const exact = commands.find((command) => command.source === 'skill' && command.name === name);
  if (exact) return exact;
  return commands.find((command) => command.source === 'skill' && command.name.toLowerCase() === lower);
}

/**
 * What the palette shows for a partially typed command. An empty query lists
 * everything: pressing `/` is asking to see what there is.
 */
export function matchCommands(commands: SlashCommand[], query: string): SlashCommand[] {
  const needle = query.trim().toLowerCase();
  if (needle === '') return commands;
  const starts = commands.filter((command) => command.name.toLowerCase().startsWith(needle));
  if (starts.length > 0) return starts;
  return commands.filter((command) => command.description.toLowerCase().includes(needle));
}

/** The `@`-style trigger: the composer is typing a command and nothing else. */
export function slashQuery(text: string): string | undefined {
  const trimmed = text.trim();
  if (!trimmed.startsWith('/')) return undefined;
  const body = trimmed.slice(1);
  // Once there is whitespace the command is settled and the rest is its
  // argument, so the palette must get out of the way.
  if (/\s/.test(body)) return undefined;
  return body;
}

/**
 * What a composer line means once the catalogue is known.
 *
 * The split matters: deciding *what* was asked is here, and doing it — opening
 * a picker, re-opening a session — is in `chatPanel.ts`, where a window exists.
 * The decision is the part with the rules in it (a built-in never shadowed by a
 * skill, `/usr/bin/env` not a command at all), so it is the part worth testing
 * without a window.
 */
export type CommandAction =
  | { kind: 'prompt'; text: string }
  | { kind: 'mode'; mode: PermissionMode; args: string }
  | { kind: 'skill'; skillId: string; args: string }
  | { kind: 'model'; model: string }
  | { kind: 'resume'; agentId: string }
  | { kind: 'clear' }
  | { kind: 'tasks' }
  | { kind: 'help' };

export function route(text: string, commands: SlashCommand[]): CommandAction {
  const parsed = parseSlash(text);
  if (!parsed) return { kind: 'prompt', text };
  const command = findCommand(commands, parsed.name);
  // A slash that names nothing is an ordinary message, not an error: a path
  // typed at the start of a sentence is still a sentence.
  if (!command) return { kind: 'prompt', text };
  switch (command.name) {
    case 'plan':
      return { kind: 'mode', mode: 'plan', args: parsed.args };
    case 'model':
      return { kind: 'model', model: parsed.args };
    case 'resume':
      return { kind: 'resume', agentId: parsed.args };
    case 'clear':
      return { kind: 'clear' };
    case 'tasks':
      return { kind: 'tasks' };
    case 'help':
      return { kind: 'help' };
    default:
      break;
  }
  if (command.source === 'skill' && command.skillId) {
    return { kind: 'skill', skillId: command.skillId, args: parsed.args };
  }
  return { kind: 'prompt', text };
}
