/**
 * A media file's webview URL, fingerprinted by its contents.
 *
 * `webview.asWebviewUri` builds the same URL for the same path whatever the
 * file now holds, so a reinstall that changes `chat.js` without changing the
 * version leaves the webview fetching the copy it already cached — which is
 * exactly how a fix can ship, install, and appear to do nothing. A digest in
 * the query string makes the URL change whenever the bytes do, so the cache
 * can only ever serve the file that is actually on disk.
 *
 * All five documents use this one builder, so a panel cannot quietly keep the
 * stale-cache behaviour the next one fixed.
 */

import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import * as vscode from 'vscode';

/** One builder per webview, resolving names under the extension's `media/`. */
export function mediaAsset(
  webview: vscode.Webview,
  extensionUri: vscode.Uri,
): (name: string) => vscode.Uri {
  return (name: string): vscode.Uri => {
    const file = vscode.Uri.joinPath(extensionUri, 'media', name);
    const uri = webview.asWebviewUri(file);
    let digest = '';
    try {
      digest = createHash('sha256').update(readFileSync(file.fsPath)).digest('hex').slice(0, 12);
    } catch {
      // A missing asset is a packaging bug, not a reason to fail the panel.
      return uri;
    }
    return uri.with({ query: `v=${digest}` });
  };
}
