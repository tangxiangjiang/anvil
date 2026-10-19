/**
 * The MCP file layers, as the two settings say them, and what a listing adds
 * up to.
 *
 * Pure, so both halves can be checked without VS Code: the mapping from the
 * two booleans to the wire's `setting_sources`, and the note the card draws for
 * a layer whose file is not there. The card itself is chat.js's job; this
 * module decides what it is told.
 */

export interface McpSettings {
  /** Read `.cursor/mcp.json` in the workspace. */
  project: boolean;
  /** Read `~/.cursor/mcp.json`. */
  user: boolean;
}

export interface McpSourceInfo {
  source: string;
  path: string;
  exists: boolean;
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
