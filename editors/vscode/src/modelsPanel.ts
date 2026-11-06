import { randomBytes } from 'node:crypto';
import * as vscode from 'vscode';
import { catalogBaseUrl, normalizeModelIds, validModelId } from './models';
import { ModelEntry, SubagentProfile, SubagentsResult } from './protocol';
import {
  readSettings,
  saveAvailableModels,
  saveDefaultModel,
  saveGatewayModelCapabilities,
  saveModelCatalogUrl,
  saveModelRoute,
  promptForModelApiKey,
  clearModelApiKey,
  readModelApiKey,
} from './settings';
import { mediaAsset } from './webviewAsset';

export const ModelsViewType = 'anvil.models';

export interface ModelsPanelHost {
  listModels(options: { base_url?: string }): Promise<ModelEntry[]>;
  subagentProfiles(): Promise<SubagentsResult>;
  upsertSubagentProfile(profile: SubagentProfile): Promise<SubagentsResult>;
  removeSubagentProfile(id: string): Promise<SubagentsResult>;
  readonly running: boolean;
  restart(): Promise<void>;
  modelsChanged(): void;
  openModelConnection(): void;
}

export class ModelsPanels implements vscode.Disposable {
  private panel: vscode.WebviewPanel | undefined;
  private discovered: ModelEntry[] = [];
  private profiles: SubagentsResult | undefined;

  constructor(
    private readonly context: vscode.ExtensionContext,
    private readonly host: ModelsPanelHost,
  ) {}

  open(): void {
    if (this.panel) {
      this.panel.reveal();
      return;
    }
    const panel = vscode.window.createWebviewPanel(
      ModelsViewType,
      'Models',
      vscode.ViewColumn.Active,
      {
        enableScripts: true,
        localResourceRoots: [vscode.Uri.joinPath(this.context.extensionUri, 'media')],
      },
    );
    this.panel = panel;
    panel.webview.html = renderHtml(panel.webview, this.context.extensionUri);
    panel.webview.onDidReceiveMessage((raw: unknown) => void this.handle(raw));
    panel.onDidDispose(() => {
      if (this.panel === panel) this.panel = undefined;
    });
  }

  configurationChanged(): void {
    if (this.panel) void this.sendState();
  }

  dispose(): void {
    this.panel?.dispose();
    this.panel = undefined;
  }

  private async handle(raw: unknown): Promise<void> {
    if (!raw || typeof raw !== 'object') return;
    const message = raw as Record<string, unknown>;
    switch (message.type) {
      case 'ready':
      case 'reload':
        await this.sendState();
        return;
      case 'discover':
        await this.discover(message.url);
        return;
      case 'add':
        await this.add(message.id);
        return;
      case 'remove':
        await this.remove(message.id);
        return;
      case 'setDefault':
        await this.setDefault(message.id);
        return;
      case 'saveRoute':
        await this.saveRoute(message.id, message.route);
        return;
      case 'removeRoute':
        await this.removeRoute(message.id);
        return;
      case 'setRouteKey':
        await this.setRouteKey(message.provider);
        return;
      case 'clearRouteKey':
        await this.clearRouteKey(message.provider);
        return;
      case 'saveProfile':
        await this.saveProfile(message.profile);
        return;
      case 'removeProfile':
        await this.removeProfile(message.id);
        return;
      case 'openProfiles':
        await this.openProfiles();
        return;
      case 'backSettings':
        this.panel?.dispose();
        this.host.openModelConnection();
        return;
    }
  }

  private async discover(value: unknown): Promise<void> {
    if (readSettings().models.mode !== 'gateway') {
      await this.post({ type: 'error', message: 'Switch Model Mode to Gateway before fetching models.' });
      return;
    }
    const endpoint = typeof value === 'string' ? value.trim() : '';
    let base: string;
    try {
      base = catalogBaseUrl(endpoint);
    } catch (err) {
      await this.post({ type: 'error', message: (err as Error).message });
      return;
    }
    try {
      await saveModelCatalogUrl(this.context, endpoint);
      this.discovered = await this.host.listModels({ base_url: base });
      await this.sendState('Models loaded. Add the ones this workspace may use.');
    } catch (err) {
      await this.post({ type: 'error', message: `Could not load models: ${(err as Error).message}` });
    }
  }

