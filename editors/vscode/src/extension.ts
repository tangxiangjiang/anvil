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
import { ChatSidebarProvider } from './chatSidebar';
import { currentSelection, rememberSourceEditor } from './editorContext';
import { Hub } from './hub';
import { PLAN_SCHEME, planTarget } from './plan';
import { PlanDocuments } from './planDocument';
import { PreviewDocuments } from './review';
import {
  CodeMethodNotFound,
  SESSION_HISTORY_ANCHOR_ERROR,
  SESSION_SEARCH_CURSOR_ERROR,
  SessionInfo,
  SessionSearchHit,
} from './protocol';
import { SessionNode, SessionRow } from './sessionList';
import {
  SessionChoice,
  SessionSearchEpoch,
  SessionSearchFilters,
  buildSessionSearchParams,
  contentSearchEligible,
  filterSessions,
  loadMoreChoice,
  mergeSessionSearchHits,
  retryChoice,
  sessionChoices,
  sessionSearchChoices,
  statusChoice,
} from './sessionSearch';
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
import { HostRpcError } from './hostClient';
import { WorkflowDocuments } from './workflowDocument';
import {
  WorkflowArtifactNode,
  WorkflowItemNode,
  WorkflowTreeProvider,
  WorkflowWorktreeNode,
} from './workflowTree';
import { workflowDecisionParams } from './workflowView';
import {
  strategyLabel,
  worktreeCleanupParams,
  worktreeDiscardParams,
  worktreeIntegrationParams,
  worktreeReviewMarkdown,
} from './worktreeView';

/** The window's shared half, so `deactivate` can kill the child. */
let active: Hub | undefined;
let workflowDecisionPending = false;

