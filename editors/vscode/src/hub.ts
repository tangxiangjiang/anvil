/**
 * The window's shared half: one host process, and the things that are true of
 * every tab.
 *
 * A chat tab is a conversation. This is everything a conversation is not. There
 * is one `anvil-host` child per window — sessions are multiplexed over it, so a
 * second tab costs a session and not a process — and a catalogue a second tab
 * would otherwise fetch again is fetched once and kept here.
 *
 * It also owns the panels, because two questions cannot be answered from inside
 * one of them: "is this agent already on screen" (a sidebar click reveals that
 * tab instead of opening a second copy) and "which tab is the user looking at"
 * (the Stop command has to mean the one in front of them).
 */

import { dirname, join } from 'node:path';
import { homedir } from 'node:os';
import * as vscode from 'vscode';
import { ChatPhase, phaseOf } from './chatStatus';
import { ChatPanel } from './chatPanel';
import { SlashCommand, commandCatalogue } from './commands';
import { HostManager } from './hostManager';
import { HostRpcError } from './hostClient';
import { McpSettings } from './mcp';
import { McpPanels } from './mcpPanel';
import { PermissionsPanels } from './permissionsPanel';
import { PlanDocuments } from './planDocument';
import { TaskRow, TasksPanels } from './tasksPanel';
import {
  CheckpointRestoreResult,
  CheckpointRun,
  CodeNoSession,
  McpAddParams,
  McpAuthenticateParams,
  McpAuthenticateResult,
  McpListResult,
  McpReconnectParams,
  McpRemoveParams,
  McpScope,
  McpSetEnabledParams,
  McpSignOutParams,
  McpStatusParams,
  McpUpdateParams,
  ModelEntry,
  PermissionsAddResult,
  PermissionsResult,
  PermissionsSetParams,
  RuleFileInfo,
  RuleProblem,
  RuleSource,
  RuleSpec,
  SessionInfo,
  SessionTranscriptResult,
  SkillEntry,
  TaskInfo,
} from './protocol';
import { PreviewDocuments } from './review';
import { describeProblem, emptyRulesFile, emptyUserRulesFile, problemPosition } from './rulesFile';
import { readSettings } from './settings';

export class Hub {
  /** The one child this window talks to, shared by every tab. */
  readonly manager: HostManager;

  private readonly panels = new Set<ChatPanel>();
  /** The window's rules manager; one document, revealed rather than duplicated. */
  private readonly permissions: PermissionsPanels;
  /** The window's MCP manager; one document for every layer's servers. */
  private readonly mcp: McpPanels;
  /** The window's Tasks panel; one table of background work, revealed the same. */
  private readonly tasks: TasksPanels;
  /**
   * The running background tasks of each live session, by session id.
   *
   * It exists for the status-bar count, which is a window-wide number while the
   * table belongs to a session. The panel and the sidebar read the host for the
   * rows themselves; this is only "how many are running", fed by the `task`
   * notifications and never by a poll.
   */
  private readonly runningTasks = new Map<string, Set<string>>();
  /** The slash catalogue: built-ins plus this workspace's skills. */
  private catalogue: SlashCommand[] | undefined;
  /** The rule files the last session reported. Workspace-level, like the files. */
  private rulesCache: RuleFileInfo[] | undefined;
  /**
   * A rule file that will not compile, reported against the file itself. One
   * collection for the window rather than one per tab: two complaints about one
   * file would be two clicks in the Problems panel for one problem.
   */
  private diagnostics: vscode.DiagnosticCollection | undefined;

  private readonly tabsChanged = new vscode.EventEmitter<void>();
  /** A tab opened, closed, or changed which agent it shows. */
  readonly onTabsChanged = this.tabsChanged.event;
  private readonly sessionsChanged = new vscode.EventEmitter<void>();
  /** The stored session list, or any tab's marker on it, could have changed. */
  readonly onSessionsChanged = this.sessionsChanged.event;
  private readonly runningChanged = new vscode.EventEmitter<void>();
  /** A turn started or ended, in any tab, or the front tab changed. */
  readonly onRunningChanged = this.runningChanged.event;
  private readonly tasksChanged = new vscode.EventEmitter<void>();
  /** A background task started or ended, in any session of this window. */
  readonly onTasksChanged = this.tasksChanged.event;

