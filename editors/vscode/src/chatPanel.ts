/**
 * The conversation: a webview in an editor tab, fed by one `HostManager`.
 *
 * The webview is a rendering surface only. Everything with a decision in it —
 * spawning, sessions, approvals, context composition, review tabs — happens on
 * this side of the message port, so the untrusted-ish HTML never holds state
 * the host needs and never names a path the host will act on without this side
 * having decided that it should.
 *
 * The tab is a document, which sets two rules this class exists to keep. Closing
 * it is not stopping the turn: the session and the host child outlive the view,
 * and messages that arrive with no view attached are dropped rather than queued.
 * And reopening it re-announces everything, because a fresh document knows
 * nothing — which is what the `ready` path is for, and now its main job.
 */

import { randomBytes } from 'node:crypto';
import * as vscode from 'vscode';
import { ChangesTracker, ChangedFile } from './changes';
import { buildPromptContext, pickMentions } from './editorContext';
import { attachmentsFromDataUrls } from './attachments';
import { HostListener, HostManager } from './hostManager';
import { PreviewDocuments } from './review';
import { SessionState } from './session';
import { dedupeMentions } from './mentions';
import {
  CommandAction,
  SlashCommand,
  commandCatalogue,
  parseSlash,
  route,
} from './commands';
import {
  ApprovalRequest,
  Decision,
  DoneParams,
  HostEvent,
  PermissionMode,
  PermissionModes,
  RuleFileInfo,
  SessionInfo,
  SkillEntry,
} from './protocol';
import { RunFailure } from './errors';
import { readSettings } from './settings';

/** A message from the webview. */
type Inbound =
  | { type: 'ready' }
  | { type: 'send'; text: string; mentions?: string[]; images?: string[] }
  | { type: 'cancel' }
  | { type: 'answer'; requestId: string; decision: Decision; scope?: string }
  | { type: 'pickMention' }
  | { type: 'commands' }
  | { type: 'mode'; mode: string }
  | { type: 'review'; requestId: string }
  | { type: 'reviewChange'; path: string };

/** A message to the webview. */
type Outbound =
  | { type: 'trust'; trusted: boolean }
  | {
      type: 'state';
      state: SessionState;
      runId?: string;
      sessionId?: string;
      agentId?: string;
      mode?: PermissionMode;
    }
  | { type: 'event'; event: HostEvent }
  | { type: 'approval'; request: ApprovalRequest }
  | { type: 'approvalWithdrawn'; requestId: string }
  | { type: 'done'; done: DoneParams; failure: RunFailure }
  | { type: 'mentions'; paths: string[] }
  | { type: 'vision'; enabled: boolean }
  | { type: 'changes'; files: Array<Pick<ChangedFile, 'path' | 'kind'>> }
  | { type: 'commands'; commands: SlashCommand[] }
  | { type: 'help'; commands: SlashCommand[] }
  | { type: 'mode'; mode: PermissionMode }
  | { type: 'rules'; files: RuleFileInfo[] }
  | { type: 'session'; agentId: string; started: boolean }
  /**
   * Empties the transcript. The tab is a singleton pointed at one conversation
   * at a time, so adopting another agent has to take the old one off the
   * screen — otherwise two conversations are stacked in one document, which
   * reads as corruption rather than as a switch.
   */
  | { type: 'reset' }
  /** The tab became the active editor: the composer takes the keyboard. */
  | { type: 'focus' }
  | { type: 'notice'; level: 'info' | 'warn' | 'error'; message: string };

/** Where the chosen permission mode is remembered between windows. */
const MODE_KEY = 'anvil.permissionMode';

/** How the modes are named to the user. The wire values are the SDK's. */
const MODE_LABELS: Record<PermissionMode, string> = {
  ask: 'Ask',
  accept_edits: 'Accept edits',
  plan: 'Plan',
  bypass: 'Bypass',
};

function isPermissionMode(value: string): value is PermissionMode {
  return (PermissionModes as readonly string[]).includes(value);
}

