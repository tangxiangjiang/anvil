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
import { argv, lspParams } from './languageTools';
import { McpSettings, settingSourcesFor } from './mcp';
import {
  DEFAULT_MODEL_CATALOG_URL,
  ModelConnectionMode,
  ModelSettings,
  connectionMode,
  connectionProblem,
  normalizeModelIds,
  validModelId,
} from './models';
import { Effort } from './protocol';
import { SessionConfig } from './session';
import { WebSearchSettings, webSearchParams } from './webSearch';
import { rootUri } from './workspaceRoot';

const SECTION = 'anvil';
const SECRET_KEY = 'anvil.apiKey';
const WEB_SECRET_KEY = 'anvil.webSearchKey';
const WEB_ENV_KEY = 'ANVIL_WEB_SEARCH_API_KEY';
/** Global state only. Not registered with `setKeysForSync`, so it stays on this machine. */
const SAVED_KEY = 'anvil.apiKey';
const DEFAULT_MODEL_KEY = 'anvil.defaultModel';
const MODEL_SETUP_KEY = 'anvil.modelSetup';
const AVAILABLE_MODELS_KEY = 'anvil.availableModels';
const MODEL_CATALOG_KEY = 'anvil.modelCatalogUrl';
let selectedDefaultModel = '';
let modelSetup: ModelSetup = {
  mode: 'gateway',
  provider: 'openai',
  baseUrl: '',
  singleModelId: '',
  effort: 'medium',
};
let availableModels: string[] = [];
let modelCatalogUrl = DEFAULT_MODEL_CATALOG_URL;

export interface ModelSetup {
  mode: ModelConnectionMode;
  provider: 'openai' | 'anthropic';
  baseUrl: string;
  singleModelId: string;
  effort: Effort;
}

/**
 * Loads the workspace's curated default and migrates the former free-text
 * setting. The value is installed synchronously; removing the old keys can
 * finish after activation without changing what the first session sees.
 */
export function initDefaultModel(context: vscode.ExtensionContext): void {
  const cfg = vscode.workspace.getConfiguration(SECTION, rootUri());
  const legacy = (cfg.get<string>('model', '') ?? '').trim();
  const savedConnection = cfg.get<unknown>('modelConnection');
  const connection =
    savedConnection && typeof savedConnection === 'object'
      ? savedConnection as Record<string, unknown>
      : undefined;
  const legacyProvider = connection?.provider ?? cfg.get<string>('provider', 'openai');
  const migratedSetup: ModelSetup = {
    mode: connectionMode(connection?.mode ?? cfg.get<unknown>('modelMode', 'gateway')),
    provider: legacyProvider === 'anthropic' ? 'anthropic' : 'openai',
    baseUrl: (
      typeof connection?.baseUrl === 'string'
        ? connection.baseUrl
        : cfg.get<string>('baseUrl', '') ?? ''
    ).trim(),
    singleModelId: (
      typeof connection?.singleModelId === 'string'
        ? connection.singleModelId
        : cfg.get<string>('singleModelId', '') ?? ''
    ).trim(),
    effort: readEffort(cfg.get<string>('effort', 'medium')),
  };
  modelSetup = normalizeModelSetup(context.workspaceState.get<ModelSetup>(MODEL_SETUP_KEY) ?? migratedSetup);
  availableModels = normalizeModelIds(
    context.workspaceState.get<unknown>(AVAILABLE_MODELS_KEY)
      ?? cfg.get<unknown>('availableModels', []),
  );
  modelCatalogUrl = (
    context.workspaceState.get<string>(MODEL_CATALOG_KEY)
      ?? cfg.get<string>('modelCatalogUrl', DEFAULT_MODEL_CATALOG_URL)
      ?? DEFAULT_MODEL_CATALOG_URL
  ).trim();
  void context.workspaceState.update(MODEL_SETUP_KEY, modelSetup);
  void context.workspaceState.update(AVAILABLE_MODELS_KEY, availableModels);
  void context.workspaceState.update(MODEL_CATALOG_KEY, modelCatalogUrl);
  selectedDefaultModel = context.workspaceState.get<string>(DEFAULT_MODEL_KEY, legacy);
  if (selectedDefaultModel !== '') {
    void context.workspaceState.update(DEFAULT_MODEL_KEY, selectedDefaultModel);
  }
  const inspected = cfg.inspect<string>('model');
  if (!inspected) return;
  const clears: Thenable<void>[] = [];
  if (inspected.workspaceFolderValue !== undefined) {
    clears.push(cfg.update('model', undefined, vscode.ConfigurationTarget.WorkspaceFolder));
  }
  if (inspected.workspaceValue !== undefined) {
    clears.push(cfg.update('model', undefined, vscode.ConfigurationTarget.Workspace));
  }
  if (inspected.globalValue !== undefined) {
    clears.push(cfg.update('model', undefined, vscode.ConfigurationTarget.Global));
  }
  void Promise.all(clears);
}

