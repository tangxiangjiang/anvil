/**
 * What the editor knows, read at the moment a prompt is sent.
 *
 * Everything here touches VS Code, so nothing here is unit-tested; the pure
 * part it depends on (`mentions.ts`) is. The split is deliberate: this file
 * answers "what is in the editor right now", and that answer is only meaningful
 * against a real window.
 */

import * as vscode from 'vscode';
import { Diagnostic, PromptContext, Quote, Range, Selection } from './protocol';
import { mentionSearchGlob, relativeMention } from './mentions';
import { rootFolder } from './workspaceRoot';

/** How many diagnostics the extension will put on the wire for one prompt. */
export const MAX_DIAGNOSTICS = 100;

const MENTION_EXCLUDE = '**/{node_modules,.git,.venv,dist,out,build,target}/**';
/** Empty searches are a preview; typed searches are narrow enough to return more. */
const MENTION_PREVIEW_LIMIT = 200;
const MENTION_SEARCH_LIMIT = 500;
const MENTION_SEARCH_DELAY_MS = 100;

/**
 * The last real source editor the user worked in.
 *
 * Focusing a webview does not clear its selection, but it can make
 * `activeTextEditor` undefined. Keep the editor identity, not a text snapshot,
 * so an explicit attach always reads the selection that still exists when the
 * user confirms it.
 */
let lastSourceEditor: vscode.TextEditor | undefined;

export function rememberSourceEditor(editor: vscode.TextEditor | undefined): void {
  if (editor?.document.uri.scheme === 'file') lastSourceEditor = editor;
}

/**
 * The active selection, or undefined when there is nothing worth sending.
 *
 * It is read only for an explicit Attach action. Merely selecting code never
 * adds it to a prompt.
 *
 * The path is deliberately *not* required to be inside the workspace. VS Code
 * edits files outside the folder, the text is already in hand, and no file is
 * read on this account — refusing it would break an ordinary session for no
 * gain.
 */
export function currentSelection(): Selection | undefined {
  const active = vscode.window.activeTextEditor;
  if (active?.document.uri.scheme === 'file') rememberSourceEditor(active);
  const editor = active?.document.uri.scheme === 'file' ? active : lastSourceEditor;
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

  type MentionItem = vscode.QuickPickItem & { relative: string };
  const picker = vscode.window.createQuickPick<MentionItem>();
  picker.canSelectMany = true;
  picker.matchOnDescription = true;
  picker.title = 'Reference workspace files';
  picker.placeholder = 'Type a filename or path; the host reads each selected file into this prompt';

  let generation = 0;
  let timer: NodeJS.Timeout | undefined;
  let settled = false;
  const disposables: vscode.Disposable[] = [];
  const result = new Promise<string[]>((resolve) => {
    const finish = (paths: string[]): void => {
      if (settled) return;
      settled = true;
      if (timer) clearTimeout(timer);
      for (const disposable of disposables) disposable.dispose();
      picker.dispose();
      resolve(paths);
    };
    disposables.push(
      picker.onDidAccept(() => finish(picker.selectedItems.map((item) => item.relative))),
      picker.onDidHide(() => finish([])),
    );
  });

  const update = async (query: string): Promise<void> => {
    const request = ++generation;
    picker.busy = true;
    const include = new vscode.RelativePattern(folder, mentionSearchGlob(query));
    const exclude = new vscode.RelativePattern(folder, MENTION_EXCLUDE);
    const limit = query.trim() === '' ? MENTION_PREVIEW_LIMIT : MENTION_SEARCH_LIMIT;
    const found = await vscode.workspace.findFiles(include, exclude, limit);
    if (settled || request !== generation) return;
    const items: MentionItem[] = [];
    for (const uri of found) {
      const relative = relativeMention(folder.uri.fsPath, uri.fsPath);
      if (relative !== undefined) items.push({ label: relative, relative });
    }
    items.sort((a, b) => a.label.localeCompare(b.label));
    picker.items = items;
    picker.busy = false;
  };

  disposables.push(
    picker.onDidChangeValue((query) => {
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => void update(query), MENTION_SEARCH_DELAY_MS);
    }),
  );
  picker.show();
  await update('');
  return result;
}

/**
 * Builds the context object for one prompt, or undefined when there is nothing
 * to say — so a plain question goes on the wire exactly as it did before.
 */
export function buildPromptContext(
  mentions: string[],
  quotes: Quote[] = [],
  explicitSelection?: Selection | null,
): PromptContext | undefined {
  const context: PromptContext = {};
  if (mentions.length > 0) context.mentions = mentions.map((path) => ({ path }));
  if (quotes.length > 0) context.quotes = quotes;
  const selection = explicitSelection ?? undefined;
  if (selection) context.selection = selection;
  const diagnostics = currentDiagnostics();
  if (diagnostics.length > 0) context.diagnostics = diagnostics;
  return Object.keys(context).length === 0 ? undefined : context;
}
