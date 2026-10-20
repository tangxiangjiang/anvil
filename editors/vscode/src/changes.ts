/**
 * What a turn changed.
 *
 * The post-image of a change is just the file on disk once the turn is over.
 * The pre-image is the hard part: the only moment it exists is *before* the
 * write, and nothing keeps it afterwards. So the extension reads it there — at
 * the tool call that is about to run, and again at the approval that is about
 * to be answered — and holds it in memory, keyed by path, for the length of the
 * turn.
 *
 * Git would answer the same question, and is rejected for the same reason the
 * host keeps its own preview: it would make the feature depend on the workspace
 * being a clean checkout of a repository, which an editor has no business
 * requiring.
 *
 * The list is only as good as the arguments it watches. It covers the tools
 * that name the file they write (`write_file`, `edit_file`, and the file
 * headers of an `apply_patch`); it cannot see a `shell` command that redirects
 * into a file. Rescanning the workspace after every turn would close that gap
 * and cost far more than it is worth in a panel with no index of its own.
 */

import * as vscode from 'vscode';
import { toolCallPaths } from './mentions';
import { rootUri } from './workspaceRoot';

export interface ChangedFile {
  /** Workspace-relative, forward-slashed. */
  path: string;
  before: string;
  /** Whether the file existed when it was snapshotted. */
  existed: boolean;
  kind: 'created' | 'modified' | 'deleted';
}

interface Snapshot {
  text: string;
  existed: boolean;
}

export class ChangesTracker {
  /** Snapshots for the current turn, first touch wins. */
  private snapshots = new Map<string, Snapshot>();
  /** Paths already snapshotted this turn, so a second write keeps the original. */
  private touched = new Set<string>();

  /** Starts a new turn: last turn's pre-images are not this turn's. */
  begin(): void {
    this.snapshots = new Map();
    this.touched = new Set();
  }

  /**
   * Records the pre-image of a file, if this turn has not recorded one already.
   *
   * First touch wins because that is the state the user started from: a file
   * written twice in one turn should be described by one diff against the
   * state before the turn, not by its intermediate contents.
   */
  async snapshot(relPath: string): Promise<void> {
    if (this.touched.has(relPath)) return;
    this.touched.add(relPath);
    const uri = this.uriFor(relPath);
    if (!uri) return;
    try {
      const bytes = await vscode.workspace.fs.readFile(uri);
      this.snapshots.set(relPath, { text: decode(bytes), existed: true });
    } catch {
      // Not there yet: a create. Recorded rather than skipped, so the diff
      // shows the whole file as an addition.
      this.snapshots.set(relPath, { text: '', existed: false });
    }
  }

  /** Snapshots the targets of a tool call, when the call has any. */
  async snapshotToolCall(name: string | undefined, args: unknown): Promise<void> {
    for (const path of toolCallPaths(name, args)) {
      await this.snapshot(path);
    }
  }

  /**
   * The pre-image recorded for a path this turn, when one was taken. Undefined
   * when this turn has not snapshotted that path.
   */
  preImage(relPath: string): string | undefined {
    return this.snapshots.get(relPath)?.text;
  }

  /** The file as it is now, without recording it. A replay has no pre-image. */
  async read(relPath: string): Promise<string | undefined> {
    const uri = this.uriFor(relPath);
    if (!uri) return undefined;
    try {
      return decode(await vscode.workspace.fs.readFile(uri));
    } catch {
      return undefined;
    }
  }

  /**
   * Reads everything back after a turn and keeps what actually changed. A file
   * the model read but did not write, or wrote identically, is not a change.
   */
  async finish(): Promise<ChangedFile[]> {
    const out: ChangedFile[] = [];
    for (const [path, snapshot] of this.snapshots) {
      const uri = this.uriFor(path);
      let now: string | undefined;
      if (uri) {
        try {
          now = decode(await vscode.workspace.fs.readFile(uri));
        } catch {
          now = undefined;
        }
      }
      const kind = classify(snapshot, now);
      if (kind === undefined) continue;
      out.push({ path, before: snapshot.text, existed: snapshot.existed, kind });
    }
    out.sort((a, b) => a.path.localeCompare(b.path));
    return out;
  }

  private uriFor(relPath: string): vscode.Uri | undefined {
    const root = rootUri();
    if (!root) return undefined;
    // The path came from the model's tool arguments, so it is not trusted to
    // stay inside the folder: the SDK's sandbox governs the write, and this
    // governs which file the diff describes.
    const segments = relPath.split('/').filter((segment) => segment !== '' && segment !== '..' && segment !== '.');
    if (segments.length === 0) return undefined;
    return vscode.Uri.joinPath(root, ...segments);
  }
}

function classify(snapshot: Snapshot, now: string | undefined): ChangedFile['kind'] | undefined {
  if (now === undefined) {
    // Gone now. Only a change if it was there before — a path that never
    // existed is a failed write, not a deletion.
    return snapshot.existed ? 'deleted' : undefined;
  }
  if (!snapshot.existed) return 'created';
  return now === snapshot.text ? undefined : 'modified';
}

function decode(bytes: Uint8Array): string {
  return new TextDecoder().decode(bytes);
}
