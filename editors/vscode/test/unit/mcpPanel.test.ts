/**
 * The MCP manager's wiring, and the form-to-draft rules it owns, checked as
 * text and as functions.
 *
 * There is no webview and no host child here. What can be checked is where each
 * half is wired — one document, one write per verb, a status fan-out rather than
 * a session — and the agreement that fails silently: a form that sends an empty
 * env map where "keep" was meant erases a server's token, and a document that
 * concatenates a server's description into markup trusts something it did not
 * write.
 */

import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import {
  draftFromForm,
  formValues,
  lineList,
  managerServer,
  pairLines,
  scopeOfSource,
} from '../../src/mcp';
import { McpSourceInfo } from '../../src/protocol';

const EXTENSION_ROOT = join(__dirname, '..', '..', '..');
const PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'mcpPanel.ts'), 'utf8');
const DOC = readFileSync(join(EXTENSION_ROOT, 'media', 'mcp.js'), 'utf8');
const HUB = readFileSync(join(EXTENSION_ROOT, 'src', 'hub.ts'), 'utf8');
const EXTENSION = readFileSync(join(EXTENSION_ROOT, 'src', 'extension.ts'), 'utf8');
const CHAT_PANEL = readFileSync(join(EXTENSION_ROOT, 'src', 'chatPanel.ts'), 'utf8');
const PACKAGE = JSON.parse(readFileSync(join(EXTENSION_ROOT, 'package.json'), 'utf8')) as {
  contributes: { commands: Array<{ command: string; title: string }> };
};

const SOURCES: McpSourceInfo[] = [
  { source: 'project', path: 'E:\\work\\.cursor\\mcp.json', exists: true },
  { source: 'user', path: 'C:\\Users\\me\\.cursor\\mcp.json', exists: false },
];

const FORM = {
  name: 'fs',
  transport: 'stdio',
  command: 'npx',
  args: '-y\n@modelcontextprotocol/server-filesystem\n',
  env: '',
  url: '',
  headers: '',
  enabled: true,
};

test('the document renders host data, never markup', () => {
  assert.doesNotMatch(DOC, /\.innerHTML/);
  assert.doesNotMatch(DOC, /innerHTML\s*[=+]/);
  assert.match(DOC, /textContent/);
  assert.match(DOC, /acquireVsCodeApi/);
  // A tool description is the one string in this document written by a server,
  // so it is worth naming: it travels as a text node like the rest.
  assert.match(DOC, /tool-desc/);
});

test('the document runs under the usual CSP and its own nonce', () => {
  assert.match(PANEL, /default-src 'none'/);
  assert.match(PANEL, /script-src 'nonce-\$\{nonce\}'/);
  assert.match(PANEL, /style-src \$\{webview\.cspSource\}/);
  // The document gets nothing but the media folder, as the other managers do.
  assert.match(PANEL, /localResourceRoots: \[vscode\.Uri\.joinPath\(this\.extensionUri, 'media'\)\]/);
});

test('a file that will not parse, or a host with no runtime, is read-only', () => {
  // The document decides from the listing: a problem means the rows it is
  // showing may not be what the file says, and a host without runtime cannot
  // observe a state the buttons would act on.
  assert.match(DOC, /return state\.runtime && state\.problem === '' && state\.listingError === '';/);
  assert.match(DOC, /const can = editable\(\) && server\.scope !== '';/);
  assert.match(DOC, /if \(!formEl \|\| !editable\(\) \|\| state\.saving !== ''\) return;/);
  // A host that could not be asked at all still sends the envelope, with no
  // runtime and the reason — which is what turns the form off.
  assert.match(PANEL, /runtime: false,\s*servers: \[\],\s*sources: \[\],\s*notes: \[\],\s*settings: this\.host\.loadMcpSettings\(\),\s*error: \(err as Error\)\.message,/);
});

test('a sign-in opens the host URL and nothing else', () => {
  assert.match(PANEL, /vscode\.env\.openExternal\(uri\)/);
  // Only a web address is handed to the operating system, and the URL is never
  // written anywhere: it carries the flow's state.
  assert.match(PANEL, /uri\.scheme === 'https' \|\| uri\.scheme === 'http'/);
  assert.doesNotMatch(PANEL, /console\.log/);
  assert.doesNotMatch(PANEL, /outputChannel/);
  // The document asks the host to start the flow; it never builds a request.
  assert.match(DOC, /vscode\.postMessage\(\{ type: 'authenticate', name: server\.name \}\)/);
  assert.match(PANEL, /this\.host\.authenticateMcpServer\(\{ name, setting_sources: LAYERS \}\)/);
});

