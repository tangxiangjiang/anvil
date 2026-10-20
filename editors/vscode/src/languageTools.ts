/**
 * Language, test and build settings as wire values.
 *
 * The editor settings are loose (`unknown` from the configuration API), while
 * `session.new` expects exact shapes. This module is the one place that narrows
 * one to the other, and it is deliberately free of a `vscode` import so the
 * mapping can be tested without an editor.
 *
 * Both builders return `undefined` for "nothing to send". That is what keeps a
 * client with no settings from adding a field to `session.new`; the host's
 * absence and the setting's emptiness have to mean the same thing.
 */

import { LspParams, LspServerParams } from './protocol';

/**
 * A language-server list for `session.new`, or undefined when there is nothing
 * to turn on.
 *
 * Entries that could never start a process (no command, or neither language nor
 * pattern) are dropped rather than sent to fail the whole session: one bad row
 * in a settings array is not a reason to refuse a conversation. The host
 * refuses the same shapes when they arrive from a client that sends them
 * directly, so the dropping here is a convenience, not the only guard.
 */
export function lspParams(enabled: boolean, servers: unknown): LspParams | undefined {
  if (!enabled || !Array.isArray(servers)) return undefined;
  const out: LspServerParams[] = [];
  for (const raw of servers) {
    if (!raw || typeof raw !== 'object') continue;
    const entry = raw as Record<string, unknown>;
    const command = text(entry.command);
    const language = text(entry.language);
    const pattern = text(entry.pattern);
    if (command === '' || (language === '' && pattern === '')) continue;
    const server: LspServerParams = { command };
    if (language !== '') server.language = language;
    if (pattern !== '') server.pattern = pattern;
    const args = strings(entry.args);
    if (args) server.args = args;
    out.push(server);
  }
  if (out.length === 0) return undefined;
  return { enabled: true, servers: out };
}

/**
 * An argv array for `test_command` / `build_command`, or undefined.
 *
 * The command is a list because it is never handed to a shell. An array with a
 * non-string or an empty first word is not a command; treating it as "unset"
 * leaves the tool unregistered instead of failing the session at create time.
 */
export function argv(value: unknown): string[] | undefined {
  if (!Array.isArray(value) || value.length === 0) return undefined;
  if (!value.every((item): item is string => typeof item === 'string')) return undefined;
  if (value[0].trim() === '') return undefined;
  return value;
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

/** An array of strings, or undefined when the value is not one. */
function strings(value: unknown): string[] | undefined {
  if (!Array.isArray(value)) return undefined;
  if (!value.every((item): item is string => typeof item === 'string')) return undefined;
  return value.length === 0 ? undefined : value;
}