/** The mode this workspace last used. An unknown stored value means `ask`. */
function readMode(context: vscode.ExtensionContext): PermissionMode {
  const stored = context.workspaceState.get<string>(MODE_KEY);
  return stored !== undefined && isPermissionMode(stored) ? stored : 'ask';
}

export class ChatPanel implements HostListener {
  public static readonly viewType = 'anvil.chat';

  private panel: vscode.WebviewPanel | undefined;
  /** Whether the document in `panel` has asked for its state yet. */
  private listening = false;
  /** Messages held until it does. See `post`. */
  private pending: Outbound[] = [];
  private manager: HostManager | undefined;
  /** Fires when the host comes up or goes away, for the title-bar stop button. */
  private readonly runningChanged = new vscode.EventEmitter<void>();
  /** Fires when the session list, or the markers on it, could have changed. */
  private readonly sessionsChanged = new vscode.EventEmitter<void>();
  /** Pre-images for the current turn, so the changed-files list can show diffs. */
  private readonly changes = new ChangesTracker();
  /** The changed files of the last finished turn, by path. */
  private lastChanges = new Map<string, ChangedFile>();
  /** Review tabs opened for an approval, closed when it is answered. */
  private readonly reviews = new Map<string, vscode.Uri[]>();
  /**
   * The slash catalogue: built-ins plus the workspace's skills. Fetched once per
   * host, because it costs a round trip and only changes when the process does.
   */
  private catalogue: SlashCommand[] | undefined;
  /** The rule files the last session reported, re-announced to a reloaded view. */
  private rules: RuleFileInfo[] = [];
  /** The permission mode in force, remembered per workspace. */
  private mode: PermissionMode = 'ask';

  constructor(
    private readonly context: vscode.ExtensionContext,
    private readonly log: (line: string) => void,
    private readonly previews: PreviewDocuments,
  ) {
    this.mode = readMode(context);
  }

  /**
   * Opens the conversation tab, or reveals the one that is already open.
   *
   * `ViewColumn.Active` puts it where the user is looking, which is the least
   * surprising answer for a command they ran from wherever they were. A reveal
   * names the panel's own column rather than the active one so that a tab the
   * user dragged somewhere stays there.
   */
  open(): void {
    if (this.panel) {
      this.panel.reveal(this.panel.viewColumn, false);
      return;
    }
    const panel = vscode.window.createWebviewPanel(
      ChatPanel.viewType,
      'Anvil',
      vscode.ViewColumn.Active,
      {
        enableScripts: true,
        // Without this, clicking another editor destroys the document and the
        // transcript with it — and there is no replay to rebuild it from. It is
        // an optimisation for the panel this side creates, not a guarantee:
        // VS Code creates the panel a serializer is handed, and that one may
        // not retain, which is why `ready` is the source of truth either way.
        retainContextWhenHidden: true,
        localResourceRoots: [vscode.Uri.joinPath(this.context.extensionUri, 'media')],
      },
    );
    panel.iconPath = vscode.Uri.joinPath(this.context.extensionUri, 'media', 'anvil.svg');
    this.adopt(panel);
  }

  /**
   * Attaches to a panel that already exists: one this side just created, or one
   * VS Code restored from a previous window. Both need the same three things —
   * a document, a message port, and a way to notice the tab going away.
   */
  adopt(panel: vscode.WebviewPanel): void {
    this.panel = panel;
    // Each document announces itself once; until it does, nothing is listening.
    this.listening = false;
    this.pending = [];
    panel.webview.options = {
      enableScripts: true,
      localResourceRoots: [vscode.Uri.joinPath(this.context.extensionUri, 'media')],
    };
    panel.webview.html = renderHtml(panel.webview, this.context.extensionUri);
    panel.webview.onDidReceiveMessage((msg: Inbound) => void this.onMessage(msg));
    panel.onDidChangeViewState((event) => {
      if (event.webviewPanel.active) this.post({ type: 'focus' });
    });
    panel.onDidDispose(() => {
      // Guarded: `deactivate` disposes the panel too, and a stale disposal must
      // not clear a panel that has since replaced it.
      if (this.panel !== panel) return;
      this.panel = undefined;
      this.listening = false;
      this.pending = [];
    });
    this.updateTitle();
  }

