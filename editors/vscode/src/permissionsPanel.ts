/**
 * The permissions manager: one document that edits the workspace rule file.
 *
 * It is a separate document from the chat tab for the same reason the change
 * review is: "which calls need a card" is workspace policy, not one conversation,
 * and a form inside a transcript would scroll away while it is being filled in.
 * Hand editing stays one click away — the JSON is where a rule's unfamiliar
 * corners still live — but a row's effect, tool, pattern and reason are four
 * fields, and nobody should have to remember the JSON for those four.
 *
 * The editor is deliberately dumb about what a rule means. The host compiles the
 * list with the same parser a session uses and writes it atomically, so a row
 * this document accepts is one the next session opens under; the document's job
 * is shape (a tool, an effect of allow/ask/deny) and rendering what came back.
 * Nothing here writes the file itself, and nothing here echoes the list it sent:
 * the answer to a save is `permissions.get`'s reading of the file, so a change
 * the host normalised is shown the way a session will see it.
 *
 * One document per window. Reopening reveals it rather than starting a second
 * copy, because two forms over one file would each save the other's work away.
 */

import { randomBytes } from 'node:crypto';
import * as vscode from 'vscode';
import {
  PermissionsResult,
  PermissionsSetParams,
  PermissionMode,
  PermissionModes,
  RuleInfo,
  RuleSource,
  RuleSpec,
} from './protocol';

export const PermissionsViewType = 'anvil.permissions';

/** What the manager needs from the window it belongs to. */
export interface PermissionsPanelHost {
  /** Both rule files as the host reads them, merged in the order they decide. */
  permissionRules(): Promise<PermissionsResult>;
  /** Replaces one layer with the list the document showed. */
  savePermissionRules(params: PermissionsSetParams): Promise<PermissionsResult>;
  /** Opens one layer's JSON itself, creating it when that layer has none. */
  openPermissionRulesFile(source?: RuleSource): Promise<void>;
  /** False when `anvil.permissions` is off, which makes every rule decide nothing. */
  loadPermissionsEnabled(): boolean;
}

export class PermissionsPanels implements vscode.Disposable {
  private panel: vscode.WebviewPanel | undefined;

  constructor(
    private readonly extensionUri: vscode.Uri,
    private readonly host: PermissionsPanelHost,
  ) {}

  /** Opens the manager, or brings the one that is open to the front. */
  open(): void {
    if (this.panel) {
      this.panel.reveal();
      return;
    }
    const panel = vscode.window.createWebviewPanel(
      PermissionsViewType,
      'Permissions',
      vscode.ViewColumn.Active,
      {
        enableScripts: true,
        // Reopening re-reads; nothing in a half-filled form is worth the memory
        // of a hidden document, and a stale form over a changed file is worse.
        localResourceRoots: [vscode.Uri.joinPath(this.extensionUri, 'media')],
      },
    );
    this.panel = panel;
    panel.webview.html = renderHtml(panel.webview, this.extensionUri);
    panel.webview.onDidReceiveMessage((raw: unknown) => {
      void this.handle(raw);
    });
    panel.onDidDispose(() => {
      if (this.panel === panel) this.panel = undefined;
    });
  }

  dispose(): void {
    this.panel?.dispose();
    this.panel = undefined;
  }

  private async handle(raw: unknown): Promise<void> {
    if (!raw || typeof raw !== 'object') return;
    const message = raw as Record<string, unknown>;

    switch (message.type) {
      case 'ready':
      case 'reload':
        await this.sendState('state');
        return;
      case 'save':
        await this.save(message);
        return;
      case 'openFile':
        // The document names the layer whose JSON it is asking for; an older
        // document that names none wants the workspace file, as it always did.
        await this.host.openPermissionRulesFile(
          message.source === 'user' ? 'user' : 'workspace',
        );
        return;
    }
  }

  /** Reads both files and posts them, in the envelope the document asked for. */
  private async sendState(type: 'state' | 'saved'): Promise<void> {
    let state: PermissionsResult;
    try {
      state = await this.host.permissionRules();
    } catch (err) {
      // The host could not be asked. The document says so, and Open JSON stays
      // available: a person in an untrusted workspace still has an editor.
      await this.post({
        type,
        path: '',
        exists: false,
        defaultMode: '',
        userPath: '',
        userExists: false,
        protected: [],
        userRules: [],
        workspaceRules: [],
        modes: declarableModes(),
        loadPermissions: this.host.loadPermissionsEnabled(),
        error: (err as Error).message,
      });
      return;
    }
    await this.post(this.stateMessage(type, state));
  }

  /**
   * The document's state message.
   *
   * One `rules` array arrives in merge order with each rule's source, because
   * that is how the host reads the two files together; the document draws two
   * sections, so the array is split here where the grouping decision can be
   * commented on. A rule with no source is a workspace rule: that is the only
   * layer a host without scopes has.
   */
  private stateMessage(type: 'state' | 'saved', state: PermissionsResult): Record<string, unknown> {
    return {
      type,
      path: state.path,
      exists: state.exists,
      // A declaration the host will not honour is not a value the select can
      // hold, so it travels in words beside the empty selection — the same split
      // `permissions.get` makes.
      defaultMode: state.default_mode ?? '',
      defaultModeRefused: state.default_mode_refused,
      userPath: state.user_path ?? '',
      userExists: state.user_exists === true,
      userProblem: state.user_problem,
      protected: state.protected ?? [],
      userRules: rowsFor(state.rules, 'user'),
      workspaceRules: rowsFor(state.rules, 'workspace'),
      modes: declarableModes(),
      problem: state.problem,
      loadPermissions: this.host.loadPermissionsEnabled(),
    };
  }