  private async add(value: unknown): Promise<void> {
    const id = typeof value === 'string' ? value.trim() : '';
    if (!validModelId(id)) {
      await this.post({ type: 'error', message: 'Enter a model id without spaces (maximum 256 characters).' });
      return;
    }
    const settings = readSettings().models;
    const available = normalizeModelIds([...settings.gatewayAvailable, id]);
    const discovered = this.discovered.find((model) => model.id === id);
    await saveGatewayModelCapabilities(this.context, id, discovered?.capabilities);
    await saveAvailableModels(this.context, available);
    this.host.modelsChanged();
    await this.sendState(`${id} is now available.`);
  }

  private async remove(value: unknown): Promise<void> {
    const id = typeof value === 'string' ? value : '';
    const settings = readSettings().models;
    const available = settings.gatewayAvailable.filter((model) => model !== id);
    await saveAvailableModels(this.context, available);
    await saveModelRoute(this.context, id, undefined);
    if (settings.gatewayDefaultModel === id) {
      await saveDefaultModel(this.context, '');
    }
    this.host.modelsChanged();
    if (this.host.running) await this.host.restart();
    await this.sendState(`${id} was removed.`);
  }

  private async setDefault(value: unknown): Promise<void> {
    const id = typeof value === 'string' ? value.trim() : '';
    const available = readSettings().models.gatewayAvailable;
    if (id !== '' && !available.includes(id)) {
      await this.post({ type: 'error', message: 'The default model must be in the available list.' });
      return;
    }
    await saveDefaultModel(this.context, id);
    this.host.modelsChanged();
    await this.sendState(id === '' ? 'Host default selected.' : `${id} is the default model.`);
  }

  private async saveRoute(idValue: unknown, value: unknown): Promise<void> {
    const id = typeof idValue === 'string' ? idValue.trim() : '';
    if (!readSettings().models.gatewayAvailable.includes(id) || !value || typeof value !== 'object') {
      await this.post({ type: 'error', message: 'Choose an available model before saving its route.' });
      return;
    }
    const raw = value as Record<string, unknown>;
    try {
      await saveModelRoute(this.context, id, {
        provider: raw.provider === 'anthropic' ? 'anthropic' : 'openai',
        baseUrl: typeof raw.baseUrl === 'string' ? raw.baseUrl : '',
        credential:
          raw.credential === 'openai' || raw.credential === 'anthropic' ? raw.credential : 'default',
      });
      this.host.modelsChanged();
      if (this.host.running) await this.host.restart();
      await this.sendState(`${id} route saved.`);
    } catch (err) {
      await this.post({ type: 'error', message: (err as Error).message });
    }
  }

  private async removeRoute(value: unknown): Promise<void> {
    const id = typeof value === 'string' ? value.trim() : '';
    if (!id) return;
    await saveModelRoute(this.context, id, undefined);
    this.host.modelsChanged();
    if (this.host.running) await this.host.restart();
    await this.sendState(`${id} now inherits the main connection.`);
  }

  private async setRouteKey(value: unknown): Promise<void> {
    const provider = value === 'anthropic' ? 'anthropic' : value === 'openai' ? 'openai' : undefined;
    if (!provider || !await promptForModelApiKey(this.context, provider)) return;
    if (this.host.running) await this.host.restart();
    await this.sendState(`${provider === 'openai' ? 'OpenAI-compatible' : 'Anthropic'} route key saved.`);
  }

  private async clearRouteKey(value: unknown): Promise<void> {
    const provider = value === 'anthropic' ? 'anthropic' : value === 'openai' ? 'openai' : undefined;
    if (!provider) return;
    await clearModelApiKey(this.context, provider);
    if (this.host.running) await this.host.restart();
    await this.sendState(`${provider === 'openai' ? 'OpenAI-compatible' : 'Anthropic'} route key cleared.`);
  }