  /**
   * Creates the manager lazily: no host work happens at activation, and the two
   * things that can ask for a host are both user actions — a prompt in the tab,
   * or the session view being opened.
   */
  private ensureManager(): HostManager {
    if (!this.manager) {
      this.manager = new HostManager(this.context, this.log, this);
      // The mode is remembered per workspace, so it has to be in force before
      // the session that will carry it is created. This only records the
      // choice; a session is not opened to hold it.
      void this.manager.applyOptions({ permission_mode: this.mode });
    }
    return this.manager;
  }

  private async onMessage(msg: Inbound): Promise<void> {
    if (msg.type === 'ready') {
      // Whatever was said before this document started listening, in the order
      // it was said — which for a tab opened by a session click is the reset,
      // the session marker and the state, in that order.
      const queued = this.pending;
      this.pending = [];
      this.listening = true;
      for (const message of queued) this.post(message);

      // A fresh document has lost whatever transcript it might have had, so
      // re-announce everything it cannot infer: trust, the run in flight, and
      // any open prompt.
      this.post({ type: 'trust', trusted: vscode.workspace.isTrusted });
      this.post({ type: 'vision', enabled: readSettings().session.vision === true });
      this.post({ type: 'mode', mode: this.mode });
      if (this.rules.length > 0) this.post({ type: 'rules', files: this.rules });
      if (this.manager) this.post({ type: 'state', ...this.manager.state() });
      for (const request of this.pendingApprovals()) {
        this.post({ type: 'approval', request });
      }
      return;
    }

    try {
      HostManager.assertTrusted();
    } catch (err) {
      this.post({ type: 'notice', level: 'error', message: (err as Error).message });
      return;
    }

    switch (msg.type) {
      case 'send':
        await this.sendTurn(msg.text, msg.mentions ?? [], msg.images ?? []);
        return;
      case 'cancel':
        await this.ensureManager().cancel();
        return;
      case 'answer':
        await this.answer(msg.requestId, msg.decision, msg.scope);
        return;
      case 'pickMention':
        // The picker is the editor's, not the webview's: it searches the real
        // workspace and returns paths, and a path is all the protocol carries.
        this.post({ type: 'mentions', paths: dedupeMentions(await pickMentions()) });
        return;
      case 'commands':
        await this.sendCatalogue();
        return;
      case 'mode':
        await this.setMode(msg.mode);
        return;
      case 'review':
        await this.openReview(msg.requestId);
        return;
      case 'reviewChange':
        await this.openChangeReview(msg.path);
        return;
      default:
        return;
    }
  }

  // --- commands ---------------------------------------------------------------

  /**
   * The catalogue, fetched once. Built-ins need no host, so they are always
   * available; the skills are the reason it is a round trip at all.
   */
  private async commandCatalogue(): Promise<SlashCommand[]> {
    if (this.catalogue) return this.catalogue;
    let skills: SkillEntry[] = [];
    try {
      skills = await this.ensureManager().skills();
    } catch (err) {
      // A missing or broken host must not cost the user the built-ins.
      this.log(`could not list skills: ${(err as Error).message}`);
      this.post({
        type: 'notice',
        level: 'warn',
        message: `Skill commands are unavailable: ${(err as Error).message}`,
      });
    }
    this.catalogue = commandCatalogue(skills);
    return this.catalogue;
  }

  private async sendCatalogue(): Promise<void> {
    this.post({ type: 'commands', commands: await this.commandCatalogue() });
  }

