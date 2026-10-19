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
import { SessionConfig } from './session';

const SECTION = 'anvil';
const SECRET_KEY = 'anvil.apiKey';
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
  return {
    hostPath: (cfg.get<string>('hostPath', '') ?? '').trim(),
    session,
    rules: { load_project: loadProject, load_cursor_rules: loadCursor },
  };
}

function emptyToUndefined(value: string | undefined): string | undefined {
  const trimmed = (value ?? '').trim();
  return trimmed === '' ? undefined : trimmed;
}

/**
 * Builds the child environment. The API key is the only thing added beyond the
 * user's own environment; provider / model / base URL travel in `session.new`,
 * where the host also accepts them per session.
 *
 * The key is read by the caller and handed straight to the spawn, so it never
 * lands in a settings file, a log line, or an error object. With no saved key,
 * `ANVIL_API_KEY` already on this process is left alone.
 */
export function childEnv(apiKey: string | undefined): NodeJS.ProcessEnv {
  return applyApiKeyEnv(process.env, apiKey);
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