  constructor(
    private readonly context: vscode.ExtensionContext,
    private readonly log: (line: string) => void,
    private readonly previews: PreviewDocuments,
    // One provider for the window: a plan document is addressed by conversation
    // and run, so two tabs reviewing plans get two documents rather than a
    // second registry.
    private readonly plans: PlanDocuments,
  ) {
    // A crash or a restart ends every session this window holds. Each panel
    // drops its handle and opens a new one for its agent on the next prompt;
    // nothing here may keep a handle to a child that is gone.
    this.manager = new HostManager(context, log, {
      onNotice: (level, message) => this.notice(level, message),
    });
    this.manager.onReset(() => {
      for (const panel of this.panels) panel.hostReset();
      // The tasks belonged to the child that is gone; no terminal notification
      // will arrive for them, so the count is dropped here rather than left
      // claiming work nobody is doing.
      this.forgetAllTasks();
    });
    this.permissions = new PermissionsPanels(context.extensionUri, this);
    this.mcp = new McpPanels(context.extensionUri, this);
    this.tasks = new TasksPanels(context.extensionUri, {
      listTasks: () => this.listTasks(),
      taskOutput: (sessionId, taskId, offset, limit) =>
        this.manager.taskOutput(sessionId, taskId, offset, limit),
      cancelTask: (sessionId, taskId) => this.manager.cancelTask(sessionId, taskId),
      listSessions: () => this.listSessions(),
      openSession: (agentId) => this.openSession(agentId),
      onTasksChanged: (listener) => this.tasksChanged.event(listener),
    });
  }

  // --- panels -----------------------------------------------------------------

  /**
   * Builds a tab without opening it. The caller adopts a webview panel onto it
   * — either one this side just created, or one VS Code restored — which is why
   * construction and `open()` are separate. `focus` is the reading view the
   * previous document saved, which only a restored tab can have: a tab opened
   * now starts in the full view.
   */
  createPanel(agentId?: string, focus = false): ChatPanel {
    const panel = new ChatPanel(this.context, this.log, this.previews, this.plans, this, agentId, focus);
    this.panels.add(panel);
    this.tabsChanged.fire();
    return panel;
  }

  /** Opens a new tab, optionally on a stored agent. */
  openTab(agentId?: string): ChatPanel {
    const panel = this.createPanel(agentId);
    panel.open();
    return panel;
  }

  /** Called by a panel when its webview is disposed. */
  unregister(panel: ChatPanel): void {
    if (!this.panels.delete(panel)) return;
    this.tabsChanged.fire();
    this.runningChanged.fire();
  }

  /** The tab already showing an agent, if one is. */
  findByAgent(agentId: string): ChatPanel | undefined {
    for (const panel of this.panels) {
      if (panel.currentAgentId() === agentId) return panel;
    }
    return undefined;
  }

  /** The agents with a tab open. The sidebar marks those rows. */
  openAgentIds(): string[] {
    const ids: string[] = [];
    for (const panel of this.panels) {
      const agentId = panel.currentAgentId();
      if (agentId !== undefined) ids.push(agentId);
    }
    return ids;
  }

  /** The agents with a turn in flight, for the sidebar's running marker. */
  runningAgentIds(): string[] {
    const ids: string[] = [];
    for (const panel of this.panels) {
      const agentId = panel.runningAgentId();
      if (agentId !== undefined) ids.push(agentId);
    }
    return ids;
  }

  /** The tab the user is looking at, when that is a chat tab. */
  activePanel(): ChatPanel | undefined {
    for (const panel of this.panels) {
      if (panel.isActive()) return panel;
    }
    return undefined;
  }

  /**
   * The reveal-or-create entry point, for a click on a stored session.
   *
   * A click is "take me there", so a session already on screen is revealed
   * rather than duplicated. `openSessionInNewTab` is the explicit way to get a
   * second copy: two tabs are two sessions, each resuming the same stored
   * conversation.
   */
  async openSession(agentId: string): Promise<void> {
    const existing = this.findByAgent(agentId);
    if (existing) {
      existing.reveal();
      return;
    }
    this.openTab(agentId).open();
  }

