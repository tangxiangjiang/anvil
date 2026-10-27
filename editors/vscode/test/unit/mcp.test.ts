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
import { configurationProperties } from '../helpers/package';
import {
  cardServer,
  hasRuntime,
  isActionable,
  mcpEnabled,
  missingMcpSources,
  settingSourcesFor,
  stateLabel,
} from '../../src/mcp';

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
  assert.match(CHAT_JS, /case 'mcp':/);
});

/**
 * The pre-0.44 card asserted that this function never mentioned a state. Chapter
 * 43 changes that deliberately: the host now observes a connection, so the card
 * draws what it reports — and only when it says it can. A host without `runtime`
 * still gets the configuration listing, because a dot for a file entry would
 * mean "the file mentioned it", which is not the thing a person cares about.
 */
test('the card draws a state only from a host that reports one', () => {
  const body = bodyOf(CHAT_JS, 'showMcp');
  assert.match(body, /msg\.runtime === true/);
  const row = bodyOf(CHAT_JS, 'mcpRow');
  assert.match(row, /server\.state/);
  assert.match(row, /runtime && state !== ''/);
  assert.match(bodyOf(CHAT_JS, 'mcpStateText'), /server\.state_label/);
  assert.match(bodyOf(CHAT_JS, 'mcpStateText'), /server\.tool_count/);
  // The reason a server failed is shown, but it is host text and goes through
  // textContent like everything else in this card.
  assert.match(bodyOf(CHAT_JS, 'mcpErrorText'), /server\.error/);
  // A file that will not parse is a result field now, not only an error.
  assert.match(body, /msg\.problem/);
});

test('a state is spelled once, for the card and the manager', () => {
  assert.equal(stateLabel('connected'), 'connected');
  assert.equal(stateLabel('connecting'), 'connecting');
  assert.equal(stateLabel('failed'), 'failed');
  assert.equal(stateLabel('disabled'), 'disabled');
  // The one word that is not the enum's: what a person does about it.
  assert.equal(stateLabel('auth_required'), 'needs sign-in');
  // A host that reports nothing gets nothing invented for it.
  assert.equal(stateLabel(undefined), '');
});

test('a listing is believed only when the host says it can observe state', () => {
  assert.equal(hasRuntime({ servers: [], sources: [], runtime: true }), true);
  assert.equal(hasRuntime({ servers: [], sources: [] }), false);
});

test('only the states with something to do are actionable', () => {
  assert.equal(isActionable({ name: 'a', state: 'failed' }), true);
  assert.equal(isActionable({ name: 'a', state: 'auth_required' }), true);
  assert.equal(isActionable({ name: 'a', state: 'disabled' }), true);
  assert.equal(isActionable({ name: 'a', state: 'connected' }), false);
  assert.equal(isActionable({ name: 'a', state: 'connecting' }), false);
  // A 0.40.6 host has no state to act on.
  assert.equal(isActionable({ name: 'a' }), false);
});

test('a card row carries the state word and nothing it was not told', () => {
  const row = cardServer({
    name: 'docs',
    transport: 'http',
    source: 'E:\\ws\\.cursor\\mcp.json',
    state: 'connected',
    tool_count: 2,
  });
  assert.deepEqual(row, {
    name: 'docs',
    transport: 'http',
    source: 'E:\\ws\\.cursor\\mcp.json',
    state: 'connected',
    state_label: 'connected',
    tool_count: 2,
  });
  // An absent state stays absent: the configuration-only card.
  const plain = cardServer({ name: 'docs', transport: 'stdio', source: 'x' });
  assert.equal(plain.state, undefined);
  assert.equal(plain.state_label, '');
  assert.equal('tool_count' in plain, false);
});

test('the card names the server, its file and its transport', () => {
  const row = bodyOf(CHAT_JS, 'mcpRow');
  assert.match(row, /server\.name/);
  assert.match(row, /server\.source/);
  assert.match(row, /server\.transport/);
  const card = bodyOf(CHAT_JS, 'showMcp');
  assert.match(card, /msg\.notes/);
  assert.match(card, /msg\.problem/);
  assert.match(card, /Could not read the MCP configuration/);
  assert.match(card, /No MCP servers are configured\./);
});