  /**
   * Applies a permission mode to the conversation. The mode is a session
   * option, so the session is re-opened under it — same agent, so the history
   * and the transcript both stay.
   */
  private async setMode(mode: string): Promise<void> {
    if (!isPermissionMode(mode)) {
      this.post({ type: 'notice', level: 'warn', message: `Unknown mode: ${mode}` });
      this.post({ type: 'mode', mode: this.mode });
      return;
    }
    if (mode === this.mode) return;
    const manager = this.ensureManager();
    // Checked here rather than left to the re-open, so the refusal is reported
    // as a refusal. `reopen` would throw the same thing, but by then the chosen
    // mode is already recorded as an override and would silently take effect on
    // the *next* session — a mode the user was told had been refused.
    if (manager.state().state === 'running') {
      this.post({ type: 'notice', level: 'warn', message: 'Stop the turn before changing the mode.' });
      this.post({ type: 'mode', mode: this.mode });
      return;
    }
    // false means either "nothing was open to re-open" (the ordinary case
    // before the first prompt: the choice is simply recorded) or "it refused".
    // A live session that did not take the change is the second one.
    const applied = await manager.applyOptions({ permission_mode: mode });
    if (!applied && manager.running) {
      this.post({ type: 'mode', mode: this.mode });
      return;
    }
    this.mode = mode;
    await this.context.workspaceState.update(MODE_KEY, mode);
    this.post({ type: 'mode', mode });
    this.postState();
    this.post({
      type: 'notice',
      level: mode === 'bypass' ? 'warn' : 'info',
      message:
        mode === 'bypass'
          ? 'Bypass is on: tool calls run without asking. Nothing will be shown for approval.'
          : `Mode: ${MODE_LABELS[mode]}.`,
    });
  }

  // --- session history --------------------------------------------------------

  /** Opens a past agent from this workspace as the current conversation. */
  private async pickResume(): Promise<void> {
    const manager = this.ensureManager();
    let sessions: Awaited<ReturnType<HostManager['listSessions']>> = [];
    try {
      sessions = await manager.listSessions();
    } catch (err) {
      this.post({ type: 'notice', level: 'error', message: `Could not list sessions: ${(err as Error).message}` });
      return;
    }
    if (sessions.length === 0) {
      this.post({ type: 'notice', level: 'info', message: 'No earlier sessions in this workspace yet.' });
      return;
    }
    const items = sessions.map((session) => ({
      label: session.agent_id,
      description: `${session.runs} run${session.runs === 1 ? '' : 's'} · ${session.last_status || 'unknown'}`,
      detail: [session.model, session.updated_at ? `updated ${session.updated_at}` : ''].filter(Boolean).join(' · '),
      session,
    }));
    const picked = await vscode.window.showQuickPick(items, {
      title: 'Resume a session',
      placeHolder: 'A past agent in this workspace',
      matchOnDescription: true,
      matchOnDetail: true,
    });
    if (!picked) return;
    await this.resumeAgent(picked.session.agent_id);
  }

  /** Ends the conversation. The host stays up; the next prompt starts fresh. */
  private async clearConversation(): Promise<void> {
    await this.ensureManager().clearConversation();
    this.changes.begin();
    this.lastChanges = new Map();
    this.post({ type: 'reset' });
    this.post({ type: 'session', agentId: '', started: false });
    this.postState();
    this.updateTitle();
    this.sessionsChanged.fire();
  }

  // --- the session sidebar's view of this -------------------------------------

  /** The agents this workspace has stored. Starts a host if none is live. */
  async listSessions(): Promise<SessionInfo[]> {
    return this.ensureManager().listSessions();
  }

  /** The agent the tab is showing, whether or not a turn is in flight. */
  currentAgentId(): string | undefined {
    return this.manager?.state().agentId;
  }

  /** The agent with a turn in flight. */
  runningAgentId(): string | undefined {
    const state = this.manager?.state();
    return state?.state === 'running' ? state.agentId : undefined;
  }

  /** Whether a host child is live, so a redraw need not start one. */
  isHostUp(): boolean {
    return this.manager?.running ?? false;
  }

  /**
   * Continues a stored agent, from the sidebar. The tab is opened first: the
   * resume reports its outcome as a message to the panel, and a message with no
   * document behind it is dropped — the failure would be silent.
   */
  async openSession(agentId: string): Promise<void> {
    this.open();
    await this.resumeAgent(agentId);
  }