export async function saveModelSetup(context: vscode.ExtensionContext, value: ModelSetup): Promise<void> {
  modelSetup = normalizeModelSetup(value);
  await context.workspaceState.update(MODEL_SETUP_KEY, modelSetup);
}

export async function saveAvailableModels(context: vscode.ExtensionContext, value: string[]): Promise<void> {
  availableModels = normalizeModelIds(value);
  await context.workspaceState.update(AVAILABLE_MODELS_KEY, availableModels);
}

export async function saveModelCatalogUrl(context: vscode.ExtensionContext, value: string): Promise<void> {
  modelCatalogUrl = value.trim();
  await context.workspaceState.update(MODEL_CATALOG_KEY, modelCatalogUrl);
}

function normalizeModelSetup(value: ModelSetup): ModelSetup {
  return {
    mode: connectionMode(value.mode),
    provider: value.provider === 'anthropic' ? 'anthropic' : 'openai',
    baseUrl: typeof value.baseUrl === 'string' ? value.baseUrl.trim() : '',
    singleModelId: typeof value.singleModelId === 'string' ? value.singleModelId.trim() : '',
    effort: readEffort(value.effort),
  };
}

export async function saveDefaultModel(
  context: vscode.ExtensionContext,
  model: string,
): Promise<void> {
  selectedDefaultModel = model.trim();
  await context.workspaceState.update(
    DEFAULT_MODEL_KEY,
    selectedDefaultModel === '' ? undefined : selectedDefaultModel,
  );
}

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
  /** Curated model catalogue and the workspace's default selection. */
  models: ModelSettings;
  /**
   * The context window the meter measures the last run against, or 0 when it is
   * unknown. This is display arithmetic, not a session option: it never travels
   * to the host, and nothing here derives it from a model name — a guessed
   * window would make the percentage a guess too.
   */
  contextWindow: number;
  /**
   * Where the session compacts, as a fraction of the window. The host decides
   * when the conversation is full; the fraction is the client's declaration of
   * how much of the window counts as full.
   */
  autoCompactAt: number;
  /** Recent messages a compaction keeps verbatim. */
  contextKeepRecent: number;
  /**
   * The prompt cache's TTL for the meter's countdown, or 0 when none is known.
   * Resolved here because the provider is: 0 in the setting means "whatever the
   * provider does", which is 300 seconds for Anthropic and nothing to count for
   * a provider that never said.
   */
  cacheTtlSeconds: number;
  /**
   * The chat document's base text size, in CSS pixels. Display only: it never
   * travels to the host, and a change to it alone must not restart one.
   */
  fontSize: number;
}

