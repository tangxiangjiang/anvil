/**
 * Extension entry point: register the view, the commands, and the teardown.
 *
 * Deliberately thin. The interesting behaviour lives in `hostManager.ts`
 * (lifecycle), `session.ts` (conversation state), and `chatPanel.ts` (the view).
 */

import * as vscode from 'vscode';
import { ChatPanelProvider } from './chatPanel';
import { PreviewDocuments } from './review';
import { clearApiKey, promptForApiKey } from './settings';

/** The window's provider, so `deactivate` can kill the child. */
let active: ChatPanelProvider | undefined;

export function activate(context: vscode.ExtensionContext): void {
  const output = vscode.window.createOutputChannel('Anvil');
  context.subscriptions.push(output);
  const log = (line: string): void => output.appendLine(line);

  // One content provider for the window, for the life of the extension: it is
  // what makes a diff view of a file that has not been written yet possible.
  const previews = new PreviewDocuments();
  context.subscriptions.push(previews);

  const provider = new ChatPanelProvider(context, log, previews);
  active = provider;

  context.subscriptions.push(
    vscode.window.registerWebviewViewProvider(ChatPanelProvider.viewType, provider, {
      webviewOptions: { retainContextWhenHidden: true },
    }),

    vscode.commands.registerCommand('anvil.openChat', async () => {
      await vscode.commands.executeCommand(`${ChatPanelProvider.viewType}.focus`);
    }),

    vscode.commands.registerCommand('anvil.stop', async () => {
      await provider.stop();
    }),

    vscode.commands.registerCommand('anvil.restartHost', async () => {
      await provider.restart();
    }),

    vscode.commands.registerCommand('anvil.setApiKey', async () => {
      const saved = await promptForApiKey(context);
      if (!saved) return;
      // The key lives in the child's environment, so a running host still holds
      // the old one until it is replaced.
      await provider.restart();
      vscode.window.showInformationMessage('Anvil: API key saved.');
    }),

    vscode.commands.registerCommand('anvil.clearApiKey', async () => {
      await clearApiKey(context);
      await provider.restart();
      vscode.window.showInformationMessage('Anvil: API key cleared.');
    }),

    // The context key the stop button in the view title hangs off.
    provider.onRunningChanged(() => {
      void vscode.commands.executeCommand('setContext', 'anvil.running', provider.running);
    }),

    // A different folder is a different workspace root, and the host resolves
    // every path against its cwd: it has to be replaced.
    vscode.workspace.onDidChangeWorkspaceFolders(async () => {
      await provider.restart();
    }),

    // Configuration changes take effect on the next host, not mid-turn.
    vscode.workspace.onDidChangeConfiguration(async (e) => {
      if (e.affectsConfiguration('anvil') && provider.running) {
        await provider.restart();
      }
    }),
  );

  void vscode.commands.executeCommand('setContext', 'anvil.running', false);
}

export function deactivate(): void {
  // VS Code may not run subscription disposals before the process ends, and a
  // sidecar that outlives its window leaks. Kill it explicitly.
  const provider = active;
  active = undefined;
  if (provider) void provider.dispose();
}
