/**
 * The API key has to survive a VS Code restart. SecretStorage is preferred,
 * and a local copy covers the launch where that store answers empty.
 */

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { ApiKeyStore, applyApiKeyEnv, forgetApiKey, loadApiKey, saveApiKey } from '../../src/apiKeyStore';

class MemoryStore implements ApiKeyStore {
  secret: string | undefined;
  saved: string | undefined;
  secretError: 'get' | 'set' | 'delete' | undefined;

  getSecret(): Promise<string | undefined> {
    if (this.secretError === 'get') return Promise.reject(new Error('secret get failed'));
    return Promise.resolve(this.secret);
  }

  setSecret(value: string): Promise<void> {
    if (this.secretError === 'set') return Promise.reject(new Error('secret set failed'));
    this.secret = value;
    return Promise.resolve();
  }

  deleteSecret(): Promise<void> {
    if (this.secretError === 'delete') return Promise.reject(new Error('secret delete failed'));
    this.secret = undefined;
    return Promise.resolve();
  }

  getSaved(): string | undefined {
    return this.saved;
  }

  setSaved(value: string | undefined): Promise<void> {
    this.saved = value;
    return Promise.resolve();
  }
}

test('a stored secret is the key, and it refreshes a stale local copy', async () => {
  const store = new MemoryStore();
  store.secret = 'live-key';
  store.saved = 'old-key';
  assert.equal(await loadApiKey(store), 'live-key');
  assert.equal(store.saved, 'live-key');
});

test('an empty secret store falls back to the local copy and writes it back', async () => {
  const store = new MemoryStore();
  store.saved = 'kept-key';
  assert.equal(await loadApiKey(store), 'kept-key');
  assert.equal(store.secret, 'kept-key');
});

test('a secret store that throws on read still uses the local copy', async () => {
  const store = new MemoryStore();
  store.secretError = 'get';
  store.saved = 'kept-key';
  assert.equal(await loadApiKey(store), 'kept-key');
});

test('nothing stored means no key', async () => {
  const store = new MemoryStore();
  store.secret = '   ';
  store.saved = '';
  assert.equal(await loadApiKey(store), undefined);
});

test('saving writes the local copy even when the secret store rejects it', async () => {
  const store = new MemoryStore();
  store.secretError = 'set';
  await saveApiKey(store, '  sk-once  ');
  assert.equal(store.saved, 'sk-once');
  assert.equal(store.secret, undefined);
  assert.equal(await loadApiKey(store), 'sk-once');
});

test('no saved key leaves an inherited ANVIL_API_KEY in place', () => {
  const env = applyApiKeyEnv({ ANVIL_API_KEY: 'from-env', OTHER: '1' }, undefined);
  assert.equal(env.ANVIL_API_KEY, 'from-env');
  assert.equal(env.OTHER, '1');
});

test('a saved key replaces ANVIL_API_KEY', () => {
  const env = applyApiKeyEnv({ ANVIL_API_KEY: 'from-env' }, '  saved-key  ');
  assert.equal(env.ANVIL_API_KEY, 'saved-key');
});

test('clearing removes both copies', async () => {
  const store = new MemoryStore();
  await saveApiKey(store, 'sk-once');
  await forgetApiKey(store);
  assert.equal(store.secret, undefined);
  assert.equal(store.saved, undefined);
  assert.equal(await loadApiKey(store), undefined);
});

test('a failed secret delete still drops the local copy', async () => {
  const store = new MemoryStore();
  await saveApiKey(store, 'sk-once');
  store.secretError = 'delete';
  await forgetApiKey(store);
  assert.equal(store.saved, undefined);
  // The secret itself is still there, so the next read uses it and restores the copy.
  assert.equal(await loadApiKey(store), 'sk-once');
  assert.equal(store.saved, 'sk-once');
});