  /** Starts a fresh conversation, from the sidebar. */
  async newSession(): Promise<void> {
    this.open();
    await this.clearConversation();
  }

  /** Subscribes to changes in the session list, or in the markers on it. */
  onSessionsChanged(listener: () => void): vscode.Disposable {
    return this.sessionsChanged.event(listener);
  }

  /** Composes and sends one turn, with whatever context the editor has. */
  private async sendTurn(text: string, mentions: string[], images: string[]): Promise<void> {
    // The catalogue is only consulted for a line that could be a command, so an
    // ordinary prompt never pays for the round trip (or for the host process
    // the round trip would start).
    const action = parseSlash(text)
      ? route(text, await this.commandCatalogue())
      : { kind: 'prompt' as const, text };
    await this.runAction(action, text, mentions, images);
  }

  /**
   * Carries out what the composer line meant. Everything here maps onto
   * something the session can already do — a mode, a model, a skill, a stored
   * agent — so no command is a prompt trick.
   */
  private async runAction(
    action: CommandAction,
    text: string,
    mentions: string[],
    images: string[],
  ): Promise<void> {
    switch (action.kind) {
      case 'prompt':
        await this.sendQuestion(action.text, mentions, images);
        return;
      case 'mode':
        await this.setMode(action.mode);
        if (action.args !== '') {
          await this.sendQuestion(action.args, mentions, images);
          return;
        }
        this.postState();
        return;
      case 'model':
        await this.pickModel(action.model);
        // Re-announced even when nothing ran: the composer locked itself the
        // moment the command was submitted, and only a state message unlocks
        // it. A cancelled picker has no other ending.
        this.postState();
        return;
      case 'resume':
        if (action.agentId !== '') {
          await this.resumeAgent(action.agentId);
          return;
        }
        await this.pickResume();
        this.postState();
        return;
      case 'clear':
        await this.clearConversation();
        return;
      case 'help':
        this.post({ type: 'help', commands: await this.commandCatalogue() });
        this.postState();
        return;
      case 'skill': {
        // The manager says so itself when the skill is already on; a second
        // notice here would be the same sentence twice.
        const added = await this.ensureManager().useSkill(action.skillId);
        if (added) {
          this.post({
            type: 'notice',
            level: 'info',
            message: `The ${action.skillId} skill is on for this conversation.`,
          });
        }
        if (action.args !== '') {
          await this.sendQuestion(action.args, mentions, images);
          return;
        }
        this.postState();
        return;
      }
      default:
        // Unreachable while every action has a case. Sending what was typed is
        // the safe ending: a new command cannot swallow the user's words.
        await this.sendQuestion(text, mentions, images);
        return;
    }
  }

  /** Adopts a named agent, for `/resume <id>` typed rather than picked. */
  private async resumeAgent(agentId: string): Promise<void> {
    try {
      await this.ensureManager().resumeSession(agentId);
    } catch (err) {
      this.post({
        type: 'notice',
        level: 'error',
        message: `Could not resume ${agentId}: ${(err as Error).message}`,
      });
      this.postState();
      return;
    }
    // Cleared before the marker, never after: the transcript that was on screen
    // belonged to the agent that was current a moment ago, and the marker is
    // the first line of the conversation that replaced it.
    this.post({ type: 'reset' });
    this.post({ type: 'session', agentId, started: true });
    this.postState();
    this.updateTitle();
    this.sessionsChanged.fire();
    await this.reportRules();
  }

  /** Names the tab after the conversation in it, so a tab strip is readable. */
  private updateTitle(): void {
    const agentId = this.currentAgentId();
    const title = agentId ? `Anvil — ${agentId.slice(0, 8)}` : 'Anvil';
    if (this.panel && this.panel.title !== title) this.panel.title = title;
  }

