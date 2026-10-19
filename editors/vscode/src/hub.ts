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

import { dirname } from 'node:path';
import * as vscode from 'vscode';
import { ChatPanel } from './chatPanel';
import { SlashCommand, commandCatalogue } from './commands';
import { HostManager } from './hostManager';
import {
  CheckpointRestoreResult,
  CheckpointRun,
  McpListResult,
  ModelEntry,
  PermissionsAddResult,
  PermissionsResult,
  RuleFileInfo,
  RuleProblem,
  RuleSpec,
  SessionInfo,
  SessionTranscriptResult,
  SkillEntry,
} from './protocol';
import { PreviewDocuments } from './review';
import { describeProblem, emptyRulesFile, problemPosition } from './rulesFile';
import { readSettings } from './settings';

export class Hub {
  /** The one child this window talks to, shared by every tab. */
  readonly manager: HostManager;

  private readonly panels = new Set<ChatPanel>();
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

  constructor(
    private readonly context: vscode.ExtensionContext,
    private readonly log: (line: string) => void,
    private readonly previews: PreviewDocuments,
  ) {
    // A crash or a restart ends every session this window holds. Each panel
    // drops its handle and opens a new one for its agent on the next prompt;
    // nothing here may keep a handle to a child that is gone.
    this.manager = new HostManager(context, log, {
      onNotice: (level, message) => this.notice(level, message),
    });
    this.manager.onReset(() => {
      for (const panel of this.panels) panel.hostReset();
    });
  }

  // --- panels -----------------------------------------------------------------

  /**
   * Builds a tab without opening it. The caller adopts a webview panel onto it
   * — either one this side just created, or one VS Code restored — which is why
   * construction and `open()` are separate.
   */
  createPanel(agentId?: string): ChatPanel {
    const panel = new ChatPanel(this.context, this.log, this.previews, this, agentId);
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
   * The MCP servers the given layers declare.
   *
   * Not cached, unlike the rules: the layers are the caller's, and a malformed
   * file is an answer the caller has to be able to see on every ask, not just
   * the first. The call reads two small files, so repeating it per tab costs
   * less than a cache that could answer for a different layer set.
   */
  mcpServers(settingSources: string[]): Promise<McpListResult> {
    return this.manager.mcpServers(settingSources);
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

  /**
   * Shows the workspace rule file where it lives, creating it when this
   * workspace has written no rules yet.
   *
   * A file that will not compile is opened at the place the parser objected to,
   * reported against the file in the Problems panel, and said in every tab: it
   * is the one failure of this feature the user cannot see coming, so "the
   * session would not start" would leave them looking for which bracket. The
   * session itself reports the same problem as a failure to open, and this is
   * the way to it.
   */
  async openPermissionRules(): Promise<void> {
    let state: PermissionsResult;
    try {
      state = await this.manager.permissionRules();
    } catch (err) {
      this.notice('error', `Could not read the permission rules: ${(err as Error).message}`);
      return;
    }
    const uri = vscode.Uri.file(state.path);
    if (!state.exists) {
      const create = 'Create it';
      const answer = await vscode.window.showInformationMessage(
        'This workspace has no permission rules. Nothing is allowed or refused by rule yet.',
        { modal: false, detail: state.path },
        create,
      );
      if (answer !== create) return;
      try {
        await vscode.workspace.fs.createDirectory(vscode.Uri.file(dirname(state.path)));
        await vscode.workspace.fs.writeFile(uri, Buffer.from(emptyRulesFile(), 'utf8'));
      } catch (err) {
        this.notice('error', `Could not create ${state.path}: ${(err as Error).message}`);
        return;
      }
    }
    const document = await vscode.workspace.openTextDocument(uri);
    const editor = await vscode.window.showTextDocument(document);
    this.reportProblem(state.path, state.problem);
    // A file the session does not read is a file whose rules do nothing. The
    // setting is what turns it off, so saying which setting is the whole help
    // a user needs — the alternative is editing a rule and watching it be
    // ignored, which reads as the feature being broken.
    if (readSettings().session.load_permissions === false) {
      this.notice('warn', `anvil.permissions is off, so ${state.path} is not read: its rules decide nothing.`);
    }
    if (!state.problem) return;
    const position = problemPosition(state.problem);
    const at = new vscode.Position(position.line, position.column);
    editor.selection = new vscode.Selection(at, at);
    editor.revealRange(new vscode.Range(at, at), vscode.TextEditorRevealType.InCenter);
    this.notice('warn', describeProblem(state.path, state.problem));
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
    this.diagnostics?.dispose();
    this.diagnostics = undefined;
    for (const panel of panels) await panel.dispose().catch(() => undefined);
    await this.manager.dispose();
  }
}
