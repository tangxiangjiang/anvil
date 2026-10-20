/**
 * The read-only document a plan-mode answer opens into.
 *
 * The content is registered under a scheme nothing can write to: there is no
 * `FileSystemProvider` for `anvil-plan:`, so VS Code treats the document as
 * content the extension supplies rather than as a file. That is the whole of
 * the safety property — the document cannot be edited, saved over the plan, or
 * used to run anything, and its text is drawn by the editor rather than by the
 * webview.
 *
 * One provider serves the window. Plans are kept by URI string and bounded: a
 * reader can open the last few a conversation produced, and an old one that has
 * aged out says so rather than returning an empty document.
 */

import * as vscode from 'vscode';
import { PLAN_SCHEME, planPath, planQuery, planTitle } from './plan';

/** How many plan documents keep their text. */
const KEPT = 16;

export class PlanDocuments implements vscode.TextDocumentContentProvider, vscode.Disposable {
  private readonly contents = new Map<string, string>();
  private readonly changed = new vscode.EventEmitter<vscode.Uri>();
  private readonly registration: vscode.Disposable;

  readonly onDidChange = this.changed.event;

  constructor() {
    this.registration = vscode.workspace.registerTextDocumentContentProvider(PLAN_SCHEME, this);
  }

  provideTextDocumentContent(uri: vscode.Uri): string {
    return this.contents.get(uri.toString()) ?? '';
  }

  /**
   * Shows a plan beside the conversation.
   *
   * The text is copied into the provider before the document is shown, so the
   * first render cannot race an empty answer. An earlier tab for the same
   * conversation and run is revealed rather than duplicated.
   */
  async open(agentId: string, runId: string, text: string): Promise<vscode.Uri> {
    const uri = vscode.Uri.parse(
      `${PLAN_SCHEME}:${encodeURI(planPath(planTitle(text)))}?${planQuery(agentId, runId)}`,
    );
    const key = uri.toString();
    if (!this.contents.has(key)) {
      this.contents.set(key, text);
      if (this.contents.size > KEPT) {
        const oldest = this.contents.keys().next().value;
        if (oldest !== undefined) this.contents.delete(oldest);
      }
    }
    await vscode.window.showTextDocument(uri, {
      // Beside the transcript, and not as a preview: the plan is a document the
      // reader comes back to while deciding, not a transient glance.
      viewColumn: vscode.ViewColumn.Beside,
      preview: false,
      preserveFocus: false,
    });
    return uri;
  }

  dispose(): void {
    this.contents.clear();
    this.registration.dispose();
    this.changed.dispose();
  }
}