test('the manager follows transitions for its own layers, and stops when closed', () => {
  // One subscription per window, made in the constructor and disposed with the
  // manager — the same shape the other panels use.
  assert.match(PANEL, /this\.subscription = host\.onMcpStatus\(\(params\) => this\.noteStatus\(params\)\)/);
  assert.match(PANEL, /dispose\(\): void \{\s*this\.subscription\.dispose\(\);/);
  // The card's runtime is a different setting set; a status naming another one
  // would repaint these rows with servers this document never asked about.
  assert.match(PANEL, /if \(named !== '' && named !== LAYER_SET\) return;/);
  // A burst of transitions costs one listing, and it repaints rows only so a
  // half-typed form survives.
  assert.match(PANEL, /setTimeout\(\(\) => \{\s*this\.rowsTimer = undefined;\s*void this\.sendRows\(\);/, 'the re-list is debounced');
  assert.match(DOC, /case 'rows':[\s\S]*?renderLive\(msg\)/);
});

test('both entry points exist and the composer opens the manager', () => {
  const commands = PACKAGE.contributes.commands.map((entry) => entry.command);
  assert.ok(commands.includes('anvil.openMcp'), 'the manager command is contributed');
  assert.ok(commands.includes('anvil.openMcpFile'), 'the file command is contributed');
  assert.match(EXTENSION, /registerCommand\('anvil\.openMcp', \(\) => \{\s*hub\.openMcp\(\);/);
  assert.match(EXTENSION, /registerCommand\('anvil\.openMcpFile', async \(\) => \{\s*await hub\.openMcpFile\(\);/);
  assert.match(HUB, /openMcp\(\): void \{\s*this\.mcp\.open\(\);/);
  // `/mcp` and the palette command are the same document.
  assert.match(CHAT_PANEL, /case 'mcp':[\s\S]*?this\.hub\.openMcp\(\);[\s\S]*?return;/);
  // The file button goes to the layer the document chose, not always to project.
  assert.match(PANEL, /case 'openFile':\s*await this\.host\.openMcpFile\(scopeOf\(message\.scope\) \?\? 'project'\);/);
  assert.match(DOC, /vscode\.postMessage\(\{ type: 'openFile', scope \}\)/);
});

test('the manager edits both layers and says when one is not read', () => {
  // Both, always: the layer the settings exclude is the one a person opens the
  // manager to see.
  assert.match(PANEL, /const LAYERS: McpScope\[\] = \['project', 'user'\];/);
  assert.match(PANEL, /this\.host\.mcpServers\(LAYERS, true\)/);
  assert.match(DOC, /anvil\.mcp\./);
  assert.match(DOC, /is off, so sessions do not read/);
  // A write names the layer it lands in; the form's select is the only source.
  assert.match(PANEL, /addMcpServer\(\{ scope, server, setting_sources: LAYERS, include_tools: true \}\)/);
  assert.match(PANEL, /removeMcpServer\(\{ scope, name, setting_sources: LAYERS, include_tools: true \}\)/);
});

// --- the pure half ----------------------------------------------------------

test('a source path resolves to the layer that declared it', () => {
  assert.equal(scopeOfSource(SOURCES[0].path, SOURCES), 'project');
  assert.equal(scopeOfSource(SOURCES[1].path, SOURCES), 'user');
  // Windows paths can differ in case between two APIs that name one file.
  assert.equal(scopeOfSource(SOURCES[0].path.toUpperCase(), SOURCES), 'project');
  assert.equal(scopeOfSource('E:\\elsewhere\\mcp.json', SOURCES), '');
  assert.equal(scopeOfSource('', SOURCES), '');
});

test('a manager row is complete, with no field left to print as undefined', () => {
  const row = managerServer({ name: 'fs', source: SOURCES[0].path }, SOURCES);
  assert.equal(row.name, 'fs');
  assert.equal(row.scope, 'project');
  assert.equal(row.enabled, true);
  assert.equal(row.state, undefined);
  assert.equal(row.state_label, '');
  assert.deepEqual(row.tools, []);
  assert.deepEqual(row.args, []);
  assert.deepEqual(row.env_names, []);
  assert.deepEqual(row.header_names, []);
  assert.equal(row.error, '');
  assert.equal(row.connected_at, '');
  assert.equal(row.command, '');
  assert.equal(row.url, '');

  const connected = managerServer(
    {
      name: 'remote',
      source: SOURCES[1].path,
      transport: 'http',
      state: 'connected',
      tool_count: 1,
      tools: [{ name: 'search' }],
      url: 'https://example.test/mcp',
      header_names: ['Authorization'],
      connected_at: '2026-10-20T00:00:00Z',
    },
    SOURCES,
  );
  assert.equal(connected.scope, 'user');
  assert.equal(connected.state, 'connected');
  assert.equal(connected.state_label, 'connected');
  assert.equal(connected.url, 'https://example.test/mcp');
  assert.deepEqual(connected.header_names, ['Authorization']);
});

test('a status with no source has no scope, so it has no per-entry action', () => {
  const row = managerServer({ name: 'fs', state: 'failed' }, SOURCES);
  assert.equal(row.scope, '');
  assert.equal(row.state_label, 'failed');
});

test('a form is read field by field and rejects what is not one', () => {
  assert.deepEqual(formValues(FORM), FORM);
  // The transport is a word from a fixed set, matched case-insensitively.
  assert.equal(formValues({ ...FORM, transport: '  STDIO ' })?.transport, 'stdio');
  assert.equal(formValues({ ...FORM, transport: 'websocket' }), undefined);
  assert.equal(formValues({ ...FORM, enabled: 'yes' }), undefined);
  assert.equal(formValues({ ...FORM, headers: undefined }), undefined);
  assert.equal(formValues(null), undefined);
  assert.equal(formValues('form'), undefined);
});

test('a stdio draft carries a command and keeps an untyped env', () => {
  const draft = draftFromForm(FORM);
  assert.ok('draft' in draft);
  assert.deepEqual(draft.draft, {
    name: 'fs',
    transport: 'stdio',
    command: 'npx',
    args: ['-y', '@modelcontextprotocol/server-filesystem'],
    enabled: true,
  });
  // No env field: nil means keep. An empty map would erase every value.
  assert.equal('env' in draft.draft, false);
  // A remote form never leaks a URL into a stdio entry.
  assert.equal('url' in draft.draft, false);
});

test('a remote draft carries a URL and keeps an untyped header map', () => {
  const draft = draftFromForm({
    ...FORM,
    transport: 'http',
    command: '',
    args: '',
    url: 'https://example.test/mcp',
  });
  assert.ok('draft' in draft);
  assert.equal(draft.draft.transport, 'http');
  assert.equal(draft.draft.url, 'https://example.test/mcp');
  assert.equal('command' in draft.draft, false);
  assert.equal('env' in draft.draft, false);
});

test('an empty transport is inferred, and the ambiguity is refused', () => {
  const stdio = draftFromForm({ ...FORM, transport: '' });
  assert.ok('draft' in stdio);
  assert.equal(stdio.draft.transport, 'stdio');
  const http = draftFromForm({ ...FORM, transport: '', command: '', args: '', url: 'http://x.test/mcp' });
  assert.ok('draft' in http);
  assert.equal(http.draft.transport, 'http');
  const both = draftFromForm({ ...FORM, transport: '', url: 'https://x.test/mcp' });
  assert.ok('error' in both);
  assert.match(both.error, /not both/);
});

test('the form refuses what the host would refuse, before asking it', () => {
  const cases: Array<[Record<string, unknown>, RegExp]> = [
    [{ ...FORM, name: '  ' }, /needs a name/],
    [{ ...FORM, transport: '', command: '', url: '' }, /command or a URL/],
    [{ ...FORM, command: '' }, /stdio server needs a command/],
    [{ ...FORM, transport: 'sse', command: '', url: 'ftp://x.test' }, /absolute http\(s\) URL/],
    [{ ...FORM, env: 'TOKEN' }, /KEY=value/],
    [{ ...FORM, env: '=value' }, /name is empty/],
  ];
  for (const [raw, expected] of cases) {
    const values = formValues(raw);
    assert.ok(values, 'the form is shaped');
    const result = draftFromForm(values);
    assert.ok('error' in result, `${expected} is refused`);
    assert.match(result.error, expected);
  }
});

test('a typed map reaches the transport it belongs to, and only that one', () => {
  const stdio = draftFromForm({ ...FORM, env: 'TOKEN=a=b\nMODE=strict\n' });
  assert.ok('draft' in stdio);
  assert.deepEqual(stdio.draft.env, { TOKEN: 'a=b', MODE: 'strict' });
  assert.equal('headers' in stdio.draft, false);

  const remote = draftFromForm({
    ...FORM,
    transport: 'http',
    command: '',
    args: '',
    url: 'https://example.test/mcp',
    env: 'TOKEN=kept\n',
    headers: 'Authorization=Bearer abc\n',
  });
  assert.ok('draft' in remote);
  assert.deepEqual(remote.draft.headers, { Authorization: 'Bearer abc' });
  // env was typed into the stdio half; the remote draft does not send it.
  assert.equal('env' in remote.draft, false);
});

test('lines are one per line, CR or not, and pairs split on the first equals', () => {
  assert.deepEqual(lineList('a\r\nb\n\n  \nc'), ['a', 'b', 'c']);
  assert.deepEqual(pairLines('', 'header'), {});
  // The key is trimmed; the value is not — a trailing space may be half a token.
  assert.deepEqual(pairLines(' A = one \n', 'header'), { pairs: { A: ' one ' } });
  assert.deepEqual(pairLines('A=b=c', 'header'), { pairs: { A: 'b=c' } });
  const noise = pairLines('A=one\nnot a pair', 'header');
  assert.ok('error' in noise);
  assert.match(noise.error, /has no “=”/);
});
