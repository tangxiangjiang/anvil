import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import {
  DEFAULT_MODEL_CATALOG_URL,
  baseUrlProblem,
  catalogBaseUrl,
  connectionMode,
  connectionProblem,
  normalizeModelIds,
  validModelId,
} from '../../src/models';
import { configurationProperties } from '../helpers/package';

const ROOT = join(__dirname, '..', '..', '..');
const PACKAGE = JSON.parse(readFileSync(join(ROOT, 'package.json'), 'utf8'));
const PANEL = readFileSync(join(ROOT, 'src', 'modelsPanel.ts'), 'utf8');
const MODELS_JS = readFileSync(join(ROOT, 'media', 'models.js'), 'utf8');
const CONNECTION = readFileSync(join(ROOT, 'src', 'modelConnectionPanel.ts'), 'utf8');
const CONNECTION_JS = readFileSync(join(ROOT, 'media', 'model-connection.js'), 'utf8');
const CHAT = readFileSync(join(ROOT, 'src', 'chatPanel.ts'), 'utf8');
const CHAT_JS = readFileSync(join(ROOT, 'media', 'chat.js'), 'utf8');
const SETTINGS = readFileSync(join(ROOT, 'src', 'settings.ts'), 'utf8');
const EXTENSION = readFileSync(join(ROOT, 'src', 'extension.ts'), 'utf8');

test('model ids are trimmed, validated, deduplicated, and stable', () => {
  assert.deepEqual(normalizeModelIds([' gpt-5 ', 'gpt-5', '', 7, 'claude/opus']), [
    'gpt-5',
    'claude/opus',
  ]);
  assert.equal(validModelId('provider/model:latest'), true);
  assert.equal(validModelId('two words'), false);
});

test('an exact models endpoint is normalized for the existing host RPC', () => {
  assert.equal(DEFAULT_MODEL_CATALOG_URL, 'https://athenai.mihoyo.com/v1/models');
  assert.equal(catalogBaseUrl(DEFAULT_MODEL_CATALOG_URL), 'https://athenai.mihoyo.com/v1');
  assert.equal(catalogBaseUrl('http://127.0.0.1:8080/v1/models/'), 'http://127.0.0.1:8080/v1');
  assert.throws(() => catalogBaseUrl('https://example.test/v1'), /end in \/models/);
  assert.throws(() => catalogBaseUrl('file:///models'), /http or https/);
});

test('model connection modes require their effective endpoint and model', () => {
  assert.equal(connectionMode('single'), 'single');
  assert.equal(connectionMode('gateway'), 'gateway');
  assert.equal(connectionMode('legacy'), 'gateway');
  assert.match(baseUrlProblem('') ?? '', /required/);
  assert.equal(baseUrlProblem('https://gateway.example/v1'), undefined);
  assert.match(baseUrlProblem('file:///models') ?? '', /http/);
  assert.equal(connectionProblem('gateway', 'https://gateway.example/v1', ''), undefined);
  assert.match(connectionProblem('single', 'https://api.example/v1', '') ?? '', /Model ID/);
  assert.equal(connectionProblem('single', 'https://api.example/v1', 'claude-sonnet-5-5'), undefined);
});

test('native settings omit model state managed by the focused setup pages', () => {
  const sections = PACKAGE.contributes.configuration;
  assert.ok(Array.isArray(sections));
  assert.deepEqual(
    sections.map((section: { title: string }) => section.title),
    [
      'Anvil: Runtime',
      'Anvil: Agent and Context',
      'Anvil: Editor',
      'Anvil: Sessions and Tools',
    ],
  );
  const properties = configurationProperties(PACKAGE);
  assert.equal(properties['anvil.model'], undefined);
  for (const key of ['modelConnection', 'provider', 'baseUrl', 'modelMode', 'singleModelId', 'effort', 'modelCatalogUrl', 'availableModels']) {
    assert.equal(properties[`anvil.${key}`], undefined);
  }
  assert.ok(PACKAGE.contributes.commands.some((command: { command: string }) => command.command === 'anvil.openModels'));
  assert.ok(PACKAGE.contributes.commands.some((command: { command: string }) => command.command === 'anvil.configureModels'));
  assert.ok(PACKAGE.contributes.commands.some((command: { command: string }) => command.command === 'anvil.selectDefaultModel'));
});

test('the manager curates fetched models without sending credentials to its webview', () => {
  assert.match(PANEL, /this\.host\.listModels\(\{ base_url: base \}\)/);
  assert.match(PANEL, /case 'discover'/);
  assert.match(PANEL, /case 'add'/);
  assert.match(PANEL, /case 'remove'/);
  assert.match(PANEL, /case 'setDefault'/);
  assert.match(PANEL, /case 'backSettings'/);
  assert.match(PANEL, /this\.host\.openModelConnection\(\)/);
  assert.match(PANEL, /Switch Model Mode to Gateway before fetching models/);
  assert.match(PANEL, /class="model-workbench"/);
  assert.match(PANEL, /id="available" class="model-list list-box"/);
  assert.match(PANEL, /id="discovered" class="model-list list-box discovered"/);
  assert.doesNotMatch(PANEL, /api[_A-Z]?key/i);
  assert.match(PANEL, /settings\.gatewayDefaultModel === id[\s\S]*?saveDefaultModel\(this\.context, ''\)/);
  assert.match(PANEL, /id="closeManual"/);
  assert.match(MODELS_JS, /if \(manualForm\.hidden\)[\s\S]*?manualId\.focus\(\)/);
  assert.doesNotMatch(MODELS_JS, /manualForm\.hidden = !/);
});

