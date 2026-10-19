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
import { SessionInfo } from './protocol';
import { SessionNode, SessionRow } from './sessionList';
import { SessionChoice, filterSessions, sessionChoices } from './sessionSearch';
import { SessionTreeProvider } from './sessionTree';
import {
  clearApiKey,
  clearWebSearchKey,
  promptForApiKey,
  promptForWebSearchKey,
} from './settings';

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
    vscode.commands.registerCommand('anvil.openSession', async (arg: unknown) => {
      const agentId = agentIdOf(arg);
      if (agentId !== undefined) await hub.openSession(agentId);
    }),

    vscode.commands.registerCommand('anvil.openSessionInNewTab', async (arg: unknown) => {
      const agentId = agentIdOf(arg);
      if (agentId !== undefined) hub.openTab(agentId);
    }),

    // A rename is an appended record, so the conversation underneath it is the
    // same one. The box starts from the name the session has: for a session
    // nobody named, that is the first message the store derived it from.
    vscode.commands.registerCommand('anvil.renameSession', async (arg: unknown) => {
      const agentId = agentIdOf(arg);
      if (agentId === undefined) return;
      const current = sessionRowOf(arg);
      const title = await vscode.window.showInputBox({
        prompt: 'Name this session',
        placeHolder: 'A name you will recognise in the sidebar',
        value: current?.title ?? '',
        validateInput: (value) => (value.trim() === '' ? 'A session needs a name.' : undefined),
      });
      if (title === undefined) return;
      try {
        await hub.renameSession(agentId, title);
      } catch (err) {
        void vscode.window.showErrorMessage(`Anvil: could not rename the session: ${(err as Error).message}`);
      }
    }),

    vscode.commands.registerCommand('anvil.deleteSession', async (arg: unknown) => {
      const agentId = agentIdOf(arg);
      if (agentId === undefined) return;
      const label = sessionRowOf(arg)?.label ?? agentId;
      const answer = await vscode.window.showWarningMessage(
        `Delete “${label}”?`,
        {
          modal: true,
          detail:
            'Its stored conversation is removed from this workspace. Files it changed are left as they are. This cannot be undone.',
        },
        'Delete',
      );
      if (answer !== 'Delete') return;
      try {
        await hub.deleteSession(agentId);
      } catch (err) {
        void vscode.window.showErrorMessage(`Anvil: could not delete the session: ${(err as Error).message}`);
      }
    }),

    // A branch of a stored conversation. It copies through the last run the
    // session finished, so the row's whole history is what the new tab shows;
    // nothing about the conversation it came from is touched.
    vscode.commands.registerCommand('anvil.forkSession', async (arg: unknown) => {
      const agentId = agentIdOf(arg);
      if (agentId === undefined) return;
      const runId = sessionRowOf(arg)?.lastRunId;
      if (runId === undefined) {
        void vscode.window.showInformationMessage(
          'Anvil: this session has no finished run to branch from.',
        );
        return;
      }
      try {
        await hub.forkSession(agentId, runId);
      } catch (err) {
        void vscode.window.showErrorMessage(`Anvil: could not fork the session: ${(err as Error).message}`);
      }
    }),

    // A filter over the stored list rather than the sidebar's scrollbar: titles
    // repeat and ids are long, and the person looking for one already knows a
    // word in it.
    vscode.commands.registerCommand('anvil.searchSessions', async () => {
      let sessions: SessionInfo[];
      try {
        sessions = await hub.listSessions();
      } catch (err) {
        void vscode.window.showErrorMessage(`Anvil: could not list sessions: ${(err as Error).message}`);
        return;
      }
      if (sessions.length === 0) {
        void vscode.window.showInformationMessage('Anvil: no sessions in this workspace yet.');
        return;
      }
      const pick = vscode.window.createQuickPick<SessionChoice>();
      pick.title = 'Search sessions';
      pick.placeholder = 'A word from the title, or the agent id';
      pick.items = sessionChoices(sessions);
      pick.onDidChangeValue((value) => {
        pick.items = sessionChoices(filterSessions(sessions, value));
      });
      pick.onDidAccept(() => {
        const chosen = pick.selectedItems[0];
        pick.hide();
        if (chosen) void hub.openSession(chosen.agentId);
      });
      pick.onDidHide(() => pick.dispose());
      pick.show();
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

    // The web-search key follows the same discipline as the model key: it is
    // stored in SecretStorage and reaches the host only through the child
    // environment. The restart is what makes a new key take effect, because a
    // running process keeps the environment it was spawned with.
    vscode.commands.registerCommand('anvil.setWebSearchKey', async () => {
      const saved = await promptForWebSearchKey(context);
      if (!saved) return;
      await hub.restart();
      vscode.window.showInformationMessage('Anvil: web search API key saved.');
    }),

    vscode.commands.registerCommand('anvil.clearWebSearchKey', async () => {
      await clearWebSearchKey(context);
      await hub.restart();
      vscode.window.showInformationMessage('Anvil: web search API key cleared.');
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

/**
 * The agent id behind a command argument.
 *
 * A row's own click passes the id as a string, because that is what its
 * `TreeItem.command` was given; a context-menu entry passes the tree element
 * instead. Both arrive here, so no command has to care which one invoked it.
 */
function agentIdOf(arg: unknown): string | undefined {
  if (typeof arg === 'string') return arg === '' ? undefined : arg;
  const row = sessionRowOf(arg);
  return row?.agentId;
}

/** The row, when the argument is a session element rather than a bare id. */
function sessionRowOf(arg: unknown): SessionRow | undefined {
  if (arg === null || typeof arg !== 'object') return undefined;
  const node = arg as SessionNode;
  return node.kind === 'session' ? node : undefined;
}

function setContext(key: string, value: unknown): Thenable<unknown> {
  return vscode.commands.executeCommand('setContext', key, value);
}
