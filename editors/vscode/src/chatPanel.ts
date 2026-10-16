/**
 * The chat view: a webview on the left rail, fed by one `HostManager`.
 *
 * The webview is a rendering surface only. Everything with a decision in it —
 * spawning, sessions, approvals, context composition, review tabs — happens on
 * this side of the message port, so the untrusted-ish HTML never holds state
 * the host needs and never names a path the host will act on without this side
 * having decided that it should.
 */

import { randomBytes } from 'node:crypto';
import * as vscode from 'vscode';
import { ChangesTracker, ChangedFile } from './changes';
import { buildPromptContext, pickMentions } from './editorContext';
import { attachmentsFromDataUrls } from './attachments';
import { HostListener, HostManager } from './hostManager';
import { PreviewDocuments } from './review';
import { SessionState } from './session';
import { dedupeMentions } from './mentions';
import { ApprovalRequest, Decision, DoneParams, HostEvent } from './protocol';
import { RunFailure } from './errors';
import { readSettings } from './settings';

/** A message from the webview. */
type Inbound =
  | { type: 'ready' }
  | { type: 'send'; text: string; mentions?: string[]; images?: string[] }
  | { type: 'cancel' }
  | { type: 'answer'; requestId: string; decision: Decision; scope?: string }
  | { type: 'restart' }
  | { type: 'pickMention' }
  | { type: 'review'; requestId: string }
  | { type: 'reviewChange'; path: string };

/** A message to the webview. */
type Outbound =
  | { type: 'trust'; trusted: boolean }
  | { type: 'state'; state: SessionState; runId?: string; sessionId?: string; agentId?: string }
  | { type: 'event'; event: HostEvent }
  | { type: 'approval'; request: ApprovalRequest }
  | { type: 'approvalWithdrawn'; requestId: string }
  | { type: 'done'; done: DoneParams; failure: RunFailure }
  | { type: 'mentions'; paths: string[] }
  | { type: 'vision'; enabled: boolean }
  | { type: 'changes'; files: Array<Pick<ChangedFile, 'path' | 'kind'>> }
  | { type: 'notice'; level: 'info' | 'warn' | 'error'; message: string };

export class ChatPanelProvider implements vscode.WebviewViewProvider, HostListener {
  public static readonly viewType = 'anvil.chatPanel';

  private view: vscode.WebviewView | undefined;
  private manager: HostManager | undefined;
  /** Fires when the host comes up or goes away, for the title-bar stop button. */
  private readonly runningChanged = new vscode.EventEmitter<void>();
  /** Pre-images for the current turn, so the changed-files list can show diffs. */
  private readonly changes = new ChangesTracker();
  /** The changed files of the last finished turn, by path. */
  private lastChanges = new Map<string, ChangedFile>();
  /** Review tabs opened for an approval, closed when it is answered. */
  private readonly reviews = new Map<string, vscode.Uri[]>();