/** Reads the workspace configuration into a session config. */
export function readSettings(): ResolvedSettings {
  const cfg = vscode.workspace.getConfiguration(SECTION);
  const maxSteps = cfg.get<number>('maxSteps', 0) ?? 0;
  const store = (cfg.get<string>('store', '') ?? '').trim();
  const loadProject = cfg.get<boolean>('rules', true) ?? true;
  const loadCursor = cfg.get<boolean>('cursorRules', true) ?? true;
  const { mode, provider, baseUrl, singleModelId, effort } = normalizeModelSetup(modelSetup);
  const effectiveModel = mode === 'single' ? singleModelId : selectedDefaultModel;
  const session: SessionConfig = {
    provider,
    model: emptyToUndefined(effectiveModel),
    base_url: emptyToUndefined(baseUrl),
    effort,
  };
  const defaultModel = session.model ?? '';
  const curated = availableModels;
  const models: ModelSettings = {
    mode,
    provider,
    baseUrl,
    singleModelId,
    catalogUrl: modelCatalogUrl,
    gatewayAvailable: curated,
    gatewayDefaultModel: selectedDefaultModel,
    available: mode === 'single' && validModelId(singleModelId) ? [singleModelId] : curated,
    defaultModel,
    problem: connectionProblem(mode, baseUrl, singleModelId),
  };
  if (maxSteps > 0) session.max_steps = maxSteps;
  if (store !== '') session.store = store;
  // Vision is on by default: pasting a screenshot into a chat is a request to
  // send it, and the setting is the switch that turns the capability off rather
  // than the one that turns it on. The session still asks explicitly, so the
  // host's refusal — and its message naming this setting — is unchanged for a
  // user who does turn it off.
  session.vision = cfg.get<boolean>('vision', true) ?? true;
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
  session.subagents = cfg.get<boolean>('subagents', false) ?? false;
  // Language servers, the test command and the build command are all "a tool
  // the user configured": when nothing is configured the fields are omitted,
  // so the request is exactly the one an older client sent. A workspace's
  // `.anvil/lsp.json` is merged by the SDK independently; this list adds to it.
  const lsp = lspParams(cfg.get<boolean>('lsp.enabled', false) ?? false, cfg.get<unknown>('lsp.servers', []));
  if (lsp) session.lsp = lsp;
  const testCommand = argv(cfg.get<unknown>('testCommand', []));
  if (testCommand) session.test_command = testCommand;
  const buildCommand = argv(cfg.get<unknown>('buildCommand', []));
  if (buildCommand) session.build_command = buildCommand;
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
  // Fresh installs get a conservative safety budget: leaving this unknown used
  // to disable compaction silently and let a long editor conversation grow
  // until the provider rejected it. Zero remains an explicit opt-out.
  const window = cfg.get<number>('contextWindow', 128000) ?? 128000;
  const contextWindow = Number.isFinite(window) && window > 0 ? Math.floor(window) : 0;
  const compactShare = cfg.get<number>('autoCompactAt', 0.8) ?? 0.8;
  const autoCompactAt = fractionOf(compactShare, 0.8);
  const keepRaw = cfg.get<number>('contextKeepRecent', 8) ?? 8;
  const contextKeepRecent = Number.isFinite(keepRaw) ? Math.max(2, Math.floor(keepRaw)) : 8;
  // The budget travels only with a window: `window_tokens: 0` is the host's
  // "nothing is full", so sending a fraction with it would declare a rule that
  // can never fire. Omitted, the request is the one an older client sent.
  if (contextWindow > 0) {
    session.context = {
      window_tokens: contextWindow,
      auto_compact_at: autoCompactAt,
      keep_recent: contextKeepRecent,
    };
  }
  const ttlRaw = cfg.get<number>('cacheTtlSeconds', 0) ?? 0;
  const configuredTtl = Number.isFinite(ttlRaw) && ttlRaw > 0 ? Math.floor(ttlRaw) : 0;
  // "The provider's default" is the host's knowledge of the endpoint, so it is
  // read the same way the host reads it: the session setting first, the
  // environment second. An unset provider is OpenAI, which is the host's
  // default too, and it makes no caching promise a client could count on.
  const cacheProvider = (session.provider ?? emptyToUndefined(process.env.ANVIL_PROVIDER) ?? '').toLowerCase();
  const cacheTtlSeconds = cacheTtlOf(configuredTtl, cacheProvider);
  // Display only, so it is clamped here rather than trusted: the panel writes
  // the number into the document, and a value outside the range the setting
  // offers would be a document nobody could size back.
  const fontSizeRaw = cfg.get<number>('chatFontSize', 15) ?? 15;
  const fontSize = Number.isFinite(fontSizeRaw)
    ? Math.min(24, Math.max(10, Math.round(fontSizeRaw)))
    : 15;
  return {
    hostPath: (cfg.get<string>('hostPath', '') ?? '').trim(),
    session,
    rules: { load_project: loadProject, load_cursor_rules: loadCursor },
    mcp,
    webSearch,
    models,
    contextWindow,
    autoCompactAt,
    contextKeepRecent,
    cacheTtlSeconds,
    fontSize,
  };
}

function readEffort(value: string | undefined): Effort {
  return value === 'low' || value === 'high' ? value : 'medium';
}

/**
 * A reading reduced to the settings that reach the host, for change detection.
 *
 * The comparison is the point: a configuration listener that restarts the host
 * on any `anvil.*` change would end a run to change a text size. Comparing the
 * host-relevant fields means a new display setting is covered by deleting it
 * here rather than by remembering to name it in the listener.
 */
export function settingsKey(settings: ResolvedSettings): string {
  const hostRelevant: Partial<ResolvedSettings> = { ...settings };
  delete hostRelevant.fontSize;
  delete hostRelevant.models;
  return JSON.stringify(hostRelevant);
}

/** A fraction in (0, 1], or the fallback when the setting is not one. */
function fractionOf(value: number, fallback: number): number {
  if (!Number.isFinite(value) || value <= 0) return fallback;
  return value > 1 ? 1 : value;
}

/**
 * The TTL the meter counts against. An explicit setting wins; otherwise the
 * only provider default this client knows is Anthropic's five minutes. Any
 * other provider gets 0, which draws the cached share with no countdown — a
 * countdown invented for a provider that never stated one would be a clock
 * telling the time in a zone nobody named.
 */
function cacheTtlOf(configured: number, provider: string): number {
  if (configured > 0) return configured;
  return provider === 'anthropic' ? 300 : 0;
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