  /**
   * Always a new tab, even when the agent is already on screen.
   *
   * That is two sessions resuming one stored conversation, which the host
   * supports; it is not two tabs sharing one session. The second tab is for
   * looking at where a conversation went while the first one carries on.
   */
  async openSessionInNewTab(agentId: string): Promise<void> {
    this.openTab(agentId);
  }

  /** True when a host child is live; the sidebar uses it to avoid starting one. */
  isHostUp(): boolean {
    return this.manager.running;
  }

  /**
   * Replaces the host child. Every tab keeps its agent and opens a new session
   * on it when it next needs one.
   */
  async restart(): Promise<void> {
    // The catalogue described the old process's workspace (its skills, its
    // rules); it is fetched again on the next `/` rather than carried over.
    this.catalogue = undefined;
    this.rulesCache = undefined;
    await this.manager.restart();
    // The context window is client-side arithmetic, so a tab still on screen
    // learns the new value without being reloaded.
    for (const panel of this.panels) panel.reportContext();
    this.runningChanged.fire();
    this.sessionsChanged.fire();
  }

  /** Stops the turn in front of the user, or any running one when that is idle. */
  async stop(): Promise<void> {
    const active = this.activePanel();
    if (active?.busy) {
      await active.stop();
      return;
    }
    for (const panel of this.panels) {
      if (!panel.busy) continue;
      await panel.stop();
      return;
    }
  }

  /** Whether any tab is running a turn. */
  get busy(): boolean {
    for (const panel of this.panels) if (panel.busy) return true;
    return false;
  }

  /** Whether the front tab is running a turn; what the editor title bar shows. */
  get activeBusy(): boolean {
    return this.activePanel()?.busy ?? false;
  }

  /**
   * What the window's chat is doing, for the status bar.
   *
   * The front tab does not decide it: an approval waiting in a tab behind the
   * one being read is exactly the thing a window-wide indicator is for. The
   * rule for combining the tabs lives in `chatStatus.ts`, where it is tested.
   */
  get phase(): ChatPhase {
    return phaseOf([...this.panels].map((panel) => ({ busy: panel.busy, awaiting: panel.awaitingApproval() })));
  }

  /**
   * Takes the user to the chat: the tab that is asking something, else the
   * front tab, else any open one, and only then a new tab.
   *
   * This is the status bar's click. Opening a fresh tab when one already
   * exists would make the indicator's one job — "go where the work is" —
   * produce a second conversation instead.
   */
  revealChat(): void {
    for (const panel of this.panels) {
      if (panel.awaitingApproval()) {
        panel.reveal();
        return;
      }
    }
    const active = this.activePanel();
    if (active) {
      active.reveal();
      return;
    }
    for (const panel of this.panels) {
      panel.reveal();
      return;
    }
    this.openTab();
  }

  /** Whether a host child is live, so a redraw need not start one. */
  get running(): boolean {
    return this.manager.running;
  }

  // --- broadcasts -------------------------------------------------------------

  /** Posts a message to every tab. */
  notice(level: 'info' | 'warn' | 'error', message: string): void {
    this.log(`${level}: ${message}`);
    for (const panel of this.panels) panel.notice(level, message);
  }

  /** A tab's state changed: the sidebar's markers and the title bar may be stale. */
  notifyRunning(): void {
    this.runningChanged.fire();
  }

  /** A conversation changed: the stored list may not describe it any more. */
  notifySessions(): void {
    this.sessionsChanged.fire();
  }

  /** A tab learned which agent it is showing. */
  notifyTabs(): void {
    this.tabsChanged.fire();
  }

  // --- background tasks -------------------------------------------------------

