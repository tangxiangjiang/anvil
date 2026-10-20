/**
 * The MCP file layers, as the two settings say them, and what a listing adds
 * up to.
 *
 * Pure, so both halves can be checked without VS Code: the mapping from the
 * two booleans to the wire's `setting_sources`, the note the card draws for a
 * layer whose file is not there, and the words a state is shown in. The card
 * itself is chat.js's job; this module decides what it is told.
 */

import {
  McpListResult,
  McpScope,
  McpServerDraft,
  McpServerInfo,
  McpSourceInfo,
  McpState,
  McpToolInfo,
} from './protocol';

export interface McpSettings {
  /** Read `.cursor/mcp.json` in the workspace. */
  project: boolean;
  /** Read `~/.cursor/mcp.json`. */
  user: boolean;
}

/**
 * The wire value for the enabled layers, in the order the host validates them.
 *
 * Both off is the empty list, which is the SDK's inline-only behaviour — the
 * state every editor session was in before this setting existed.
 */
export function settingSourcesFor(settings: McpSettings): string[] {
  const sources: string[] = [];
  if (settings.project) sources.push('project');
  if (settings.user) sources.push('user');
  return sources;
}

/** Whether a session has any file layer to load. */
export function mcpEnabled(settings: McpSettings): boolean {
  return settingSourcesFor(settings).length > 0;
}

/**
 * The lines the card adds under the servers: one per enabled layer whose file
 * is absent.
 *
 * "Off" and "enabled but no file" are different states, and only the second is
 * worth explaining: the first is what the settings pane shows, while the second
 * is a user who expects servers and has a path that does not exist. The path is
 * printed as the host resolved it, so it can be opened.
 */
export function missingMcpSources(sources: McpSourceInfo[]): string[] {
  return sources
    .filter((source) => source.exists !== true)
    .map((source) => `${source.path} was not found.`);
}

/**
 * Whether the host behind this listing can observe a connection at all.
 *
 * The flag is 0.44's: a host from before it reports configuration and no state,
 * and a card that drew a status word for such a host would be inventing one. The
 * check is `=== true` rather than truthiness so a host that starts sending the
 * field with another value fails closed, to the card that always worked.
 */
export function hasRuntime(result: McpListResult): boolean {
  return result.runtime === true;
}

/**
 * A state as the card and the manager spell it.
 *
 * `auth_required` is the one that is not the enum's name: "needs sign-in" is
 * what a person does about it. An absent state answers with an empty string,
 * which the card reads as "this host says nothing" and draws nothing for.
 */
export function stateLabel(state: McpState | undefined): string {
  switch (state) {
    case 'disabled':
      return 'disabled';
    case 'connecting':
      return 'connecting';
    case 'connected':
      return 'connected';
    case 'failed':
      return 'failed';
    case 'auth_required':
      return 'needs sign-in';
    default:
      return '';
  }
}

/**
 * Whether a manager has anything to offer for this row.
 *
 * Three states have an action and two do not: a disabled entry can be enabled, a
 * failed one can be reconnected, and one that wants a sign-in can be signed in
 * to. A connected server is working and a connecting one is on its way; offering
 * a button for either would only be a way to break it. A listing from a host
 * with no runtime has no state, and so no action.
 */
export function isActionable(server: McpServerInfo): boolean {
  if (server.enabled === false || server.state === 'disabled') return true;
  return server.state === 'failed' || server.state === 'auth_required';
}

/**
 * One row of the chat card, with its state already spelled.
 *
 * The projection exists so the vocabulary lives in one place: the webview is
 * plain JavaScript and would otherwise repeat the words, and a state added on
 * the host would then need editing in two languages. Fields a server does not
 * have are left off rather than set to `undefined`, because the card's `typeof`
 * checks are the whole contract.
 */
export interface McpCardServer {
  name: string;
  transport?: string;
  source?: string;
  state?: McpState;
  /** Empty when the host reported no state — the configuration-only card. */
  state_label: string;
  tool_count?: number;
  error?: string;
}