  /** Applies a model to the conversation, from an argument or a picker. */
  private async pickModel(given: string): Promise<void> {
    const manager = this.ensureManager();
    let model = given;
    if (model === '') {
      let entries: Awaited<ReturnType<HostManager['listModels']>> = [];
      try {
        entries = await manager.listModels();
      } catch (err) {
        this.post({
          type: 'notice',
          level: 'error',
          message: `Could not list models: ${(err as Error).message} (check anvil.baseUrl)`,
        });
        return;
      }
      if (entries.length === 0) {
        this.post({ type: 'notice', level: 'info', message: 'The provider reported no models.' });
        return;
      }
      const picked = await vscode.window.showQuickPick(
        entries.map((entry) => ({
          label: entry.id,
          description: [entry.provider, entry.vision ? 'vision' : ''].filter(Boolean).join(' · '),
          detail: entry.owned_by,
          id: entry.id,
        })),
        { title: 'Model', placeHolder: 'The model this conversation uses' },
      );
      if (!picked) return;
      model = picked.id;
    }
    await manager.applyOptions({ model });
    this.post({ type: 'notice', level: 'info', message: `Model: ${model}. The conversation continues.` });
    this.postState();
  }

  /** One ordinary turn, with the context the editor has. */
  private async sendQuestion(text: string, mentions: string[], images: string[]): Promise<void> {
    const context = buildPromptContext(dedupeMentions(mentions));
    const { attachments, errors } = attachmentsFromDataUrls(images);
    for (const error of errors) {
      this.post({ type: 'notice', level: 'warn', message: `Dropped an attachment: ${error}.` });
    }
    // The pre-images this turn will need are gone the moment it starts writing,
    // so the tracker is reset here rather than when the first write arrives.
    this.changes.begin();
    this.lastChanges = new Map();
    await this.ensureManager()
      .send(text, { context, attachments })
      .catch(() => undefined);
    // The session exists now, so what it loaded can be reported. This is the
    // first moment the rules are a fact about a session rather than a guess.
    await this.reportRules();
  }

  /**
   * Reports which rule files apply, once per change. It is the answer to "why
   * is the model ignoring my AGENTS.md", and it is invisible from here without
   * asking the host.
   */
  private async reportRules(): Promise<void> {
    const manager = this.manager;
    if (!manager) return;
    try {
      const files = await manager.rules();
      const key = JSON.stringify(files);
      if (key === JSON.stringify(this.rules)) return;
      this.rules = files;
      this.post({ type: 'rules', files });
    } catch (err) {
      this.log(`could not list rule files: ${(err as Error).message}`);
    }
  }

  /** Answers an approval, and closes the review it was answered from. */
  private async answer(requestId: string, decision: Decision, scope?: string): Promise<void> {
    await this.ensureManager().respond(requestId, decision, { scope });
    await this.closeReview(requestId);
  }

  private pendingApprovals(): ApprovalRequest[] {
    return this.manager?.pendingApprovals() ?? [];
  }

  // --- reviews ---------------------------------------------------------------

  /**
   * Opens the preview in the editor's own diff view. Only write_file and
   * edit_file carry both bodies; apply_patch spans files and so is left to the
   * diff text the card already shows.
   */
  private async openReview(requestId: string): Promise<void> {
    const request = this.pendingApprovals().find((candidate) => candidate.request_id === requestId);
    const preview = request?.preview;
    if (!request || !preview || typeof preview.diff !== 'string') {
      this.post({ type: 'notice', level: 'warn', message: 'There is no preview to review for this call.' });
      return;
    }
    if (preview.truncated || typeof preview.before !== 'string' || typeof preview.after !== 'string') {
      // The host dropped the bodies rather than send a clipped post-image, so
      // the honest thing is to say the diff text is all there is.
      this.post({
        type: 'notice',
        level: 'warn',
        message: 'This change is too large to open as a diff. Review the diff shown in the card.',
      });
      return;
    }
    const label = preview.path ?? request.tool;
    const uris = await this.previews.openPreview(
      preview.before,
      preview.after,
      label,
      `${label} (proposed)`,
    );
    const existing = this.reviews.get(requestId) ?? [];
    this.reviews.set(requestId, [...existing, ...uris]);
  }

