/**
 * Settings and secrets.
 *
 * The API key is passed to the child through its environment, matching the
 * protocol's rule that credentials never cross the pipe. It is not a setting:
 * nothing here writes it to settings.json, a log, or an error message. It is
 * kept in SecretStorage and in the extension's global state, so a restart
 * still has it when the system secret store comes back empty.
 */

import * as vscode from 'vscode';
import { ApiKeyStore, applyApiKeyEnv, forgetApiKey, loadApiKey, saveApiKey } from './apiKeyStore';
import { McpSettings, settingSourcesFor } from './mcp';
import { SessionConfig } from './session';
import { WebSearchSettings, webSearchParams } from './webSearch';

const SECTION = 'anvil';
const SECRET_KEY = 'anvil.apiKey';
const WEB_SECRET_KEY = 'anvil.webSearchKey';
const WEB_ENV_KEY = 'ANVIL_WEB_SEARCH_API_KEY';
/** Global state only. Not registered with `setKeysForSync`, so it stays on this machine. */
const SAVED_KEY = 'anvil.apiKey';

export interface ResolvedSettings {
  /**
   * The raw `anvil.hostPath` setting, or empty when it is not set. Resolving it
   * — against the bundle, the workspace, then `PATH` — is `hostBundle.ts`'s job;
   * this module only reports what was typed.
   */
  hostPath: string;
  /** Everything `session.new` needs, minus the session id. */
  session: SessionConfig;
  /**
   * The rule layers the session was configured with. Kept beside the session
   * config so `rules.list` can be asked about *this* session's layers rather
   * than a generic set: a list that does not describe the session that is
   * running is worse than no list.
   */
  rules: { load_project: boolean; load_cursor_rules: boolean };
  /**
   * The MCP file layers this session is told to load. Kept beside the session
   * config so the panel's card asks about the same layers the conversation
   * uses: a card describing a different set would be worse than no card.
   */
  mcp: McpSettings;
  /**
   * The web-tool configuration the session was created with. Kept beside the
   * session config for the same reason as `mcp`: the panel's row describes the
   * tools this conversation actually has, and the key it asks about is the one
   * the child was started with.
   */
  webSearch: WebSearchSettings;
  /**
   * The context window the meter measures the last run against, or 0 when it is
   * unknown. This is display arithmetic, not a session option: it never travels
   * to the host, and nothing here derives it from a model name — a guessed
   * window would make the percentage a guess too.
   */
  contextWindow: number;
}

/** Reads the workspace configuration into a session config. */
export function readSettings(): ResolvedSettings {
  const cfg = vscode.workspace.getConfiguration(SECTION);
  const maxSteps = cfg.get<number>('maxSteps', 0) ?? 0;
  const store = (cfg.get<string>('store', '') ?? '').trim();
  const loadProject = cfg.get<boolean>('rules', true) ?? true;
  const loadCursor = cfg.get<boolean>('cursorRules', true) ?? true;
  const session: SessionConfig = {
    provider: emptyToUndefined(cfg.get<string>('provider', '')),
    model: emptyToUndefined(cfg.get<string>('model', '')),
    base_url: emptyToUndefined(cfg.get<string>('baseUrl', '')),
  };
  if (maxSteps > 0) session.max_steps = maxSteps;
  if (store !== '') session.store = store;
  // Vision is opt-in, and the setting is the whole switch: a prompt carrying an
  // image on a session created without it is refused by the host, so a paste
  // path with this off would fail every time.
  session.vision = cfg.get<boolean>('vision', false) ?? false;
  // The workspace, not the process, decides where sessions land: an editor
  // session belongs to the folder the user opened.
  session.load_rules = loadProject;
  session.load_cursor_rules = loadCursor;
  // The permission rules are policy rather than context, so this is not the
  // rules switch with a different name: `.anvil/permissions.json` says what the
  // agent may do without asking, and a session that ignores it also offers
  // nothing to remember on an approval card — a rule written there would not be
  // in force. It is sent explicitly, both ways, because the host's default is
  // on and this is the one place a user turns it off.
  session.load_permissions = cfg.get<boolean>('permissions', true) ?? true;
  // Subagents are on by default: a session that hands work to a child is the
  // editor's normal shape. Sent explicitly both ways, because a host's default
  // is not this client's to assume, and turning it off has to reach the session
  // that was just opened rather than the next one.
  session.subagents = cfg.get<boolean>('subagents', true) ?? true;
  // MCP is per source, with deliberately different defaults. The workspace file
  // is reviewed like the rest of the workspace, and the extension refuses
  // untrusted workspaces; a home-directory file a user may not remember writing
  // is a larger act to read, so it is opt-in.
  const mcp: McpSettings = {
    project: cfg.get<boolean>('mcp.project', true) ?? true,
    user: cfg.get<boolean>('mcp.user', false) ?? false,
  };
  // Omitted when both are off, which keeps the request exactly as it was before
  // these settings existed.
  const sources = settingSourcesFor(mcp);
  if (sources.length > 0) session.setting_sources = sources;
  // Off is absent: the field is omitted entirely, so a session that does not
  // want the web tools is exactly the request it was before they existed.
  const webSearch: WebSearchSettings = {
    enabled: cfg.get<boolean>('webSearch.enabled', false) ?? false,
    provider: cfg.get<string>('webSearch.provider', 'http') ?? 'http',
    baseUrl: cfg.get<string>('webSearch.baseUrl', '') ?? '',
    maxResults: cfg.get<number>('webSearch.maxResults', 0) ?? 0,
  };
  const web = webSearchParams(webSearch);
  if (web) session.web_search = web;
  // A window of zero, or a nonsense value, means the user has not told the
  // panel one: the meter then shows counts and no fraction.
  const window = cfg.get<number>('contextWindow', 0) ?? 0;
  return {
    hostPath: (cfg.get<string>('hostPath', '') ?? '').trim(),
    session,
    rules: { load_project: loadProject, load_cursor_rules: loadCursor },
    mcp,
    webSearch,
    contextWindow: Number.isFinite(window) && window > 0 ? Math.floor(window) : 0,
  };
}

