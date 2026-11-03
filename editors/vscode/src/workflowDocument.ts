import * as vscode from 'vscode';

const SCHEME = 'anvil-workflow';

/** Read-only plan bodies opened from the Workflow view. */
export class WorkflowDocuments implements vscode.TextDocumentContentProvider, vscode.Disposable {
  private readonly changed = new vscode.EventEmitter<vscode.Uri>();
  readonly onDidChange = this.changed.event;
  private readonly bodies = new Map<string, string>();
  private readonly registration: vscode.Disposable;

  constructor() {
    this.registration = vscode.workspace.registerTextDocumentContentProvider(SCHEME, this);
  }

  provideTextDocumentContent(uri: vscode.Uri): string {
    return this.bodies.get(uri.toString()) ?? 'This workflow artifact is no longer available.';
  }

  async open(agentId: string, artifactId: string, title: string, body: string): Promise<void> {
    const uri = vscode.Uri.from({
      scheme: SCHEME,
      authority: encodeURIComponent(agentId),
      path: `/${encodeURIComponent(artifactId)}/${encodeURIComponent(safeTitle(title))}.md`,
    });
    this.bodies.set(uri.toString(), body || '(This artifact has no prose body.)');
    this.changed.fire(uri);
    const document = await vscode.workspace.openTextDocument(uri);
    await vscode.window.showTextDocument(document, { preview: true });
  }

  dispose(): void {
    this.registration.dispose();
    this.changed.dispose();
    this.bodies.clear();
  }
}

function safeTitle(value: string): string {
  const title = value.replace(/[\\/:*?"<>|]/g, ' ').replace(/\s+/g, ' ').trim();
  return (title || 'Plan').slice(0, 80);
}
