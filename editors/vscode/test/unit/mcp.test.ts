/**
 * The MCP card's two halves, checked where they cannot be run.
 *
 * The round trip is `test/integration/mcp.test.ts`. What is left is agreement
 * between settings, the request, the panel and the renderer: a source list that
 * is built from one setting and asked about with another, or a card that draws
 * a state the host never reported, fails here rather than as a listing about
 * files the session did not load.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import { mcpEnabled, missingMcpSources, settingSourcesFor } from '../../src/mcp';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const CHAT_JS = readFileSync(join(EXTENSION_ROOT, 'media', 'chat.js'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');
const HOST_MANAGER = readFileSync(join(EXTENSION_ROOT, 'src', 'hostManager.ts'), 'utf8');
const PROTOCOL = readFileSync(join(EXTENSION_ROOT, 'src', 'protocol.ts'), 'utf8');
const SETTINGS = readFileSync(join(EXTENSION_ROOT, 'src', 'settings.ts'), 'utf8');
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

test('the two settings become the wire sources they enable', () => {
  assert.deepEqual(settingSourcesFor({ project: true, user: false }), ['project']);
  assert.deepEqual(settingSourcesFor({ project: false, user: true }), ['user']);
  assert.deepEqual(settingSourcesFor({ project: true, user: true }), ['project', 'user']);
  // Both off is the empty request: the session keeps the inline-only behaviour
  // it had before these settings existed.
  assert.deepEqual(settingSourcesFor({ project: false, user: false }), []);
  assert.equal(mcpEnabled({ project: false, user: false }), false);
  assert.equal(mcpEnabled({ project: true, user: false }), true);
});

test('only enabled layers whose file is absent get a note', () => {
  const notes = missingMcpSources([
    { source: 'project', path: 'E:\\ws\\.cursor\\mcp.json', exists: true },
    { source: 'user', path: 'C:\\Users\\me\\.cursor\\mcp.json', exists: false },
  ]);
  assert.deepEqual(notes, ['C:\\Users\\me\\.cursor\\mcp.json was not found.']);
});

test('the card draws text only, never markup', () => {
  const body = bodyOf(CHAT_JS, 'showMcp');
  assert.doesNotMatch(body, /\.innerHTML\s*(\+)?=(?!=)/);
  assert.doesNotMatch(body, /insertAdjacentHTML\s*\(/);
  assert.match(body, /el\(/);
  // Configuration, not connection: a field the host never sent must not be
  // invented here.
  assert.doesNotMatch(body, /connected|status|running/i);
  assert.match(CHAT_JS, /case 'mcp':/);
});

test('the card names the server, its file and its transport', () => {
  const body = bodyOf(CHAT_JS, 'showMcp');
  assert.match(body, /server\.name/);
  assert.match(body, /server\.source/);
  assert.match(body, /server\.transport/);
  assert.match(body, /msg\.notes/);
  assert.match(body, /Could not read the MCP configuration/);
  assert.match(body, /No MCP servers are configured\./);
});

test('the panel asks about the layers it enabled, once per document', () => {
  const report = memberOf(CHAT_PANEL, 'private async reportMcp(');
  assert.match(report, /const \{ mcp \} = readSettings\(\)/);
  assert.match(report, /mcpEnabled\(mcp\)/);
  assert.match(report, /this\.hub\.mcpServers\(settingSourcesFor\(mcp\)\)/);
  assert.match(report, /missingMcpSources\(listing\.sources/);
  assert.match(report, /type: 'mcp',\s+servers: listing\.servers/);
  // A file that will not parse has to say so; an empty card would hide it.
  assert.match(report, /error: \(err as Error\)\.message/);
  // The card describes a workspace, so it follows the restored conversation
  // rather than racing it.
  assert.match(CHAT_PANEL, /\.then\(\(\) => this\.reportMcp\(\)\)/);
});

test('the manager sends the sources it was given', () => {
  const call = memberOf(HOST_MANAGER, 'async mcpServers(');
  assert.match(call, /Method\.McpList/);
  assert.match(call, /setting_sources: settingSources/);
});

test('the settings default the project file on and the user file off', () => {
  const project = PACKAGE.contributes.configuration.properties['anvil.mcp.project'];
  const user = PACKAGE.contributes.configuration.properties['anvil.mcp.user'];
  assert.equal(project.type, 'boolean');
  assert.equal(project.default, true);
  assert.equal(user.type, 'boolean');
  assert.equal(user.default, false);

  assert.match(SETTINGS, /get<boolean>\('mcp\.project', true\)/);
  assert.match(SETTINGS, /get<boolean>\('mcp\.user', false\)/);
  assert.match(SETTINGS, /session\.setting_sources = sources/);
});

test('the protocol names the method and the listing shape', () => {
  assert.match(PROTOCOL, /McpList: 'mcp\.list'/);
  assert.match(PROTOCOL, /interface McpServerInfo/);
  assert.match(PROTOCOL, /interface McpSourceInfo/);
  assert.match(PROTOCOL, /interface McpListResult/);
});
