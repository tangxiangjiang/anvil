/**
 * One root per window.
 *
 * A conversation, the host child behind it, its checkpoints and every path it
 * reports belong to one folder. VS Code calls a window's folders a workspace,
 * and a multi-root window has several; the rule of this client is that one of
 * them is *this window's* root — chosen once, named in the header, and
 * unchanged for as long as the window lives.
 *
 * This module is the only place that decides which folder that is. Every call
 * site asks it instead of reading `workspace.workspaceFolders` itself: a first
 * folder taken silently is a conversation bound to a folder nobody chose, which
 * in a multi-root window is the wrong answer half the time and looks like a
 * bug the rest of the time.
 */

import * as vscode from 'vscode';

/** Where the remembered choice lives. `workspaceState` is per workspace file. */
const STATE_KEY = 'anvil.workspaceRoot';

/** What the header draws: the folder's name, its path, and whether there was a choice. */
export interface RootInfo {
  name: string;
  /** The absolute path, which is also the host child's cwd. */
  path: string;
  /** Whether the window has more than one folder, so the line is not the obvious one. */
  multi: boolean;
}

/**
 * Raised when a person dismisses the root picker.
 *
 * It is not a failure, and no caller should report it as one: the picker is a
 * question, "no" means no session was created, and the composer is exactly
 * where it was. `instanceof` is how the panel tells it from the errors it does
 * show.
 */
export class RootChoiceCancelled extends Error {
  constructor() {
    super('no folder was chosen for this window');
    this.name = 'RootChoiceCancelled';
  }
}

let active: vscode.WorkspaceFolder | undefined;
let choosing: Promise<vscode.WorkspaceFolder | undefined> | undefined;

function folders(): readonly vscode.WorkspaceFolder[] {
  return vscode.workspace.workspaceFolders ?? [];
}

/** True when the window has more than one folder, which is the one case with a choice. */
export function isMultiRoot(): boolean {
  return folders().length > 1;
}

/**
 * The window's root, without asking.
 *
 * A single-folder window needs no decision, so it is that folder. A multi-root
 * window answers only once something has chosen — a pick made in this window,
 * or the remembered choice read at activation. Undefined before then, which is
 * the honest answer rather than some other folder's.
 */
export function rootFolder(): vscode.WorkspaceFolder | undefined {
  const all = folders();
  if (all.length === 1) return all[0];
  const chosen = active;
  if (!chosen) return undefined;
  const still = all.find((folder) => folder.uri.toString() === chosen.uri.toString());
  if (!still) {
    // The folder was taken out of the workspace while the window was open. The
    // thing that was chosen no longer exists, so the next conversation asks
    // again rather than carrying a path that is not in the window.
    active = undefined;
    return undefined;
  }
  return still;
}

/** The window's root as a URI, or undefined when a multi-root window has not chosen. */
export function rootUri(): vscode.Uri | undefined {
  return rootFolder()?.uri;
}

/** The header's facts, or undefined when there is nothing to name yet. */
export function rootInfo(): RootInfo | undefined {
  const folder = rootFolder();
  if (!folder) return undefined;
  return { name: folder.name, path: folder.uri.fsPath, multi: isMultiRoot() };
}

/**
 * Reads the remembered choice and installs the folder-change listener.
 *
 * Called once at activation. Reading `workspaceState` needs the extension's
 * context and no call site has it, so the remembered folder becomes `active`
 * here; every later question is then a synchronous lookup. A remembered folder
 * that is not in this window is a stale entry from a workspace whose folders
 * were edited, and is ignored — the picker asks instead of guessing.
 */
export function initWorkspaceRoot(context: vscode.ExtensionContext): void {
  active = rememberedFolder(context);
  context.subscriptions.push(
    vscode.workspace.onDidChangeWorkspaceFolders(() => {
      const chosen = active;
      if (chosen && !folders().some((folder) => folder.uri.toString() === chosen.uri.toString())) {
        active = undefined;
      }
    }),
  );
}

function rememberedFolder(context: vscode.ExtensionContext): vscode.WorkspaceFolder | undefined {
  const value = context.workspaceState.get<string>(STATE_KEY);
  if (typeof value !== 'string' || value === '') return undefined;
  return folders().find((folder) => folder.uri.toString() === value);
}

/**
 * The window's root, asking in a multi-root window that has not chosen one.
 *
 * Undefined means no folder is open at all. `RootChoiceCancelled` means the
 * picker was dismissed: the caller must create nothing and stay quiet. A pick is
 * remembered for the next window on this workspace, which is also what the
 * header shows when it reopens — the question is asked once per workspace, not
 * once per conversation.
 */
export function resolveWorkspaceRoot(
  context: vscode.ExtensionContext,
): Promise<vscode.WorkspaceFolder | undefined> {
  const all = folders();
  if (all.length === 0) return Promise.resolve(undefined);
  if (all.length === 1) {
    active = all[0];
    return Promise.resolve(all[0]);
  }
  const known = rootFolder();
  if (known) return Promise.resolve(known);
  // Two prompts at once would be two answers to one question, and the second
  // would overwrite the first. The first caller's picker is the one that counts.
  if (!choosing) {
    choosing = ask(context, all).finally(() => {
      choosing = undefined;
    });
  }
  return choosing;
}

async function ask(
  context: vscode.ExtensionContext,
  all: readonly vscode.WorkspaceFolder[],
): Promise<vscode.WorkspaceFolder> {
  const remembered = rememberedFolder(context);
  const items = all.map((folder) => ({
    label: folder.name,
    description: folder.uri.fsPath,
    folder,
    picked: remembered !== undefined && folder.uri.toString() === remembered.uri.toString(),
  }));
  const choice = await vscode.window.showQuickPick(items, {
    title: 'Choose the root of this window',
    placeHolder: 'The conversation, its checkpoints and the host process all live in one folder',
    ignoreFocusOut: true,
  });
  if (!choice) throw new RootChoiceCancelled();
  active = choice.folder;
  await context.workspaceState.update(STATE_KEY, active.uri.toString());
  return active;
}
