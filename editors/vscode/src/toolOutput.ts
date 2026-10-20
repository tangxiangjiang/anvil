/**
 * The read-only document a tool result opens into.
 *
 * A result can be longer than a row's pane is allowed to show, and the reader
 * asked for the rest without putting it in the conversation. The content is
 * registered under a scheme nothing can write to: there is no `FileSystemProvider`
 * for `anvil-tool-output:`, so VS Code treats the document as text this
 * extension supplies rather than as a file. Nothing reaches the disk, the
 * document cannot be edited or saved over anything, and the text is drawn by
 * the editor rather than by the webview.
 *
 * One provider serves the window. Documents are kept by URI string and
 * bounded: the last few outputs a reader asked for stay readable, and an old
 * one that has aged out says so rather than returning an empty document.
 */

import * as vscode from 'vscode';
import { TOOL_OUTPUT_SCHEME, outputPath } from './toolOutputName';

/** How many output documents keep their text. */
const KEPT = 16;

export class ToolOutputDocuments implements vscode.TextDocumentContentProvider, vscode.Disposable {
  private readonly contents = new Map<string, string>();
  private readonly changed = new vscode.EventEmitter<vscode.Uri>();
  private readonly registration: vscode.Disposable;
  private untitled = 0;

  readonly onDidChange = this.changed.event;

  constructor() {
    this.registration = vscode.workspace.registerTextDocumentContentProvider(TOOL_OUTPUT_SCHEME, this);
  }

  provideTextDocumentContent(uri: vscode.Uri): string {
    return this.contents.get(uri.toString()) ?? '';
  }

  /**
   * Shows a whole result beside the conversation.
   *
   * The text is copied into the provider before the document is shown, so the
   * first render cannot race an empty answer. The call id is in the query, so
   * clicking one row twice reveals one tab rather than making a second.
   */
  async open(tool: string, id: string, text: string): Promise<vscode.Uri> {
    const key = id !== '' ? id : `untitled-${++this.untitled}`;
    const uri = vscode.Uri.parse(
      `${TOOL_OUTPUT_SCHEME}:${encodeURI(outputPath(tool, key))}?id=${encodeURIComponent(key)}`,
    );
    const at = uri.toString();
    if (!this.contents.has(at)) {
      this.contents.set(at, text);
      if (this.contents.size > KEPT) {
        const oldest = this.contents.keys().next().value;
        if (oldest !== undefined) this.contents.delete(oldest);
      }
    }
    await vscode.window.showTextDocument(uri, {
      // A preview, like following a definition: the output is a glance at one
      // call, and the conversation is what the reader comes back to.
      preview: true,
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