  /**
   * A background task started or ended in one session tree.
   *
   * The notification carries the task's whole snapshot, so a terminal row is
   * known by its status and not by a separate "removed" message. Every task
   * notification fires the event, start or end: the panel re-lists on either,
   * and the count only changes on the two edges.
   */
  noteTask(sessionId: string, task: TaskInfo): void {
    let running = this.runningTasks.get(sessionId);
    if (task.status === 'running') {
      if (!running) {
        running = new Set<string>();
        this.runningTasks.set(sessionId, running);
      }
      running.add(task.id);
    } else if (running) {
      running.delete(task.id);
      if (running.size === 0) this.runningTasks.delete(sessionId);
    }
    this.tasksChanged.fire();
  }

  /**
   * Forgets one session's running tasks, when that session is closed.
   *
   * Closing a session stops its tasks on the host, which cannot notify for a
   * session that is no longer there. Dropping the bucket is what keeps the
   * status-bar count from outliving the tab that started the work.
   */
  forgetTasks(sessionId: string): void {
    if (!this.runningTasks.delete(sessionId)) return;
    this.tasksChanged.fire();
  }

  private forgetAllTasks(): void {
    if (this.runningTasks.size === 0) return;
    this.runningTasks.clear();
    this.tasksChanged.fire();
  }

  /** How many background tasks are running across this window's sessions. */
  get runningTaskCount(): number {
    let count = 0;
    for (const tasks of this.runningTasks.values()) count += tasks.size;
    return count;
  }

  /**
   * Every running or finished task of every live session, in start order.
   *
   * One table per session tree, rooted at the agent the person opened; this
   * flattens them so the window's panel can show them together. A session the
   * host no longer knows is dropped from the count rather than reported as a
   * failure: it was closed between the tab and the question.
   */
  async listTasks(): Promise<TaskRow[]> {
    const rows: TaskRow[] = [];
    for (const session of this.manager.liveSessions()) {
      const sessionId = session.id;
      if (!sessionId) continue;
      try {
        for (const task of await this.manager.listTasks(sessionId)) {
          rows.push({ sessionId, task });
        }
      } catch (err) {
        if (err instanceof HostRpcError && err.code === CodeNoSession) {
          this.forgetTasks(sessionId);
          continue;
        }
        throw err;
      }
    }
    return rows;
  }

  /** Opens the Tasks panel, or brings the open one to the front. */
  openTasks(): void {
    this.tasks.open();
  }

  // --- catalogue --------------------------------------------------------------

  /**
   * The slash catalogue, fetched once per host. Built-ins need no host, so they
   * are always available; the skills are the reason it is a round trip at all.
   */
  async commandCatalogue(): Promise<SlashCommand[]> {
    if (this.catalogue) return this.catalogue;
    let skills: SkillEntry[] = [];
    try {
      skills = await this.manager.skills();
    } catch (err) {
      // A missing or broken host must not cost the user the built-ins.
      this.log(`could not list skills: ${(err as Error).message}`);
      this.notice('warn', `Skill commands are unavailable: ${(err as Error).message}`);
    }
    this.catalogue = commandCatalogue(skills);
    return this.catalogue;
  }

  /** This workspace's stored agents. Starts a host if none is live. */
  listSessions(): Promise<SessionInfo[]> {
    return this.manager.listSessions();
  }

  /** The stored conversation for one agent, oldest first. */
  transcript(agentId: string, limit?: number): Promise<SessionTranscriptResult> {
    return this.manager.transcript(agentId, limit);
  }

  /** The runs of one agent that can be put back, newest first. */
  async listCheckpoints(agentId: string): Promise<CheckpointRun[]> {
    const result = await this.manager.listCheckpoints(agentId);
    return result.runs;
  }

  /** Puts one run's files back. */
  restoreCheckpoint(agentId: string, runId: string): Promise<CheckpointRestoreResult> {
    return this.manager.restoreCheckpoint(agentId, runId);
  }

  /** Gives a stored session a name, then redraws every list that shows it. */
  async renameSession(agentId: string, title: string): Promise<void> {
    await this.manager.renameSession(agentId, title);
    this.sessionsChanged.fire();
  }

