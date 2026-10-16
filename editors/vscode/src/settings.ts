/**
 * Settings and secrets.
 *
 * The API key lives in SecretStorage and is passed to the child through its
 * environment, matching the protocol's rule that credentials never cross the
 * pipe. Nothing here ever writes the key to a setting, a log, or an error
 * message — the settings written to disk are exactly the ones the user typed.
 */

import * as vscode from 'vscode';
import { SessionConfig } from './session';

const SECTION = 'anvil';
const SECRET_KEY = 'anvil.apiKey';
const ENV_KEY = 'ANVIL_API_KEY';

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
 * The key is read from SecretStorage by the caller and handed straight to the
 * spawn, so it never lands in a settings file, a log line, or an error object.
 */
export function childEnv(apiKey: string | undefined): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = { ...process.env };
  if (apiKey && apiKey.trim() !== '') {
    env[ENV_KEY] = apiKey;
  } else {
    delete env[ENV_KEY];
  }
  return env;
}

/** Reads the stored key, or undefined when the user has not set one. */
export async function readApiKey(context: vscode.ExtensionContext): Promise<string | undefined> {
  return context.secrets.get(SECRET_KEY);
}

/** Prompts for a key and stores it. Returns false when the user cancels. */
export async function promptForApiKey(context: vscode.ExtensionContext): Promise<boolean> {
  const value = await vscode.window.showInputBox({
    title: 'Anvil API key',
    prompt: 'Stored in VS Code SecretStorage and passed to the host process environment.',
    password: true,
    ignoreFocusOut: true,
    validateInput: (input) => (input.trim() === '' ? 'The key cannot be empty.' : undefined),
  });
  if (value === undefined) return false;
  await context.secrets.store(SECRET_KEY, value.trim());
  return true;
}

/** Removes the stored key. */
export async function clearApiKey(context: vscode.ExtensionContext): Promise<void> {
  await context.secrets.delete(SECRET_KEY);
}