test('the default picker uses only curated models and migrates the old text setting', () => {
  assert.match(EXTENSION, /registerCommand\('anvil\.selectDefaultModel'/);
  assert.match(EXTENSION, /\.\.\.models\.available\.map\(\(id\) => \(\{ label: id, id \}\)\)/);
  assert.match(SETTINGS, /context\.workspaceState\.get<string>\(DEFAULT_MODEL_KEY, legacy\)/);
  assert.match(SETTINGS, /cfg\.update\('model', undefined, vscode\.ConfigurationTarget\.Workspace\)/);
  assert.match(SETTINGS, /workspaceState\.get<ModelSetup>\(MODEL_SETUP_KEY\) \?\? migratedSetup/);
  assert.match(SETTINGS, /cfg\.get<unknown>\('availableModels', \[\]\)/);
  assert.match(SETTINGS, /cfg\.get<string>\('modelCatalogUrl', DEFAULT_MODEL_CATALOG_URL\)/);
  assert.match(SETTINGS, /workspaceState\.update\(MODEL_SETUP_KEY, modelSetup\)/);
  assert.match(SETTINGS, /const effectiveModel = mode === 'single' \? singleModelId : selectedDefaultModel/);
  assert.match(SETTINGS, /model: emptyToUndefined\(effectiveModel\)/);
});

test('the model setup page keeps the five setup controls together', () => {
  assert.match(CONNECTION, /Model settings/);
  assert.match(CONNECTION, /id="baseUrl"[^>]*required/);
  assert.match(CONNECTION, /id="setApiKey"/);
  assert.match(CONNECTION, /id="provider"/);
  assert.match(CONNECTION, /id="effort"/);
  assert.match(CONNECTION, /id="mode"/);
  assert.match(CONNECTION, /<option value="single">Single API/);
  assert.match(CONNECTION, /<option value="gateway">Gateway/);
  assert.match(CONNECTION, /id="singleModelId"/);
  assert.match(CONNECTION, /id="openManager"/);
  assert.match(CONNECTION, /connectionProblem\(mode, baseUrl, singleModelId\)/);
  assert.match(CONNECTION, /saveModelSetup\(this\.context, setup\)/);
  assert.match(CONNECTION, /executeCommand\('anvil\.setApiKey'\)/);
  assert.match(CONNECTION_JS, /singleFields\.hidden = !single/);
  assert.match(CONNECTION_JS, /gatewayFields\.hidden = single/);
  assert.match(CONNECTION_JS, /singleModelId\.required = single/);
  assert.match(CONNECTION_JS, /payload\('openManager'\)/);
});

test('invalid model connection opens configuration before a session starts', () => {
  const ensure = CHAT.slice(CHAT.indexOf('private ensureSession()'), CHAT.indexOf('private async liveSession()'));
  assert.match(ensure, /settings\.models\.problem/);
  assert.match(ensure, /this\.hub\.openModelConnection\(\)/);
  assert.ok(ensure.indexOf('settings.models.problem') < ensure.indexOf('this.hub.manager.open'));
});

test('chat model selection uses the curated list and remains a session option', () => {
  assert.match(CHAT, /type: 'models',\s*available,\s*current:/);
  assert.match(CHAT, /case 'selectModel':[\s\S]*?this\.setModel\(msg\.model\)/);
  assert.match(CHAT, /No models are available\. Run Anvil: Manage Models first\./);
  assert.match(CHAT, /await this\.applyOptions\(\{ model/);
  assert.match(CHAT_JS, /modelPicker\.addEventListener\('click'/);
  assert.match(CHAT_JS, /type: 'selectModel', model: value/);
  assert.match(CHAT_JS, /type: 'effort', effort: button\.dataset\.effort/);
});

test('chat model and effort choices persist per conversation', () => {
  assert.match(CHAT, /const COMPOSER_OPTIONS_KEY = 'anvil\.composerOptionsByAgent'/);
  assert.match(CHAT, /workspaceState\.get<Record<string, StoredComposerOptions>>\(COMPOSER_OPTIONS_KEY, \{\}\)\[agentId\]/);
  assert.match(CHAT, /const savedComposer = storedComposerOptions\(context, this\.agentId\)/);
  assert.match(CHAT, /if \(savedComposer\.model\) this\.overrides\.model = savedComposer\.model/);
  assert.match(CHAT, /this\.overrides\.effort = savedComposer\.effort \?\?/);
  assert.match(CHAT, /await this\.rememberComposerOptions\(\);[\s\S]*?this\.postModels\(\)/);
  assert.match(CHAT, /await this\.rememberComposerOptions\(\);[\s\S]*?this\.postEffort\(\)/);
  assert.match(CHAT, /Object\.fromEntries\(entries\.slice\(-COMPOSER_OPTIONS_KEPT\)\)/);
});

test('the models manager owns workspace subagent profile CRUD', () => {
  assert.match(PANEL, /id="profiles" class="profile-list"/);
  assert.match(PANEL, /id="profileInstructions"/);
  assert.match(PANEL, /this\.host\.subagentProfiles\(\)/);
  assert.match(PANEL, /this\.host\.upsertSubagentProfile/);
  assert.match(PANEL, /this\.host\.removeSubagentProfile/);
  assert.match(PANEL, /Open subagents\.json/);
  assert.match(MODELS_JS, /Inherit main/);
  assert.match(MODELS_JS, /not available/);
  assert.match(MODELS_JS, /profileState\.problem/);
  assert.match(MODELS_JS, /type: 'saveProfile'/);
});