  private async saveProfile(value: unknown): Promise<void> {
    if (!value || typeof value !== 'object') return;
    try {
      this.profiles = await this.host.upsertSubagentProfile(value as SubagentProfile);
      await this.sendState('Subagent profile saved.', true);
    } catch (err) {
      await this.post({ type: 'profileError', message: (err as Error).message });
    }
  }

  private async removeProfile(value: unknown): Promise<void> {
    const id = typeof value === 'string' ? value : '';
    if (!id) return;
    try {
      this.profiles = await this.host.removeSubagentProfile(id);
      await this.sendState(`${id} was removed.`);
    } catch (err) {
      await this.post({ type: 'profileError', message: (err as Error).message });
    }
  }

  private async openProfiles(): Promise<void> {
    const path = this.profiles?.path;
    if (!path || !this.profiles?.exists) return;
    await vscode.window.showTextDocument(await vscode.workspace.openTextDocument(vscode.Uri.file(path)));
  }

  private async sendState(note = '', profileSaved = false): Promise<void> {
    const settings = readSettings().models;
    const [openaiApiKey, anthropicApiKey] = await Promise.all([
      readModelApiKey(this.context, 'openai'),
      readModelApiKey(this.context, 'anthropic'),
    ]);
    try {
      this.profiles = await this.host.subagentProfiles();
    } catch (err) {
      await this.post({ type: 'error', message: `Could not load subagent profiles: ${(err as Error).message}` });
      return;
    }
    await this.post({
      type: 'state',
      ...settings,
      available: settings.gatewayAvailable,
      defaultModel: settings.gatewayDefaultModel,
      discovered: this.discovered,
      subagents: this.profiles,
      routeKeys: {
        openai: Boolean(openaiApiKey),
        anthropic: Boolean(anthropicApiKey),
      },
      profileSaved,
      note,
    });
  }

  private post(message: unknown): Thenable<boolean> {
    return this.panel?.webview.postMessage(message) ?? Promise.resolve(false);
  }
}

