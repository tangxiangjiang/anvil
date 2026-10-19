/**
 * The per-hunk review of a gated edit.
 *
 * One panel per pending approval: it shows the change's hunks with a checkbox
 * each, and closes when the question is answered. It is a separate document
 * from the chat tab because the answer is a different kind of action — picking
 * what to keep, not reading a conversation — and because a dialog inside the
 * transcript would scroll away while the user is deciding.
 *
 * The panel renders data it receives, never markup: the hunk lines are file
 * content, so they are written with `textContent` by `review.js`. What this
 * file decides is the document, the message plumbing, and which answer counts.
 */

import { randomBytes } from 'node:crypto';
import { basename } from 'node:path';
import * as vscode from 'vscode';
import { DiffHunk } from './hunks';

/** One hunk as the webview draws it. The terminators are not shown. */
interface ShownHunk {
  header: string;
  lines: Array<{ kind: 'context' | 'add' | 'del'; text: string }>;
}

/** What the review is for, and what its three buttons mean. */
export interface PartialReviewRequest {
  /** The file the change is to, or the tool's name when the host named none. */
  path: string;
  hunks: DiffHunk[];
  /** The indices kept. An empty selection is a rejection, answered by the caller. */
  onAccept(accepted: number[]): void | Promise<void>;
  onReject(): void | Promise<void>;
  /** The document closed without an answer; the approval stays pending. */
  onCancel?(): void;
}

export const ReviewViewType = 'anvil.review';

export class ReviewPanels implements vscode.Disposable {
  private readonly panels = new Map<string, vscode.WebviewPanel>();

  constructor(private readonly extensionUri: vscode.Uri) {}

  /** Opens the review for one approval, or brings it back to the front. */
  open(requestId: string, request: PartialReviewRequest): void {
    const existing = this.panels.get(requestId);
    if (existing) {
      existing.reveal(existing.viewColumn, false);
      return;
    }

    const panel = vscode.window.createWebviewPanel(
      ReviewViewType,
      `${basename(request.path)} (review changes)`,
      vscode.ViewColumn.Beside,
      {
        enableScripts: true,
        // Deciding takes time and the user may click into the file they are
        // deciding about; a document that is torn down on the way would lose
        // the selection.
        retainContextWhenHidden: true,
        localResourceRoots: [vscode.Uri.joinPath(this.extensionUri, 'media')],
      },
    );
    panel.webview.html = renderHtml(panel.webview, this.extensionUri);
    this.panels.set(requestId, panel);

    panel.webview.onDidReceiveMessage((raw: unknown) => {
      void this.handle(requestId, request, panel, raw);
    });
    panel.onDidDispose(() => {
      if (this.panels.get(requestId) === panel) this.panels.delete(requestId);
    });
  }

  /** Closes one approval's review. A no-op when it was never opened. */
  close(requestId: string): void {
    const panel = this.panels.get(requestId);
    if (!panel) return;
    this.panels.delete(requestId);
    panel.dispose();
  }

  dispose(): void {
    const panels = [...this.panels.values()];
    this.panels.clear();
    for (const panel of panels) panel.dispose();
  }

  private async handle(
    requestId: string,
    request: PartialReviewRequest,
    panel: vscode.WebviewPanel,
    raw: unknown,
  ): Promise<void> {
    if (!raw || typeof raw !== 'object') return;
    const message = raw as { type?: unknown; accepted?: unknown };

    if (message.type === 'ready') {
      await panel.webview.postMessage({
        type: 'review',
        path: request.path,
        hunks: shown(request.hunks),
      });
      return;
    }

    if (message.type === 'accept') {
      const accepted = selectionOf(message.accepted, request.hunks.length);
      if (!accepted) return;
      await this.settle(requestId, panel, () => request.onAccept(accepted));
      return;
    }

    if (message.type === 'reject') {
      await this.settle(requestId, panel, () => request.onReject());
      return;
    }

    if (message.type === 'cancel') {
      this.close(requestId);
      request.onCancel?.();
    }
  }

  /**
   * Runs the answer, then closes the document.
   *
   * The close is in `finally`: whatever the answer did, the question is no
   * longer the one this document was opened for, and a panel that stayed up
   * over a settled request would offer buttons that answer nothing.
   */
  private async settle(
    requestId: string,
    panel: vscode.WebviewPanel,
    answer: () => void | Promise<void>,
  ): Promise<void> {
    try {
      await answer();
    } finally {
      if (this.panels.get(requestId) === panel) this.close(requestId);
    }
  }
}

/**
 * The hunk indices the document selected.
 *
 * Anything that is not a list of in-range integers is not an answer — this
 * message comes from a document, and the indices it names are applied to a
 * file. Duplicates collapse; the order the boxes were written in is kept.
 */
function selectionOf(raw: unknown, count: number): number[] | undefined {
  if (!Array.isArray(raw)) return undefined;
  const seen = new Set<number>();
  for (const value of raw) {
    if (typeof value !== 'number' || !Number.isInteger(value) || value < 0 || value >= count) {
      return undefined;
    }
    seen.add(value);
  }
  return [...seen];
}

function shown(hunks: readonly DiffHunk[]): ShownHunk[] {
  return hunks.map((hunk) => ({
    header: hunk.header,
    lines: hunk.lines.map((line) => ({ kind: line.kind, text: line.text })),
  }));
}

function makeNonce(): string {
  return randomBytes(16).toString('base64');
}

function renderHtml(webview: vscode.Webview, extensionUri: vscode.Uri): string {
  const nonce = makeNonce();
  const asset = (name: string): vscode.Uri =>
    webview.asWebviewUri(vscode.Uri.joinPath(extensionUri, 'media', name));
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src ${webview.cspSource}; img-src data: ${webview.cspSource}; script-src 'nonce-${nonce}';">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<link rel="stylesheet" href="${asset('review.css')}">
<title>Review changes</title>
</head>
<body>
<main id="root"></main>
<script nonce="${nonce}" src="${asset('review.js')}"></script>
</body>
</html>`;
}