  /**
   * Branches a conversation at a finished run and opens the branch in a new tab.
   *
   * A fork is a new conversation, so it always gets its own tab — even when the
   * session it came from is on screen. The source tab is not touched: it keeps
   * the conversation it had, which is what makes the branch useful.
   */
  async forkSession(agentId: string, runId: string): Promise<string> {
    const result = await this.manager.forkSession(agentId, runId);
    const newId = result.agent_id;
    this.sessionsChanged.fire();
    this.openTab(newId);
    return newId;
  }

  /**
   * Forgets a stored session, closing every tab that has it open first.
   *
   * The close is not tidiness. A session that stayed open would hold the agent
   * and write the store file again on its next prompt, so the delete would look
   * like it worked and come undone on the next turn. Every tab is closed, not
   * just the first one: two tabs can resume one conversation.
   */
  async deleteSession(agentId: string): Promise<void> {
    for (const panel of [...this.panels]) {
      if (panel.currentAgentId() === agentId) await panel.close();
    }
    await this.manager.deleteSession(agentId);
    this.sessionsChanged.fire();
  }

  /** The rule files that apply to this workspace, fetched once. */
  async rules(): Promise<RuleFileInfo[]> {
    if (this.rulesCache) return this.rulesCache;
    this.rulesCache = await this.manager.rules();
    return this.rulesCache;
  }

  /**
   * The MCP servers the given layers declare, and what the host observed for each.
   *
   * Not cached, unlike the rules: the layers are the caller's, and a malformed
   * file is an answer the caller has to be able to see on every ask, not just
   * the first. The call reads two small files, so repeating it per tab costs
   * less than a cache that could answer for a different layer set.
   */
  mcpServers(settingSources: string[], includeTools?: boolean): Promise<McpListResult> {
    return this.manager.mcpServers(settingSources, includeTools);
  }

  /**
   * An MCP server changed state — connecting, connected, failed, needs a sign-in.
   *
   * Window-level, because the runtime behind it is: the transition happens in the
   * host, not in a conversation. A listener is a view and must dispose what it
   * gets back.
   */
  onMcpStatus(listener: (params: McpStatusParams) => void): vscode.Disposable {
    return this.manager.onMcpStatus(listener);
  }

  /** Every management call answers with the listing as it stands after the write. */
  addMcpServer(params: McpAddParams): Promise<McpListResult> {
    return this.manager.addMcpServer(params);
  }

  updateMcpServer(params: McpUpdateParams): Promise<McpListResult> {
    return this.manager.updateMcpServer(params);
  }

  removeMcpServer(params: McpRemoveParams): Promise<McpListResult> {
    return this.manager.removeMcpServer(params);
  }

  setMcpServerEnabled(params: McpSetEnabledParams): Promise<McpListResult> {
    return this.manager.setMcpServerEnabled(params);
  }

  reconnectMcpServer(params: McpReconnectParams): Promise<McpListResult> {
    return this.manager.reconnectMcpServer(params);
  }

  /**
   * Starts a sign-in and answers with the URL to open.
   *
   * The panel opens it with `vscode.env.openExternal`; the host is what completes
   * the callback and reconnects. Completion arrives as an `mcp.status`, not as
   * the resolution of this promise — the browser round trip is the user's, and
   * how long it takes is not something to hold a request open for.
   */
  authenticateMcpServer(params: McpAuthenticateParams): Promise<McpAuthenticateResult> {
    return this.manager.authenticateMcpServer(params);
  }

  signOutMcpServer(params: McpSignOutParams): Promise<McpListResult> {
    return this.manager.signOutMcpServer(params);
  }

  /** Whether sessions load each MCP layer; the manager says so when one is off. */
  loadMcpSettings(): McpSettings {
    return readSettings().mcp;
  }

  /** Opens the MCP manager: every server, with what it is doing and what to do. */
  openMcp(): void {
    this.mcp.open();
  }