  /**
   * Saves the list the document sent.
   *
   * A message from a document is shape-checked before it becomes a file: the
   * fields are copied one by one, and anything that is not the shape a rule has
   * is an error shown in place. A failed save never closes the document — the
   * rows are the user's edit, and throwing them away because the host refused
   * one of them would be the one destructive thing this panel could do.
   */
  private async save(message: Record<string, unknown>): Promise<void> {
    const source: RuleSource = message.source === 'user' ? 'user' : 'workspace';
    const rules = rulesOf(message.rules);
    if (!rules) {
      await this.post({
        type: 'error',
        message: 'A rule needs a tool name and an effect of allow, ask, or deny.',
      });
      return;
    }

    // The two files have different shapes, and this is the client half of that
    // rule: a workspace save sends its starting mode and no protected globs, a
    // user save the reverse. The host refuses the wrong field either way, so a
    // message that smuggled one is an error rather than a silent drop.
    let params: PermissionsSetParams;
    if (source === 'user') {
      const protectedPaths = stringsOf(message.protected);
      if (!protectedPaths) {
        await this.post({ type: 'error', message: 'Protected paths must be a list of strings.' });
        return;
      }
      params = {
        source: 'user',
        rules,
        protected: protectedPaths,
        // Only the document's "Create user file" step asks for this; ordinary
        // Save on a file that exists never has to.
        create: message.create === true,
      };
    } else {
      const declared = message.defaultMode;
      if (declared !== undefined && typeof declared !== 'string') {
        await this.post({ type: 'error', message: 'The default mode must be a mode word or empty.' });
        return;
      }
      const mode = declared ?? '';
      params = {
        source: 'workspace',
        rules,
        default_mode: mode === '' ? undefined : (mode as PermissionMode),
      };
    }

    await this.post({ type: 'saving', saving: true, source });
    try {
      const result = await this.host.savePermissionRules(params);
      // The reply to a save is a full reading, so it is rendered like one.
      await this.post(this.stateMessage('saved', result));
    } catch (err) {
      await this.post({ type: 'error', message: (err as Error).message });
    }
  }

  private async post(message: Record<string, unknown>): Promise<void> {
    if (!this.panel) return;
    await this.panel.webview.postMessage(message);
  }
}

/** The three effects; anything else a file might hold reads as its default. */
function effectOf(effect: string | undefined): 'allow' | 'ask' | 'deny' {
  return effect === 'ask' || effect === 'deny' ? effect : 'allow';
}

/**
 * The document's row for one rule: the four fields a form edits, and nothing a
 * form cannot change. The rule's index and describe stay on the wire — the
 * manager draws the form, and a citation belongs to the decision that quotes
 * it.
 */
function rowOf(rule: RuleInfo): RuleSpec {
  return {
    effect: effectOf(rule.effect),
    tool: rule.tool,
    pattern: rule.pattern ?? '',
    reason: rule.reason ?? '',
  };
}

/**
 * One layer's rows. A rule that names no source is the workspace's: that is the
 * only layer a host that predates scopes reports.
 */
function rowsFor(rules: RuleInfo[], source: RuleSource): RuleSpec[] {
  return rules
    .filter((rule) => (source === 'user' ? rule.source === 'user' : rule.source !== 'user'))
    .map(rowOf);
}

/**
 * The protected globs a save message names, or undefined when it is not a list
 * of strings. Blanks are dropped: an empty glob protects nothing, and writing
 * one to the file would read as if it did something.
 */
function stringsOf(raw: unknown): string[] | undefined {
  if (raw === undefined || raw === null) return [];
  if (!Array.isArray(raw)) return undefined;
  const out: string[] = [];
  for (const item of raw) {
    if (typeof item !== 'string') return undefined;
    const trimmed = item.trim();
    if (trimmed !== '') out.push(trimmed);
  }
  return out;
}

/**
 * The rows a save message names, or undefined when the message is not rows.
 *
 * Every field is read one at a time rather than spreading the object, so a
 * message from a document cannot smuggle a field the host does not expect. The
 * strings are trimmed because a trailing space in a tool name is never what a
 * person meant; the host checks them again, and the parser is the authority.
 */
function rulesOf(raw: unknown): RuleSpec[] | undefined {
  if (!Array.isArray(raw)) return undefined;
  const rules: RuleSpec[] = [];
  for (const item of raw) {
    if (!item || typeof item !== 'object') return undefined;
    const row = item as Record<string, unknown>;
    if (typeof row.tool !== 'string') return undefined;
    if (row.effect !== 'allow' && row.effect !== 'ask' && row.effect !== 'deny') return undefined;
    for (const field of ['pattern', 'reason'] as const) {
      const value = row[field];
      if (value !== undefined && typeof value !== 'string') return undefined;
    }
    rules.push({
      effect: row.effect,
      tool: row.tool.trim(),
      pattern: (row.pattern as string | undefined)?.trim() ?? '',
      reason: (row.reason as string | undefined)?.trim() ?? '',
    });
  }
  return rules;
}

/** The words the mode select offers, without the one a project file may not grant. */
export function declarableModes(): PermissionMode[] {
  return PermissionModes.filter((mode) => mode !== 'bypass');
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
<link rel="stylesheet" href="${asset('tokens.css')}">
<link rel="stylesheet" href="${asset('panel.css')}">
<link rel="stylesheet" href="${asset('permissions.css')}">
<title>Permissions</title>
</head>
<body>
<main id="root"></main>
<script nonce="${nonce}" src="${asset('permissions.js')}"></script>
</body>
</html>`;
}
