import { randomBytes } from 'node:crypto';
import * as vscode from 'vscode';
import { connectionMode, connectionProblem } from './models';
import { ModelSetup, readSettings, saveModelSetup } from './settings';
import { mediaAsset } from './webviewAsset';

export const ModelConnectionViewType = 'anvil.modelConnection';

export interface ModelConnectionHost {
  openModels(): void;
  modelsChanged(): void;
  readonly running: boolean;
  restart(): Promise<void>;
}

export class ModelConnectionPanel implements vscode.Disposable {
  private panel: vscode.WebviewPanel | undefined;

  constructor(
    private readonly context: vscode.ExtensionContext,
    private readonly host: ModelConnectionHost,
  ) {}

  open(): void {
    if (this.panel) {
      this.panel.reveal();
      return;
    }
    const panel = vscode.window.createWebviewPanel(
      ModelConnectionViewType,
      'Model settings',
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
        await this.sendState();
        return;
      case 'save':
        await this.save(message);
        return;
      case 'openManager':
        if (await this.save(message)) this.host.openModels();
        return;
      case 'setApiKey':
        await vscode.commands.executeCommand('anvil.setApiKey');
        await this.sendState('API key updated.');
        return;
    }
  }

  private async save(message: Record<string, unknown>): Promise<boolean> {
    const provider = message.provider === 'anthropic' ? 'anthropic' : 'openai';
    const mode = connectionMode(message.mode);
    const baseUrl = typeof message.baseUrl === 'string' ? message.baseUrl.trim() : '';
    const singleModelId = typeof message.singleModelId === 'string' ? message.singleModelId.trim() : '';
    const effort = message.effort === 'low' || message.effort === 'high' ? message.effort : 'medium';
    const problem = connectionProblem(mode, baseUrl, singleModelId);
    if (problem) {
      await this.post({ type: 'error', message: problem });
      return false;
    }
    const setup: ModelSetup = { provider, baseUrl, mode, singleModelId, effort };
    await saveModelSetup(this.context, setup);
    this.host.modelsChanged();
    if (this.host.running) await this.host.restart();
    await this.sendState('Model settings saved.');
    return true;
  }

  private sendState(note = ''): Thenable<boolean> {
    const settings = readSettings().models;
    return this.post({
      type: 'state',
      mode: settings.mode,
      provider: settings.provider,
      baseUrl: settings.baseUrl,
      singleModelId: settings.singleModelId,
      effort: readSettings().session.effort ?? 'medium',
      problem: settings.problem ?? '',
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
  <link rel="stylesheet" href="${asset('model-connection.css')}">
  <title>Model settings</title>
</head>
<body>
  <header class="panel-header">
    <div><h1>Model settings</h1><p>Connect this workspace to one model API or a model gateway.</p></div>
  </header>
  <main class="connection-main">
    <div id="banner" class="banner" hidden></div>
    <form id="connectionForm">
      <label class="field"><span>Base URL</span>
        <input id="baseUrl" type="url" required spellcheck="false" placeholder="https://api.example.com/v1">
      </label>
      <div class="api-key-row"><div><strong>API Key</strong><p>Stored securely by VS Code and shared with the selected endpoint.</p></div>
        <button id="setApiKey" type="button" class="secondary">Set API Key</button>
      </div>
      <label class="field"><span>Provider</span>
        <select id="provider"><option value="openai">OpenAI</option><option value="anthropic">Anthropic</option></select>
      </label>
      <label class="field"><span>Default Effort</span>
        <select id="effort"><option value="low">Low</option><option value="medium">Medium</option><option value="high">High</option></select>
      </label>
      <label class="field"><span>Model Mode</span>
        <select id="mode"><option value="single">Single API</option><option value="gateway">Gateway</option></select>
      </label>
      <section id="singleFields" class="mode-detail">
        <label class="field"><span>Model ID</span>
          <input id="singleModelId" type="text" placeholder="claude-sonnet-5-5" autocomplete="off">
        </label>
      </section>
      <section id="gatewayFields" class="mode-detail">
        <button id="openManager" type="button" class="secondary">Open Model Manager</button>
      </section>
      <div class="form-actions"><button type="submit" class="primary">Save</button></div>
    </form>
  </main>
  <script nonce="${nonce}" src="${asset('model-connection.js')}"></script>
</body>
</html>`;
}