  /**
   * Shows one layer's `mcp.json` where it lives, creating it when it is not
   * there.
   *
   * The paths are the ones the host uses — the first folder's `.cursor/mcp.json`
   * and `~/.cursor/mcp.json` — because a file the manager edits is the file a
   * session reads, and a person sent to the wrong path would edit a file nothing
   * looks at. The seed is the smallest document the writer accepts, so the next
   * add has a file to write into rather than a directory to guess at.
   *
   * Hand editing stays one click away for the same reason the permissions
   * manager keeps its Open JSON: an entry with an OAuth block or a field this
   * milestone does not model is easier to see in the file than in the form.
   */
  async openMcpFile(scope: McpScope = 'project'): Promise<void> {
    const path = mcpFilePath(scope);
    if (path === undefined) {
      this.notice('error', 'Could not find a workspace folder to resolve mcp.json against.');
      return;
    }
    const uri = vscode.Uri.file(path);
    const exists = await vscode.workspace.fs.stat(uri).then(
      () => true,
      () => false,
    );
    if (!exists) {
      const create = 'Create it';
      const answer = await vscode.window.showInformationMessage(
        scope === 'user'
          ? 'This user account has no MCP configuration yet.'
          : 'This workspace has no MCP configuration yet.',
        { modal: false, detail: path },
        create,
      );
      if (answer !== create) return;
      try {
        await vscode.workspace.fs.createDirectory(vscode.Uri.file(dirname(path)));
        await vscode.workspace.fs.writeFile(uri, Buffer.from('{\n  "mcpServers": {}\n}\n', 'utf8'));
      } catch (err) {
        this.notice('error', `Could not create ${path}: ${(err as Error).message}`);
        return;
      }
    }
    const document = await vscode.workspace.openTextDocument(uri);
    await vscode.window.showTextDocument(document);
  }

  /** The provider's model catalogue, for a tab's /model picker. */
  listModels(options: { provider?: string; base_url?: string } = {}): Promise<ModelEntry[]> {
    return this.manager.listModels(options);
  }

  /** Appends a rule to the workspace file, optionally settling a pending prompt. */
  addPermissionRule(rule: RuleSpec, requestId?: string): Promise<PermissionsAddResult> {
    return this.manager.addPermissionRule(rule, requestId);
  }

  // --- permission rules -------------------------------------------------------

  /** The workspace rule file as the host reads it, for the manager. */
  permissionRules(): Promise<PermissionsResult> {
    return this.manager.permissionRules();
  }

  /**
   * Replaces the workspace rule file with the list the manager showed.
   *
   * It lives here rather than in the panel because the panel is a document: it
   * knows what a form looks like, not that there is a host child behind it. The
   * answer is the file read back, which the panel renders.
   */
  savePermissionRules(params: PermissionsSetParams): Promise<PermissionsResult> {
    return this.manager.savePermissionRules(params);
  }

  /** Whether sessions read the rule file at all; the panel says so when they do not. */
  loadPermissionsEnabled(): boolean {
    return readSettings().session.load_permissions !== false;
  }

  /** Opens the manager: the rules as a form, without knowing the JSON. */
  openPermissionRules(): void {
    this.permissions.open();
  }

  /**
   * Shows one rule file where it lives, creating it when that layer has
   * written nothing yet.
   *
   * The manager's Open JSON buttons and the `Anvil: Open Permission Rules File`
   * command are this path; the user file is reached by its section's button, or
   * by `source: 'user'`. Hand editing is not the legacy way to edit a rule — a
   * pattern with quoting in it is still easier to see in the file — so it stays
   * one click from the form rather than behind an advanced setting.
   *
   * A file that will not compile is opened at the place the parser objected to,
   * reported against the file in the Problems panel, and said in every tab: it
   * is the one failure of this feature the user cannot see coming, so "the
   * session would not start" would leave them looking for which bracket. The
   * session itself reports the same problem as a failure to open, and this is
   * the way to it.
   */
  async openPermissionRulesFile(source: RuleSource = 'workspace'): Promise<void> {
    let state: PermissionsResult;
    try {
      state = await this.manager.permissionRules();
    } catch (err) {
      this.notice('error', `Could not read the permission rules: ${(err as Error).message}`);
      return;
    }
    if (source === 'user') {
      const path = state.user_path ?? '';
      if (path === '') {
        this.notice('error', 'The home directory could not be resolved, so there is no user rule file to open.');
        return;
      }
      await this.openRulesFile(
        path,
        state.user_exists === true,
        state.user_problem,
        emptyUserRulesFile(),
        'You have no user permission rules. Nothing follows you between workspaces by rule yet.',
      );
      return;
    }
    await this.openRulesFile(
      state.path,
      state.exists,
      state.problem,
      emptyRulesFile(),
      'This workspace has no permission rules. Nothing is allowed or refused by rule yet.',
    );
  }

