/**
 * Reviewing a change before it happens, and looking at one after.
 *
 * The preview is opened in VS Code's own diff editor rather than drawn again
 * inside the webview. That is the whole point of the host sending `before` and
 * `after`: the review gets inline change detection, folding, and the user's
 * theme for free, and the extension never has to apply a diff to a buffer to
 * know what the result would look like.
 *
 * Both sides are served from memory through a read-only content provider. The
 * "after" side of a change that has already happened is the file on disk
 * instead — that is the real result, not a projection of it.
 */

import { randomBytes } from 'node:crypto';
import * as vscode from 'vscode';

/** The scheme the preview documents are served under. */
export const PREVIEW_SCHEME = 'anvil-preview';

/** Serves preview bodies. Registered once, for the life of the extension. */
export class PreviewDocuments implements vscode.TextDocumentContentProvider, vscode.Disposable {
  private readonly bodies = new Map<string, string>();
  private readonly changed = new vscode.EventEmitter<vscode.Uri>();
  private readonly registration: vscode.Disposable;

  readonly onDidChange = this.changed.event;

  constructor() {
    this.registration = vscode.workspace.registerTextDocumentContentProvider(PREVIEW_SCHEME, this);
  }

  provideTextDocumentContent(uri: vscode.Uri): string {
    return this.bodies.get(uri.toString()) ?? '';
  }

  /**
   * Opens a diff of `before` against `after`, both in memory. Returns the URIs
   * so the caller can close the review when the question it belongs to is
   * answered.
   */
  async openPreview(before: string, after: string, label: string, title: string): Promise<vscode.Uri[]> {
    const id = randomBytes(8).toString('hex');
    const left = this.store(id, 'before', label, before);
    const right = this.store(id, 'after', label, after);
    await vscode.commands.executeCommand('vscode.diff', left, right, title);
    return [left, right];
  }

  /**
   * Opens a diff of `before` (in memory) against the file as it now stands on
   * disk. Used by the changed-files list, where the post-image is not a
   * projection but the real thing.
   */
  async openAgainstDisk(before: string, file: vscode.Uri, title: string): Promise<vscode.Uri[]> {
    const id = randomBytes(8).toString('hex');
    const left = this.store(id, 'before', basename(file), before);
    await vscode.commands.executeCommand('vscode.diff', left, file, title);
    return [left, file];
  }

  /** Opens an empty side for a file that no longer exists. */
  async openDeleted(before: string, label: string): Promise<vscode.Uri[]> {
    return this.openPreview(before, '', label, `${basename(label)} (deleted)`);
  }

  private store(id: string, side: string, label: string, body: string): vscode.Uri {
    const uri = vscode.Uri.from({
      scheme: PREVIEW_SCHEME,
      path: `/${id}/${side}/${sanitise(label)}`,
    });
    this.bodies.set(uri.toString(), body);
    return uri;
  }

  /**
   * Closes every review tab on the given preview URIs.
   *
   * The review exists to answer one approval. Leaving it open after the answer
   * would mean a window full of diffs of changes that were never made — and,
   * worse, a diff of a file that has since moved on.
   */
  async close(reviews: vscode.Uri[]): Promise<void> {
    if (reviews.length === 0) return;
    const owned = new Set(reviews.map((uri) => uri.toString()));
    const closing: vscode.Tab[] = [];
    for (const group of vscode.window.tabGroups.all) {
      for (const tab of group.tabs) {
        if (belongsToReview(tab, owned)) closing.push(tab);
      }
    }
    if (closing.length > 0) {
      await vscode.window.tabGroups.close(closing, true);
    }
    for (const uri of reviews) this.bodies.delete(uri.toString());
  }

  dispose(): void {
    this.registration.dispose();
    this.changed.dispose();
    this.bodies.clear();
  }
}

function belongsToReview(tab: vscode.Tab, owned: Set<string>): boolean {
  const input = tab.input;
  if (input instanceof vscode.TabInputTextDiff) {
    return owned.has(input.original.toString()) || owned.has(input.modified.toString());
  }
  if (input instanceof vscode.TabInputText) {
    return owned.has(input.uri.toString());
  }
  return false;
}

function basename(uri: vscode.Uri | string): string {
  const path = typeof uri === 'string' ? uri : uri.path;
  const parts = path.split(/[/\\]/);
  return parts[parts.length - 1] || 'preview';
}

/**
 * Keeps a label usable as a URI path segment. The host guarantees that a
 * preview path is workspace-relative, but a URI built from text is still a URI
 * built from text, and ".." in one would climb out of the document the review
 * belongs to.
 */
function sanitise(label: string): string {
  const segments = label.split(/[/\\]/).filter((segment) => segment !== '' && segment !== '..' && segment !== '.');
  return segments.length === 0 ? 'preview' : segments.join('/');
}
