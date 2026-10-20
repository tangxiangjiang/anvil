/**
 * The MCP manager: one document that lists the servers and edits the two
 * `mcp.json` files.
 *
 * It is a separate document for the reason the permissions manager is: "which
 * servers does this workspace have, and is one of them down" is a question about
 * the workspace, not about one conversation, and a form inside a transcript
 * would scroll away while it is being filled in. The chat card stays the
 * per-conversation summary; this is the window's one place to change anything.
 *
 * The document decides nothing about MCP. It shape-checks a form into a draft
 * (`draftFromForm` in `mcp.ts`) and renders the listing that came back from the
 * host after each write, so a normalised transport or a surviving unknown field
 * is shown the way the next session will read it. Nothing here writes a file,
 * nothing here keeps tokens: a sign-in opens the URL the host answered with, and
 * the completion arrives as an `mcp.status` notification like every other state.
 *
 * One document per window. Reopening reveals it rather than starting a second
 * copy, because two forms over one file would each save the other's work away.
 */

import { randomBytes } from 'node:crypto';
import * as vscode from 'vscode';
import { HostManager } from './hostManager';
import {
  draftFromForm,
  formValues,
  hasRuntime,
  managerServer,
  McpSettings,
  missingMcpSources,
} from './mcp';
import {
  McpAddParams,
  McpAuthenticateParams,
  McpAuthenticateResult,
  McpListResult,
  McpReconnectParams,
  McpRemoveParams,
  McpScope,
  McpSetEnabledParams,
  McpSignOutParams,
  McpSourceInfo,
  McpStatusParams,
  McpUpdateParams,
} from './protocol';

export const McpViewType = 'anvil.mcp';

/**
 * The layers the manager lists.
 *
 * Both, always — unlike a session, which reads the layers its settings enable.
 * The manager is where those files are edited, and the layer the settings
 * exclude is exactly the one a person opens it to look at, sign in to, or add a
 * first server to. `scope` decides which file a write lands in; the settings
 * only decide what a session loads, which the document says in a banner.
 */
const LAYERS: McpScope[] = ['project', 'user'];

/** The canonical form of that set, which is what a status names. */
const LAYER_SET = [...LAYERS].sort().join(',');

/** How long a burst of status notifications is coalesced before re-listing. */
const ROWS_DELAY_MS = 150;

/** What the manager needs from the window it belongs to. */
export interface McpPanelHost {
  /** The listing, with each connected server's tool catalogue. */
  mcpServers(settingSources: string[], includeTools?: boolean): Promise<McpListResult>;
  addMcpServer(params: McpAddParams): Promise<McpListResult>;
  updateMcpServer(params: McpUpdateParams): Promise<McpListResult>;
  removeMcpServer(params: McpRemoveParams): Promise<McpListResult>;
  setMcpServerEnabled(params: McpSetEnabledParams): Promise<McpListResult>;
  reconnectMcpServer(params: McpReconnectParams): Promise<McpListResult>;
  authenticateMcpServer(params: McpAuthenticateParams): Promise<McpAuthenticateResult>;
  signOutMcpServer(params: McpSignOutParams): Promise<McpListResult>;
  /** Fires when a server changes state, from any window-level runtime. */
  onMcpStatus(listener: (params: McpStatusParams) => void): vscode.Disposable;
  /** Opens one layer's file, creating it when this workspace has none. */
  openMcpFile(scope: McpScope): Promise<void>;
  /** Which layers sessions read, for the banner about one that is off. */
  loadMcpSettings(): McpSettings;
}

export class McpPanels implements vscode.Disposable {
  private panel: vscode.WebviewPanel | undefined;

  /**
   * The layers the last listing described. The form's scope target is read from
   * it, so a "which file does this write" answer needs no second round trip.
   */
  private lastSources: McpSourceInfo[] = [];

  /** A pending re-list, so a burst of transitions costs one listing. */
  private rowsTimer: ReturnType<typeof setTimeout> | undefined;

  /** Every runtime's transitions arrive here; only this document's set is used. */
  private readonly subscription: vscode.Disposable;