  private async openChangeReview(path: string): Promise<void> {
    const change = this.lastChanges.get(path);
    if (!change) return;
    if (change.kind === 'deleted') {
      await this.previews.openDeleted(change.before, path);
      return;
    }
    const folder = vscode.workspace.workspaceFolders?.[0];
    if (!folder) return;
    const segments = path.split('/').filter((segment) => segment !== '' && segment !== '..' && segment !== '.');
    if (segments.length === 0) return;
    await this.previews.openAgainstDisk(change.before, vscode.Uri.joinPath(folder.uri, ...segments), path);
  }

  private async closeReview(requestId: string): Promise<void> {
    const uris = this.reviews.get(requestId);
    if (!uris) return;
    this.reviews.delete(requestId);
    await this.previews.close(uris);
  }

  // --- HostListener ----------------------------------------------------------

  onEvent = (event: HostEvent): void => {
    // The tool call is the last moment the file it names still holds its old
    // contents, and it arrives whether or not the call is gated — which is what
    // makes the changed-files list work in accept_edits and bypass too.
    if (event.type === 'tool_call') {
      void this.changes.snapshotToolCall(event.name, event.args);
    }
    this.post({ type: 'event', event });
  };

  onApproval = (request: ApprovalRequest): void => {
    // Also snapshotted here, where nothing can be racing the write: a gated
    // call is blocked until the user answers.
    void this.changes.snapshotToolCall(request.tool, request.args);
    this.post({ type: 'approval', request });
  };

  onApprovalWithdrawn = (requestId: string): void => {
    void this.closeReview(requestId);
    this.post({ type: 'approvalWithdrawn', requestId });
  };

  onDone = (done: DoneParams, failure: RunFailure): void => {
    this.post({ type: 'done', done, failure });
    void this.reportChanges();
    // The turn changed the stored record — its run count, its status, its
    // updated_at — so the sidebar's copy of it is stale from here on.
    this.sessionsChanged.fire();
  };

  onState = (): void => {
    if (this.manager) this.post({ type: 'state', ...this.manager.state() });
    this.runningChanged.fire();
    this.sessionsChanged.fire();
  };

  onNotice = (level: 'info' | 'warn' | 'error', message: string): void =>
    this.post({ type: 'notice', level, message });

  /** Reports what the turn actually changed, once the writes have landed. */
  private async reportChanges(): Promise<void> {
    try {
      const files = await this.changes.finish();
      this.lastChanges = new Map(files.map((file) => [file.path, file]));
      this.post({
        type: 'changes',
        files: files.map((file) => ({ path: file.path, kind: file.kind })),
      });
    } catch (err) {
      this.log(`could not compute the changed files: ${(err as Error).message}`);
    }
  }

  /**
   * Sends to the tab, if there is one. Closing the tab is now routine — it is
   * how the user puts the conversation away — so a message that arrives with no
   * document behind it is expected rather than exceptional, and the rejection
   * `postMessage` returns for a disposed webview is swallowed rather than left
   * to surface as an unhandled one.
   */
  private post(message: Outbound): void {
    const panel = this.panel;
    // Closed tabs drop events rather than queueing them for a document that
    // may never exist: the session carries on, and what was missed is the
    // transcript, which the `ready` announcement does not pretend to rebuild.
    if (!panel) return;
    if (!this.listening) {
      // A document that has not run its script yet has no listener, and a
      // message posted into it is delivered to nobody. This is not a corner
      // case: opening a tab and resuming a session in it is one command, so the
      // marker for that session would otherwise be the first thing lost.
      this.pending.push(message);
      return;
    }
    void panel.webview.postMessage(message).then(undefined, () => undefined);
  }

  /** Re-announces where the session stands, mode included. */
  private postState(): void {
    const manager = this.manager;
    this.post(
      manager
        ? { type: 'state', ...manager.state() }
        : { type: 'state', state: 'idle', mode: this.mode },
    );
  }

