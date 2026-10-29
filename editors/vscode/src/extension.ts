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
import { PLAN_SCHEME, planTarget } from './plan';
import { PlanDocuments } from './planDocument';
import { PreviewDocuments } from './review';
import { SessionInfo } from './protocol';
import { SessionNode, SessionRow } from './sessionList';
import { SessionChoice, filterSessions, sessionChoices } from './sessionSearch';
import { SessionTreeProvider } from './sessionTree';
import { ChatStatusBar, STATUS_BAR_COMMAND } from './statusBar';
import { initWorkspaceRoot } from './workspaceRoot';
import {
  clearApiKey,
  clearWebSearchKey,
  initDefaultModel,
  promptForApiKey,
  promptForWebSearchKey,
  readSettings,
  saveDefaultModel,
  settingsKey,
} from './settings';
import { ToolOutputDocuments } from './toolOutput';

/** The window's shared half, so `deactivate` can kill the child. */
let active: Hub | undefined;

export function activate(context: vscode.ExtensionContext): void {
  const output = vscode.window.createOutputChannel('Anvil');
  context.subscriptions.push(output);
  const log = (line: string): void => output.appendLine(line);

  // The root is per window and is read from state this context owns, so it has
  // to be installed before a panel is restored or a command can run. It is the
  // one place that knows which folder a conversation belongs to (0.46).
  initWorkspaceRoot(context);
  initDefaultModel(context);

  // One content provider for the window, for the life of the extension: it is
  // what makes a diff view of a file that has not been written yet possible.
  const previews = new PreviewDocuments();
  context.subscriptions.push(previews);

  // The same shape for plans, but the content is only ever read: there is no
  // file system provider for `anvil-plan:`, so the document is a view of text
  // this extension holds rather than a file anything can write.
  const plans = new PlanDocuments();
  context.subscriptions.push(plans);

  // Tool output is the same shape of read-only document, but it is created on
  // demand from the result the webview already holds rather than from a file.
  const outputs = new ToolOutputDocuments();
  context.subscriptions.push(outputs);

  const hub = new Hub(context, log, previews, plans, outputs);
  active = hub;

  // Registered before anything can await: VS Code hands a restored panel to the
  // serializer during activation, and a registration that lands after the first
  // `await` can miss it — which shows up as a tab that comes back blank.
  context.subscriptions.push(
    vscode.window.registerWebviewPanelSerializer(ChatPanel.viewType, {
      async deserializeWebviewPanel(
        webviewPanel: vscode.WebviewPanel,
        state: { agentId?: unknown; focus?: unknown } | undefined,
      ): Promise<void> {
        // No `reveal`: restoring a tab is not a request to be taken to it. The
        // document remembered its agent; handing it back is what makes the tab
        // come back on the same conversation rather than on a blank one.
        const agentId = typeof state?.agentId === 'string' && state.agentId !== '' ? state.agentId : undefined;
        const existing = agentId ? hub.findByAgent(agentId) : undefined;
        if (existing) {
          webviewPanel.dispose();
          return;
        }
        // The reading view is the document's own state, saved with the same
        // call. A restored tab that comes back in the other view would be a
        // reload the reader has to notice and undo.
        const panel = hub.createPanel(agentId, state?.focus === true);
        panel.adopt(webviewPanel);
      },
    }),
  );

  const tree = new SessionTreeProvider(hub, context.workspaceState);
  const sessionView = vscode.window.createTreeView(SessionTreeProvider.viewType, {
    treeDataProvider: tree,
    showCollapseAll: true,
  });
  /**
   * The last reading the configuration listener saw. Kept outside the
   * subscription list because it is state, not a disposable; a change is
   * compared against it to tell a display-only edit from one the host needs.
   */
  let lastSettings = readSettings();

  context.subscriptions.push(
    sessionView,
    tree,

    // A new tab, always. This is the command the chat title bar and the
    // "+" affordances point at: two conversations are two tabs.
    vscode.commands.registerCommand('anvil.openChat', () => {
      hub.openTab();
    }),

    // What the status bar runs: take the user to the work rather than to a new
    // tab. When the tab asking a question is in front, this is a no-op.
    vscode.commands.registerCommand(STATUS_BAR_COMMAND, () => {
      hub.revealChat();
    }),

    // The background tasks of this window, in their own document. It is not a
    // chat tab: the table belongs to every live session rather than to one
    // conversation, and the same command is the status-bar item's click.
    vscode.commands.registerCommand('anvil.openTasks', () => {
      hub.openTasks();
    }),

    // The MCP manager: every server of both layers with what it is doing, and the
    // form that changes it. `/mcp` in a composer opens the same document.
    vscode.commands.registerCommand('anvil.openMcp', () => {
      hub.openMcp();
    }),

    vscode.commands.registerCommand('anvil.openModels', () => {
      hub.openModels();
    }),

    vscode.commands.registerCommand('anvil.selectDefaultModel', async () => {
      const models = readSettings().models;
      const picked = await vscode.window.showQuickPick(
        [
          { label: 'Host default', id: '' },
          ...models.available.map((id) => ({ label: id, id })),
        ],
        {
          title: 'Default model',
          placeHolder: models.defaultModel || 'Host default',
        },
      );
      if (!picked) return;
      await saveDefaultModel(context, picked.id);
      hub.modelsChanged();
    }),

    // One layer's `mcp.json`, created when it is not there. The manager's Open
    // mcp.json button takes this path for whichever layer it is looking at.
    vscode.commands.registerCommand('anvil.openMcpFile', async () => {
      await hub.openMcpFile();
    }),

    // Plan review from the palette. The card is the usual way in; these exist
    // so that the same three choices are reachable when the plan document is
    // the active editor and the webview is not on screen.
    vscode.commands.registerCommand('anvil.openPlan', async () => {
      const target = currentPlan(hub);
      if (!target) {
        void vscode.window.showInformationMessage('There is no plan to open.');
        return;
      }
      await target.panel.openPlan(target.runId);
    }),

    vscode.commands.registerCommand('anvil.approvePlan', async () => {
      const target = currentPlan(hub);
      if (!target) {
        void vscode.window.showInformationMessage('There is no plan to approve.');
        return;
      }
      await target.panel.approvePlan(target.runId);
    }),

    vscode.commands.registerCommand('anvil.rejectPlan', () => {
      const target = currentPlan(hub);
      if (!target) {
        void vscode.window.showInformationMessage('There is no plan to reject.');
        return;
      }
      // Focus is asked of the composer, so the tab has to be visible for the
      // refusal to mean anything. The plan document was the active editor.
      target.panel.reveal();
      target.panel.rejectPlan(target.runId);
    }),

    // The reading view, from the palette and from Ctrl+Alt+F. It is the front
    // tab that answers, which is the one the keybinding was aimed at; the
    // document owns the flag and flips it.
    vscode.commands.registerCommand('anvil.toggleFocusView', () => {
      hub.activePanel()?.toggleFocusView();
    }),

    // Built here rather than in `activate`'s body so its lifetime is the
    // extension's: the item and its two subscriptions are disposed together.
    new ChatStatusBar(hub),

    // A click on a stored session: reveal the tab already showing it, or open
    // one. One conversation in two tabs would be two runs on one file.
    vscode.commands.registerCommand('anvil.openSession', async (arg: unknown) => {
      const agentId = agentIdOf(arg);
      if (agentId !== undefined) await hub.openSession(agentId);
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

    vscode.commands.registerCommand('anvil.archiveSession', async (arg: unknown) => {
      const agentId = agentIdOf(arg);
      if (agentId !== undefined) await tree.setArchived(agentId, true);
    }),

    vscode.commands.registerCommand('anvil.unarchiveSession', async (arg: unknown) => {
      const agentId = agentIdOf(arg);
      if (agentId !== undefined) await tree.setArchived(agentId, false);
    }),

    vscode.commands.registerCommand('anvil.deleteSession', async (arg: unknown) => {
      const agentId = agentIdOf(arg);
      if (agentId === undefined) return;
      const label = sessionRowOf(arg)?.label ?? agentId;
      // The host deletes a parent's subagent sessions with it. Saying so before
      // the click is the only warning there will be: the children are rows the
      // person may not have opened, and they are stored conversations.
      const children = sessionRowOf(arg)?.children.length ?? 0;
      const childNote =
        children === 0
          ? ''
          : children === 1
            ? ' 1 subagent session it started is deleted too.'
            : ` ${children} subagent sessions it started are deleted too.`;
      const answer = await vscode.window.showWarningMessage(
        `Delete “${label}”?`,
        {
          modal: true,
          detail:
            'Its stored conversation is removed from this workspace. Files it changed are left as they are. This cannot be undone.' +
            childNote,
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

    // Opens the rules as a form: effect, tool, pattern and reason, plus the mode
    // new sessions start in. The file is still one click away, because a pattern
    // with quoting in it is easier to see in JSON than in a one-line input.
    vscode.commands.registerCommand('anvil.openPermissionRules', async () => {
      hub.openPermissionRules();
    }),

    // The JSON itself, created when the workspace has none. This is the path the
    // manager's Open JSON button takes too.
    vscode.commands.registerCommand('anvil.openPermissionRulesFile', async () => {
      await hub.openPermissionRulesFile();
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

    // Configuration changes take effect on the next host, not mid-turn — except
    // the chat's text size, which is display-only: it reaches the open tabs at
    // once, and a run in flight is not interrupted to change a number of pixels.
    // The comparison is against the previous reading rather than a list of keys,
    // so a setting added later is covered by `settingsKey` instead of by a list
    // that has to be kept in step with `package.json`.
    vscode.workspace.onDidChangeConfiguration(async (e) => {
      if (!e.affectsConfiguration('anvil')) return;
      const next = readSettings();
      const previous = lastSettings;
      lastSettings = next;
      if (next.fontSize !== previous.fontSize) hub.setFontSize(next.fontSize);
      if (JSON.stringify(next.models) !== JSON.stringify(previous.models)) hub.modelsChanged();
      if (hub.running && settingsKey(previous) !== settingsKey(next)) {
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

/**
 * Which plan a palette command means.
 *
 * A plan document is the active editor as soon as it opens, and its URI names
 * both the conversation and the run, so that is the precise answer. Everywhere
 * else — the composer, another tab, an unrelated file — the command means the
 * front tab's newest plan, which is the one the person was just shown.
 */
function currentPlan(hub: Hub): { panel: ChatPanel; runId: string } | undefined {
  const uri = vscode.window.activeTextEditor?.document.uri;
  if (uri?.scheme === PLAN_SCHEME) {
    const target = planTarget(uri.query);
    const panel = target ? hub.findByAgent(target.agentId) : undefined;
    if (target && panel) return { panel, runId: target.runId };
  }
  const panel = hub.activePanel();
  const runId = panel?.latestPlanRun();
  return panel && runId ? { panel, runId } : undefined;
}