export function cardServer(server: McpServerInfo): McpCardServer {
  const row: McpCardServer = { name: server.name, state_label: stateLabel(server.state) };
  if (typeof server.transport === 'string' && server.transport !== '') row.transport = server.transport;
  if (typeof server.source === 'string' && server.source !== '') row.source = server.source;
  if (server.state !== undefined) row.state = server.state;
  if (typeof server.tool_count === 'number') row.tool_count = server.tool_count;
  if (typeof server.error === 'string' && server.error !== '') row.error = server.error;
  return row;
}

// --- the manager document ----------------------------------------------------

/**
 * One row of the manager: the card's fields, the layer the entry belongs to, and
 * the configuration the form prefills from.
 *
 * It is a projection and not a passthrough for the same reason the card's is:
 * the webview is plain JavaScript, and a field it reads as `undefined` would
 * draw the word "undefined" in a cell. Every string here is present, possibly
 * empty, and every list is present, possibly empty.
 *
 * Env and header **values** never cross this boundary — the host sends key names
 * only, and the form's blank field means "leave the file's values alone".
 */
export interface McpManagerServer {
  name: string;
  /** The layer that declared it, or '' when nothing in this listing did. */
  scope: McpScope | '';
  enabled: boolean;
  transport: string;
  source: string;
  state?: McpState;
  state_label: string;
  tool_count?: number;
  tools: McpToolInfo[];
  error: string;
  connected_at: string;
  command: string;
  args: string[];
  url: string;
  env_names: string[];
  header_names: string[];
}

/**
 * Which layer's file a reported source path is.
 *
 * Both sides come from the same resolver — the host builds the layer's path with
 * `MCPConfigPath` and the entry's `source` from the file it read — so equality is
 * the normal case. The case-insensitive comparison is for Windows, where a path
 * that travelled through two APIs can differ in case and still name one file.
 */
export function scopeOfSource(source: string, sources: McpSourceInfo[]): McpScope | '' {
  if (source === '') return '';
  for (const layer of sources) {
    if (layer.path === source) return layerScope(layer.source);
    if (layer.path.toLowerCase() === source.toLowerCase()) return layerScope(layer.source);
  }
  return '';
}

function layerScope(source: string): McpScope | '' {
  return source === 'project' || source === 'user' ? source : '';
}

/**
 * A manager row from the wire's server and the listing's layers.
 *
 * A server whose `source` is absent — a status from a host whose file would not
 * parse — has no scope, which disables the per-entry actions in the document: a
 * write has to name a file.
 */
export function managerServer(server: McpServerInfo, sources: McpSourceInfo[]): McpManagerServer {
  const row: McpManagerServer = {
    name: server.name,
    scope: scopeOfSource(server.source ?? '', sources),
    enabled: server.enabled !== false,
    transport: server.transport ?? '',
    source: server.source ?? '',
    state_label: stateLabel(server.state),
    tools: Array.isArray(server.tools) ? server.tools : [],
    error: server.error ?? '',
    connected_at: server.connected_at ?? '',
    command: server.command ?? '',
    args: Array.isArray(server.args) ? server.args : [],
    url: server.url ?? '',
    env_names: Array.isArray(server.env_names) ? server.env_names : [],
    header_names: Array.isArray(server.header_names) ? server.header_names : [],
  };
  if (server.state !== undefined) row.state = server.state;
  if (typeof server.tool_count === 'number') row.tool_count = server.tool_count;
  return row;
}

/** What the add/edit form holds before it becomes a draft. */
export interface McpFormValues {
  name: string;
  /** `''` infers the transport from what is filled; else stdio, http or sse. */
  transport: string;
  command: string;
  /** One argument per line: an argument may contain a space, a tab may not. */
  args: string;
  /** `KEY=value` per line. Blank means "keep the file's values". */
  env: string;
  url: string;
  /** `KEY=value` per line. Blank means "keep the file's values". */
  headers: string;
  enabled: boolean;
}

const TRANSPORTS = ['', 'stdio', 'http', 'sse'];

/**
 * The form a document sent, or undefined when it is not one.
 *
 * Every field is read one at a time, so a message cannot smuggle a field the
 * host does not expect; the strings are not trimmed here, because a draft's
 * error message should quote what the person typed.
 */