  /** Stops the active run. Safe when nothing is running. */
  async stop(): Promise<void> {
    await this.manager?.cancel();
  }

  /** Replaces the host child and tells the view to start over. */
  async restart(): Promise<void> {
    await this.ensureManager().restart();
    this.catalogue = undefined;
    this.postState();
    this.runningChanged.fire();
    this.sessionsChanged.fire();
  }

  /** Whether a host child is live right now. */
  get running(): boolean {
    return this.manager?.running ?? false;
  }

  /**
   * Whether a turn is in flight. Distinct from `running`, which only means a
   * session is open: the Stop button hangs off this one, because stopping an
   * idle conversation is not a thing the user needs a button for.
   */
  get busy(): boolean {
    return this.manager?.state().state === 'running';
  }

  /** Subscribes to host up/down transitions. */
  onRunningChanged(listener: () => void): vscode.Disposable {
    return this.runningChanged.event(listener);
  }

  async dispose(): Promise<void> {
    const manager = this.manager;
    this.manager = undefined;
    this.runningChanged.dispose();
    this.sessionsChanged.dispose();
    for (const uris of this.reviews.values()) {
      await this.previews.close(uris).catch(() => undefined);
    }
    this.reviews.clear();
    if (manager) await manager.dispose();
  }
}

/** Builds the webview document. Nothing here is fetched at runtime. */
function renderHtml(webview: vscode.Webview, extensionUri: vscode.Uri): string {
  const nonce = makeNonce();
  const asset = (name: string): vscode.Uri =>
    webview.asWebviewUri(vscode.Uri.joinPath(extensionUri, 'media', name));
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src ${webview.cspSource}; font-src ${webview.cspSource}; img-src data: ${webview.cspSource}; script-src 'nonce-${nonce}';">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<link rel="stylesheet" href="${asset('chat.css')}">
<title>Anvil</title>
</head>
<body>
  <div id="banner" class="banner" hidden></div>
  <div id="transcript" class="transcript" role="log" aria-live="polite">
    <div id="empty" class="empty">
      <p>Ask for a change. Anvil reads, edits, and runs commands in this workspace.</p>
      <p class="hint">Mutating calls stop for your approval. <kbd>Stop</kbd> interrupts a turn.</p>
      <p class="hint"><kbd>/</kbd> lists commands, <kbd>@</kbd> references a file.</p>
      <p class="hint">Past conversations are in the Anvil view in the activity bar.</p>
    </div>
  </div>
  <!--
    The composer is out of the flow, so the transcript scrolls underneath it and
    the reading column stays full height. chat.js measures this box into
    --anvil-composer-h, which is what the transcript reserves at its bottom.
  -->
  <div id="composerDock" class="dock">
    <div id="status" class="status" hidden></div>
    <div id="bypass" class="bypass" hidden>Bypass is on. Tool calls run without asking.</div>
    <form id="composer" class="composer">
      <div id="palette" class="palette" hidden></div>
      <div id="chips" class="chips" hidden></div>
      <textarea id="input" rows="1" placeholder="Describe a change…  (@ references a file, / lists commands)" aria-label="Prompt"></textarea>
      <div class="actions">
        <select id="mode" aria-label="Permission mode" title="What the agent may do without asking">
          <option value="ask">Ask before changes</option>
          <option value="accept_edits">Accept edits</option>
          <option value="plan">Plan only</option>
          <option value="bypass">Bypass approvals</option>
        </select>
        <button id="rules" type="button" class="ghost" title="Which rule files this workspace loaded" hidden>Rules</button>
        <span class="spacer"></span>
        <button id="attach" type="button" class="secondary" title="Reference a file or folder">@</button>
        <button id="stop" type="button" class="secondary" hidden>Stop</button>
        <button id="send" type="submit" class="primary">Send</button>
      </div>
    </form>
  </div>
  <script nonce="${nonce}" src="${asset('chat.js')}"></script>
</body>
</html>`;
}

/** A fresh CSP nonce per render, from the platform's random source. */
function makeNonce(): string {
  return randomBytes(16).toString('base64');
}
