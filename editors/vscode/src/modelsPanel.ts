import { randomBytes } from 'node:crypto';
import * as vscode from 'vscode';
import { catalogBaseUrl, normalizeModelIds, validModelId } from './models';
import { ModelEntry, SubagentProfile, SubagentsResult } from './protocol';
import { readSettings, saveDefaultModel } from './settings';
import { mediaAsset } from './webviewAsset';
import { rootUri } from './workspaceRoot';

export const ModelsViewType = 'anvil.models';

export interface ModelsPanelHost {
  listModels(options: { base_url?: string }): Promise<ModelEntry[]>;
  subagentProfiles(): Promise<SubagentsResult>;
  upsertSubagentProfile(profile: SubagentProfile): Promise<SubagentsResult>;
  removeSubagentProfile(id: string): Promise<SubagentsResult>;
  modelsChanged(): void;
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
        await vscode.commands.executeCommand(
          'workbench.action.openSettings',
          '@ext:anvil-dev.anvil-vscode',
        );
        return;
    }
  }

  private async discover(value: unknown): Promise<void> {
    const endpoint = typeof value === 'string' ? value.trim() : '';
    let base: string;
    try {
      base = catalogBaseUrl(endpoint);
    } catch (err) {
      await this.post({ type: 'error', message: (err as Error).message });
      return;
    }
    try {
      await this.configuration().update('modelCatalogUrl', endpoint, this.target());
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
    const available = normalizeModelIds([...settings.available, id]);
    await this.configuration().update('availableModels', available, this.target());
    await this.sendState(`${id} is now available.`);
  }

  private async remove(value: unknown): Promise<void> {
    const id = typeof value === 'string' ? value : '';
    const settings = readSettings().models;
    const available = settings.available.filter((model) => model !== id);
    await this.configuration().update('availableModels', available, this.target());
    if (settings.defaultModel === id) {
      await saveDefaultModel(this.context, '');
      this.host.modelsChanged();
    }
    await this.sendState(`${id} was removed.`);
  }

  private async setDefault(value: unknown): Promise<void> {
    const id = typeof value === 'string' ? value.trim() : '';
    const available = readSettings().models.available;
    if (id !== '' && !available.includes(id)) {
      await this.post({ type: 'error', message: 'The default model must be in the available list.' });
      return;
    }
    await saveDefaultModel(this.context, id);
    this.host.modelsChanged();
    await this.sendState(id === '' ? 'Host default selected.' : `${id} is the default model.`);
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
    try {
      this.profiles = await this.host.subagentProfiles();
    } catch (err) {
      await this.post({ type: 'error', message: `Could not load subagent profiles: ${(err as Error).message}` });
      return;
    }
    await this.post({
      type: 'state',
      ...settings,
      discovered: this.discovered,
      subagents: this.profiles,
      profileSaved,
      note,
    });
  }

  private configuration(): vscode.WorkspaceConfiguration {
    return vscode.workspace.getConfiguration('anvil', rootUri());
  }

  private target(): vscode.ConfigurationTarget {
    return rootUri() ? vscode.ConfigurationTarget.Workspace : vscode.ConfigurationTarget.Global;
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
  <title>Models</title>
</head>
<body>
  <header class="panel-header">
    <div>
      <h1>Models</h1>
      <p>Choose the models this workspace offers to conversations.</p>
    </div>
    <div class="header-actions">
      <button id="backSettings" type="button" class="secondary">← Anvil Settings</button>
      <button id="reload" type="button" class="icon-button" aria-label="Reload models" title="Reload">↻</button>
    </div>
  </header>
  <main>
    <div id="banner" class="banner" hidden></div>
    <section class="default-section">
      <div class="section-head"><div><h2>Default model</h2><p>New conversations start with this model.</p></div></div>
      <label class="field default-field"><span>Default model</span><select id="defaultModel"></select></label>
    </section>
    <div class="model-workbench">
      <section class="model-pane available-pane">
        <div class="section-head">
          <div><h2>Available models</h2><p>Shown in the chat model selector.</p></div>
          <button id="addManual" type="button" class="add-button" aria-expanded="false" aria-controls="manual">+ Add</button>
        </div>
        <form id="manual" class="inline-form" hidden>
          <label class="field"><span>Model id</span><input id="manualId" type="text" placeholder="gpt-5.2" autocomplete="off"></label>
          <button type="submit" class="primary">Add</button>
        </form>
        <div id="available" class="model-list list-box" role="list" aria-label="Available models"></div>
      </section>
      <section class="model-pane gateway-pane">
        <div class="section-head"><div><h2>Gateway models</h2><p>Fetch, then add selected models to the left.</p></div></div>
        <form id="discover" class="catalog-form">
          <label class="field"><span>Models API endpoint</span><input id="catalogUrl" type="url" spellcheck="false"></label>
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
