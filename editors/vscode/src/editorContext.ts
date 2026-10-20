/**
 * What the editor knows, read at the moment a prompt is sent.
 *
 * Everything here touches VS Code, so nothing here is unit-tested; the pure
 * part it depends on (`mentions.ts`) is. The split is deliberate: this file
 * answers "what is in the editor right now", and that answer is only meaningful
 * against a real window.
 */

import * as vscode from 'vscode';
import { Diagnostic, PromptContext, Range, Selection } from './protocol';
import { relativeMention } from './mentions';
import { rootFolder } from './workspaceRoot';

/** How many diagnostics the extension will put on the wire for one prompt. */
export const MAX_DIAGNOSTICS = 100;

/** What the mention picker will look through before giving up. */
const MENTION_GLOB = '**/*';
const MENTION_EXCLUDE = '**/{node_modules,.git,.venv,dist,out,build,target}/**';
const MENTION_LIMIT = 5000;

/**
 * The active selection, or undefined when there is nothing worth sending.
 *
 * It is read when the user presses Send, not when the panel opens: the
 * selection a prompt should carry is the one that exists at that moment, and
 * one captured earlier is a stale one.
 *
 * The path is deliberately *not* required to be inside the workspace. VS Code
 * edits files outside the folder, the text is already in hand, and no file is
 * read on this account — refusing it would break an ordinary session for no
 * gain.
 */
export function currentSelection(): Selection | undefined {
  const editor = vscode.window.activeTextEditor;
  if (!editor || editor.document.uri.scheme !== 'file') return undefined;
  const selection = editor.selection;
  if (selection.isEmpty) return undefined;
  const text = editor.document.getText(selection);
  if (text.trim() === '') return undefined;
  return {
    path: editor.document.uri.fsPath,
    range: toRange(selection),
    text,
  };
}

function toRange(selection: vscode.Selection): Range {
  return {
    start_line: selection.start.line + 1,
    start_column: selection.start.character + 1,
    end_line: selection.end.line + 1,
    end_column: selection.end.character + 1,
  };
}

/**
 * The workspace diagnostics, worst first.
 *
 * These are the language server's, taken as they are: the host neither computes
 * nor filters them, and re-deriving anything here would risk disagreeing with
 * the Problems panel the user is looking at. The cap is applied *after* sorting
 * so a file full of hints cannot crowd out the one error that matters.
 */
export function currentDiagnostics(limit = MAX_DIAGNOSTICS): Diagnostic[] {
  const out: Diagnostic[] = [];
  const folder = rootFolder();
  for (const [uri, diagnostics] of vscode.languages.getDiagnostics()) {
    if (uri.scheme !== 'file') continue;
    if (folder && !isInside(folder.uri.fsPath, uri.fsPath)) continue;
    for (const diagnostic of diagnostics) {
      out.push({
        path: vscode.workspace.asRelativePath(uri, false).split('\\').join('/'),
        line: diagnostic.range.start.line + 1,
        severity: severityName(diagnostic.severity),
        message: diagnostic.message,
      });
    }
  }
  return sortBySeverity(out).slice(0, limit);
}

function severityName(severity: vscode.DiagnosticSeverity): string {
  switch (severity) {
    case vscode.DiagnosticSeverity.Error:
      return 'error';
    case vscode.DiagnosticSeverity.Warning:
      return 'warning';
    case vscode.DiagnosticSeverity.Information:
      return 'info';
    default:
      return 'hint';
  }
}

function sortBySeverity(diagnostics: Diagnostic[]): Diagnostic[] {
  const rank = (severity: string | undefined): number => {
    switch (severity) {
      case 'error':
        return 0;
      case 'warning':
        return 1;
      case 'info':
        return 2;
      default:
        return 3;
    }
  };
  return [...diagnostics].sort((a, b) => rank(a.severity) - rank(b.severity));
}

function isInside(root: string, path: string): boolean {
  const separator = process.platform === 'win32' ? '\\' : '/';
  const normalize = (value: string): string =>
    (process.platform === 'win32' ? value.toLowerCase() : value).replace(/[\\/]+$/, '');
  const base = normalize(root);
  const target = normalize(path);
  return target === base || target.startsWith(base + separator);
}

/**
 * Opens the mention picker and returns the paths the user chose, as
 * workspace-relative paths.
 *
 * The pick resolves to a path, never to contents: what gets read, how much of
 * it, and whether it is inside the workspace at all are the host's decisions,
 * and a client that pre-read files would be making them from the wrong side of
 * the pipe.
 */
export async function pickMentions(): Promise<string[]> {
  const folder = rootFolder();
  if (!folder) return [];
  const found = await vscode.workspace.findFiles(MENTION_GLOB, MENTION_EXCLUDE, MENTION_LIMIT);
  const items: Array<vscode.QuickPickItem & { relative: string }> = [];
  for (const uri of found) {
    const relative = relativeMention(folder.uri.fsPath, uri.fsPath);
    if (relative === undefined) continue;
    items.push({ label: relative, relative });
  }
  items.sort((a, b) => a.label.localeCompare(b.label));

  const picked = await vscode.window.showQuickPick(items, {
    canPickMany: true,
    title: 'Reference files or folders',
    placeHolder: 'The host reads each one into this prompt',
    matchOnDescription: true,
  });
  if (!picked) return [];
  return picked.map((item) => item.relative);
}

/**
 * Builds the context object for one prompt, or undefined when there is nothing
 * to say — so a plain question goes on the wire exactly as it did before.
 */
export function buildPromptContext(mentions: string[]): PromptContext | undefined {
  const context: PromptContext = {};
  if (mentions.length > 0) context.mentions = mentions.map((path) => ({ path }));
  const selection = currentSelection();
  if (selection) context.selection = selection;
  const diagnostics = currentDiagnostics();
  if (diagnostics.length > 0) context.diagnostics = diagnostics;
  return Object.keys(context).length === 0 ? undefined : context;
}
