/**
 * Where the API key is kept between VS Code launches.
 *
 * SecretStorage is the encrypted store and the one we read first. On some
 * macOS setups it answers empty after a restart even though `store` succeeded,
 * which is why a second copy lives in the extension's global state: not a
 * setting, not synced, and enough for the next launch to spawn the host with
 * a key. The copy is what `loadApiKey` uses when the secret store misses.
 */

/** The two places a key can sit. Neither one logs the value. */
export interface ApiKeyStore {
  getSecret(): Promise<string | undefined>;
  setSecret(value: string): Promise<void>;
  deleteSecret(): Promise<void>;
  /** The local copy. `undefined` when this extension has never saved one. */
  getSaved(): string | undefined;
  setSaved(value: string | undefined): Promise<void>;
}

/** The variable the host reads. A saved key overrides it; otherwise it is left as inherited. */
export const API_KEY_ENV = 'ANVIL_API_KEY';

/**
 * The child environment. A key saved in the extension replaces `ANVIL_API_KEY`.
 * When nothing was saved, the variable already on `env` stays, so a launch that
 * inherited it does not need **Set API Key**.
 */
export function applyApiKeyEnv(env: NodeJS.ProcessEnv, apiKey: string | undefined): NodeJS.ProcessEnv {
  const next: NodeJS.ProcessEnv = { ...env };
  const key = present(apiKey);
  if (key) next[API_KEY_ENV] = key;
  return next;
}

function present(value: string | undefined): string | undefined {
  const trimmed = (value ?? '').trim();
  return trimmed === '' ? undefined : trimmed;
}

/**
 * The key to put in the child environment, or undefined when the user has not
 * set one. A secret that is present wins and refreshes the local copy. A
 * missing secret falls back to the copy and, when that hits, is written back
 * so the encrypted store can catch up.
 */
export async function loadApiKey(store: ApiKeyStore): Promise<string | undefined> {
  let secret: string | undefined;
  try {
    secret = present(await store.getSecret());
  } catch {
    secret = undefined;
  }
  const saved = present(store.getSaved());
  if (secret) {
    if (secret !== saved) {
      try {
        await store.setSaved(secret);
      } catch {
        // The secret is still the key this launch will use.
      }
    }
    return secret;
  }
  if (!saved) return undefined;
  try {
    await store.setSecret(saved);
  } catch {
    // The copy is what this launch uses when the secret store is down.
  }
  return saved;
}

/** Remembers a key in both places. The local copy is written first. */
export async function saveApiKey(store: ApiKeyStore, value: string): Promise<void> {
  const trimmed = present(value);
  if (!trimmed) throw new Error('The key cannot be empty.');
  await store.setSaved(trimmed);
  try {
    await store.setSecret(trimmed);
  } catch {
    // A secret store that rejects the write must not throw away the copy:
    // that copy is the one the next launch reads.
  }
}

/** Drops both copies. A secret store that fails to delete still loses the local one. */
export async function forgetApiKey(store: ApiKeyStore): Promise<void> {
  try {
    await store.deleteSecret();
  } catch {
    // Still drop the copy, or the next launch would put the key back.
  }
  await store.setSaved(undefined);
}