  constructor(
    private readonly context: vscode.ExtensionContext,
    private readonly log: (line: string) => void,
    private readonly previews: PreviewDocuments,
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
      this.post({ type: 'vision', enabled: readSettings().session.vision === true });
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
        await this.sendTurn(msg.text, msg.mentions ?? [], msg.images ?? []);
        return;
      case 'cancel':
        await this.ensureManager().cancel();
        return;
      case 'answer':
        await this.answer(msg.requestId, msg.decision, msg.scope);
        return;
      case 'restart':
        await this.ensureManager().restart();
        this.post({ type: 'state', state: 'idle' });
        return;
      case 'pickMention':
        // The picker is the editor's, not the webview's: it searches the real
        // workspace and returns paths, and a path is all the protocol carries.
        this.post({ type: 'mentions', paths: dedupeMentions(await pickMentions()) });
        return;
      case 'review':
        await this.openReview(msg.requestId);
        return;
      case 'reviewChange':
        await this.openChangeReview(msg.path);
        return;
      default:
        return;
    }
  }

  /** Composes and sends one turn, with whatever context the editor has. */
  private async sendTurn(text: string, mentions: string[], images: string[]): Promise<void> {
    const context = buildPromptContext(dedupeMentions(mentions));
    const { attachments, errors } = attachmentsFromDataUrls(images);
    for (const error of errors) {
      this.post({ type: 'notice', level: 'warn', message: `Dropped an attachment: ${error}.` });
    }
    // The pre-images this turn will need are gone the moment it starts writing,
    // so the tracker is reset here rather than when the first write arrives.
    this.changes.begin();
    this.lastChanges = new Map();
    await this.ensureManager()
      .send(text, { context, attachments })
      .catch(() => undefined);
  }

  /** Answers an approval, and closes the review it was answered from. */
  private async answer(requestId: string, decision: Decision, scope?: string): Promise<void> {
    await this.ensureManager().respond(requestId, decision, { scope });
    await this.closeReview(requestId);
  }

  private pendingApprovals(): ApprovalRequest[] {
    return this.manager?.pendingApprovals() ?? [];
  }

  // --- reviews ---------------------------------------------------------------

  /**
   * Opens the preview in the editor's own diff view. Only write_file and
   * edit_file carry both bodies; apply_patch spans files and so is left to the
   * diff text the card already shows.
   */
  private async openReview(requestId: string): Promise<void> {
    const request = this.pendingApprovals().find((candidate) => candidate.request_id === requestId);
    const preview = request?.preview;
    if (!request || !preview || typeof preview.diff !== 'string') {
      this.post({ type: 'notice', level: 'warn', message: 'There is no preview to review for this call.' });
      return;
    }
    if (preview.truncated || typeof preview.before !== 'string' || typeof preview.after !== 'string') {
      // The host dropped the bodies rather than send a clipped post-image, so
      // the honest thing is to say the diff text is all there is.
      this.post({
        type: 'notice',
        level: 'warn',
        message: 'This change is too large to open as a diff. Review the diff shown in the card.',
      });
      return;
    }
    const label = preview.path ?? request.tool;
    const uris = await this.previews.openPreview(
      preview.before,
      preview.after,
      label,
      `${label} (proposed)`,
    );
    const existing = this.reviews.get(requestId) ?? [];
    this.reviews.set(requestId, [...existing, ...uris]);
  }

  private async openChangeReview(path: string): Promise<void> {
    const change = this.lastChanges.get(path);
    if (!change) return;
    if (change.kind === 'deleted') {
      await this.previews.openDeleted(change.before, path);
      return;
    }
    const folder = vscode.workspace.workspaceFolders?.[0];
    if (!folder) return;
    const segments = path.split('/').filter((segment) => segment !== '' && segment !== '..' && segment !== '.');
    if (segments.length === 0) return;
    await this.previews.openAgainstDisk(change.before, vscode.Uri.joinPath(folder.uri, ...segments), path);
  }

  private async closeReview(requestId: string): Promise<void> {
    const uris = this.reviews.get(requestId);
    if (!uris) return;
    this.reviews.delete(requestId);
    await this.previews.close(uris);
  }

  // --- HostListener ----------------------------------------------------------

  onEvent = (event: HostEvent): void => {
    // The tool call is the last moment the file it names still holds its old
    // contents, and it arrives whether or not the call is gated — which is what
    // makes the changed-files list work in accept_edits and bypass too.
    if (event.type === 'tool_call') {
      void this.changes.snapshotToolCall(event.name, event.args);
    }
    this.post({ type: 'event', event });
  };

  onApproval = (request: ApprovalRequest): void => {
    // Also snapshotted here, where nothing can be racing the write: a gated
    // call is blocked until the user answers.
    void this.changes.snapshotToolCall(request.tool, request.args);
    this.post({ type: 'approval', request });
  };

  onApprovalWithdrawn = (requestId: string): void => {
    void this.closeReview(requestId);
    this.post({ type: 'approvalWithdrawn', requestId });
  };

  onDone = (done: DoneParams, failure: RunFailure): void => {
    this.post({ type: 'done', done, failure });
    void this.reportChanges();
  };

  onState = (): void => {
    if (this.manager) this.post({ type: 'state', ...this.manager.state() });
    this.runningChanged.fire();
  };

  onNotice = (level: 'info' | 'warn' | 'error', message: string): void =>
    this.post({ type: 'notice', level, message });

  /** Reports what the turn actually changed, once the writes have landed. */
  private async reportChanges(): Promise<void> {
    try {
      const files = await this.changes.finish();
      this.lastChanges = new Map(files.map((file) => [file.path, file]));
      this.post({
        type: 'changes',
        files: files.map((file) => ({ path: file.path, kind: file.kind })),
      });
    } catch (err) {
      this.log(`could not compute the changed files: ${(err as Error).message}`);
    }
  }

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
    for (const uris of this.reviews.values()) {
      await this.previews.close(uris).catch(() => undefined);
    }
    this.reviews.clear();
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
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src ${webview.cspSource}; font-src ${webview.cspSource}; img-src data: ${webview.cspSource}; script-src 'nonce-${nonce}';">
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
    <div id="chips" class="chips" hidden></div>
    <textarea id="input" rows="3" placeholder="Describe a change…  (@ references a file)" aria-label="Prompt"></textarea>
    <div class="actions">
      <button id="attach" type="button" class="secondary" title="Reference a file or folder">@</button>
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
