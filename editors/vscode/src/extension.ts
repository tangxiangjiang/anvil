/**
 * Extension entry point: register the tabs, the session tree, the commands, and
 * the teardown.
 *
 * Deliberately thin. The interesting behaviour lives in `hub.ts` (the window's
 * shared host and the set of open tabs), `hostManager.ts` (lifecycle),
 * `session.ts` (conversation state), `sessionList.ts` (what the sidebar shows),
 * and `chatPanel.ts` (one tab).
 */

import * as vscode from 'vscode';
import { ChatPanel } from './chatPanel';
import { Hub } from './hub';
import { PreviewDocuments } from './review';
import { SessionTreeProvider } from './sessionTree';
import { clearApiKey, promptForApiKey } from './settings';

/** The window's shared half, so `deactivate` can kill the child. */
let active: Hub | undefined;

export function activate(context: vscode.ExtensionContext): void {
  const output = vscode.window.createOutputChannel('Anvil');
  context.subscriptions.push(output);
  const log = (line: string): void => output.appendLine(line);

  // One content provider for the window, for the life of the extension: it is
  // what makes a diff view of a file that has not been written yet possible.
  const previews = new PreviewDocuments();
  context.subscriptions.push(previews);

  const hub = new Hub(context, log, previews);
  active = hub;

  // Registered before anything can await: VS Code hands a restored panel to the
  // serializer during activation, and a registration that lands after the first
  // `await` can miss it — which shows up as a tab that comes back blank.
  context.subscriptions.push(
    vscode.window.registerWebviewPanelSerializer(ChatPanel.viewType, {
      async deserializeWebviewPanel(
        webviewPanel: vscode.WebviewPanel,
        state: { agentId?: unknown } | undefined,
      ): Promise<void> {
        // No `reveal`: restoring a tab is not a request to be taken to it. The
        // document remembered its agent; handing it back is what makes the tab
        // come back on the same conversation rather than on a blank one.
        const agentId = typeof state?.agentId === 'string' && state.agentId !== '' ? state.agentId : undefined;
        const panel = hub.createPanel(agentId);
        panel.adopt(webviewPanel);
      },
    }),
  );

  const tree = new SessionTreeProvider(hub);

  context.subscriptions.push(
    vscode.window.createTreeView(SessionTreeProvider.viewType, {
      treeDataProvider: tree,
      showCollapseAll: true,
    }),
    tree,

    // A new tab, always. This is the command the chat title bar and the
    // "+" affordances point at: two conversations are two tabs.
    vscode.commands.registerCommand('anvil.openChat', () => {
      hub.openTab();
    }),

    // A click on a stored session: reveal the tab already showing it, or open
    // one. One conversation in two tabs would be two runs on one file.
    vscode.commands.registerCommand('anvil.openSession', async (agentId: string) => {
      await hub.openSession(agentId);
    }),

    vscode.commands.registerCommand('anvil.openSessionInNewTab', async (agentId: string) => {
      if (typeof agentId !== 'string' || agentId === '') return;
      hub.openTab(agentId);
    }),

    vscode.commands.registerCommand('anvil.newSession', async () => {
      hub.openTab();
    }),

    vscode.commands.registerCommand('anvil.refreshSessions', async () => {
      await tree.refresh();
    }),

    // Stop means the tab in front of the user. When that tab is idle, the first
    // running one is stopped instead: the command is also the title-bar button,
    // and a button that does nothing because focus was elsewhere is worse than
    // one that stops the only turn there is.
    vscode.commands.registerCommand('anvil.stop', async () => {
      await hub.stop();
    }),

    vscode.commands.registerCommand('anvil.restartHost', async () => {
      await hub.restart();
    }),

    // Opens — or creates — the file that says what the agent may do without
    // asking. The same layers the SDK reads, so a rule written here is a rule
    // the next session obeys.
    vscode.commands.registerCommand('anvil.openPermissionRules', async () => {
      await hub.openPermissionRules();
    }),

    vscode.commands.registerCommand('anvil.setApiKey', async () => {
      const saved = await promptForApiKey(context);
      if (!saved) return;
      // The key lives in the child's environment, so a running host still holds
      // the old one until it is replaced.
      await hub.restart();
      vscode.window.showInformationMessage('Anvil: API key saved.');
    }),

    vscode.commands.registerCommand('anvil.clearApiKey', async () => {
      await clearApiKey(context);
      await hub.restart();
      vscode.window.showInformationMessage('Anvil: API key cleared.');
    }),

    // The context keys the title-bar stop buttons hang off, and the one the
    // sidebar reads to decide whether it may ask the host for anything.
    hub.onRunningChanged(() => {
      // Two keys because there are two buttons: the sidebar's Stop is about the
      // whole window, and the chat title bar's is about the tab it sits on.
      void setContext('anvil.busy', hub.busy);
      void setContext('anvil.activeBusy', hub.activeBusy);
      tree.refreshSoon();
    }),
    hub.onSessionsChanged(() => {
      tree.refreshSoon();
    }),
    vscode.workspace.onDidGrantWorkspaceTrust(() => {
      void setContext('anvil.trusted', true);
      tree.refreshSoon();
    }),

    // A different folder is a different workspace root, and the host resolves
    // every path against its cwd: it has to be replaced. The sidebar is scoped
    // to that folder too, so its list is now about somewhere else.
    vscode.workspace.onDidChangeWorkspaceFolders(async () => {
      await hub.restart();
      tree.refreshSoon();
    }),

    // Configuration changes take effect on the next host, not mid-turn.
    vscode.workspace.onDidChangeConfiguration(async (e) => {
      if (e.affectsConfiguration('anvil') && hub.running) {
        await hub.restart();
      }
    }),
  );

  void setContext('anvil.busy', false);
  void setContext('anvil.activeBusy', false);
  void setContext('anvil.trusted', vscode.workspace.isTrusted);
}

export function deactivate(): void {
  // VS Code may not run subscription disposals before the process ends, and a
  // sidecar that outlives its window leaks. Kill it explicitly.
  const hub = active;
  active = undefined;
  if (hub) void hub.dispose();
}

function setContext(key: string, value: unknown): Thenable<unknown> {
  return vscode.commands.executeCommand('setContext', key, value);
}