/**
 * Acceptance for chapter 43 §7: a transition reaches the card that is already on
 * screen. The row is rebuilt from the same builder the listing used, so the two
 * cannot draw a server differently, and the update is text like everything else.
 */
test('a state change redraws the row it belongs to, in place', () => {
  const update = bodyOf(CHAT_JS, 'updateMcp');
  assert.match(update, /mcpCard/);
  assert.match(update, /dataset\.name === name/);
  assert.match(update, /replaceChild\(fresh, row\)/);
  // A server added after the card was drawn gets a row rather than being lost.
  assert.match(update, /appendChild\(fresh\)/);
  assert.doesNotMatch(update, /\.innerHTML\s*(\+)?=(?!=)/);
  assert.match(CHAT_JS, /case 'mcpStatus':\s*\n\s*updateMcp\(msg\.server\)/);
  // A legacy card is drawn without a dot or a word, so a status must not add one.
  assert.match(update, /dataset\.runtime !== '1'/);
});

test('the panel asks about the layers it enabled, once per document', () => {
  const report = memberOf(CHAT_PANEL, 'private async reportMcp(');
  assert.match(report, /const \{ mcp \} = readSettings\(\)/);
  assert.match(report, /mcpEnabled\(mcp\)/);
  assert.match(report, /const sources = settingSourcesFor\(mcp\)/);
  assert.match(report, /this\.hub\.mcpServers\(sources\)/);
  assert.match(report, /missingMcpSources\(listing\.sources/);
  assert.match(report, /runtime: hasRuntime\(listing\)/);
  assert.match(report, /\(listing\.servers \?\? \[\]\)\.map\(cardServer\)/);
  // The card keeps itself current from the host's transitions, and the
  // subscription is dropped with the document it belonged to.
  assert.match(report, /this\.listenForMcpStatus\(sources\)/);
  assert.match(CHAT_PANEL, /this\.hub\.onMcpStatus\(/);
  assert.match(CHAT_PANEL, /type: 'mcpStatus', server: cardServer\(server\)/);
  assert.match(CHAT_PANEL, /this\.mcpStatusSub\?\.dispose\(\)/);
  // The card describes a workspace, so it follows the restored conversation
  // rather than racing it.
  assert.match(CHAT_PANEL, /\.then\(\(\) => this\.reportMcp\(\)\)/);
});

test('the manager sends the sources it was given', () => {
  const call = memberOf(HOST_MANAGER, 'async mcpServers(');
  assert.match(call, /Method\.McpList/);
  assert.match(call, /setting_sources: settingSources/);
  // The manager fans a status out to window-level listeners rather than letting
  // the router drop it as a notification with no session.
  assert.match(HOST_MANAGER, /method === Notify\.McpStatus/);
  assert.match(HOST_MANAGER, /onMcpStatus\(listener/);
});

test('the settings default the project file on and the user file off', () => {
  const properties = configurationProperties(PACKAGE);
  const project = properties['anvil.mcp.project'];
  const user = properties['anvil.mcp.user'];
  assert.equal(project.type, 'boolean');
  assert.equal(project.default, true);
  assert.equal(user.type, 'boolean');
  assert.equal(user.default, false);

  assert.match(SETTINGS, /get<boolean>\('mcp\.project', true\)/);
  assert.match(SETTINGS, /get<boolean>\('mcp\.user', false\)/);
  assert.match(SETTINGS, /session\.setting_sources = sources/);
});

test('the protocol names the methods and the listing shape', () => {
  assert.match(PROTOCOL, /McpList: 'mcp\.list'/);
  // 0.44: the management surface and the push a transition arrives on.
  for (const method of ['add', 'update', 'remove', 'set_enabled', 'reconnect', 'authenticate', 'sign_out']) {
    assert.match(PROTOCOL, new RegExp(`Mcp\\w+: 'mcp\\.${method}'`), `mcp.${method} is declared`);
  }
  assert.match(PROTOCOL, /McpStatus: 'mcp\.status'/);
  assert.match(PROTOCOL, /interface McpServerInfo/);
  assert.match(PROTOCOL, /interface McpSourceInfo/);
  assert.match(PROTOCOL, /interface McpListResult/);
  // The runtime flag is what a client degrades on.
  assert.match(PROTOCOL, /runtime\?: boolean/);
});
