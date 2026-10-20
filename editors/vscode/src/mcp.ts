/**
 * The MCP file layers, as the two settings say them, and what a listing adds
 * up to.
 *
 * Pure, so both halves can be checked without VS Code: the mapping from the
 * two booleans to the wire's `setting_sources`, the note the card draws for a
 * layer whose file is not there, and the words a state is shown in. The card
 * itself is chat.js's job; this module decides what it is told.
 */

import { McpListResult, McpServerInfo, McpSourceInfo, McpState } from './protocol';

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