export function activate(context: vscode.ExtensionContext): void {
  const output = vscode.window.createOutputChannel('Anvil');
  context.subscriptions.push(output);
  const log = (line: string): void => output.appendLine(line);
  rememberSourceEditor(vscode.window.activeTextEditor);

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

  const workflowDocuments = new WorkflowDocuments();
  context.subscriptions.push(workflowDocuments);

  const hub = new Hub(context, log, previews, plans, outputs);
  active = hub;

  const chatSidebar = new ChatSidebarProvider(hub);
  context.subscriptions.push(
    vscode.window.registerWebviewViewProvider(ChatSidebarProvider.viewType, chatSidebar, {
      webviewOptions: { retainContextWhenHidden: true },
    }),
  );

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
  const workflowTree = new WorkflowTreeProvider(hub);
  const workflowView = vscode.window.createTreeView(WorkflowTreeProvider.viewType, {
    treeDataProvider: workflowTree,
    showCollapseAll: true,
    canSelectMany: true,
  });
  const workflowChecks = workflowView.onDidChangeCheckboxState((event) => {
    for (const [node, state] of event.items) {
      workflowTree.toggle(node, state === vscode.TreeItemCheckboxState.Checked);
    }
    void setContext('anvil.workflowSelection', workflowTree.selected() !== undefined);
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
    workflowView,
    workflowTree,
    workflowChecks,
    vscode.window.onDidChangeActiveTextEditor((editor) => rememberSourceEditor(editor)),
    vscode.window.onDidChangeTextEditorSelection((event) => rememberSourceEditor(event.textEditor)),

    // A new tab, always. This is the command the chat title bar and the
    // "+" affordances point at: two conversations are two tabs.
    vscode.commands.registerCommand('anvil.openChat', () => {
      hub.openTab();
    }),

    vscode.commands.registerCommand('anvil.addSelectionToChat', () => {
      const selection = currentSelection();
      if (!selection) {
        void vscode.window.showInformationMessage('Select non-empty text in a file before attaching it to Anvil Chat.');
        return;
      }
      hub.addSelectionToChat(selection);
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

    vscode.commands.registerCommand('anvil.refreshWorkflow', async () => {
      await workflowTree.refresh();
      await setContext('anvil.workflowSelection', false);
    }),

    vscode.commands.registerCommand('anvil.filterWorkflow', async () => {
      const value = await vscode.window.showInputBox({
        prompt: 'Filter workflow plans and items',
        placeHolder: 'Artifact, title, item, or goal',
      });
      if (value !== undefined) {
        workflowTree.setFilter(value);
        await setContext('anvil.workflowSelection', false);
      }
    }),

    vscode.commands.registerCommand('anvil.approveWorkflowSelection', async () => {
      await decideWorkflowSelection(hub, workflowTree, 'approve');
    }),

    vscode.commands.registerCommand('anvil.denyWorkflowSelection', async () => {
      await decideWorkflowSelection(hub, workflowTree, 'deny');
    }),

    vscode.commands.registerCommand('anvil.workflow.openArtifact', async (arg: unknown) => {
      const node = workflowArtifactNode(arg);
      if (!node) return;
      await workflowDocuments.open(
        node.agentId,
        node.artifact.artifact_id,
        node.artifact.title ?? node.artifact.artifact_id,
        node.artifact.body ?? '',
      );
    }),

    vscode.commands.registerCommand('anvil.workflow.openSession', async (arg: unknown) => {
      const agentId = workflowAgentId(arg);
      if (agentId) await hub.openSession(agentId);
    }),

    vscode.commands.registerCommand('anvil.worktree.prepare', async (arg: unknown) => {
      const node = workflowItemNode(arg);
      if (!node || node.item.approval !== 'approved') return;
      const root = configuredWorktreeRoot();
      if (!root) return;
      try {
        await hub.prepareWorktree({
          agent_id: node.agentId,
          artifact_id: node.artifact.artifact_id,
          work_item_id: node.item.id,
          attempt: node.item.execution_ids.length + 1,
          worktree_root: root,
        });
        await workflowTree.refreshAgent(node.agentId);
      } catch (err) {
        void vscode.window.showErrorMessage(`Anvil: could not prepare worktree: ${(err as Error).message}`);
      }
    }),

    vscode.commands.registerCommand('anvil.worktree.review', async (arg: unknown) => {
      const node = workflowWorktreeNode(arg);
      if (!node) return;
      const root = configuredWorktreeRoot();
      if (!root) return;
      try {
        const result = await hub.reviewWorktree({
          agent_id: node.agentId, worker_id: node.worker.id,
          worktree_root: root, expected_revision: node.worker.revision,
        });
        await workflowDocuments.open(
          node.agentId, `worktree-${node.worker.id}`,
          `Worktree ${node.worker.worktree_label}`,
          worktreeReviewMarkdown(node.worker, result.review),
        );
        await workflowTree.refreshAgent(node.agentId);
      } catch (err) {
        void vscode.window.showErrorMessage(`Anvil: worktree review failed: ${(err as Error).message}`);
      }
    }),

    vscode.commands.registerCommand('anvil.worktree.integrate', async (arg: unknown) => {
      const node = workflowWorktreeNode(arg);
      if (!node) return;
      await integrateReviewedWorktree(hub, workflowTree, workflowDocuments, node);
    }),

    vscode.commands.registerCommand('anvil.worktree.discard', async (arg: unknown) => {
      const node = workflowWorktreeNode(arg);
      if (!node) return;
      await discardReviewedWorktree(hub, workflowTree, workflowDocuments, node);
    }),

    vscode.commands.registerCommand('anvil.worktree.cleanup', async (arg: unknown) => {
      const node = workflowWorktreeNode(arg);
      if (!node) return;
      await cleanupReviewedWorktree(hub, workflowTree, node);
    }),

    // The MCP manager: every server of both layers with what it is doing, and the
    // form that changes it. `/mcp` in a composer opens the same document.
    vscode.commands.registerCommand('anvil.openMcp', () => {
      hub.openMcp();
    }),

    vscode.commands.registerCommand('anvil.openModels', () => {
      hub.openModels();
    }),

    vscode.commands.registerCommand('anvil.configureModels', () => {
      hub.openModelConnection();
    }),

    vscode.commands.registerCommand('anvil.selectDefaultModel', async () => {
      const models = readSettings().models;
      if (models.mode === 'single') {
        hub.openModelConnection();
        return;
      }
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

    // Local full-text search over stored JSONL/SQLite transcripts. Short input
    // stays a cheap title/id filter; content requests are debounced and only
    // the latest response may replace the picker.
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
      const epoch = new SessionSearchEpoch();
      let timer: NodeJS.Timeout | undefined;
      let requestAbort: AbortController | undefined;
      let disposed = false;
      let legacyHost = false;
      let hits: SessionSearchHit[] = [];
      let nextCursor: string | undefined;
      let filters: SessionSearchFilters = { role: 'all', kind: 'all', includeArgs: false };
      let roleButton: vscode.QuickInputButton;
      let kindButton: vscode.QuickInputButton;
      let argsButton: vscode.QuickInputButton;
      let refreshButton: vscode.QuickInputButton;

      const contentRequested = (): boolean =>
        contentSearchEligible(pick.value) || filters.role !== 'all' || filters.kind !== 'all';

      const setButtons = (): void => {
        roleButton = {
          iconPath: new vscode.ThemeIcon('person'),
          tooltip: `Role: ${filters.role} (click to cycle)`,
        };
        kindButton = {
          iconPath: new vscode.ThemeIcon('symbol-keyword'),
          tooltip: `Kind: ${filters.kind} (click to cycle)`,
        };
        argsButton = {
          iconPath: new vscode.ThemeIcon(filters.includeArgs ? 'unlock' : 'lock'),
          tooltip: filters.includeArgs
            ? 'Tool arguments included; they may contain secrets (click to exclude)'
            : 'Tool arguments excluded (click to include; they may contain secrets)',
        };
        refreshButton = {
          iconPath: new vscode.ThemeIcon('refresh'),
          tooltip: 'Refresh this search',
        };
        pick.buttons = [roleButton, kindButton, argsButton, refreshButton];
        pick.title = filters.includeArgs
          ? 'Search sessions — local tool arguments included'
          : 'Search sessions — local content only';
      };

      const showMetadata = (notice?: string): void => {
        const choices = sessionChoices(filterSessions(sessions, pick.value));
        pick.items = notice ? [...choices, statusChoice(notice)] : choices;
        pick.busy = false;
        nextCursor = undefined;
        hits = [];
      };

      const showResults = (partial: boolean): void => {
        const choices = sessionSearchChoices(hits, sessions);
        if (partial) {
          choices.push(statusChoice('$(warning) Partial local results — the scan limit was reached.'));
        }
        if (choices.length === 0) {
          choices.push(statusChoice('No local transcript content matched.'));
        }
        if (nextCursor) choices.push(loadMoreChoice());
        pick.items = choices;
      };

      const runSearch = async (append: boolean): Promise<void> => {
        if (disposed) return;
        if (legacyHost) {
          showMetadata('$(info) This Host supports title/id search only; update it for content search.');
          return;
        }
        const requestCursor = append ? nextCursor : undefined;
        if (append && !requestCursor) return;
        const requestEpoch = epoch.supersede();
        const abort = new AbortController();
        requestAbort?.abort();
        requestAbort = abort;
        pick.busy = true;
        if (!append) {
          hits = [];
          nextCursor = undefined;
          pick.items = [statusChoice('Searching local transcripts…')];
        }
        try {
          const result = await hub.searchSessions(
            buildSessionSearchParams(pick.value, filters, requestCursor),
            abort.signal,
          );
          if (disposed || !epoch.current(requestEpoch)) return;
          hits = append ? mergeSessionSearchHits(hits, result.hits) : result.hits;
          nextCursor = result.next_cursor;
          showResults(result.partial);
        } catch (err) {
          if (disposed || !epoch.current(requestEpoch)) return;
          if (err instanceof HostRpcError && err.code === CodeMethodNotFound) {
            legacyHost = true;
            showMetadata('$(info) This Host supports title/id search only; update it for content search.');
          } else if (err instanceof HostRpcError && err.code === SESSION_SEARCH_CURSOR_ERROR) {
            hits = [];
            nextCursor = undefined;
            pick.items = [
              retryChoice('The result snapshot expired. Retry starts again from page one.'),
            ];
          } else {
            pick.items = [retryChoice((err as Error).message)];
          }
        } finally {
          if (requestAbort === abort) requestAbort = undefined;
          if (!disposed && epoch.current(requestEpoch)) pick.busy = false;
        }
      };

      const scheduleSearch = (immediate = false): void => {
        epoch.supersede();
        requestAbort?.abort();
        requestAbort = undefined;
        if (timer) clearTimeout(timer);
        timer = undefined;
        nextCursor = undefined;
        hits = [];
        pick.busy = false;
        if (!contentRequested()) {
          showMetadata();
          return;
        }
        if (legacyHost) {
          showMetadata('$(info) Content search is unavailable with this older Host.');
          return;
        }
        pick.items = [statusChoice('Search local transcript content…')];
        if (immediate) {
          void runSearch(false);
        } else {
          timer = setTimeout(() => {
            timer = undefined;
            void runSearch(false);
          }, 250);
        }
      };

      const cycle = <T extends string>(value: T, values: readonly T[]): T =>
        values[(values.indexOf(value) + 1) % values.length];

      pick.placeholder = 'Search content, or use role:, kind:, status:, after:, before:';
      pick.matchOnDescription = true;
      pick.matchOnDetail = true;
      pick.items = sessionChoices(sessions);
      setButtons();
      pick.onDidChangeValue((value) => {
        void value;
        scheduleSearch();
      });
      pick.onDidTriggerButton((button) => {
        if (button === roleButton) {
          filters = {
            ...filters,
            role: cycle(filters.role, ['all', 'user', 'assistant', 'tool'] as const),
          };
        } else if (button === kindButton) {
          filters = {
            ...filters,
            kind: cycle(filters.kind, ['all', 'message', 'error', 'plan', 'todo'] as const),
          };
        } else if (button === argsButton) {
          filters = { ...filters, includeArgs: !filters.includeArgs };
        } else if (button === refreshButton) {
          legacyHost = false;
        }
        setButtons();
        scheduleSearch(true);
      });
      pick.onDidAccept(() => {
        const chosen = pick.selectedItems[0];
        if (!chosen || chosen.choiceKind === 'status') {
          if (contentRequested()) scheduleSearch(true);
          return;
        }
        if (chosen.choiceKind === 'loadMore') {
          void runSearch(true);
          return;
        }
        if (chosen.choiceKind === 'retry') {
          scheduleSearch(true);
          return;
        }
        pick.hide();
        if (chosen.hit) {
          void hub.openSearchHit(chosen.hit).catch((err) => {
            const message =
              err instanceof HostRpcError && err.code === SESSION_HISTORY_ANCHOR_ERROR
                ? 'The search result changed on disk. Refresh the search to locate it again.'
                : `Could not load the matching history: ${(err as Error).message}`;
            void vscode.window.showWarningMessage(`Anvil: ${message}`);
          });
        } else if (chosen.agentId) {
          void hub.openSession(chosen.agentId);
        }
      });
      pick.onDidHide(() => {
        disposed = true;
        epoch.supersede();
        requestAbort?.abort();
        if (timer) clearTimeout(timer);
        pick.dispose();
      });
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
  void setContext('anvil.workflowSelection', false);
  void setContext('anvil.workflowDecisionPending', false);
  void setContext('anvil.trusted', vscode.workspace.isTrusted);
}

export function deactivate(): void {
  // VS Code may not run subscription disposals before the process ends, and a
  // sidecar that outlives its window leaks. Kill it explicitly.
  const hub = active;
  active = undefined;
  if (hub) void hub.dispose();
}

async function decideWorkflowSelection(
  hub: Hub,
  tree: WorkflowTreeProvider,
  decision: 'approve' | 'deny',
): Promise<void> {
  const selection = tree.selected();
  if (!selection) {
    void vscode.window.showInformationMessage('Select one or more pending workflow items first.');
    return;
  }
  if (workflowDecisionPending) {
    void vscode.window.showInformationMessage('A workflow decision is already in progress.');
    return;
  }
  workflowDecisionPending = true;
  await setContext('anvil.workflowDecisionPending', true);
  try {
    let reason: string | undefined;
    if (decision === 'deny') {
      reason = await vscode.window.showInputBox({
        prompt: 'Why should the selected work be denied?',
        validateInput: (value) => value.trim() === '' ? 'A denial reason is required.' : undefined,
      });
      if (reason === undefined) return;
    }
    const names = selection.itemIds.map((id) =>
      selection.artifact.items.find((item) => item.id === id)?.title ?? id);
    const verb = decision === 'approve' ? 'Approve' : 'Deny';
    const answer = await vscode.window.showWarningMessage(
      `${verb} ${names.length} selected workflow item${names.length === 1 ? '' : 's'}?`,
      {
        modal: true,
        detail: `Artifact ${selection.artifact.artifact_id}, revision ${selection.artifact.revision}\n${names.join('\n')}`,
      },
      verb,
    );
    if (answer !== verb) return;
    try {
      await hub.decideWorkflow(workflowDecisionParams(selection, decision, reason));
      tree.clearSelection();
      await setContext('anvil.workflowSelection', false);
      await tree.refreshAgent(selection.agentId);
    } catch (err) {
      if (err instanceof HostRpcError && err.code === -32006) {
        tree.clearSelection();
        await setContext('anvil.workflowSelection', false);
        await tree.refreshAgent(selection.agentId);
        void vscode.window.showWarningMessage(
          'The workflow changed before this decision was applied. The view was refreshed; nothing was retried.',
        );
        return;
      }
      void vscode.window.showErrorMessage(`Anvil: could not ${decision} workflow items: ${(err as Error).message}`);
    }
  } finally {
    workflowDecisionPending = false;
    await setContext('anvil.workflowDecisionPending', false);
  }
}

function configuredWorktreeRoot(): string | undefined {
  const root = readSettings().worktreeRoot;
  if (root) return root;
  void vscode.window.showWarningMessage(
    'Set anvil.worktreeRoot to review and manage isolated worktrees.',
    'Open Settings',
  ).then((choice) => {
    if (choice === 'Open Settings') {
      void vscode.commands.executeCommand('workbench.action.openSettings', 'anvil.worktreeRoot');
    }
  });
  return undefined;
}

async function integrateReviewedWorktree(
  hub: Hub,
  tree: WorkflowTreeProvider,
  documents: WorkflowDocuments,
  node: WorkflowWorktreeNode,
): Promise<void> {
  const root = configuredWorktreeRoot();
  if (!root) return;
  try {
    const preflight = await hub.reviewWorktree({
      agent_id: node.agentId, worker_id: node.worker.id,
      worktree_root: root, expected_revision: node.worker.revision,
    });
    await documents.open(
      node.agentId, `worktree-${node.worker.id}`,
      `Worktree ${node.worker.worktree_label}`,
      worktreeReviewMarkdown(node.worker, preflight.review),
    );
    if (!preflight.review.integrable) {
      await tree.refreshAgent(node.agentId);
      void vscode.window.showWarningMessage(
        `Integration is blocked: ${preflight.review.refusal_reasons.join('; ')}`,
      );
      return;
    }
    const strategy = await vscode.window.showQuickPick(
      preflight.review.allowed_strategies.map((value) => ({
        label: strategyLabel(value), value,
      })),
      { placeHolder: 'Choose the reviewed integration strategy' },
    );
    if (!strategy) {
      await tree.refreshAgent(node.agentId);
      return;
    }
    const confirmation = await vscode.window.showWarningMessage(
      `Integrate ${preflight.review.changed_paths.length} reviewed path(s) from `
        + `${node.worker.worktree_label} using ${strategy.label}?`,
      { modal: true, detail: `Base ${preflight.review.current_base}\nHead ${preflight.review.head_commit}\nDigest ${preflight.review.patch_digest}` },
      'Integrate Reviewed Changes',
    );
    if (confirmation !== 'Integrate Reviewed Changes') {
      await tree.refreshAgent(node.agentId);
      return;
    }
    await hub.integrateWorktree(
      worktreeIntegrationParams(root, node.worker, preflight.review, strategy.value),
    );
    await tree.refreshAgent(node.agentId);
    void vscode.window.showInformationMessage('Anvil: reviewed worktree changes integrated.');
  } catch (err) {
    await tree.refreshAgent(node.agentId);
    void vscode.window.showErrorMessage(`Anvil: integration failed safely: ${(err as Error).message}`);
  }
}

async function discardReviewedWorktree(
  hub: Hub,
  tree: WorkflowTreeProvider,
  documents: WorkflowDocuments,
  node: WorkflowWorktreeNode,
): Promise<void> {
  const root = configuredWorktreeRoot();
  if (!root) return;
  try {
    const preflight = await hub.reviewWorktree({
      agent_id: node.agentId, worker_id: node.worker.id,
      worktree_root: root, expected_revision: node.worker.revision,
    });
    await documents.open(
      node.agentId, `worktree-${node.worker.id}`,
      `Discard ${node.worker.worktree_label}`,
      worktreeReviewMarkdown(node.worker, preflight.review),
    );
    const confirmation = await vscode.window.showWarningMessage(
      `Discard the reviewed changes in ${node.worker.worktree_label}?`,
      {
        modal: true,
        detail: `${preflight.review.changed_paths.length} committed path(s), `
          + `${preflight.review.dirty_tracked.length} dirty path(s), `
          + `${preflight.review.untracked.length + preflight.review.ignored.length} untracked/ignored path(s).\n`
          + `Digest ${preflight.review.patch_digest}`,
      },
      'Discard Reviewed Changes',
    );
    if (confirmation !== 'Discard Reviewed Changes') {
      await tree.refreshAgent(node.agentId);
      return;
    }
    await hub.discardWorktree(
      worktreeDiscardParams(root, node.worker, preflight.review),
    );
    await tree.refreshAgent(node.agentId);
    void vscode.window.showInformationMessage(
      'Anvil: worktree marked discarded. Cleanup remains a separate confirmed action.',
    );
  } catch (err) {
    await tree.refreshAgent(node.agentId);
    void vscode.window.showErrorMessage(`Anvil: discard failed safely: ${(err as Error).message}`);
  }
}

async function cleanupReviewedWorktree(
  hub: Hub,
  tree: WorkflowTreeProvider,
  node: WorkflowWorktreeNode,
): Promise<void> {
  const root = configuredWorktreeRoot();
  if (!root) return;
  const confirmation = await vscode.window.showWarningMessage(
    `Remove the proven-owned worktree ${node.worker.worktree_label}?`,
    {
      modal: true,
      detail: node.worker.state === 'discarded'
        ? 'Only content bound to the displayed discard review will be removed.'
        : 'The worktree must be clean. Ambiguous content will be retained with a blocked error.',
    },
    'Remove Worktree',
  );
  if (confirmation !== 'Remove Worktree') return;
  try {
    await hub.cleanupWorktree(worktreeCleanupParams(root, node.worker));
    await tree.refreshAgent(node.agentId);
    void vscode.window.showInformationMessage('Anvil: owned worktree cleanup completed.');
  } catch (err) {
    await tree.refreshAgent(node.agentId);
    void vscode.window.showErrorMessage(
      `Anvil: cleanup blocked; worktree and branch were retained: ${(err as Error).message}`,
    );
  }
}

function workflowArtifactNode(arg: unknown): WorkflowArtifactNode | undefined {
  if (!arg || typeof arg !== 'object') return undefined;
  const node = arg as Partial<WorkflowArtifactNode>;
  return node.kind === 'artifact' && typeof node.agentId === 'string' && node.artifact
    ? node as WorkflowArtifactNode
    : undefined;
}

function workflowItemNode(arg: unknown): WorkflowItemNode | undefined {
  if (!arg || typeof arg !== 'object') return undefined;
  const node = arg as Partial<WorkflowItemNode>;
  return node.kind === 'item' && typeof node.agentId === 'string' &&
    typeof node.item?.id === 'string' && node.artifact
    ? node as WorkflowItemNode
    : undefined;
}

function workflowWorktreeNode(arg: unknown): WorkflowWorktreeNode | undefined {
  const node = arg as Partial<WorkflowWorktreeNode>;
  return node?.kind === 'worktree' && typeof node.agentId === 'string' &&
    typeof node.worker?.id === 'string'
    ? node as WorkflowWorktreeNode
    : undefined;
}

function workflowAgentId(arg: unknown): string | undefined {
  if (!arg || typeof arg !== 'object') return undefined;
  const node = arg as { agentId?: unknown };
  return typeof node.agentId === 'string' && node.agentId !== '' ? node.agentId : undefined;
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