  /** Opens one layer's rule file, offering to create it when it is missing. */
  private async openRulesFile(
    path: string,
    exists: boolean,
    problem: RuleProblem | undefined,
    empty: string,
    missing: string,
  ): Promise<void> {
    const uri = vscode.Uri.file(path);
    if (!exists) {
      const create = 'Create it';
      const answer = await vscode.window.showInformationMessage(
        missing,
        { modal: false, detail: path },
        create,
      );
      if (answer !== create) return;
      try {
        await vscode.workspace.fs.createDirectory(vscode.Uri.file(dirname(path)));
        await vscode.workspace.fs.writeFile(uri, Buffer.from(empty, 'utf8'));
      } catch (err) {
        this.notice('error', `Could not create ${path}: ${(err as Error).message}`);
        return;
      }
    }
    const document = await vscode.workspace.openTextDocument(uri);
    const editor = await vscode.window.showTextDocument(document);
    this.reportProblem(path, problem);
    // A file the session does not read is a file whose rules do nothing. The
    // setting is what turns it off, so saying which setting is the whole help
    // a user needs — the alternative is editing a rule and watching it be
    // ignored, which reads as the feature being broken.
    if (readSettings().session.load_permissions === false) {
      this.notice('warn', `anvil.permissions is off, so ${path} is not read: its rules decide nothing.`);
    }
    if (!problem) return;
    const position = problemPosition(problem);
    const at = new vscode.Position(position.line, position.column);
    editor.selection = new vscode.Selection(at, at);
    editor.revealRange(new vscode.Range(at, at), vscode.TextEditorRevealType.InCenter);
    this.notice('warn', describeProblem(path, problem));
  }

  /** Reports a rule file that will not compile against the file itself. */
  private reportProblem(path: string, problem: RuleProblem | undefined): void {
    if (!problem) {
      this.diagnostics?.delete(vscode.Uri.file(path));
      return;
    }
    if (!this.diagnostics) this.diagnostics = vscode.languages.createDiagnosticCollection('anvil');
    const position = problemPosition(problem);
    const at = new vscode.Position(position.line, position.column);
    // A zero-width range at the exact place the parser stopped: the file is
    // being edited by hand, so pointing at a character beats underlining a line.
    const diagnostic = new vscode.Diagnostic(
      new vscode.Range(at, at),
      problem.message,
      vscode.DiagnosticSeverity.Error,
    );
    diagnostic.source = 'anvil';
    this.diagnostics.set(vscode.Uri.file(path), [diagnostic]);
  }

  async dispose(): Promise<void> {
    const panels = [...this.panels];
    this.panels.clear();
    this.tabsChanged.dispose();
    this.sessionsChanged.dispose();
    this.runningChanged.dispose();
    this.tasksChanged.dispose();
    this.diagnostics?.dispose();
    this.diagnostics = undefined;
    this.permissions.dispose();
    this.tasks.dispose();
    this.mcp.dispose();
    for (const panel of panels) await panel.dispose().catch(() => undefined);
    await this.manager.dispose();
  }
}

/**
 * One layer's `mcp.json`, resolved the way the host resolves it.
 *
 * `undefined` when a project path has no workspace folder to sit in — the host
 * would be launched without a cwd, so there is no file to open either.
 *
 * The paths are computed rather than asked for so this works when the file is
 * the problem: `mcp.list` answers with a path only after it has parsed, and the
 * button that opens a file that will not parse is the one worth having.
 */
function mcpFilePath(scope: McpScope): string | undefined {
  if (scope === 'user') return join(homedir(), '.cursor', 'mcp.json');
  const folder = vscode.workspace.workspaceFolders?.[0];
  if (!folder || folder.uri.scheme !== 'file') return undefined;
  return join(folder.uri.fsPath, '.cursor', 'mcp.json');
}