  constructor(
    private readonly extensionUri: vscode.Uri,
    private readonly host: McpPanelHost,
  ) {
    this.subscription = host.onMcpStatus((params) => this.noteStatus(params));
  }

  /** Opens the manager, or brings the one that is open to the front. */
  open(): void {
    if (this.panel) {
      this.panel.reveal();
      return;
    }
    const panel = vscode.window.createWebviewPanel(McpViewType, 'MCP servers', vscode.ViewColumn.Active, {
      enableScripts: true,
      // Reopening re-reads; nothing in a half-filled form is worth the memory of
      // a hidden document, and a form over a file someone else changed is worse.
      localResourceRoots: [vscode.Uri.joinPath(this.extensionUri, 'media')],
    });
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
    this.subscription.dispose();
    if (this.rowsTimer) clearTimeout(this.rowsTimer);
    this.rowsTimer = undefined;
    this.panel?.dispose();
    this.panel = undefined;
  }

  private async handle(raw: unknown): Promise<void> {
    if (!raw || typeof raw !== 'object') return;
    const message = raw as Record<string, unknown>;

    // The gate is here as well as in the manager: this handler is a second entry
    // point into the host, and an untrusted workspace must not get one through it
    // just because the document asked.
    try {
      HostManager.assertTrusted();
    } catch (err) {
      await this.post({ type: 'error', message: (err as Error).message });
      return;
    }

    switch (message.type) {
      case 'ready':
      case 'reload':
        await this.sendState();
        return;
      case 'add':
        await this.add(message);
        return;
      case 'update':
        await this.update(message);
        return;
      case 'remove':
        await this.remove(message);
        return;
      case 'setEnabled':
        await this.setEnabled(message);
        return;
      case 'reconnect':
        await this.reconnect(message);
        return;
      case 'authenticate':
        await this.authenticate(message);
        return;
      case 'signOut':
        await this.signOut(message);
        return;
      case 'openFile':
        await this.host.openMcpFile(scopeOf(message.scope) ?? 'project');
        return;
      case 'scopeChanged':
        await this.sendTarget(scopeOf(message.scope));
        return;
      default:
        return;
    }
  }

  /**
   * A server changed state.
   *
   * The listing is re-read rather than patched from the notification: a status
   * carries no tool catalogue, so a server that just connected would otherwise
   * show its old (empty) list. Re-reading also cannot disagree with the host. The
   * refresh repaints the rows only — a half-typed form is not an input to it —
   * and a status naming another layer set is dropped: the chat card's runtime is
   * not this document's.
   */
  private noteStatus(params: McpStatusParams): void {
    const named = [...(params.setting_sources ?? [])].sort().join(',');
    if (named !== '' && named !== LAYER_SET) return;
    if (!this.panel) return;
    this.scheduleRows();
  }

  private scheduleRows(): void {
    if (this.rowsTimer) return;
    this.rowsTimer = setTimeout(() => {
      this.rowsTimer = undefined;
      void this.sendRows();
    }, ROWS_DELAY_MS);
  }

  /** Reads the listing and posts it in the envelope the document asked for. */
  private async sendState(type: 'state' | 'saved' = 'state', note = ''): Promise<void> {
    let listing: McpListResult;
    try {
      listing = await this.host.mcpServers(LAYERS, true);
    } catch (err) {
      // The host could not be asked. The document says so, and Open mcp.json
      // stays available: a person in an untrusted workspace still has an editor.
      await this.post({
        type,
        runtime: false,
        servers: [],
        sources: [],
        notes: [],
        settings: this.host.loadMcpSettings(),
        error: (err as Error).message,
        note,
      });
      return;
    }
    await this.post({ type, ...this.listingMessage(listing), note });
  }

  /** The rows only, for a state change the form did not cause. */
  private async sendRows(): Promise<void> {
    if (!this.panel) return;
    let listing: McpListResult;
    try {
      listing = await this.host.mcpServers(LAYERS, true);
    } catch (err) {
      // The rows on screen still describe the last good listing; the sentence
      // beside them says they may be stale.
      await this.post({ type: 'error', message: (err as Error).message });
      return;
    }
    await this.post({ type: 'rows', ...this.listingMessage(listing) });
  }