function emptyToUndefined(value: string | undefined): string | undefined {
  const trimmed = (value ?? '').trim();
  return trimmed === '' ? undefined : trimmed;
}

/** The credentials the host may need, read from SecretStorage by the caller. */
export interface ChildKeys {
  apiKey?: string;
  webSearchKey?: string;
}

/**
 * Builds the child environment. The two credentials are the only things added
 * beyond the user's own environment; provider / model / base URL travel in
 * `session.new`, where the host also accepts them per session.
 *
 * Both keys are read by the caller and handed straight to the spawn, so neither
 * lands in a settings file, a log line, or an error object. The API key keeps
 * the rule that a saved key overrides `ANVIL_API_KEY` while an inherited one is
 * left alone, so a launch that already has the variable needs no command; the
 * web-search key has no such precedence and is *deleted* when absent, because
 * a value the window did not decide on would make "clear the key" untrue until
 * the next process.
 */
export function childEnv(keys: ChildKeys): NodeJS.ProcessEnv {
  const env = applyApiKeyEnv(process.env, keys.apiKey);
  setSecret(env, WEB_ENV_KEY, keys.webSearchKey);
  return env;
}

function keyStore(context: vscode.ExtensionContext): ApiKeyStore {
  return {
    getSecret: () => Promise.resolve(context.secrets.get(SECRET_KEY)),
    setSecret: (value) => Promise.resolve(context.secrets.store(SECRET_KEY, value)),
    deleteSecret: () => Promise.resolve(context.secrets.delete(SECRET_KEY)),
    getSaved: () => context.globalState.get<string>(SAVED_KEY),
    setSaved: (value) => Promise.resolve(context.globalState.update(SAVED_KEY, value)),
  };
}

function setSecret(env: NodeJS.ProcessEnv, name: string, value: string | undefined): void {
  if (value && value.trim() !== '') {
    env[name] = value;
  } else {
    delete env[name];
  }
}

/** Reads the stored key, or undefined when the user has not set one. */
export async function readApiKey(context: vscode.ExtensionContext): Promise<string | undefined> {
  return loadApiKey(keyStore(context));
}

/** Prompts for a key and stores it. Returns false when the user cancels. */
export async function promptForApiKey(context: vscode.ExtensionContext): Promise<boolean> {
  const value = await vscode.window.showInputBox({
    title: 'Anvil API key',
    prompt: 'Remembered on this machine and passed to the host process environment. You only need to set it once.',
    password: true,
    ignoreFocusOut: true,
    validateInput: (input) => (input.trim() === '' ? 'The key cannot be empty.' : undefined),
  });
  if (value === undefined) return false;
  await saveApiKey(keyStore(context), value);
  return true;
}

/** Removes the stored key. */
export async function clearApiKey(context: vscode.ExtensionContext): Promise<void> {
  await forgetApiKey(keyStore(context));
}

/** Reads the stored web-search key, or undefined when none is set. */
export async function readWebSearchKey(
  context: vscode.ExtensionContext,
): Promise<string | undefined> {
  return context.secrets.get(WEB_SECRET_KEY);
}

/** Prompts for a web-search key and stores it. Returns false when cancelled. */
export async function promptForWebSearchKey(context: vscode.ExtensionContext): Promise<boolean> {
  const value = await vscode.window.showInputBox({
    title: 'Anvil web search API key',
    prompt:
      'Stored in VS Code SecretStorage and passed to the host as ANVIL_WEB_SEARCH_API_KEY. Not written to settings.',
    password: true,
    ignoreFocusOut: true,
    validateInput: (input) => (input.trim() === '' ? 'The key cannot be empty.' : undefined),
  });
  if (value === undefined) return false;
  await context.secrets.store(WEB_SECRET_KEY, value.trim());
  return true;
}

/** Removes the stored web-search key. */
export async function clearWebSearchKey(context: vscode.ExtensionContext): Promise<void> {
  await context.secrets.delete(WEB_SECRET_KEY);
}

