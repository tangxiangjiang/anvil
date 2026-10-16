/**
 * The chat view: a webview on the left rail, fed by one `HostManager`.
 *
 * The webview is a rendering surface only. Everything with a decision in it —
 * spawning, sessions, approvals, error classification — happens on this side of
 * the message port, so the untrusted-ish HTML never holds state the host needs.
 */

import { randomBytes } from 'node:crypto';
import * as vscode from 'vscode';
import { HostListener, HostManager } from './hostManager';
import { SessionState } from './session';
import { ApprovalRequest, Decision, DoneParams, HostEvent } from './protocol';
import { RunFailure } from './errors';

/** A message from the webview. */
type Inbound =
  | { type: 'ready' }
  | { type: 'send'; text: string }
  | { type: 'cancel' }
  | { type: 'answer'; requestId: string; decision: Decision; scope?: string }
  | { type: 'restart' };

/** A message to the webview. */
type Outbound =
  | { type: 'trust'; trusted: boolean }
  | { type: 'state'; state: SessionState; runId?: string; sessionId?: string; agentId?: string }
  | { type: 'event'; event: HostEvent }
  | { type: 'approval'; request: ApprovalRequest }
  | { type: 'approvalWithdrawn'; requestId: string }
  | { type: 'done'; done: DoneParams; failure: RunFailure }
  | { type: 'notice'; level: 'info' | 'warn' | 'error'; message: string };

export class ChatPanelProvider implements vscode.WebviewViewProvider, HostListener {
  public static readonly viewType = 'anvil.chatPanel';

  private view: vscode.WebviewView | undefined;
  private manager: HostManager | undefined;
  /** Fires when the host comes up or goes away, for the title-bar stop button. */
  private readonly runningChanged = new vscode.EventEmitter<void>();

  constructor(
    private readonly context: vscode.ExtensionContext,
    private readonly log: (line: string) => void,
  ) {}

  resolveWebviewView(view: vscode.WebviewView): void {
    this.view = view;
    view.webview.options = {
      enableScripts: true,
      localResourceRoots: [vscode.Uri.joinPath(this.context.extensionUri, 'media')],
    };
    view.webview.html = renderHtml(view.webview, this.context.extensionUri);
    view.webview.onDidReceiveMessage((msg: Inbound) => void this.onMessage(msg));
    view.onDidDispose(() => {
      this.view = undefined;
    });
  }

  /** Creates the manager lazily: no host work happens until a view exists. */
  private ensureManager(): HostManager {
    if (!this.manager) {
      this.manager = new HostManager(this.context, this.log, this);
    }
    return this.manager;
  }

  private async onMessage(msg: Inbound): Promise<void> {
    if (msg.type === 'ready') {
      // A reloaded webview has lost its transcript, so re-announce everything
      // it cannot infer: trust, the run in flight, and any open prompt.
      this.post({ type: 'trust', trusted: vscode.workspace.isTrusted });
      if (this.manager) this.post({ type: 'state', ...this.manager.state() });
      for (const request of this.pendingApprovals()) {
        this.post({ type: 'approval', request });
      }
      return;
    }

    try {
      HostManager.assertTrusted();
    } catch (err) {
      this.post({ type: 'notice', level: 'error', message: (err as Error).message });
      return;
    }

    switch (msg.type) {
      case 'send':
        await this.ensureManager()
          .send(msg.text)
          .catch(() => undefined);
        return;
      case 'cancel':
        await this.ensureManager().cancel();
        return;
      case 'answer':
        await this.ensureManager().respond(msg.requestId, msg.decision, { scope: msg.scope });
        return;
      case 'restart':
        await this.ensureManager().restart();
        this.post({ type: 'state', state: 'idle' });
        return;
      default:
        return;
    }
  }

  private pendingApprovals(): ApprovalRequest[] {
    return this.manager?.pendingApprovals() ?? [];
  }

  // --- HostListener ----------------------------------------------------------

  onEvent = (event: HostEvent): void => this.post({ type: 'event', event });
  onApproval = (request: ApprovalRequest): void => this.post({ type: 'approval', request });
  onApprovalWithdrawn = (requestId: string): void => this.post({ type: 'approvalWithdrawn', requestId });
  onDone = (done: DoneParams, failure: RunFailure): void => this.post({ type: 'done', done, failure });
  onState = (): void => {
    if (this.manager) this.post({ type: 'state', ...this.manager.state() });
    this.runningChanged.fire();
  };
  onNotice = (level: 'info' | 'warn' | 'error', message: string): void => this.post({ type: 'notice', level, message });

  private post(message: Outbound): void {
    void this.view?.webview.postMessage(message);
  }

  /** Stops the active run. Safe when nothing is running. */
  async stop(): Promise<void> {
    await this.manager?.cancel();
  }

  /** Replaces the host child and tells the view to start over. */
  async restart(): Promise<void> {
    await this.ensureManager().restart();
    this.post({ type: 'state', state: 'idle' });
    this.runningChanged.fire();
  }

  /** Whether a host child is live right now. */
  get running(): boolean {
    return this.manager?.running ?? false;
  }

  /** Subscribes to host up/down transitions. */
  onRunningChanged(listener: () => void): vscode.Disposable {
    return this.runningChanged.event(listener);
  }

  async dispose(): Promise<void> {
    const manager = this.manager;
    this.manager = undefined;
    this.runningChanged.dispose();
    if (manager) await manager.dispose();
  }
}

/** Builds the webview document. Nothing here is fetched at runtime. */
function renderHtml(webview: vscode.Webview, extensionUri: vscode.Uri): string {
  const nonce = makeNonce();
  const asset = (name: string): vscode.Uri =>
    webview.asWebviewUri(vscode.Uri.joinPath(extensionUri, 'media', name));
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src ${webview.cspSource}; font-src ${webview.cspSource}; script-src 'nonce-${nonce}';">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<link rel="stylesheet" href="${asset('chat.css')}">
<title>Anvil</title>
</head>
<body>
  <div id="banner" class="banner" hidden></div>
  <div id="transcript" class="transcript" role="log" aria-live="polite">
    <div id="empty" class="empty">
      <p>Ask for a change. Anvil reads, edits, and runs commands in this workspace.</p>
      <p class="hint">Mutating calls stop for your approval. <kbd>Stop</kbd> interrupts a turn.</p>
    </div>
  </div>
  <div id="status" class="status" hidden></div>
  <form id="composer" class="composer">
    <textarea id="input" rows="3" placeholder="Describe a change…" aria-label="Prompt"></textarea>
    <div class="actions">
      <button id="stop" type="button" class="secondary" hidden>Stop</button>
      <button id="send" type="submit" class="primary">Send</button>
    </div>
  </form>
  <script nonce="${nonce}" src="${asset('chat.js')}"></script>
</body>
</html>`;
}

/** A fresh CSP nonce per render, from the platform's random source. */
function makeNonce(): string {
  return randomBytes(16).toString('base64');
}