function renderHtml(webview: vscode.Webview, extensionUri: vscode.Uri): string {
  const nonce = randomBytes(16).toString('hex');
  const assetUri = mediaAsset(webview, extensionUri);
  const asset = (name: string): string => assetUri(name).toString();
  const csp = [
    "default-src 'none'",
    `style-src ${webview.cspSource}`,
    `script-src 'nonce-${nonce}'`,
  ].join('; ');
  return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta http-equiv="Content-Security-Policy" content="${csp}">
  <link rel="stylesheet" href="${asset('tokens.css')}">
  <link rel="stylesheet" href="${asset('panel.css')}">
  <link rel="stylesheet" href="${asset('models.css')}">
  <title>Model Manager</title>
</head>
<body>
  <header class="panel-header">
    <div>
      <h1>Model Manager</h1>
      <p>Choose the gateway models this workspace offers to conversations.</p>
    </div>
    <div class="header-actions">
      <button id="backSettings" type="button" class="secondary">← Model settings</button>
      <button id="reload" type="button" class="icon-button" aria-label="Reload models" title="Reload">↻</button>
    </div>
  </header>
  <main>
    <div id="banner" class="banner" hidden></div>
    <section class="default-section">
      <div class="section-head"><div><h2>Default model</h2><p>New conversations start with this model.</p></div></div>
      <label class="field default-field"><span>Default model</span><select id="defaultModel"></select></label>
    </section>
    <section id="routeEditor" class="route-editor" hidden>
      <div class="section-head">
        <div><h2>Model route · <code id="routeModel"></code></h2><p>Override the inherited main connection for Subagents that select this model.</p></div>
        <button id="closeRoute" type="button" class="icon-button" aria-label="Close model route" title="Close">×</button>
      </div>
      <form id="routeForm" class="route-form">
        <label class="field"><span>Protocol</span><select id="routeProvider"><option value="openai">OpenAI-compatible</option><option value="anthropic">Anthropic</option></select></label>
        <label class="field route-url"><span>Base URL</span><input id="routeBaseUrl" type="url" required spellcheck="false" placeholder="https://gateway.example.com/v1"></label>
        <label class="field"><span>Credential</span><select id="routeCredential"><option value="default">Default API key</option><option value="openai">OpenAI route key</option><option value="anthropic">Anthropic route key</option></select></label>
        <div class="form-actions route-actions">
          <button id="removeRoute" type="button" class="secondary">Use main connection</button>
          <button type="submit" class="primary">Save route</button>
        </div>
      </form>
      <div class="route-keys">
        <span id="openaiKeyState">OpenAI route key: fallback</span>
        <button id="setOpenaiKey" type="button" class="secondary">Set key</button>
        <button id="clearOpenaiKey" type="button" class="secondary">Clear</button>
        <span id="anthropicKeyState">Anthropic route key: fallback</span>
        <button id="setAnthropicKey" type="button" class="secondary">Set key</button>
        <button id="clearAnthropicKey" type="button" class="secondary">Clear</button>
      </div>
    </section>
    <div class="model-workbench">
      <section class="model-pane available-pane">
        <div class="section-head">
          <div><h2>Available models</h2><p>Shown in the chat model selector.</p></div>
          <button id="addManual" type="button" class="add-button" aria-expanded="false" aria-controls="manual">+ Add</button>
        </div>
        <form id="manual" class="inline-form" hidden>
          <label class="field"><span>Model id</span><input id="manualId" type="text" placeholder="gpt-5.2" autocomplete="off"></label>
          <button id="closeManual" type="button" class="icon-button" aria-label="Close model input" title="Close">×</button>
          <button type="submit" class="primary">Add</button>
        </form>
        <div id="available" class="model-list list-box" role="list" aria-label="Available models"></div>
      </section>
      <section class="model-pane gateway-pane">
        <div class="section-head"><div><h2>Gateway models</h2><p>Fetch the Model List API, then add selected models to the left.</p></div></div>
        <form id="discover" class="catalog-form">
          <label class="field"><span>Model List API</span><input id="catalogUrl" type="url" spellcheck="false" placeholder="https://gateway.example.com/v1/models"></label>
          <button id="fetch" type="submit" class="primary">Fetch</button>
        </form>
        <div id="discovered" class="model-list list-box discovered" role="list" aria-label="Gateway models"></div>
      </section>
    </div>
    <section class="profiles-section">
      <div class="section-head">
        <div><h2>Subagent profiles</h2><p>Workspace specializations the main agent can choose for a task.</p></div>
        <div class="profile-head-actions">
          <button id="openProfiles" type="button" class="secondary">Open subagents.json</button>
          <button id="addProfile" type="button" class="primary">+ Add profile</button>
        </div>
      </div>
      <div id="profileProblem" class="banner error" hidden></div>
      <form id="profileForm" class="profile-form" hidden>
        <input id="profileOriginalId" type="hidden">
        <div class="profile-grid">
          <label class="field"><span>ID</span><input id="profileId" required pattern="[a-z][a-z0-9_-]{0,63}" placeholder="explorer"></label>
          <label class="field"><span>Model</span><select id="profileModel"></select></label>
          <label class="field"><span>Max steps</span><input id="profileMaxSteps" type="number" min="0" value="0"></label>
        </div>
        <label class="field"><span>Description</span><input id="profileDescription" required maxlength="512" placeholder="Investigates unfamiliar code"></label>
        <label class="field"><span>Instructions</span><textarea id="profileInstructions" required maxlength="32768" rows="5" placeholder="Trace behavior and report evidence."></textarea></label>
        <fieldset><legend>Tools</legend><div id="profileTools" class="tool-options"></div></fieldset>
        <div class="form-actions">
          <button id="cancelProfile" type="button" class="secondary">Cancel</button>
          <button type="submit" class="primary">Save profile</button>
        </div>
      </form>
      <div id="profiles" class="profile-list" role="list" aria-label="Subagent profiles"></div>
    </section>
  </main>
  <script nonce="${nonce}" src="${asset('models.js')}"></script>
</body>
</html>`;
}
