/**
 * The web-tools switch, checked where it cannot be run.
 *
 * The host's half — what `buildOptions` does with the field, and the tool list
 * a session advertises — is `source/internal/editorhost/websearch_test.go`.
 * What is left is agreement between the setting, the request, the panel and
 * the renderer: a request that carries a key, a row drawn when the tools are
 * off, or a provider that claims built-ins it did not register, fails here.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import { WebSearchSettings, webSearchNeedsKey, webSearchParams } from '../../src/webSearch';
import { configurationProperties } from '../helpers/package';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');
const HOST_MANAGER = readFileSync(join(EXTENSION_ROOT, 'src', 'hostManager.ts'), 'utf8');
const PROTOCOL = readFileSync(join(EXTENSION_ROOT, 'src', 'protocol.ts'), 'utf8');
const SETTINGS = readFileSync(join(EXTENSION_ROOT, 'src', 'settings.ts'), 'utf8');
const EXTENSION = readFileSync(join(EXTENSION_ROOT, 'src', 'extension.ts'), 'utf8');
const PACKAGE = JSON.parse(readFileSync(join(EXTENSION_ROOT, 'package.json'), 'utf8'));

/** A top-level function of the renderer, from its declaration to the next one. */
function bodyOf(source: string, name: string): string {
  const start = source.indexOf(`\n  function ${name}(`);
  assert.ok(start >= 0, `${name} is in chat.js`);
  const rest = source.slice(start + 1);
  const next = rest.indexOf('\n  function ');
  return next >= 0 ? rest.slice(0, next) : rest;
}

/** A member of a TypeScript class, from its signature to the next member. */
function memberOf(source: string, signature: string): string {
  const start = source.indexOf(signature);
  assert.ok(start >= 0, `${signature} is there`);
  const rest = source.slice(start);
  const next = rest.indexOf('\n  private ', 1);
  const publicNext = rest.indexOf('\n  async ', 1);
  const candidates = [next, publicNext].filter((at) => at > 0);
  if (candidates.length === 0) return rest;
  return rest.slice(0, Math.min(...candidates));
}

/** The default settings, with only the fields a test is about changed. */
function settings(overrides: Partial<WebSearchSettings>): WebSearchSettings {
  return { enabled: true, provider: 'http', baseUrl: 'https://s.test', maxResults: 0, ...overrides };
}

test('off is absent, not a sent false', () => {
  assert.equal(webSearchParams(settings({ enabled: false, maxResults: 5 })), undefined);
});

test('the http provider sends its endpoint and size', () => {
  assert.deepEqual(webSearchParams(settings({ provider: ' HTTP ', baseUrl: ' https://s.test/v1 ', maxResults: 3 })), {
    provider: 'http',
    base_url: 'https://s.test/v1',
    max_results: 3,
  });
  // Zero means the SDK default, so the request says nothing about a number the
  // user did not choose.
  assert.deepEqual(webSearchParams(settings({})), {
    provider: 'http',
    base_url: 'https://s.test',
  });
});

// A missing endpoint is the host's error to name, and it says which setting
// to fill in; silently dropping the field would make enabled mean nothing.
test('an http provider with no endpoint is still sent', () => {
  assert.deepEqual(webSearchParams(settings({ baseUrl: ' ' })), { provider: 'http' });
});

test('an unknown provider falls back to http rather than the wire', () => {
  assert.deepEqual(webSearchParams(settings({ provider: 'galaxy' })), {
    provider: 'http',
    base_url: 'https://s.test',
  });
});

test('the mcp provider registers the switch without an endpoint', () => {
  const params = webSearchParams({
    enabled: true,
    provider: 'mcp',
    baseUrl: 'https://unused.test',
    maxResults: 4,
  });
  assert.equal(params?.provider, 'mcp');
  assert.equal(params?.max_results, 4);
  assert.equal(params?.base_url, undefined);
  assert.equal(webSearchNeedsKey(settings({ provider: 'mcp' })), false);
  assert.equal(webSearchNeedsKey(settings({})), true);
});

test('no key field can reach the wire', () => {
  const params = webSearchParams(settings({}));
  assert.deepEqual(Object.keys(params ?? {}).sort(), ['base_url', 'provider']);
  // The credential is an environment variable name, never a value: the setting
  // module writes it there and nowhere else.
  assert.match(SETTINGS, /WEB_ENV_KEY = 'ANVIL_WEB_SEARCH_API_KEY'/);
  assert.match(SETTINGS, /setSecret\(env, WEB_ENV_KEY, keys\.webSearchKey\)/);
  assert.doesNotMatch(SETTINGS, /session\.web_search = [^;]*key/i);
});