export function formValues(raw: unknown): McpFormValues | undefined {
  if (!raw || typeof raw !== 'object') return undefined;
  const form = raw as Record<string, unknown>;
  for (const field of ['name', 'transport', 'command', 'args', 'env', 'url', 'headers'] as const) {
    if (typeof form[field] !== 'string') return undefined;
  }
  if (typeof form.enabled !== 'boolean') return undefined;
  const transport = (form.transport as string).trim().toLowerCase();
  if (!TRANSPORTS.includes(transport)) return undefined;
  return {
    name: form.name as string,
    transport,
    command: form.command as string,
    args: form.args as string,
    env: form.env as string,
    url: form.url as string,
    headers: form.headers as string,
    enabled: form.enabled as boolean,
  };
}

export type McpDraftResult = { draft: McpServerDraft } | { error: string };

/**
 * The draft a form means, checked as far as a form can check it.
 *
 * The rules are the host's ([source/mcpconfig.go]'s `validateMCPDraft`), copied
 * in the one place where a person is still typing: a stdio entry has a command
 * and no URL, a remote one the reverse, and an env or header line is `KEY=value`.
 * The host checks them all again — this exists so the answer arrives without a
 * round trip, not so the host can skip its own check.
 *
 * The irrelevant half is omitted rather than sent empty. A nil map is the SDK's
 * "keep", and an empty one would erase values the form never showed.
 */
export function draftFromForm(values: McpFormValues): McpDraftResult {
  const name = values.name.trim();
  if (name === '') return { error: 'A server needs a name.' };
  const command = values.command.trim();
  const url = values.url.trim();

  let transport = values.transport;
  if (transport === '') {
    if (command !== '' && url !== '') {
      return { error: 'A server has a command or a URL, not both: choose a transport.' };
    }
    if (command === '' && url === '') return { error: 'A server needs a command or a URL.' };
    transport = command !== '' ? 'stdio' : 'http';
  }
  if (transport === 'stdio' && command === '') {
    return { error: 'A stdio server needs a command.' };
  }
  if (transport !== 'stdio' && url === '') {
    return { error: `A ${transport} server needs a URL.` };
  }
  if (transport !== 'stdio' && !/^https?:\/\/[^\s]+$/i.test(url)) {
    return { error: `A ${transport} server needs an absolute http(s) URL, got “${url}”.` };
  }

  const draft: McpServerDraft = { name, transport, enabled: values.enabled };
  if (transport === 'stdio') {
    draft.command = command;
    const args = lineList(values.args);
    if (args.length > 0) draft.args = args;
    const env = pairLines(values.env, 'environment variable');
    if ('error' in env) return env;
    if (env.pairs) draft.env = env.pairs;
  } else {
    draft.url = url;
    const headers = pairLines(values.headers, 'header');
    if ('error' in headers) return headers;
    if (headers.pairs) draft.headers = headers.pairs;
  }
  return { draft };
}

/** One argument per line, empty lines dropped. */
export function lineList(text: string): string[] {
  return text
    .split('\n')
    .map((line) => line.replace(/\r$/, ''))
    .filter((line) => line.trim() !== '');
}

/**
 * `KEY=value` lines as a map, or the line that is not one.
 *
 * A blank textarea answers `pairs: undefined` — the caller omits the field, which
 * is how the host is told to keep what the file has. The key is trimmed and the
 * value is not: a trailing space is a mistake in a key and may be half of a
 * token. Only the first `=` splits, because a base64 value may contain one.
 */
export function pairLines(
  text: string,
  kind: string,
): { pairs?: Record<string, string> } | { error: string } {
  const lines = lineList(text);
  if (lines.length === 0) return {};
  const pairs: Record<string, string> = {};
  for (const line of lines) {
    const at = line.indexOf('=');
    if (at < 0) {
      return { error: `Every line is KEY=value: “${line}” has no “=”.` };
    }
    const key = line.slice(0, at).trim();
    if (key === '') return { error: `A ${kind} name is empty in “${line}”.` };
    pairs[key] = line.slice(at + 1);
  }
  return { pairs };
}

