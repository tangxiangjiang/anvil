/**
 * Extension entry point: register the tab, the session tree, the commands, and
 * the teardown.
 *
 * Deliberately thin. The interesting behaviour lives in `hostManager.ts`
 * (lifecycle), `session.ts` (conversation state), `sessionList.ts` (what the
 * sidebar shows), and `chatPanel.ts` (the view).
 */

import * as vscode from 'vscode';
import { ChatPanel } from './chatPanel';
import { PreviewDocuments } from './review';
import { SessionTreeProvider } from './sessionTree';
import { clearApiKey, promptForApiKey } from './settings';

/** The window's panel, so `deactivate` can kill the child. */
let active: ChatPanel | undefined;

export function activate(context: vscode.ExtensionContext): void {
  const output = vscode.window.createOutputChannel('Anvil');
  context.subscriptions.push(output);
  const log = (line: string): void => output.appendLine(line);

  // One content provider for the window, for the life of the extension: it is
  // what makes a diff view of a file that has not been written yet possible.
  const previews = new PreviewDocuments();
  context.subscriptions.push(previews);

  const panel = new ChatPanel(context, log, previews);
  active = panel;

  // Registered before anything can await: VS Code hands a restored panel to the
  // serializer during activation, and a registration that lands after the first
  // `await` can miss it — which shows up as a tab that comes back blank.
  context.subscriptions.push(
    vscode.window.registerWebviewPanelSerializer(ChatPanel.viewType, {
      async deserializeWebviewPanel(webviewPanel: vscode.WebviewPanel): Promise<void> {
        // No `reveal`: restoring a tab is not a request to be taken to it.
        panel.adopt(webviewPanel);
      },
    }),
  );

  const tree = new SessionTreeProvider(panel);

  context.subscriptions.push(
    vscode.window.createTreeView(SessionTreeProvider.viewType, {
      treeDataProvider: tree,
      showCollapseAll: true,
    }),
    tree,

    vscode.commands.registerCommand('anvil.openChat', () => {
      panel.open();
    }),

    vscode.commands.registerCommand('anvil.openSession', async (agentId: string) => {
      await panel.openSession(agentId);
    }),

    vscode.commands.registerCommand('anvil.newSession', async () => {
      await panel.newSession();
    }),

    vscode.commands.registerCommand('anvil.refreshSessions', async () => {
      await tree.refresh();
    }),

    vscode.commands.registerCommand('anvil.stop', async () => {
      await panel.stop();
    }),

    vscode.commands.registerCommand('anvil.restartHost', async () => {
      await panel.restart();
    }),

    vscode.commands.registerCommand('anvil.setApiKey', async () => {
      const saved = await promptForApiKey(context);
      if (!saved) return;
      // The key lives in the child's environment, so a running host still holds
      // the old one until it is replaced.
      await panel.restart();
      vscode.window.showInformationMessage('Anvil: API key saved.');
    }),

    vscode.commands.registerCommand('anvil.clearApiKey', async () => {
      await clearApiKey(context);
      await panel.restart();
      vscode.window.showInformationMessage('Anvil: API key cleared.');
    }),

    // The context keys the title-bar stop buttons hang off, and the one the
    // sidebar reads to decide whether it may ask the host for anything.
    panel.onRunningChanged(() => {
      void setContext('anvil.busy', panel.busy);
    }),
    panel.onSessionsChanged(() => {
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
      await panel.restart();
      tree.refreshSoon();
    }),

    // Configuration changes take effect on the next host, not mid-turn.
    vscode.workspace.onDidChangeConfiguration(async (e) => {
      if (e.affectsConfiguration('anvil') && panel.running) {
        await panel.restart();
      }
    }),
  );

  void setContext('anvil.busy', false);
  void setContext('anvil.trusted', vscode.workspace.isTrusted);
}

export function deactivate(): void {
  // VS Code may not run subscription disposals before the process ends, and a
  // sidecar that outlives its window leaks. Kill it explicitly.
  const panel = active;
  active = undefined;
  if (panel) void panel.dispose();
}

function setContext(key: string, value: unknown): Thenable<unknown> {
  return vscode.commands.executeCommand('setContext', key, value);
}