test('the child environment carries both credentials', () => {
  const spawn = memberOf(HOST_MANAGER, 'private async spawn(');
  assert.match(spawn, /readWebSearchKey\(this\.context\)/);
  assert.match(spawn, /childEnv\(\{ apiKey, webSearchKey \}\)/);
  assert.match(HOST_MANAGER, /readApiKey, readSettings, readWebSearchKey/);
});

test('the key has its own commands and its own secret', () => {
  assert.match(SETTINGS, /WEB_SECRET_KEY = 'anvil\.webSearchKey'/);
  assert.match(EXTENSION, /promptForWebSearchKey\(context\)/);
  assert.match(EXTENSION, /clearWebSearchKey\(context\)/);
  const commands = PACKAGE.contributes.commands.map((c: { command: string }) => c.command);
  assert.ok(commands.includes('anvil.setWebSearchKey'));
  assert.ok(commands.includes('anvil.clearWebSearchKey'));
  // A key command that did not restart the host would appear to save and then
  // not take effect: the child keeps the environment it was spawned with.
  const set = EXTENSION.slice(EXTENSION.indexOf("'anvil.setWebSearchKey'"));
  assert.match(set.slice(0, set.indexOf("'anvil.clearWebSearchKey'")), /await hub\.restart\(\)/);
});

test('the settings default to off and read the four fields', () => {
  const properties = configurationProperties(PACKAGE);
  const enabled = properties['anvil.webSearch.enabled'];
  const provider = properties['anvil.webSearch.provider'];
  const maxResults = properties['anvil.webSearch.maxResults'];
  assert.equal(enabled.type, 'boolean');
  assert.equal(enabled.default, false);
  assert.deepEqual(provider.enum, ['http', 'mcp']);
  assert.equal(provider.default, 'http');
  assert.equal(maxResults.default, 0);
  assert.equal(maxResults.maximum, 10);

  assert.match(SETTINGS, /get<boolean>\('webSearch\.enabled', false\)/);
  assert.match(SETTINGS, /get<string>\('webSearch\.provider', 'http'\)/);
  assert.match(SETTINGS, /get<string>\('webSearch\.baseUrl', ''\)/);
  assert.match(SETTINGS, /get<number>\('webSearch\.maxResults', 0\)/);
  // The session field is absent when the tools are off.
  assert.match(SETTINGS, /if \(web\) session\.web_search = web/);
});

test('the panel reports the tools only when enabled', () => {
  const report = memberOf(CHAT_PANEL, 'private async reportWebSearch(');
  assert.match(report, /const \{ webSearch \} = readSettings\(\)/);
  assert.match(report, /if \(!webSearch\.enabled\) return/);
  assert.match(report, /type: 'webSearch'/);
  assert.match(report, /webSearchNeedsKey\(webSearch\)/);
  assert.match(report, /readWebSearchKey\(this\.context\)/);
  // Presence, never the value.
  assert.match(report, /key: stored !== undefined && stored\.trim\(\) !== ''/);
  assert.doesNotMatch(report, /key: stored\s*$/m);
  assert.match(CHAT_PANEL, /\.then\(\(\) => this\.reportWebSearch\(\)\)/);
});

test('the row draws text only and says which provider it means', () => {
  const body = bodyOf(CHAT_JS, 'showWebSearch');
  assert.doesNotMatch(body, /\.innerHTML\s*(\+)?=(?!=)/);
  assert.doesNotMatch(body, /insertAdjacentHTML\s*\(/);
  assert.match(body, /el\(/);
  assert.match(body, /web_search, fetch_url/);
  assert.match(body, /API key is set/);
  assert.match(body, /no API key is set/);
  // The mcp provider registers no built-ins; a row claiming them would be the
  // fake green light the MCP card exists to avoid.
  assert.match(body, /No built-in web tools/);
  assert.match(CHAT_JS, /case 'webSearch':/);
});

test('the protocol names the field and the params shape', () => {
  assert.match(PROTOCOL, /web_search\?: WebSearchParams/);
  assert.match(PROTOCOL, /interface WebSearchParams/);
  assert.match(PROTOCOL, /base_url\?: string/);
  assert.match(PROTOCOL, /max_results\?: number/);
});