  /**
   * The listing as the document draws it.
   *
   * The problem is one string — path and parser message — because that is what
   * the banner says; `problemPath` is there so Open mcp.json can go to the file
   * that would not parse rather than to the project file.
   */
  private listingMessage(listing: McpListResult): Record<string, unknown> {
    this.lastSources = listing.sources ?? [];
    return {
      runtime: hasRuntime(listing),
      servers: (listing.servers ?? []).map((server) => managerServer(server, this.lastSources)),
      sources: this.lastSources,
      notes: missingMcpSources(this.lastSources),
      problem: listing.problem ? `${listing.problem.path}: ${listing.problem.message}` : undefined,
      problemPath: listing.problem?.path,
      settings: this.host.loadMcpSettings(),
    };
  }

  /**
   * Runs one write and renders the listing it answered with.
   *
   * The answer to a write is the host's own reading of the file, so the document
   * shows what a new session will see rather than the edit it sent. A refusal
   * never re-renders: the rows and the form are the user's, and a rejected save
   * is not a reason to throw them away.
   */
  private async write(what: string, call: () => Promise<McpListResult>, note: string): Promise<void> {
    await this.post({ type: 'saving', saving: true, what });
    try {
      const listing = await call();
      await this.post({ type: 'saved', ...this.listingMessage(listing), note });
    } catch (err) {
      await this.post({ type: 'saving', saving: false, what: '' });
      await this.post({ type: 'error', message: (err as Error).message });
    }
  }

  private async add(message: Record<string, unknown>): Promise<void> {
    const scope = scopeOf(message.scope);
    const form = formValues(message.form);
    if (!scope || !form) {
      await this.reject('The form did not say which file to write.');
      return;
    }
    const draft = draftFromForm(form);
    if ('error' in draft) {
      await this.reject(draft.error);
      return;
    }
    const server = draft.draft;
    await this.write(
      'Adding…',
      () => this.host.addMcpServer({ scope, server, setting_sources: LAYERS, include_tools: true }),
      `Added ${server.name}. The host is connecting it.`,
    );
  }

  private async update(message: Record<string, unknown>): Promise<void> {
    const scope = scopeOf(message.scope);
    const form = formValues(message.form);
    if (!scope || !form) {
      await this.reject('The form did not say which file to write.');
      return;
    }
    const draft = draftFromForm(form);
    if ('error' in draft) {
      await this.reject(draft.error);
      return;
    }
    const server = draft.draft;
    await this.write(
      'Saving…',
      () => this.host.updateMcpServer({ scope, server, setting_sources: LAYERS, include_tools: true }),
      `Saved ${server.name}.`,
    );
  }

  private async remove(message: Record<string, unknown>): Promise<void> {
    const scope = scopeOf(message.scope);
    const name = nameOf(message);
    if (!scope || name === '') {
      await this.reject('The document did not say which server to remove.');
      return;
    }
    // The document confirms before it sends this, because the host does not ask
    // twice and the entry may be the only record of a server's configuration.
    await this.write(
      'Removing…',
      () => this.host.removeMcpServer({ scope, name, setting_sources: LAYERS, include_tools: true }),
      `Removed ${name}.`,
    );
  }

  private async setEnabled(message: Record<string, unknown>): Promise<void> {
    const scope = scopeOf(message.scope);
    const name = nameOf(message);
    const enabled = message.enabled;
    if (!scope || name === '' || typeof enabled !== 'boolean') {
      await this.reject('The document did not say which server to change.');
      return;
    }
    await this.write(
      enabled ? 'Enabling…' : 'Disabling…',
      () => this.host.setMcpServerEnabled({ scope, name, enabled, setting_sources: LAYERS, include_tools: true }),
      enabled ? `Enabled ${name}.` : `Disabled ${name}. Sessions pick this up on their next turn.`,
    );
  }

  private async reconnect(message: Record<string, unknown>): Promise<void> {
    const name = nameOf(message);
    if (name === '') {
      await this.reject('The document did not say which server to reconnect.');
      return;
    }
    // A failed restart is a state, not a refusal: the host answers with the
    // listing, and the row keeps its error.
    await this.write(
      'Reconnecting…',
      () => this.host.reconnectMcpServer({ name, setting_sources: LAYERS, include_tools: true }),
      `Reconnected ${name}.`,
    );
  }

  private async signOut(message: Record<string, unknown>): Promise<void> {
    const name = nameOf(message);
    if (name === '') {
      await this.reject('The document did not say which server to sign out of.');
      return;
    }
    await this.write(
      'Signing out…',
      () => this.host.signOutMcpServer({ name, setting_sources: LAYERS, include_tools: true }),
      `Signed out of ${name}.`,
    );
  }

  /**
   * Starts a sign-in and opens it in the browser.
   *
   * The host owns the flow: it discovers the authorization server, registers a
   * client, opens the loopback listener and stores the token. What is left here
   * is handing the URL to the operating system — and the completion arrives as
   * an `mcp.status`, because the browser round trip is the user's and its length
   * is not something to hold a request open for. The URL is never logged: it
   * carries the flow's `state`, and the rule for this feature is that nothing
   * about a sign-in leaves the machine except the request the browser makes.
   */
  private async authenticate(message: Record<string, unknown>): Promise<void> {
    const name = nameOf(message);
    if (name === '') {
      await this.reject('The document did not say which server to sign in to.');
      return;
    }
    await this.post({ type: 'saving', saving: true, what: 'Starting sign-in…' });
    let started: McpAuthenticateResult;
    try {
      started = await this.host.authenticateMcpServer({ name, setting_sources: LAYERS });
    } catch (err) {
      await this.post({ type: 'saving', saving: false, what: '' });
      await this.post({ type: 'error', message: `Could not start a sign-in: ${(err as Error).message}` });
      return;
    }
    await this.post({ type: 'saving', saving: false, what: '' });

    const uri = authorizationUri(started.auth_url);
    if (!uri) {
      await this.post({
        type: 'error',
        message: 'The host answered with a sign-in address this editor will not open.',
      });
      return;
    }
    let opened = false;
    try {
      opened = await vscode.env.openExternal(uri);
    } catch {
      opened = false;
    }
    if (!opened) {
      await this.post({
        type: 'error',
        message: 'The editor could not open a browser. The sign-in has to finish in one; try again.',
      });
      return;
    }
    await this.post({ type: 'auth', server: name, state: 'waiting' });
  }

  /** Tells the form which file its scope writes, and whether sessions read it. */
  private async sendTarget(scope: McpScope | undefined): Promise<void> {
    if (!scope) return;
    const layer = this.lastSources.find((source) => source.source === scope);
    const settings = this.host.loadMcpSettings();
    await this.post({
      type: 'target',
      scope,
      path: layer?.path ?? '',
      exists: layer?.exists === true,
      read: settings[scope] !== false,
    });
  }

  /** A message that never reached the host: said in place, nothing re-rendered. */
  private async reject(message: string): Promise<void> {
    await this.post({ type: 'error', message });
  }

  private async post(message: Record<string, unknown>): Promise<void> {
    if (!this.panel) return;
    await this.panel.webview.postMessage(message);
  }
}

/** A layer word from a document, or undefined when it is not one. */
function scopeOf(raw: unknown): McpScope | undefined {
  return raw === 'project' || raw === 'user' ? raw : undefined;
}

/** A server name from a document, trimmed; '' when there is none. */
function nameOf(message: Record<string, unknown>): string {
  return typeof message.name === 'string' ? message.name.trim() : '';
}

/**
 * The sign-in URL as a URI, or undefined when it is not one this editor will
 * hand to the operating system. Only http(s) is opened: an authorization server
 * answers with a web address, and anything else is not one.
 */
function authorizationUri(raw: unknown): vscode.Uri | undefined {
  if (typeof raw !== 'string' || raw === '') return undefined;
  let uri: vscode.Uri;
  try {
    uri = vscode.Uri.parse(raw, true);
  } catch {
    return undefined;
  }
  return uri.scheme === 'https' || uri.scheme === 'http' ? uri : undefined;
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
<link rel="stylesheet" href="${asset('mcp.css')}">
<title>MCP servers</title>
</head>
<body>
<main id="root"></main>
<script nonce="${nonce}" src="${asset('mcp.js')}"></script>
</body>
</html>`;
}
